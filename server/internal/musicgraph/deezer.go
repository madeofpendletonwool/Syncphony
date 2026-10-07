// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// DefaultDeezerURL is Deezer's public API.
const DefaultDeezerURL = "https://api.deezer.com"

// Deezer error codes we tell apart.
const (
	deezerQuota  = 4
	deezerNoData = 800
)

// deezerTopRank is about the most a song's rank goes: Deezer's popularity
// across all its music.
const deezerTopRank = 1_000_000

// Deezer asks Deezer's public API, which needs no account: an artist's
// related artists and top songs, and a song's tempo and popularity. It
// keeps under Deezer's 50 requests per 5 seconds.
type Deezer struct {
	base string
	c    *client
}

// DeezerOptions configure a Deezer.
type DeezerOptions struct {
	UserAgent string
	// BaseURL is the API. Default DefaultDeezerURL.
	BaseURL string
	// Client makes the requests. Default http.DefaultClient.
	Client *http.Client
	// Interval is the least time between requests. Default 120ms;
	// negative means none, for tests.
	Interval time.Duration
}

// NewDeezer returns a Deezer.
func NewDeezer(opts DeezerOptions) *Deezer {
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultDeezerURL
	}
	if opts.Interval == 0 {
		opts.Interval = 120 * time.Millisecond
	}
	return &Deezer{base: trimSlash(opts.BaseURL), c: newClient("deezer", opts.UserAgent, opts.Client, opts.Interval)}
}

// Name implements Source.
func (*Deezer) Name() string { return "deezer" }

type dzArtist struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Fans int64  `json:"nb_fan"`
}

type dzTrack struct {
	ID          int64   `json:"id"`
	Title       string  `json:"title"`
	ISRC        string  `json:"isrc"`
	Duration    int     `json:"duration"` // seconds
	Rank        float64 `json:"rank"`
	BPM         float64 `json:"bpm"`
	ReleaseDate string  `json:"release_date"`
	Artist      struct {
		Name string `json:"name"`
	} `json:"artist"`
}

// Artist implements Source with artist search, then the artist's related
// artists and top songs.
func (d *Deezer) Artist(ctx context.Context, a ArtistRef) (Artist, error) {
	if a.Name == "" {
		return Artist{}, provider.ErrUnsupported
	}
	var found struct {
		Data []dzArtist `json:"data"`
	}
	if err := d.get(ctx, "/search/artist", url.Values{"q": {a.Name}, "limit": {"10"}}, &found); err != nil {
		return Artist{}, err
	}
	// The same name can be more than one artist: take the best known.
	want := match.Simplify(a.Name)
	var id dzArtist
	for _, c := range found.Data {
		if match.Simplify(c.Name) == want && c.Fans > id.Fans {
			id = c
		}
	}
	if id.ID == 0 {
		return Artist{}, fmt.Errorf("deezer: artist %q: %w", a.Name, provider.ErrNotFound)
	}
	path := "/artist/" + strconv.FormatInt(id.ID, 10)
	out := Artist{Ref: a}

	var related struct {
		Data []dzArtist `json:"data"`
	}
	if err := d.get(ctx, path+"/related", url.Values{"limit": {"50"}}, &related); err != nil {
		return Artist{}, err
	}
	for i, r := range related.Data {
		out.Similar = append(out.Similar, Similar{Artist: ArtistRef{Name: r.Name}, Score: byRank(i, len(related.Data))})
	}

	var top struct {
		Data []dzTrack `json:"data"`
	}
	if err := d.get(ctx, path+"/top", url.Values{"limit": {"50"}}, &top); err != nil {
		return Artist{}, err
	}
	most := 1.0
	for _, t := range top.Data {
		most = max(most, t.Rank)
	}
	for _, t := range top.Data {
		// Deezer's rank is already a compressed score, so it's compared as is.
		out.Top = append(out.Top, Song{SongRef: SongRef{Title: t.Title, Artist: a, ISRC: t.ISRC}, Score: t.Rank / most})
	}
	return out, nil
}

// Track implements Source: the song by ISRC, or else by searching, for
// its tempo, popularity and release date.
func (d *Deezer) Track(ctx context.Context, s SongRef) (Track, error) {
	if s.Title == "" || s.Artist.Name == "" {
		return Track{}, provider.ErrUnsupported
	}
	var t dzTrack
	err := provider.ErrNotFound
	if s.ISRC != "" {
		err = d.get(ctx, "/track/isrc:"+url.PathEscape(s.ISRC), nil, &t)
	}
	if isNotFound(err) {
		var id int64
		if id, err = d.search(ctx, s); err == nil {
			err = d.get(ctx, "/track/"+strconv.FormatInt(id, 10), nil, &t)
		}
	}
	if err != nil {
		return Track{}, err
	}
	out := Track{Ref: s, BPM: t.BPM, Rank: min(t.Rank/deezerTopRank, 1)}
	if len(t.ReleaseDate) >= 4 {
		out.Year, _ = strconv.Atoi(t.ReleaseDate[:4])
	}
	if out.BPM == 0 && out.Rank == 0 && out.Year == 0 {
		return Track{}, fmt.Errorf("deezer: song %q: %w", s.Title, provider.ErrNotFound)
	}
	return out, nil
}

// search finds a song's Deezer ID by its artist and title, matched the
// way cross-service matching matches.
func (d *Deezer) search(ctx context.Context, s SongRef) (int64, error) {
	var found struct {
		Data []dzTrack `json:"data"`
	}
	if err := d.get(ctx, "/search", url.Values{"q": {s.Artist.Name + " " + s.Title}, "limit": {"10"}}, &found); err != nil {
		return 0, err
	}
	want := provider.Track{Title: s.Title, ISRC: s.ISRC, Artists: []provider.ArtistCredit{{Name: s.Artist.Name}}}
	var best int64
	top := 0.0
	for _, t := range found.Data {
		got := provider.Track{Title: t.Title, Artists: []provider.ArtistCredit{{Name: t.Artist.Name}}}
		if sc, ok := match.Score(want, got); ok && sc > top {
			best, top = t.ID, sc
		}
	}
	if best == 0 {
		return 0, fmt.Errorf("deezer: song %q: %w", s.Title, provider.ErrNotFound)
	}
	return best, nil
}

// get fetches path into v. Deezer reports errors in the body, with a 200.
func (d *Deezer) get(ctx context.Context, path string, params url.Values, v any) error {
	return d.c.get(ctx, d.base+path, params, func(status int, body io.Reader) error {
		if status != http.StatusOK {
			return fmt.Errorf("deezer %s: HTTP %d: %w", path, status, provider.ErrUnavailable)
		}
		raw, err := io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("deezer: %w: %w", provider.ErrUnavailable, err)
		}
		var e struct {
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		switch {
		case e.Error == nil:
		case e.Error.Code == deezerNoData:
			return fmt.Errorf("deezer %s: %w", path, provider.ErrNotFound)
		case e.Error.Code == deezerQuota:
			d.c.backOff(backOffFor)
			return fmt.Errorf("deezer %s: %w", path, &provider.RateLimitError{RetryAfter: backOffFor})
		default:
			return fmt.Errorf("deezer %s: error %d: %s: %w", path, e.Error.Code, cmp.Or(e.Error.Message, "unknown"), provider.ErrUnavailable)
		}
		if err := json.Unmarshal(raw, v); err != nil {
			return fmt.Errorf("deezer %s: decoding: %w: %w", path, provider.ErrUnavailable, err)
		}
		return nil
	})
}

var _ Source = (*Deezer)(nil)
