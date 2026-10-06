// SPDX-License-Identifier: AGPL-3.0-only

package musicbrainz

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

// DefaultURL is the MusicBrainz web service.
const DefaultURL = "https://musicbrainz.org"

const (
	// requestTimeout bounds one request, including the wait for our turn.
	requestTimeout = 30 * time.Second
	maxResponse    = 4 << 20
)

// errNotFound means MusicBrainz has no such entity.
var errNotFound = errors.New("musicbrainz: not found")

// client is a MusicBrainz web service client that keeps to its rate
// limit, one request a second, across every caller.
type client struct {
	base      string
	userAgent string
	http      *http.Client
	interval  time.Duration

	mu   sync.Mutex
	next time.Time // when the next request may go
}

// wait blocks until it's our turn to send a request.
func (c *client) wait(ctx context.Context) error {
	c.mu.Lock()
	now := time.Now()
	at := now
	if c.next.After(now) {
		at = c.next
	}
	c.next = at.Add(c.interval)
	c.mu.Unlock()
	if d := at.Sub(now); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// backOff pushes the next request back, after MusicBrainz said we're too fast.
func (c *client) backOff(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if until := time.Now().Add(d); until.After(c.next) {
		c.next = until
	}
}

// get fetches path (under /ws/2) with params, decoding the JSON into v.
func (c *client) get(ctx context.Context, path string, params url.Values, v any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := c.wait(ctx); err != nil {
		return err
	}
	params.Set("fmt", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/ws/2/"+path+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("musicbrainz: %w: %w", provider.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusBadRequest:
		// 400 is an MBID or ISRC MusicBrainz finds malformed.
		return errNotFound
	case http.StatusServiceUnavailable, http.StatusTooManyRequests:
		c.backOff(5 * time.Second)
		return fmt.Errorf("musicbrainz: %w", provider.ErrRateLimited)
	default:
		return fmt.Errorf("musicbrainz: HTTP %d: %w", resp.StatusCode, provider.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(v); err != nil {
		return fmt.Errorf("musicbrainz: decoding %s: %w: %w", path, provider.ErrUnavailable, err)
	}
	return nil
}

// inc asks for what picking a release needs.
const inc = "artists+releases+release-groups"

func (c *client) recording(ctx context.Context, mbid string) (recording, error) {
	var r recording
	err := c.get(ctx, "recording/"+url.PathEscape(mbid), url.Values{"inc": {inc}}, &r)
	return r, err
}

func (c *client) isrc(ctx context.Context, isrc string) ([]recording, error) {
	var r struct {
		Recordings []recording `json:"recordings"`
	}
	err := c.get(ctx, "isrc/"+url.PathEscape(isrc), url.Values{"inc": {inc}}, &r)
	return r.Recordings, err
}

// search finds recordings by title and artist. Results carry their
// releases without asking.
func (c *client) search(ctx context.Context, title, artist string) ([]recording, error) {
	q := "recording:" + quote(title)
	if artist != "" {
		q += " AND artist:" + quote(artist)
	}
	var r struct {
		Recordings []recording `json:"recordings"`
	}
	err := c.get(ctx, "recording", url.Values{"query": {q}, "limit": {"10"}}, &r)
	return r.Recordings, err
}

// quote makes s a Lucene phrase.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// The JSON shapes we read. MusicBrainz sends many more fields.

type recording struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Length       int64          `json:"length"` // ms
	Score        int            `json:"score"`  // search only, 0-100
	ArtistCredit []artistCredit `json:"artist-credit"`
	Releases     []release      `json:"releases"`
}

type artistCredit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
}

type release struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	Date         string `json:"date"`
	ReleaseGroup struct {
		ID             string   `json:"id"`
		PrimaryType    string   `json:"primary-type"`
		SecondaryTypes []string `json:"secondary-types"`
	} `json:"release-group"`
}
