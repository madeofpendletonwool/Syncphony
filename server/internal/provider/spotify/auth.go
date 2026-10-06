// SPDX-License-Identifier: AGPL-3.0-only

package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// refreshEarly is how long before expiry an access token is refreshed, so
// a token never expires halfway through a call.
const refreshEarly = time.Minute

// linker links by device pairing alone (pair.go). Users never sign into
// our developer app: the Web API is called with the app's own token.
type linker struct{ p *Provider }

func (linker) Method() provider.LinkMethod { return provider.LinkDevice }

func (linker) Fields() []provider.LinkField { return nil }

func (linker) BeginOAuth(context.Context, provider.OAuthRequest) (provider.OAuthStart, error) {
	return provider.OAuthStart{}, provider.ErrUnsupported
}

// Complete stores the streaming login from an approved pairing.
func (l linker) Complete(_ context.Context, in provider.LinkInput) (provider.Credentials, provider.AccountInfo, error) {
	pr, err := parsePaired(in.Paired)
	if err != nil {
		return nil, provider.AccountInfo{}, err
	}
	b, err := json.Marshal(creds{StreamUser: pr.Username, StreamCreds: pr.Stored}) //nolint:gosec // credentials are only ever stored encrypted, by the vault
	if err != nil {
		return nil, provider.AccountInfo{}, err
	}
	return b, provider.AccountInfo{ID: pr.Username, Name: pr.Username}, nil
}

// tokenResponse is the accounts service's token response.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"` // seconds
}

// oauthError is an error response from the token endpoint.
type oauthError struct {
	status      int
	code        string
	description string
}

func (e *oauthError) Error() string {
	if e.description != "" {
		return fmt.Sprintf("spotify token: HTTP %d: %s: %s", e.status, e.code, e.description)
	}
	return fmt.Sprintf("spotify token: HTTP %d: %s", e.status, e.code)
}

// appTokens caches the app's own Web API token (the client credentials
// grant), which every session shares.
type appTokens struct {
	mu      sync.Mutex
	token   string
	expires time.Time
}

// appToken returns a usable app token. stale, if set, is a token the Web
// API just rejected: it's replaced even if it hasn't expired, unless
// another call already replaced it.
func (p *Provider) appToken(ctx context.Context, stale string) (string, error) {
	t := &p.app
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && t.token != stale && p.now().Before(t.expires.Add(-refreshEarly)) {
		return t.token, nil
	}
	var tok tokenResponse
	err := p.postAccounts(ctx, "/api/token", url.Values{"grant_type": {"client_credentials"}}, true, &tok)
	var oe *oauthError
	switch {
	case errors.As(err, &oe):
		// invalid_client and the like: the server's app settings are wrong,
		// which no user can fix. It's an outage, not their link expiring.
		return "", fmt.Errorf("spotify app token (check SYNCPHONY_SPOTIFY_CLIENT_ID and _SECRET): %w: %w", provider.ErrUnavailable, err)
	case err != nil:
		return "", err
	case tok.AccessToken == "":
		return "", fmt.Errorf("spotify app token: malformed response: %w", provider.ErrUnavailable)
	}
	t.token, t.expires = tok.AccessToken, p.now().Add(time.Duration(tok.ExpiresIn)*time.Second)
	return t.token, nil
}

// postAccounts posts a form to the accounts service and decodes the JSON
// response into out. appAuth authenticates as the app with its secret. A
// 4xx response is an *oauthError.
func (p *Provider) postAccounts(ctx context.Context, path string, form url.Values, appAuth bool, out any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.accountsURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if appAuth {
		req.SetBasicAuth(p.clientID, p.clientSecret)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return transportError(ctx, "token", err)
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, 1<<20)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("spotify token: %w", &provider.RateLimitError{RetryAfter: retryAfter(resp.Header.Get("Retry-After"))})
	case resp.StatusCode >= 500:
		return fmt.Errorf("spotify token: HTTP %d: %w", resp.StatusCode, provider.ErrUnavailable)
	case resp.StatusCode >= 400:
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.NewDecoder(body).Decode(&e)
		return &oauthError{status: resp.StatusCode, code: e.Error, description: e.Description}
	}
	if err := json.NewDecoder(body).Decode(out); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("spotify %s: malformed response: %w", path, provider.ErrUnavailable)
	}
	return nil
}

// login returns what the Audio backend needs to log in as the account.
func (s *session) login() Login {
	return Login{Username: s.creds.StreamUser, Stored: s.creds.StreamCreds}
}
