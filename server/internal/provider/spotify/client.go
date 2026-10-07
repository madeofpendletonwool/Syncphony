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
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

const (
	// callTimeout bounds a whole API call.
	callTimeout = 30 * time.Second
	// maxResponse caps a JSON response body.
	maxResponse = 8 << 20
)

// apiError is a Web API error response. It wraps the provider error it
// maps to, if any.
type apiError struct {
	path    string
	status  int
	message string
	kind    error
}

func (e *apiError) Error() string {
	msg := fmt.Sprintf("spotify %s: HTTP %d", e.path, e.status)
	if e.message != "" {
		msg += ": " + e.message
	}
	if e.kind != nil {
		msg += ": " + e.kind.Error()
	}
	return msg
}

func (e *apiError) Unwrap() error { return e.kind }

// api GETs a Web API path with an access token and decodes the JSON
// response into out.
func (p *Provider) api(ctx context.Context, token, path string, q url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	u := p.apiURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.client.Do(req)
	if err != nil {
		return transportError(ctx, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return responseError(resp, path)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(out); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("spotify %s: malformed response: %w", path, provider.ErrUnavailable)
	}
	return nil
}

// responseError maps a failed Web API response to an *apiError.
func responseError(resp *http.Response, path string) error {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body)
	e := &apiError{path: path, status: resp.StatusCode, message: body.Error.Message}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		e.kind = provider.ErrAuthExpired
	case resp.StatusCode == http.StatusNotFound:
		e.kind = provider.ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		e.kind = &provider.RateLimitError{RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	case resp.StatusCode >= 500:
		e.kind = provider.ErrUnavailable
	}
	// Other statuses (400, 403) are requests Spotify won't serve. Retrying
	// or relinking won't help, so they map to no provider error.
	return e
}

// transportError maps a failed round trip to ErrUnavailable.
func transportError(ctx context.Context, what string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	// Keep only the cause: url.Error's message repeats the whole URL.
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("spotify %s: %w: %w", what, provider.ErrUnavailable, err)
}

// get is api with the app's token. If Spotify rejects the token before it
// was due to expire, it's replaced once and the call retried. A rejection
// after that is the app's problem, not the user's, so it's ErrUnavailable
// rather than ErrAuthExpired: relinking wouldn't help.
func (s *session) get(ctx context.Context, path string, q url.Values, out any) error {
	tok, err := s.p.appToken(ctx, "")
	if err != nil {
		return err
	}
	err = s.p.api(ctx, tok, path, q, out)
	if !errors.Is(err, provider.ErrAuthExpired) {
		return err
	}
	if tok, err = s.p.appToken(ctx, tok); err != nil {
		return err
	}
	err = s.p.api(ctx, tok, path, q, out)
	if errors.Is(err, provider.ErrAuthExpired) {
		return fmt.Errorf("spotify %s: the app's token was refused: %w", path, provider.ErrUnavailable)
	}
	return err
}

func retryAfter(h string) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		return max(time.Until(t), 0)
	}
	return 0
}

// The JSON shapes we read. Spotify sends many more fields.

// Image is one size of an image. Width is 0 when Spotify doesn't say.
type Image struct {
	URL   string `json:"url"`
	Width int    `json:"width"`
}

type artistCredit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type artist struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Images []Image `json:"images"`
}

type album struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Artists     []artistCredit `json:"artists"`
	ReleaseDate string         `json:"release_date"` // "2024", "2024-03" or "2024-03-01"
	TotalTracks int            `json:"total_tracks"`
	Images      []Image        `json:"images"`
	// Tracks is only set by GET /albums/{id}, and its tracks have no album.
	Tracks *paging[track] `json:"tracks"`
}

type track struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	DurationMS int            `json:"duration_ms"`
	Explicit   bool           `json:"explicit"`
	IsLocal    bool           `json:"is_local"`
	Artists    []artistCredit `json:"artists"`
	Album      *album         `json:"album"`
}

type paging[T any] struct {
	Items []T     `json:"items"`
	Next  *string `json:"next"`
	Total int     `json:"total"`
}

// playlist is a simplified playlist object, as search returns them.
type playlist struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Owner struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"owner"`
	Images []Image `json:"images"`
	// Its length is under "items" for development-mode apps since February
	// 2026, and "tracks" before.
	Items  *struct{ Total int } `json:"items"`
	Tracks *struct{ Total int } `json:"tracks"`
}

type searchResponse struct {
	Tracks  *paging[track]  `json:"tracks"`
	Albums  *paging[album]  `json:"albums"`
	Artists *paging[artist] `json:"artists"`
	// Playlists can hold nulls, which decode as zero playlists.
	Playlists *paging[playlist] `json:"playlists"`
}
