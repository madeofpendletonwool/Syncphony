// SPDX-License-Identifier: AGPL-3.0-only

package navidrome

import (
	"context"
	"crypto/md5" //nolint:gosec // Subsonic token auth is defined as md5(password + salt)
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

const (
	// apiVersion is the Subsonic API version we speak. 1.13.0 added token
	// auth; 1.16.1 is what Navidrome implements.
	apiVersion = "1.16.1"
	clientName = "syncphony"
	// callTimeout bounds a whole API call. Streams aren't bounded, since a
	// song can take a long time to send.
	callTimeout = 30 * time.Second
	// maxResponse caps a JSON response body.
	maxResponse = 32 << 20
)

// api is a Subsonic API client for one account. It's safe for concurrent use.
type api struct {
	client *http.Client
	creds  creds
}

func (p *Provider) connect(c creds) *api { return &api{client: p.client, creds: c} }

// request builds a request for a Subsonic method. The URL carries the
// auth token, so it must never be logged or put in an error.
func (a *api) request(ctx context.Context, method string, params url.Values) (*http.Request, error) {
	q := url.Values{}
	for k, v := range params {
		q[k] = v
	}
	salt := strings.ToLower(rand.Text())[:16]
	sum := md5.Sum([]byte(a.creds.Password + salt)) //nolint:gosec // see import
	q.Set("u", a.creds.Username)
	q.Set("t", hex.EncodeToString(sum[:]))
	q.Set("s", salt)
	q.Set("v", apiVersion)
	q.Set("c", clientName)
	q.Set("f", "json")
	return http.NewRequestWithContext(ctx, http.MethodGet, a.creds.URL+"/rest/"+method+"?"+q.Encode(), nil)
}

// do sends req and maps transport failures and HTTP errors to provider
// errors. On success the caller owns the response body.
func (a *api) do(req *http.Request, method string) (*http.Response, error) {
	resp, err := a.client.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// url.Error's message includes the URL, and so the token. Keep
		// only the cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("navidrome %s: %w: %w", method, provider.ErrUnavailable, err)
	}
	if resp.StatusCode < 400 {
		return resp, nil
	}
	defer resp.Body.Close()
	// Navidrome reports most failures as Subsonic errors with status 200,
	// but some proxies and versions use HTTP statuses.
	if isJSON(resp.Header.Get("Content-Type")) {
		if err := decodeError(resp.Body, method); err != nil {
			return nil, err
		}
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("navidrome %s: HTTP %d: %w", method, resp.StatusCode, provider.ErrAuthExpired)
	case http.StatusRequestedRangeNotSatisfiable:
		return nil, fmt.Errorf("navidrome %s: %w", method, provider.ErrRange)
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("navidrome %s: %w", method, &provider.RateLimitError{RetryAfter: retryAfter(resp.Header.Get("Retry-After"))})
	default:
		return nil, fmt.Errorf("navidrome %s: HTTP %d: %w", method, resp.StatusCode, provider.ErrUnavailable)
	}
}

// call runs a Subsonic method and decodes its JSON response.
func (a *api) call(ctx context.Context, method string, params url.Values) (*response, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := a.request(ctx, method, params)
	if err != nil {
		return nil, err
	}
	resp, err := a.do(req, method)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var env envelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&env); err != nil || env.R.Status == "" {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("navidrome %s: %w: no Subsonic API at this URL", method, provider.ErrUnavailable)
	}
	if err := env.R.err(method); err != nil {
		return nil, err
	}
	return &env.R, nil
}

func (a *api) ping(ctx context.Context) error {
	_, err := a.call(ctx, "ping", nil)
	return err
}

// decodeError reads a Subsonic error from a JSON body. It returns nil if
// the body isn't one.
func decodeError(body io.Reader, method string) error {
	var env envelope
	if err := json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&env); err != nil {
		return nil
	}
	return env.R.err(method)
}

// err maps a failed response to a provider error.
func (r *response) err(method string) error {
	if r.Status == "ok" {
		return nil
	}
	if r.Error == nil {
		return fmt.Errorf("navidrome %s: %w: failed with no error", method, provider.ErrUnavailable)
	}
	var kind error
	switch r.Error.Code {
	case 40, 41, 42, 43, 44:
		// Wrong credentials, or an auth mechanism the server doesn't take
		// (41: token auth is unavailable for LDAP users). Linking turns
		// this into ErrInvalidCredentials.
		kind = provider.ErrAuthExpired
	case 70:
		kind = provider.ErrNotFound
	case 0, 60:
		kind = provider.ErrUnavailable
	default:
		// Bad parameters (10), version mismatch (20, 30), or a permission
		// the user lacks (50). Relinking or retrying won't help.
		return fmt.Errorf("navidrome %s: error %d: %s", method, r.Error.Code, r.Error.Message)
	}
	return fmt.Errorf("navidrome %s: %s: %w", method, r.Error.Message, kind)
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

func mediaType(contentType string) string {
	mt, _, _ := mime.ParseMediaType(contentType)
	return mt
}

func isJSON(contentType string) bool {
	return mediaType(contentType) == "application/json"
}

// The JSON shapes we read. Navidrome sends many more fields.

type envelope struct {
	R response `json:"subsonic-response"`
}

type response struct {
	Status        string         `json:"status"`
	Error         *apiError      `json:"error"`
	SearchResult3 *searchResult3 `json:"searchResult3"`
	Song          *song          `json:"song"`
	Album         *album         `json:"album"`
	Artist        *artist        `json:"artist"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type searchResult3 struct {
	Artist []artist `json:"artist"`
	Album  []album  `json:"album"`
	Song   []song   `json:"song"`
}

type credit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type song struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Album       string `json:"album"`
	AlbumID     string `json:"albumId"`
	Artist      string `json:"artist"`
	ArtistID    string `json:"artistId"`
	Duration    int    `json:"duration"` // seconds
	BitRate     int    `json:"bitRate"`  // kbit/s
	ContentType string `json:"contentType"`
	CoverArt    string `json:"coverArt"`
	// OpenSubsonic fields.
	Artists        []credit `json:"artists"`
	ISRC           []string `json:"isrc"`
	ExplicitStatus string   `json:"explicitStatus"`
}

type album struct {
	ID string `json:"id"`
	// Name is the ID3 (search3, getAlbum) field; Title is the folder-based one.
	Name      string   `json:"name"`
	Title     string   `json:"title"`
	Artist    string   `json:"artist"`
	ArtistID  string   `json:"artistId"`
	Artists   []credit `json:"artists"`
	Year      int      `json:"year"`
	SongCount int      `json:"songCount"`
	CoverArt  string   `json:"coverArt"`
	Song      []song   `json:"song"`
}

type artist struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	CoverArt string  `json:"coverArt"`
	Album    []album `json:"album"`
}
