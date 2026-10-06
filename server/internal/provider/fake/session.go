// SPDX-License-Identifier: AGPL-3.0-only

package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

type session struct {
	p    *Provider
	link provider.Link

	mu    sync.Mutex
	creds creds
}

var (
	_ provider.Session        = (*session)(nil)
	_ provider.PlaylistLister = (*session)(nil)
	_ provider.Lyricist       = (*session)(nil)
	_ provider.Streamer       = (*streamSession)(nil)
	_ provider.Remote         = (*remoteSession)(nil)
)

// begin runs before every call: it checks for cancellation, faults and
// revocation, and refreshes an expired OAuth2 token.
func (s *session) begin(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.p.check(s.creds); err != nil {
		return err
	}
	if s.creds.Expires.IsZero() || s.p.opts.Now().Before(s.creds.Expires) {
		return nil
	}
	c := s.p.issue(s.creds.Account)
	if s.link.Sink != nil {
		b, err := json.Marshal(c)
		if err != nil {
			return err
		}
		if err := s.link.Sink(ctx, b); err != nil {
			return fmt.Errorf("saving refreshed credentials: %w", err)
		}
	}
	s.creds = c
	return nil
}

func (s *session) Search(ctx context.Context, q provider.SearchQuery) (provider.SearchPage, error) {
	if err := s.begin(ctx); err != nil {
		return provider.SearchPage{}, err
	}
	kinds := q.Kinds
	if len(kinds) == 0 {
		kinds = s.p.Info().Capabilities.Search
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := 0
	if q.Cursor != "" {
		n, err := strconv.Atoi(q.Cursor)
		if err != nil || n < 0 {
			return provider.SearchPage{}, fmt.Errorf("fake: bad cursor %q", q.Cursor)
		}
		offset = n
	}
	text := strings.ToLower(strings.TrimSpace(q.Text))
	match := func(ss ...string) bool {
		return slices.ContainsFunc(ss, func(s string) bool { return strings.Contains(strings.ToLower(s), text) })
	}

	var page provider.SearchPage
	more := false
	for _, k := range kinds {
		switch k {
		case provider.KindTrack:
			ts := filter(library.tracks, func(t *track) bool { return match(t.title, t.album.title, t.album.artist.name) })
			more = window(&ts, offset, limit) || more
			for _, t := range ts {
				page.Tracks = append(page.Tracks, s.track(t))
			}
		case provider.KindAlbum:
			as := filter(library.albums, func(a *album) bool { return match(a.title, a.artist.name) })
			more = window(&as, offset, limit) || more
			for _, a := range as {
				page.Albums = append(page.Albums, toAlbum(a))
			}
		case provider.KindArtist:
			as := filter(library.artists, func(a *artist) bool { return match(a.name) })
			more = window(&as, offset, limit) || more
			for _, a := range as {
				page.Artists = append(page.Artists, toArtist(a))
			}
		case provider.KindPlaylist:
			for _, pl := range playlists {
				if match(pl.name) {
					page.Playlists = append(page.Playlists, toPlaylist(pl.id, pl.name, len(pl.tracks())))
				}
			}
		default:
			return provider.SearchPage{}, fmt.Errorf("fake: search %q: %w", k, provider.ErrUnsupported)
		}
	}
	if more {
		page.Next = strconv.Itoa(offset + limit)
	}
	return page, nil
}

func filter[T any](xs []T, keep func(T) bool) []T {
	var out []T
	for _, x := range xs {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}

// window trims xs to [offset, offset+limit) and reports whether more follow.
func window[T any](xs *[]T, offset, limit int) bool {
	s := *xs
	if offset >= len(s) {
		*xs = nil
		return false
	}
	s = s[offset:]
	more := len(s) > limit
	if more {
		s = s[:limit]
	}
	*xs = s
	return more
}

func (s *session) track(t *track) provider.Track {
	return provider.Track{
		Ref:      provider.TrackRef{Provider: s.p.opts.ID, LinkID: s.link.ID, ID: t.id},
		Title:    t.title,
		Artists:  []provider.ArtistCredit{{ID: t.album.artist.id, Name: t.album.artist.name}},
		Album:    provider.AlbumCredit{ID: t.album.id, Title: t.album.title},
		Duration: t.duration,
		ISRC:     t.isrc,
		Explicit: t.explicit,
		Artwork:  provider.ArtworkRef(t.album.id),
	}
}

func toAlbum(a *album) provider.Album {
	return provider.Album{
		ID:         a.id,
		Title:      a.title,
		Artists:    []provider.ArtistCredit{{ID: a.artist.id, Name: a.artist.name}},
		Year:       a.year,
		TrackCount: len(a.tracks),
		Artwork:    provider.ArtworkRef(a.id),
	}
}

func toArtist(a *artist) provider.Artist {
	return provider.Artist{ID: a.id, Name: a.name, Artwork: provider.ArtworkRef(a.id)}
}

func toPlaylist(id, name string, n int) provider.Playlist {
	return provider.Playlist{ID: id, Name: name, Owner: Username, TrackCount: n, Artwork: provider.ArtworkRef(id)}
}

func (s *session) Track(ctx context.Context, id string) (provider.Track, error) {
	if err := s.begin(ctx); err != nil {
		return provider.Track{}, err
	}
	t, ok := get[*track](library, id)
	if !ok {
		return provider.Track{}, fmt.Errorf("fake: track %q: %w", id, provider.ErrNotFound)
	}
	return s.track(t), nil
}

func (s *session) Album(ctx context.Context, id string) (provider.Album, []provider.Track, error) {
	if err := s.begin(ctx); err != nil {
		return provider.Album{}, nil, err
	}
	a, ok := get[*album](library, id)
	if !ok {
		return provider.Album{}, nil, fmt.Errorf("fake: album %q: %w", id, provider.ErrNotFound)
	}
	ts := make([]provider.Track, len(a.tracks))
	for i, t := range a.tracks {
		ts[i] = s.track(t)
	}
	return toAlbum(a), ts, nil
}

func (s *session) Artist(ctx context.Context, id string) (provider.Artist, []provider.Album, error) {
	if err := s.begin(ctx); err != nil {
		return provider.Artist{}, nil, err
	}
	a, ok := get[*artist](library, id)
	if !ok {
		return provider.Artist{}, nil, fmt.Errorf("fake: artist %q: %w", id, provider.ErrNotFound)
	}
	as := make([]provider.Album, len(a.albums))
	for i, al := range a.albums {
		as[i] = toAlbum(al)
	}
	return toArtist(a), as, nil
}

func (s *session) Artwork(ctx context.Context, ref provider.ArtworkRef, _ int) (io.ReadCloser, string, error) {
	if err := s.begin(ctx); err != nil {
		return nil, "", err
	}
	label, ok := artworkLabel(string(ref))
	if !ok {
		return nil, "", fmt.Errorf("fake: artwork %q: %w", ref, provider.ErrNotFound)
	}
	return io.NopCloser(bytes.NewReader(artworkSVG(string(ref), label))), "image/svg+xml", nil
}

func artworkLabel(id string) (string, bool) {
	switch v := library.byID[id].(type) {
	case *album:
		return v.title, true
	case *artist:
		return v.name, true
	}
	for _, pl := range playlists {
		if pl.id == id {
			return pl.name, true
		}
	}
	return "", false
}

func (s *session) Close() error { return nil }

func (s *session) Playlists(ctx context.Context, cursor string) (provider.Page[provider.Playlist], error) {
	if err := s.begin(ctx); err != nil {
		return provider.Page[provider.Playlist]{}, err
	}
	if cursor != "" {
		return provider.Page[provider.Playlist]{}, fmt.Errorf("fake: bad cursor %q", cursor)
	}
	var page provider.Page[provider.Playlist]
	for _, pl := range playlists {
		page.Items = append(page.Items, toPlaylist(pl.id, pl.name, len(pl.tracks())))
	}
	return page, nil
}

// playlistPageSize is small so paging gets exercised.
const playlistPageSize = 5

func (s *session) PlaylistTracks(ctx context.Context, id, cursor string) (provider.Page[provider.Track], error) {
	if err := s.begin(ctx); err != nil {
		return provider.Page[provider.Track]{}, err
	}
	i := slices.IndexFunc(playlists, func(pl playlist) bool { return pl.id == id })
	if i < 0 {
		return provider.Page[provider.Track]{}, fmt.Errorf("fake: playlist %q: %w", id, provider.ErrNotFound)
	}
	offset := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 0 {
			return provider.Page[provider.Track]{}, fmt.Errorf("fake: bad cursor %q", cursor)
		}
		offset = n
	}
	ts := playlists[i].tracks()
	var page provider.Page[provider.Track]
	if window(&ts, offset, playlistPageSize) {
		page.Next = strconv.Itoa(offset + playlistPageSize)
	}
	for _, t := range ts {
		page.Items = append(page.Items, s.track(t))
	}
	return page, nil
}

func (s *session) Lyrics(ctx context.Context, trackID string) (provider.Lyrics, error) {
	if err := s.begin(ctx); err != nil {
		return provider.Lyrics{}, err
	}
	t, ok := get[*track](library, trackID)
	if !ok {
		return provider.Lyrics{}, fmt.Errorf("fake: track %q: %w", trackID, provider.ErrNotFound)
	}
	var l provider.Lyrics
	var plain []string
	for at := time.Duration(0); at < t.duration; at += 5 * time.Second {
		line := fmt.Sprintf("%s, %.0f hertz", t.title, t.hz)
		plain = append(plain, line)
		l.Synced = append(l.Synced, provider.LyricLine{At: at, Text: line})
	}
	l.Plain = strings.Join(plain, "\n")
	return l, nil
}

type streamSession struct{ *session }

// CheckPlayable implements provider.PlayChecker.
func (s *streamSession) CheckPlayable(ctx context.Context, trackID string) error {
	_, err := s.playable(ctx, trackID)
	return err
}

func (s *streamSession) playable(ctx context.Context, trackID string) (*track, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	t, ok := get[*track](library, trackID)
	if !ok {
		return nil, fmt.Errorf("fake: track %q: %w", trackID, provider.ErrNotFound)
	}
	if slices.Contains(s.p.opts.NotPlayable, trackID) {
		return nil, fmt.Errorf("fake: track %q: %w", trackID, provider.ErrNotPlayable)
	}
	return t, nil
}

func (s *streamSession) Stream(ctx context.Context, trackID string, opts provider.StreamOpts) (*provider.AudioStream, error) {
	t, err := s.playable(ctx, trackID)
	if err != nil {
		return nil, err
	}
	b := wav(t.hz, t.duration)
	size := int64(len(b))
	start, end := int64(0), size-1
	if r := opts.Range; r != nil {
		if r.Start < 0 || r.Start >= size || (r.End >= 0 && r.End < r.Start) {
			return nil, fmt.Errorf("fake: range %d-%d of %d: %w", r.Start, r.End, size, provider.ErrRange)
		}
		start = r.Start
		if r.End >= 0 && r.End < end {
			end = r.End
		}
	}
	return &provider.AudioStream{
		Body:        io.NopCloser(bytes.NewReader(b[start : end+1])),
		ContentType: "audio/wav",
		Offset:      start,
		Length:      end - start + 1,
		Size:        size,
		Seekable:    true,
	}, nil
}

type remoteSession struct {
	*session

	pmu     sync.Mutex
	trackID string
	playing bool
	pos     time.Duration // position at `at`
	at      time.Time
}

func (s *remoteSession) Play(ctx context.Context, trackID string, at time.Duration) error {
	if err := s.begin(ctx); err != nil {
		return err
	}
	if _, ok := get[*track](library, trackID); !ok {
		return fmt.Errorf("fake: track %q: %w", trackID, provider.ErrNotFound)
	}
	s.pmu.Lock()
	defer s.pmu.Unlock()
	s.trackID, s.playing, s.pos, s.at = trackID, true, at, s.p.opts.Now()
	return nil
}

func (s *remoteSession) Pause(ctx context.Context) error {
	return s.update(ctx, func() { s.playing = false })
}

func (s *remoteSession) Resume(ctx context.Context) error {
	return s.update(ctx, func() { s.playing = s.trackID != "" })
}

func (s *remoteSession) Seek(ctx context.Context, to time.Duration) error {
	return s.update(ctx, func() { s.pos = to })
}

// update brings the position up to date, then applies f.
func (s *remoteSession) update(ctx context.Context, f func()) error {
	if err := s.begin(ctx); err != nil {
		return err
	}
	s.pmu.Lock()
	defer s.pmu.Unlock()
	s.advance()
	f()
	return nil
}

// advance moves pos to now. The caller holds pmu.
func (s *remoteSession) advance() {
	now := s.p.opts.Now()
	if s.playing {
		s.pos += now.Sub(s.at)
		if t, ok := get[*track](library, s.trackID); ok && s.pos >= t.duration {
			s.pos, s.playing = t.duration, false
		}
	}
	s.at = now
}

func (s *remoteSession) State(ctx context.Context) (provider.RemoteState, error) {
	if err := s.begin(ctx); err != nil {
		return provider.RemoteState{}, err
	}
	s.pmu.Lock()
	defer s.pmu.Unlock()
	s.advance()
	return provider.RemoteState{TrackID: s.trackID, Playing: s.playing, Position: s.pos, Device: "Fake Speaker", At: s.at}, nil
}
