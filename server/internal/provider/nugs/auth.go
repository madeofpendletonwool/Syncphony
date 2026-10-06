// SPDX-License-Identifier: AGPL-3.0-only

package nugs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

const (
	// clientID is the nugs.net apps' OAuth client, the only one nugs.net
	// lets sign in with a password.
	clientID = "Eg7HuH873H65r5rt325UytR5429"
	scope    = "openid profile email nugsnet:api nugsnet:legacyapi offline_access"
	// tokenSlack refreshes an access token this long before it expires.
	tokenSlack = time.Minute
	// subscriptionTTL is how long an account's plan is trusted before
	// it's checked again.
	subscriptionTTL = time.Hour
	// keepRetired is how many rotated-away refresh tokens a link
	// remembers, to recognize credentials that are merely stale.
	keepRetired = 8
)

// tokenResponse is the token endpoint's answer, or its error.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

// signIn trades an email and password for tokens.
func (p *Provider) signIn(ctx context.Context, email, password string) (tokenResponse, error) {
	tok, err := p.token(ctx, url.Values{
		"grant_type": {"password"},
		"username":   {email},
		"password":   {password},
		"scope":      {scope},
	})
	if err != nil {
		if errors.Is(err, provider.ErrAuthExpired) {
			return tok, fmt.Errorf("%w: nugs.net didn't accept that email and password", provider.ErrInvalidCredentials)
		}
		return tok, err
	}
	if tok.RefreshToken == "" {
		return tok, fmt.Errorf("nugs sign-in: %w: no refresh token", provider.ErrUnavailable)
	}
	return tok, nil
}

// refresh trades a refresh token for new tokens. A token nugs.net no
// longer accepts is ErrAuthExpired.
func (p *Provider) refresh(ctx context.Context, refreshToken string) (tokenResponse, error) {
	return p.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
}

func (p *Provider) token(ctx context.Context, form url.Values) (tokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	form.Set("client_id", clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.ep.Auth+"/connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := p.client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return tokenResponse{}, ctxErr
		}
		return tokenResponse{}, fmt.Errorf("nugs sign-in: %w: %w", provider.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	var tok tokenResponse
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok)
	switch {
	case resp.StatusCode == http.StatusOK && decodeErr == nil && tok.AccessToken != "":
		return tok, nil
	case tok.Error == "invalid_grant":
		// A wrong password, or a refresh token that's expired or revoked.
		return tokenResponse{}, fmt.Errorf("nugs sign-in: %w", provider.ErrAuthExpired)
	case resp.StatusCode == http.StatusTooManyRequests:
		return tokenResponse{}, fmt.Errorf("nugs sign-in: %w", &provider.RateLimitError{RetryAfter: retryAfter(resp.Header.Get("Retry-After"))})
	case tok.Error != "":
		// invalid_client and the like: nugs.net has changed how its apps
		// sign in. Relinking won't help.
		return tokenResponse{}, fmt.Errorf("nugs sign-in: %s: %w", tok.Error, provider.ErrUnavailable)
	default:
		return tokenResponse{}, fmt.Errorf("nugs sign-in: HTTP %d: %w", resp.StatusCode, provider.ErrUnavailable)
	}
}

// userID returns the signed-in account's ID.
func (p *Provider) userID(ctx context.Context, token string) (string, error) {
	var info struct {
		Sub string `json:"sub"`
	}
	if err := p.getJSON(ctx, p.ep.Auth+"/connect/userinfo", token, "user info", &info); err != nil {
		return "", err
	}
	if info.Sub == "" {
		return "", fmt.Errorf("nugs user info: %w: no user ID", provider.ErrUnavailable)
	}
	return info.Sub, nil
}

// subscription is an account's plan. Stream requests carry its IDs and
// dates.
type subscription struct {
	LegacyID flexString `json:"legacySubscriptionId"`
	Plan     *plan      `json:"plan"`
	// Promo holds the plan of trial and promotional accounts.
	Promo *struct {
		Plan *plan `json:"plan"`
	} `json:"promo"`
	StartedAt string `json:"startedAt"`
	EndsAt    string `json:"endsAt"`
}

type plan struct {
	ID flexString `json:"id"`
}

// subscriptionTime is how the subscriptions API writes dates, in UTC.
const subscriptionTime = "1/2/2006 15:04:05"

func (s *subscription) planID() string {
	switch {
	case s == nil:
		return ""
	case s.Plan != nil && s.Plan.ID.id() != "":
		return s.Plan.ID.id()
	case s.Promo != nil && s.Promo.Plan != nil:
		return s.Promo.Plan.ID.id()
	}
	return ""
}

// active reports whether the subscription has a plan that hasn't ended.
func (s *subscription) active(now time.Time) bool {
	if s.planID() == "" {
		return false
	}
	end, err := time.Parse(subscriptionTime, s.EndsAt)
	return err != nil || now.Before(end)
}

// stamp returns a subscription date as a Unix time, or 0.
func stamp(v string) int64 {
	t, err := time.Parse(subscriptionTime, v)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// subscription returns the account's plan. An account without one is a
// subscription with no plan, not an error.
func (p *Provider) subscription(ctx context.Context, token string) (*subscription, error) {
	var s subscription
	err := p.getJSON(ctx, p.ep.Subscriptions+"/api/v1/me/subscriptions", token, "subscription", &s)
	if errors.Is(err, provider.ErrNotFound) {
		return &subscription{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// auth is a link's tokens, shared by its sessions. Refreshes are
// serialized, since nugs.net rotates the refresh token each time.
type auth struct {
	p *Provider

	mu    sync.Mutex
	creds creds
	// retired are refresh tokens rotated away, newest last.
	retired []string
	sink    provider.CredentialSink
	access  string
	expiry  time.Time
	sub     *subscription
	subAt   time.Time
}

// knows reports whether refreshToken is, or was, this link's.
func (a *auth) knows(refreshToken string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if refreshToken == a.creds.RefreshToken {
		return true
	}
	for _, t := range a.retired {
		if t == refreshToken {
			return true
		}
	}
	return false
}

func (a *auth) setSink(s provider.CredentialSink) {
	a.mu.Lock()
	a.sink = s
	a.mu.Unlock()
}

func (a *auth) userID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.creds.UserID
}

// token returns a current access token, refreshing it if need be.
func (a *auth) token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.p.now()
	if a.access != "" && now.Before(a.expiry.Add(-tokenSlack)) {
		return a.access, nil
	}
	tok, err := a.p.refresh(ctx, a.creds.RefreshToken)
	if err != nil {
		return "", err
	}
	a.access = tok.AccessToken
	a.expiry = now.Add(time.Duration(tok.ExpiresIn) * time.Second)
	if tok.RefreshToken != "" && tok.RefreshToken != a.creds.RefreshToken {
		a.retired = append(a.retired, a.creds.RefreshToken)
		if len(a.retired) > keepRetired {
			a.retired = a.retired[len(a.retired)-keepRetired:]
		}
		a.creds.RefreshToken = tok.RefreshToken
		a.save(ctx)
	}
	return a.access, nil
}

// save persists rotated credentials. The old refresh token no longer
// works, so a failure is logged rather than returned: this session can
// carry on with the new one, and the link only breaks if the server
// restarts before the next rotation saves.
func (a *auth) save(ctx context.Context) {
	if a.sink == nil {
		return
	}
	b, err := json.Marshal(a.creds) //nolint:gosec // the sink stores credentials encrypted
	if err == nil {
		err = a.sink(ctx, b)
	}
	if err != nil {
		slog.Warn("saving refreshed nugs.net credentials", "account", a.creds.UserID, "err", err)
	}
}

// subscription returns the account's plan, cached for a while.
func (a *auth) subscription(ctx context.Context) (*subscription, error) {
	token, err := a.token(ctx)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	if a.sub != nil && a.p.now().Sub(a.subAt) < subscriptionTTL {
		defer a.mu.Unlock()
		return a.sub, nil
	}
	a.mu.Unlock()
	sub, err := a.p.subscription(ctx, token)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.sub, a.subAt = sub, a.p.now()
	a.mu.Unlock()
	return sub, nil
}
