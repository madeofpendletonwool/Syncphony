// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// DefaultLastFMURL is Last.fm's API.
const DefaultLastFMURL = "https://ws.audioscrobbler.com/2.0/"

// Last.fm error codes we tell apart. See https://www.last.fm/api/errorcodes.
const (
	lastfmInvalidParams = 6 // "The artist you supplied could not be found", too
	lastfmRateLimited   = 29
)

// LastFM asks Last.fm: similar artists with how alike they are, an
// artist's top songs by plays, their top tags, and songs like a song. It
// needs an API key (free, https://www.last.fm/api/account/create), and
// keeps to five requests a second.
type LastFM struct {
	key  string
	base string
	c    *client
}

// LastFMOptions configure a LastFM.
type LastFMOptions struct {
	Key       string
	UserAgent string
	// BaseURL is the API. Default DefaultLastFMURL.
	BaseURL string
	// Client makes the requests. Default http.DefaultClient.
	Client *http.Client
	// Interval is the least time between requests. Default 200ms;
	// negative means none, for tests.
	Interval time.Duration
}

// NewLastFM returns a LastFM.
func NewLastFM(opts LastFMOptions) *LastFM {
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultLastFMURL
	}
	if opts.Interval == 0 {
		opts.Interval = 200 * time.Millisecond
	}
	return &LastFM{key: opts.Key, base: opts.BaseURL, c: newClient("lastfm", opts.UserAgent, opts.Client, opts.Interval)}
}

// Name implements Source.
func (*LastFM) Name() string { return "lastfm" }

// Artist implements Source with artist.getSimilar, artist.getTopTracks
// and artist.getTopTags.
func (l *LastFM) Artist(ctx context.Context, a ArtistRef) (Artist, error) {
	if a.Name == "" {
		return Artist{}, provider.ErrUnsupported
	}
	out := Artist{Ref: a}
	var sim struct {
		SimilarArtists struct {
			Artist []struct {
				Name  string `json:"name"`
				MBID  string `json:"mbid"`
				Match number `json:"match"`
			} `json:"artist"`
		} `json:"similarartists"`
	}
	if err := l.call(ctx, "artist.getSimilar", url.Values{"artist": {a.Name}, "limit": {"100"}}, &sim); err != nil {
		return Artist{}, err
	}
	for _, s := range sim.SimilarArtists.Artist {
		out.Similar = append(out.Similar, Similar{Artist: ArtistRef{Name: s.Name, MBID: s.MBID}, Score: float64(s.Match)})
	}

	var top struct {
		TopTracks struct {
			Track []struct {
				Name      string `json:"name"`
				MBID      string `json:"mbid"`
				Playcount number `json:"playcount"`
			} `json:"track"`
		} `json:"toptracks"`
	}
	if err := l.call(ctx, "artist.getTopTracks", url.Values{"artist": {a.Name}, "limit": {"50"}}, &top); err != nil {
		return Artist{}, err
	}
	most := 0.0
	for _, t := range top.TopTracks.Track {
		most = max(most, float64(t.Playcount))
	}
	for _, t := range top.TopTracks.Track {
		out.Top = append(out.Top, Song{SongRef: SongRef{Title: t.Name, Artist: a, MBID: t.MBID}, Score: share(float64(t.Playcount), most)})
	}

	var tags struct {
		TopTags struct {
			Tag []struct {
				Name  string `json:"name"`
				Count number `json:"count"` // 0–100, relative to the top tag
			} `json:"tag"`
		} `json:"toptags"`
	}
	if err := l.call(ctx, "artist.getTopTags", url.Values{"artist": {a.Name}}, &tags); err != nil {
		return Artist{}, err
	}
	for _, t := range tags.TopTags.Tag {
		if t.Count > 0 {
			out.Tags = append(out.Tags, Tag{Name: t.Name, Weight: min(float64(t.Count)/100, 1)})
		}
	}
	if len(out.Similar)+len(out.Top)+len(out.Tags) == 0 {
		return Artist{}, fmt.Errorf("lastfm: artist %q: %w", a.Name, provider.ErrNotFound)
	}
	return out, nil
}

// Track implements Source with track.getSimilar.
func (l *LastFM) Track(ctx context.Context, s SongRef) (Track, error) {
	if s.Title == "" || s.Artist.Name == "" {
		return Track{}, provider.ErrUnsupported
	}
	var sim struct {
		SimilarTracks struct {
			Track []struct {
				Name   string `json:"name"`
				MBID   string `json:"mbid"`
				Match  number `json:"match"`
				Artist struct {
					Name string `json:"name"`
					MBID string `json:"mbid"`
				} `json:"artist"`
			} `json:"track"`
		} `json:"similartracks"`
	}
	if err := l.call(ctx, "track.getSimilar", url.Values{"artist": {s.Artist.Name}, "track": {s.Title}, "limit": {"50"}}, &sim); err != nil {
		return Track{}, err
	}
	out := Track{Ref: s}
	for _, t := range sim.SimilarTracks.Track {
		out.Similar = append(out.Similar, Song{
			SongRef: SongRef{Title: t.Name, MBID: t.MBID, Artist: ArtistRef{Name: t.Artist.Name, MBID: t.Artist.MBID}},
			Score:   float64(t.Match),
		})
	}
	if len(out.Similar) == 0 {
		return Track{}, fmt.Errorf("lastfm: song %q: %w", s.Title, provider.ErrNotFound)
	}
	return out, nil
}

// call calls an API method. Last.fm reports errors in the body, often
// with a 200.
func (l *LastFM) call(ctx context.Context, method string, params url.Values, v any) error {
	params.Set("method", method)
	params.Set("api_key", l.key)
	params.Set("format", "json")
	params.Set("autocorrect", "1")
	return l.c.get(ctx, l.base, params, func(status int, body io.Reader) error {
		raw, err := io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("lastfm: %w: %w", provider.ErrUnavailable, err)
		}
		var e struct {
			Error   int    `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		switch {
		case e.Error == lastfmInvalidParams:
			return fmt.Errorf("lastfm %s: %s: %w", method, e.Message, provider.ErrNotFound)
		case e.Error == lastfmRateLimited:
			l.c.backOff(backOffFor)
			return fmt.Errorf("lastfm %s: %w", method, &provider.RateLimitError{RetryAfter: backOffFor})
		case e.Error != 0:
			return fmt.Errorf("lastfm %s: error %d: %s: %w", method, e.Error, strings.TrimSpace(e.Message), provider.ErrUnavailable)
		case status != http.StatusOK:
			return fmt.Errorf("lastfm %s: HTTP %d: %w", method, status, provider.ErrUnavailable)
		}
		if err := json.Unmarshal(raw, v); err != nil {
			return fmt.Errorf("lastfm %s: decoding: %w: %w", method, provider.ErrUnavailable, err)
		}
		return nil
	})
}

var _ Source = (*LastFM)(nil)
