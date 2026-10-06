// SPDX-License-Identifier: AGPL-3.0-only

package nugs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

const (
	// userAgent names us honestly; nugs.net doesn't require an app's.
	userAgent = "Syncphony (+https://github.com/madeofpendletonwool/syncphony)"
	// callTimeout bounds a whole API call. Streams aren't bounded, since a
	// song can take a long time to send.
	callTimeout = 30 * time.Second
	// maxResponse caps a JSON response body. Searches for common words
	// run to a few hundred KB.
	maxResponse = 32 << 20
)

// numericID matches the IDs nugs.net uses for shows, tracks and artists.
// Checking them keeps what users send out of URL paths.
var numericID = regexp.MustCompile(`^[0-9]{1,12}$`)

// newRequest builds a GET request. token may be empty.
func newRequest(ctx context.Context, rawURL, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// do sends req and maps transport failures and HTTP errors to provider
// errors. authed says whether the request carried the account's token, so
// a 401 or 403 means it's no longer good; elsewhere (a signed audio URL
// that's expired, say) it doesn't. On success the caller owns the body.
func (p *Provider) do(req *http.Request, what string, authed bool) (*http.Response, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// url.Error's message includes the URL, and stream URLs carry the
		// account's IDs. Keep only the cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("nugs %s: %w: %w", what, provider.ErrUnavailable, err)
	}
	if resp.StatusCode < 400 {
		return resp, nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		if authed {
			return nil, fmt.Errorf("nugs %s: HTTP %d: %w", what, resp.StatusCode, provider.ErrAuthExpired)
		}
	case http.StatusNotFound, http.StatusGone:
		return nil, fmt.Errorf("nugs %s: %w", what, provider.ErrNotFound)
	case http.StatusRequestedRangeNotSatisfiable:
		return nil, fmt.Errorf("nugs %s: %w", what, provider.ErrRange)
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("nugs %s: %w", what, &provider.RateLimitError{RetryAfter: retryAfter(resp.Header.Get("Retry-After"))})
	}
	return nil, fmt.Errorf("nugs %s: HTTP %d: %w", what, resp.StatusCode, provider.ErrUnavailable)
}

// getJSON fetches rawURL and decodes its JSON body into v. A 204 (how the
// catalog says a show doesn't exist) is ErrNotFound.
func (p *Provider) getJSON(ctx context.Context, rawURL, token, what string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := newRequest(ctx, rawURL, token)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.do(req, what, token != "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return fmt.Errorf("nugs %s: %w", what, provider.ErrNotFound)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("nugs %s: %w: %w", what, provider.ErrUnavailable, err)
	}
	if err := json.Unmarshal(unwrapJSONP(body), v); err != nil {
		return fmt.Errorf("nugs %s: %w: unexpected response", what, provider.ErrUnavailable)
	}
	return nil
}

// unwrapJSONP strips a "callback(...)" wrapper, which the stream API
// sometimes puts around its JSON.
func unwrapJSONP(b []byte) []byte {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] == '{' || b[0] == '[' {
		return b
	}
	start, end := bytes.IndexByte(b, '('), bytes.LastIndexByte(b, ')')
	if start < 0 || end <= start {
		return b
	}
	return b[start+1 : end]
}

// legacy calls a method of the stream host's api.aspx, the catalog API the
// apps search with, and decodes its Response into v.
func (p *Provider) legacy(ctx context.Context, token, method string, params url.Values, v any) error {
	q := url.Values{"method": {method}}
	for k, vs := range params {
		q[k] = vs
	}
	var env struct {
		Code     int             `json:"responseAvailabilityCode"`
		CodeStr  string          `json:"responseAvailabilityCodeStr"`
		Response json.RawMessage `json:"Response"`
	}
	if err := p.getJSON(ctx, p.ep.Stream+"/api.aspx?"+q.Encode(), token, method, &env); err != nil {
		return err
	}
	if env.Code != 0 {
		return fmt.Errorf("nugs %s: %s: %w", method, env.CodeStr, provider.ErrUnavailable)
	}
	if len(env.Response) == 0 || string(env.Response) == "null" {
		return fmt.Errorf("nugs %s: %w", method, provider.ErrNotFound)
	}
	if err := json.Unmarshal(env.Response, v); err != nil {
		return fmt.Errorf("nugs %s: %w: unexpected response", method, provider.ErrUnavailable)
	}
	return nil
}

// fetchShow gets a show's details and tracks from the catalog.
func (p *Provider) fetchShow(ctx context.Context, token, id string) (*show, error) {
	if !numericID.MatchString(id) {
		return nil, fmt.Errorf("nugs: show %q: %w", id, provider.ErrNotFound)
	}
	var env struct {
		Response *show `json:"Response"`
	}
	if err := p.getJSON(ctx, p.ep.Catalog+"/api/v1/shows/"+id, token, "show", &env); err != nil {
		return nil, err
	}
	if env.Response == nil || string(env.Response.ContainerID) != id {
		return nil, fmt.Errorf("nugs: show %s: %w", id, provider.ErrNotFound)
	}
	return env.Response, nil
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

// The JSON shapes we read. nugs.net sends many more fields.

// flexString is an ID or number that nugs.net sends as a JSON number in
// some places and a string in others.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	switch {
	case string(b) == "null":
		*f = ""
	case len(b) > 0 && b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(strings.TrimSpace(s))
	default:
		*f = flexString(b)
	}
	return nil
}

// int returns the value as an integer, or 0.
func (f flexString) int() int {
	n, err := strconv.ParseFloat(string(f), 64)
	if err != nil {
		return 0
	}
	return int(n)
}

// id returns the value as an ID, or "" for the zeros nugs.net uses for none.
func (f flexString) id() string {
	if f == "" || f == "0" {
		return ""
	}
	return string(f)
}

type image struct {
	URL string `json:"url"`
}

// show is a container: usually a concert, sometimes a studio or
// compilation album. The catalog's show details and the legacy API's
// listings share this shape; only details have durations.
type show struct {
	ContainerID     flexString `json:"containerID"`
	ContainerInfo   string     `json:"containerInfo"`
	ArtistID        flexString `json:"artistID"`
	ArtistName      string     `json:"artistName"`
	PerformanceDate string     `json:"performanceDate"`
	PerformanceYear flexString `json:"performanceDateYear"`
	VenueName       string     `json:"venueName"`
	VenueCity       string     `json:"venueCity"`
	VenueState      string     `json:"venueState"`
	Img             *image     `json:"img"`
	// InSubscription is false for shows only sold, not streamed.
	InSubscription *bool       `json:"isInSubscriptionProgram"`
	Tracks         []showTrack `json:"tracks"`
	// Songs is the setlist, without durations.
	Songs []showTrack `json:"songs"`
}

type showTrack struct {
	TrackID   flexString `json:"trackID"`
	SongTitle string     `json:"songTitle"`
	Seconds   flexString `json:"totalRunningTime"`
}

// setlist returns the show's tracks: with durations if it has them.
func (s *show) setlist() []showTrack {
	if len(s.Tracks) > 0 {
		return s.Tracks
	}
	return s.Songs
}

// streamable reports whether the show is in the subscription catalog.
func (s *show) streamable() bool { return s.InSubscription == nil || *s.InSubscription }

type containersAll struct {
	Containers []show `json:"containers"`
}

// Search match types: what a group of results matched on.
const (
	matchArtist = 1
	matchSong   = 2
	matchVenue  = 3
	matchAlbum  = 6
)

type searchResponse struct {
	Types []struct {
		Groups []searchGroup `json:"catalogSearchContainers"`
	} `json:"catalogSearchTypeContainers"`
}

// searchGroup is results that matched the same thing: every performance of
// a song, or every show at a venue.
type searchGroup struct {
	MatchType int          `json:"matchType"`
	Matched   string       `json:"matchedStr"`
	Items     []searchItem `json:"catalogSearchResultItems"`
}

// searchItem is a show, and for song matches the track on it.
type searchItem struct {
	ContainerID     flexString `json:"containerID"`
	ContainerName   string     `json:"containerName"`
	TrackID         flexString `json:"trackID"`
	ArtistID        flexString `json:"artistID"`
	ArtistName      string     `json:"artistName"`
	PerformanceDate string     `json:"performanceDate"`
	VenueName       string     `json:"venueName"`
	VenueCity       string     `json:"venueCity"`
	VenueState      string     `json:"venueState"`
	Img             *image     `json:"img"`
	// Availability is 1 for shows that can be played.
	Availability int `json:"availability"`
}
