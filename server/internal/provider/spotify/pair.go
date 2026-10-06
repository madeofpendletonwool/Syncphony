// SPDX-License-Identifier: AGPL-3.0-only

package spotify

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Linking is a device pairing (OAuth2's device code flow, RFC 8628): the
// user approves a code at spotify.com/pair, for a client ID that Spotify's
// streaming protocol accepts logins from, and the Audio backend turns that
// into reusable streaming credentials. Spotify refuses our developer app's
// tokens for streaming, so the pairing uses the streaming client's ID; the
// Web API is called with our app's own token instead.

const deviceCodeGrant = "urn:ietf:params:oauth:grant-type:device_code"

// pairScope is all the pairing token is used for: logging into the
// streaming protocol once, to get reusable credentials.
const pairScope = "streaming"

// defaultPairInterval is how often to poll when Spotify doesn't say.
const defaultPairInterval = 5 * time.Second

// paired is PollPairing's result, carried to Complete.
type paired struct {
	Username string `json:"username"`
	Stored   []byte `json:"stored"`
}

// BeginPairing implements provider.DevicePairer with OAuth2's device
// authorization grant (RFC 8628).
func (l linker) BeginPairing(ctx context.Context) (provider.Pairing, error) {
	var r struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	form := url.Values{"client_id": {l.p.audio.ClientID()}, "scope": {pairScope}}
	if err := l.p.postAccounts(ctx, "/oauth2/device/authorize", form, false, &r); err != nil {
		return provider.Pairing{}, err
	}
	verify := cmp.Or(r.VerificationURIComplete, r.VerificationURI)
	if u, err := url.Parse(verify); err != nil || u.Scheme != "https" || r.DeviceCode == "" || r.UserCode == "" {
		return provider.Pairing{}, fmt.Errorf("spotify pairing: malformed response: %w", provider.ErrUnavailable)
	}
	interval := time.Duration(r.Interval) * time.Second
	if interval <= 0 {
		interval = defaultPairInterval
	}
	return provider.Pairing{
		VerifyURL: verify,
		UserCode:  r.UserCode,
		Interval:  interval,
		ExpiresIn: time.Duration(r.ExpiresIn) * time.Second,
		Secret:    r.DeviceCode,
	}, nil
}

// PollPairing implements provider.DevicePairer. Once the user approves, it
// logs into the streaming protocol to swap the short-lived token for
// reusable credentials.
func (l linker) PollPairing(ctx context.Context, deviceCode string) (string, error) {
	var tok tokenResponse
	form := url.Values{"grant_type": {deviceCodeGrant}, "device_code": {deviceCode}, "client_id": {l.p.audio.ClientID()}}
	err := l.p.postAccounts(ctx, "/api/token", form, false, &tok)
	var oe *oauthError
	switch {
	case errors.As(err, &oe):
		switch oe.code {
		case "authorization_pending":
			return "", provider.ErrPending
		case "slow_down":
			return "", fmt.Errorf("spotify pairing: %w", &provider.RateLimitError{})
		case "access_denied", "expired_token", "invalid_grant":
			return "", fmt.Errorf("%w: the pairing was declined or expired (%w)", provider.ErrInvalidCredentials, err)
		}
		return "", err
	case err != nil:
		return "", err
	case tok.AccessToken == "":
		return "", fmt.Errorf("spotify pairing: no access token: %w", provider.ErrUnavailable)
	}
	username, stored, err := l.p.audio.Pair(ctx, tok.AccessToken)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(paired{Username: username, Stored: stored}) //nolint:gosec // kept server-side, then sealed by the vault
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parsePaired reads PollPairing's result back.
func parsePaired(s string) (paired, error) {
	var p paired
	if err := json.Unmarshal([]byte(s), &p); err != nil || p.Username == "" || len(p.Stored) == 0 {
		return paired{}, fmt.Errorf("%w: no approved pairing", provider.ErrInvalidCredentials)
	}
	return p, nil
}
