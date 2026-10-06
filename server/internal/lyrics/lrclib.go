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

// lrclibTimeout bounds one LRCLIB lookup.
const lrclibTimeout = 10 * time.Second

// LRCLIB is a client for LRCLIB (https://lrclib.net), a free, keyless
// lyrics database that also ships as a self-hostable dump.
type LRCLIB struct {
	// BaseURL is the instance, without /api. Default DefaultLRCLIB.
	BaseURL string
	// UserAgent identifies us, as LRCLIB asks clients to.
	UserAgent string
	Client    *http.Client
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/get?"+q.Encode(), nil)
	if err != nil {
		return Found{}, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Found{}, fmt.Errorf("lrclib: %w: %w", provider.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Found{}, errNoMatch
	case resp.StatusCode == http.StatusTooManyRequests:
		return Found{}, fmt.Errorf("lrclib: %w", provider.ErrRateLimited)
	case resp.StatusCode != http.StatusOK:
		return Found{}, fmt.Errorf("lrclib: HTTP %d: %w", resp.StatusCode, provider.ErrUnavailable)
	}
	var r struct {
		Instrumental bool    `json:"instrumental"`
		PlainLyrics  *string `json:"plainLyrics"`
		SyncedLyrics *string `json:"syncedLyrics"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
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
