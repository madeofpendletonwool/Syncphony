// SPDX-License-Identifier: AGPL-3.0-only

package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// DefaultLRCLIB is the public LRCLIB instance.
const DefaultLRCLIB = "https://lrclib.net"

// lrclibTimeout bounds one LRCLIB lookup, retries included.
const lrclibTimeout = 10 * time.Second

// lrclibAttempts is how many times a lookup is tried. The public instance
// is often briefly overloaded and answers 503 with a Retry-After of a
// second; the next try usually gets through.
const lrclibAttempts = 3

// maxRetryAfter caps how long a Retry-After can make us wait.
const maxRetryAfter = 3 * time.Second

// LRCLIB is a client for LRCLIB (https://lrclib.net), a free, keyless
// lyrics database that also ships as a self-hostable dump.
type LRCLIB struct {
	// BaseURL is the instance, without /api. Default DefaultLRCLIB.
	BaseURL string
	// UserAgent identifies us, as LRCLIB asks clients to.
	UserAgent string
	Client    *http.Client
	// RetryDelay is the wait before trying again when LRCLIB doesn't say
	// how long to wait. Default 500ms.
	RetryDelay time.Duration
}

// Found is what LRCLIB had for a track.
type Found struct {
	provider.Lyrics
	// Instrumental means LRCLIB knows the track and it has no words.
	Instrumental bool
}

// errNoMatch means LRCLIB doesn't know the track.
var errNoMatch = errors.New("lrclib: no match")

// Get looks a track up by its signature: title, artist, album and
// duration. LRCLIB matches the duration to within a couple of seconds. It
// returns errNoMatch if there's no match.
func (c *LRCLIB) Get(ctx context.Context, t provider.Track) (Found, error) {
	if t.Title == "" || len(t.Artists) == 0 {
		return Found{}, errNoMatch
	}
	q := url.Values{"track_name": {t.Title}, "artist_name": {t.Artists[0].Name}}
	if t.Album.Title != "" {
		q.Set("album_name", t.Album.Title)
	}
	if t.Duration > 0 {
		q.Set("duration", strconv.Itoa(int(math.Round(t.Duration.Seconds()))))
	}
	ctx, cancel := context.WithTimeout(ctx, lrclibTimeout)
	defer cancel()
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultLRCLIB
	}
	var (
		f   Found
		err error
	)
	for attempt := 1; ; attempt++ {
		var wait time.Duration
		f, wait, err = c.get(ctx, base+"/api/get?"+q.Encode())
		if wait < 0 || attempt == lrclibAttempts {
			return f, err
		}
		if wait == 0 {
			wait = c.RetryDelay
			if wait <= 0 {
				wait = 500 * time.Millisecond
			}
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
			return f, err
		}
		select {
		case <-ctx.Done():
			return f, err
		case <-time.After(wait):
		}
	}
}

// get makes one lookup. wait says whether it's worth trying again: -1
// means no, 0 means after the usual delay, and more means after that long.
func (c *LRCLIB) get(ctx context.Context, u string) (_ Found, wait time.Duration, _ error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Found{}, -1, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			wait = -1
		}
		return Found{}, wait, fmt.Errorf("lrclib: %w: %w", provider.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Found{}, -1, errNoMatch
	case resp.StatusCode == http.StatusTooManyRequests:
		return Found{}, retryAfter(resp), fmt.Errorf("lrclib: %w", provider.ErrRateLimited)
	case resp.StatusCode == http.StatusServiceUnavailable, resp.StatusCode == http.StatusBadGateway, resp.StatusCode == http.StatusGatewayTimeout:
		return Found{}, retryAfter(resp), fmt.Errorf("lrclib: HTTP %d: %w", resp.StatusCode, provider.ErrUnavailable)
	case resp.StatusCode != http.StatusOK:
		return Found{}, -1, fmt.Errorf("lrclib: HTTP %d: %w", resp.StatusCode, provider.ErrUnavailable)
	}
	f, err := decode(resp.Body)
	return f, -1, err
}

// retryAfter reads a Retry-After given in seconds, capped at
// maxRetryAfter. It's 0 if there's none.
func retryAfter(resp *http.Response) time.Duration {
	n, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || n <= 0 {
		return 0
	}
	return min(time.Duration(n)*time.Second, maxRetryAfter)
}

// decode reads an /api/get answer.
func decode(body io.Reader) (Found, error) {
	var r struct {
		Instrumental bool    `json:"instrumental"`
		PlainLyrics  *string `json:"plainLyrics"`
		SyncedLyrics *string `json:"syncedLyrics"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&r); err != nil {
		return Found{}, fmt.Errorf("lrclib: decoding: %w: %w", provider.ErrUnavailable, err)
	}
	var f Found
	if r.SyncedLyrics != nil {
		f.Synced = ParseLRC(*r.SyncedLyrics)
	}
	if r.PlainLyrics != nil {
		f.Plain = strings.TrimSpace(*r.PlainLyrics)
	}
	if f.Plain == "" && len(f.Synced) > 0 {
		texts := make([]string, len(f.Synced))
		for i, l := range f.Synced {
			texts[i] = l.Text
		}
		f.Plain = strings.Join(texts, "\n")
	}
	f.Instrumental = r.Instrumental && f.Plain == ""
	if f.Plain == "" && !f.Instrumental {
		return Found{}, errNoMatch
	}
	return f, nil
}
