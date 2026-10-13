// SPDX-License-Identifier: AGPL-3.0-only

// Package releases finds artists' recent releases (MAD-822, ADR 0017):
// the albums, EPs and singles they put out in the last few months, for
// discovery's Fresh feed.
//
// It asks three sources and merges their answers by artist and title:
// ListenBrainz's fresh releases (one big list of everything lately, read
// every few hours and filtered by artist), MusicBrainz's release groups by
// first-release date, and Deezer's albums, which carry covers and songs
// with previews. Each artist's answer is cached a day. A background worker
// warms the cache one artist at a time, so the sources' rate limits hold
// (MusicBrainz's one request a second is shared with the whole server).
package releases

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Kinds of release. Compilations and live albums are told apart from
// studio albums, so a feed can rank them lower.
const (
	KindAlbum       = "album"
	KindEP          = "ep"
	KindSingle      = "single"
	KindCompilation = "compilation"
	KindLive        = "live"
)

// Release is one of an artist's releases.
type Release struct {
	// Key is how releases are compared: library.ReleaseKey of the artist
	// it was looked up for and its title.
	Key   string `json:"key"`
	Title string `json:"title"`
	// Artist is who it's credited to.
	Artist string `json:"artist"`
	// Date is when it first came out: YYYY-MM-DD, or YYYY-MM if that's
	// all that's known.
	Date string `json:"date"`
	Kind string `json:"kind"`
	// MBID is its MusicBrainz release group, CoverRelease a release of
	// it the Cover Art Archive has a cover for.
	MBID         string `json:"mbid,omitempty"`
	CoverRelease string `json:"coverRelease,omitempty"`
	// DeezerID is the release on Deezer, and Cover its cover's image ID
	// there, when Deezer has it.
	DeezerID int64  `json:"deezerId,omitempty"`
	Cover    string `json:"cover,omitempty"`
	// Sources are the sources that listed it.
	Sources []string `json:"sources"`
}

// Day is when it came out, as a time: the first of the month if only the
// month is known.
func (r Release) Day() (time.Time, bool) {
	for _, layout := range []string{time.DateOnly, "2006-01"} {
		if t, err := time.Parse(layout, r.Date); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Sources.

// Deezer lists artists' releases and their songs. musicgraph.Deezer is one.
type Deezer interface {
	ArtistAlbums(ctx context.Context, name string) ([]musicgraph.DeezerAlbum, error)
	FindAlbum(ctx context.Context, artist, title string) (musicgraph.DeezerAlbum, error)
	AlbumTracks(ctx context.Context, id int64) ([]musicgraph.DeezerTrack, error)
}

// Fresh lists everything that came out lately. musicgraph.ListenBrainz is one.
type Fresh interface {
	FreshReleases(ctx context.Context, days int) ([]musicgraph.FreshRelease, error)
}

// MusicBrainz lists an artist's release groups by date. musicbrainz.Service is one.
type MusicBrainz interface {
	RecentReleaseGroups(ctx context.Context, artistMBID, from, to string) ([]musicbrainz.ReleaseGroup, error)
}

// How far back, how long things are kept, and how much is asked.
const (
	// Lookback is how far back releases are kept: the longest Fresh
	// window, and some.
	Lookback = 100 * 24 * time.Hour
	// freshDays is how far back ListenBrainz's list goes: as far as it
	// will.
	freshDays = 90
	queueSize = 512
	// maxTracks is how many releases' songs are remembered.
	maxTracks = 256
	tracksFor = 6 * time.Hour
)

// Options configure a Service. Every source is optional.
type Options struct {
	Deezer       Deezer
	ListenBrainz Fresh
	MusicBrainz  MusicBrainz
	// TTL is how long an artist's releases are kept. Default a day.
	TTL time.Duration
	// MissTTL is how long an answer some source failed to give is kept.
	// Default an hour.
	MissTTL time.Duration
	// FreshEvery is how often ListenBrainz's list is read again. Default
	// 6 hours.
	FreshEvery time.Duration
	// Timeout bounds asking the sources about one artist. Default 45s:
	// MusicBrainz may be busy with others.
	Timeout time.Duration
	Now     func() time.Time
}

// Service finds artists' recent releases, from the cache or the sources.
// Call Run to warm the cache in the background.
type Service struct {
	db   *store.Store
	opts Options

	jobs    chan musicgraph.ArtistRef
	mu      sync.Mutex
	pending map[string]bool
	flights map[string]chan struct{}

	// fresh is ListenBrainz's list, by artist.
	freshMu sync.Mutex
	fresh   *freshIndex

	tracksMu sync.Mutex
	tracks   map[string]tracksEntry
}

type freshIndex struct {
	at     time.Time
	byMBID map[string][]musicgraph.FreshRelease
	byName map[string][]musicgraph.FreshRelease
}

type tracksEntry struct {
	at     time.Time
	album  musicgraph.DeezerAlbum
	tracks []musicgraph.DeezerTrack
}

// New returns a Service.
func New(db *store.Store, opts Options) *Service {
	if opts.TTL <= 0 {
		opts.TTL = 24 * time.Hour
	}
	if opts.MissTTL <= 0 {
		opts.MissTTL = time.Hour
	}
	if opts.FreshEvery <= 0 {
		opts.FreshEvery = 6 * time.Hour
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 45 * time.Second
	}
	if opts.Now == nil {
		opts.Now = store.Now
	}
	return &Service{
		db: db, opts: opts, jobs: make(chan musicgraph.ArtistRef, queueSize),
		pending: map[string]bool{}, flights: map[string]chan struct{}{}, tracks: map[string]tracksEntry{},
	}
}

// On reports whether any source is on.
func (s *Service) On() bool {
	return s.opts.Deezer != nil || s.opts.ListenBrainz != nil || s.opts.MusicBrainz != nil
}

func key(a musicgraph.ArtistRef) string { return match.Simplify(a.Name) }

// Cached returns an artist's releases from the cache, without asking the
// sources. ok is false if they aren't cached.
func (s *Service) Cached(ctx context.Context, a musicgraph.ArtistRef) ([]Release, bool, error) {
	k := key(a)
	if k == "" {
		return nil, true, nil
	}
	row, err := s.db.GetArtistReleases(ctx, store.GetArtistReleasesParams{Key: k, Now: s.opts.Now()})
	switch {
	case store.IsNotFound(err):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	var out []Release
	if err := json.Unmarshal([]byte(row.Releases), &out); err != nil {
		return nil, false, fmt.Errorf("releases: reading %q: %w", k, err)
	}
	return out, true, nil
}

// Get returns an artist's releases from the last Lookback, newest first:
// from the cache or, on a miss, the sources. Only one fetch of an artist
// runs at a time; others wait for it.
func (s *Service) Get(ctx context.Context, a musicgraph.ArtistRef) ([]Release, error) {
	if out, ok, err := s.Cached(ctx, a); ok || err != nil {
		return out, err
	}
	k := key(a)
	s.mu.Lock()
	wait, busy := s.flights[k]
	if !busy {
		s.flights[k] = make(chan struct{})
	}
	s.mu.Unlock()
	if busy {
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if out, ok, err := s.Cached(ctx, a); ok || err != nil {
			return out, err
		}
		return nil, fmt.Errorf("releases: %q: %w", a.Name, provider.ErrUnavailable)
	}
	defer func() {
		s.mu.Lock()
		close(s.flights[k])
		delete(s.flights, k)
		s.mu.Unlock()
	}()
	return s.fetch(ctx, a)
}

func (s *Service) fetch(ctx context.Context, a musicgraph.ArtistRef) ([]Release, error) {
	fctx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()
	now := s.opts.Now()
	from, to := now.Add(-Lookback).Format(time.DateOnly), now.Format(time.DateOnly)
	m := merger{artist: a.Name, from: from, to: to, byKey: map[string]int{}}
	// failed is whether a source that might have known failed, so the
	// answer is kept only briefly; answered, whether any source answered.
	failed, answered := false, false
	note := func(source string, err error) {
		if err == nil || errors.Is(err, provider.ErrNotFound) {
			answered = true
			return
		}
		failed = true
		slog.Debug("releases: asking about an artist", "source", source, "artist", a.Name, "err", err)
	}

	if s.opts.ListenBrainz != nil {
		ix, err := s.freshList(fctx)
		note("listenbrainz", err)
		if ix != nil {
			// By MBID, and by name for releases credited without one.
			mine := ix.byName[library.ArtistKey(a.Name)]
			if a.MBID != "" {
				mine = append(slices.Clip(ix.byMBID[strings.ToLower(a.MBID)]), mine...)
			}
			for _, f := range mine {
				m.add(Release{
					Title: f.Title, Artist: f.Artist, Date: f.Date, Kind: kindOf(f.PrimaryType, splitTypes(f.SecondaryType), f.Title),
					MBID: f.MBID, CoverRelease: f.CoverRelease,
				}, "listenbrainz")
			}
		}
	}
	if s.opts.MusicBrainz != nil && a.MBID != "" {
		rgs, err := s.opts.MusicBrainz.RecentReleaseGroups(fctx, a.MBID, from, to)
		if errors.Is(err, provider.ErrRateLimited) {
			// MusicBrainz is busy with the rest of the server: its client
			// backs off, so the second try waits its turn.
			rgs, err = s.opts.MusicBrainz.RecentReleaseGroups(fctx, a.MBID, from, to)
		}
		note("musicbrainz", err)
		for _, rg := range rgs {
			if len(rg.FirstReleased) < len("2006-01") {
				continue // a year alone doesn't say if it's recent
			}
			m.add(Release{
				Title: rg.Title, Artist: a.Name, Date: rg.FirstReleased, Kind: kindOf(rg.PrimaryType, rg.SecondaryTypes, rg.Title), MBID: rg.MBID,
			}, "musicbrainz")
		}
	}
	if s.opts.Deezer != nil {
		albums, err := s.opts.Deezer.ArtistAlbums(fctx, a.Name)
		note("deezer", err)
		for _, al := range firstEditions(a.Name, albums) {
			m.add(Release{
				Title: al.Title, Artist: a.Name, Date: al.Date, Kind: deezerKind(al.Type, al.Title), DeezerID: al.ID, Cover: al.Cover,
			}, "deezer")
		}
	}
	if !answered && !failed {
		return nil, nil // no source is on
	}
	out := m.releases()
	if !answered {
		return nil, fmt.Errorf("releases: %q: every source that might know failed: %w", a.Name, provider.ErrUnavailable)
	}
	ttl := s.opts.TTL
	if failed {
		ttl = s.opts.MissTTL
	}
	if err := s.put(ctx, a, out, ttl); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) put(ctx context.Context, a musicgraph.ArtistRef, rs []Release, ttl time.Duration) error {
	if rs == nil {
		rs = []Release{}
	}
	raw, err := json.Marshal(rs)
	if err != nil {
		return err
	}
	now := s.opts.Now()
	return s.db.PutArtistReleases(ctx, store.PutArtistReleasesParams{
		Key: key(a), Name: a.Name, Releases: string(raw), FetchedAt: now, ExpiresAt: now.Add(ttl),
	})
}

// freshList returns ListenBrainz's list of what came out lately, read
// again every FreshEvery. When reading it fails, the last one is used.
func (s *Service) freshList(ctx context.Context) (*freshIndex, error) {
	s.freshMu.Lock()
	defer s.freshMu.Unlock()
	now := s.opts.Now()
	if s.fresh != nil && now.Sub(s.fresh.at) < s.opts.FreshEvery {
		return s.fresh, nil
	}
	list, err := s.opts.ListenBrainz.FreshReleases(ctx, freshDays)
	if err != nil {
		if s.fresh != nil {
			// Try again in a while, not with every artist.
			s.fresh.at = now.Add(-s.opts.FreshEvery + 15*time.Minute)
		}
		return s.fresh, err
	}
	ix := &freshIndex{at: now, byMBID: map[string][]musicgraph.FreshRelease{}, byName: map[string][]musicgraph.FreshRelease{}}
	for _, f := range list {
		for _, id := range f.ArtistMBIDs {
			id = strings.ToLower(id)
			ix.byMBID[id] = append(ix.byMBID[id], f)
		}
		k := library.ArtistKey(f.Artist)
		ix.byName[k] = append(ix.byName[k], f)
	}
	s.fresh = ix
	return ix, nil
}

// firstEditions keeps each of an artist's releases on Deezer once, as
// its first edition: Deezer dates a deluxe edition or a reissue to when it
// came out, which would make an old album look new.
func firstEditions(artist string, albums []musicgraph.DeezerAlbum) []musicgraph.DeezerAlbum {
	first := map[string]int{}
	var out []musicgraph.DeezerAlbum
	for _, al := range albums {
		k := library.ReleaseKey(artist, al.Title)
		i, ok := first[k]
		switch {
		case !ok:
			first[k] = len(out)
			out = append(out, al)
		case al.Date != "" && (out[i].Date == "" || al.Date < out[i].Date):
			out[i] = al
		}
	}
	return out
}

// merger merges the sources' releases of one artist by title. They're
// added ListenBrainz and MusicBrainz first: the first to list a release
// says what kind it is and who it's credited to, and MusicBrainz's types
// say more than Deezer's.
type merger struct {
	artist   string
	from, to string
	byKey    map[string]int
	out      []Release
}

func (m *merger) add(r Release, source string) {
	r.Date = r.Date[:min(len(r.Date), len(time.DateOnly))]
	if r.Title == "" || len(r.Date) < len("2006-01") || r.Date < m.from[:len(r.Date)] || r.Date > m.to {
		return
	}
	r.Key = library.ReleaseKey(m.artist, r.Title)
	i, ok := m.byKey[r.Key]
	if !ok {
		r.Sources = []string{source}
		m.byKey[r.Key] = len(m.out)
		m.out = append(m.out, r)
		return
	}
	cur := &m.out[i]
	if !slices.Contains(cur.Sources, source) {
		cur.Sources = append(cur.Sources, source)
	}
	// The most precise date; of two as precise, the first.
	if len(r.Date) > len(cur.Date) || (len(r.Date) == len(cur.Date) && r.Date < cur.Date) {
		cur.Date = r.Date
	}
	cur.MBID = cmp.Or(cur.MBID, r.MBID)
	cur.CoverRelease = cmp.Or(cur.CoverRelease, r.CoverRelease)
	cur.DeezerID = cmp.Or(cur.DeezerID, r.DeezerID)
	cur.Cover = cmp.Or(cur.Cover, r.Cover)
}

// releases are the merged releases, newest first.
func (m *merger) releases() []Release {
	slices.SortStableFunc(m.out, func(a, b Release) int {
		if c := cmp.Compare(b.Date, a.Date); c != 0 {
			return c
		}
		return cmp.Compare(a.Title, b.Title)
	})
	return m.out
}

// kindOf names a release from its MusicBrainz types, and its title: a
// "(Live)" album is live whatever it's typed as.
func kindOf(primary string, secondary []string, title string) string {
	k := musicbrainz.Kind(primary, secondary)
	if k == KindAlbum && isLive(title) {
		return KindLive
	}
	return k
}

func splitTypes(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, " + ")
}

// deezerKind names a release from Deezer's record type and its title.
func deezerKind(recordType, title string) string {
	switch {
	case recordType == "compile":
		return KindCompilation
	case isLive(title):
		return KindLive
	case recordType == "ep":
		return KindEP
	case recordType == "single":
		return KindSingle
	}
	return KindAlbum
}

func isLive(title string) bool {
	_, variants := match.Title(title)
	return slices.Contains(variants, "live") || strings.HasPrefix(match.Simplify(title), "live at ") || strings.HasPrefix(match.Simplify(title), "live in ")
}

// Tracks lists a release's songs on Deezer, with what Deezer calls the
// release: by its Deezer ID, else found by artist and title. They're
// remembered a while.
func (s *Service) Tracks(ctx context.Context, artist, title string, deezerID int64) (musicgraph.DeezerAlbum, []musicgraph.DeezerTrack, error) {
	if s.opts.Deezer == nil {
		return musicgraph.DeezerAlbum{}, nil, fmt.Errorf("releases: Deezer is off: %w", provider.ErrNotFound)
	}
	k := library.ReleaseKey(artist, title)
	if deezerID != 0 {
		k = fmt.Sprint(deezerID)
	}
	now := s.opts.Now()
	s.tracksMu.Lock()
	e, ok := s.tracks[k]
	s.tracksMu.Unlock()
	if ok && now.Sub(e.at) < tracksFor {
		return e.album, e.tracks, nil
	}
	album := musicgraph.DeezerAlbum{ID: deezerID, Title: title}
	if deezerID == 0 {
		var err error
		if album, err = s.opts.Deezer.FindAlbum(ctx, artist, title); err != nil {
			return musicgraph.DeezerAlbum{}, nil, err
		}
	}
	tracks, err := s.opts.Deezer.AlbumTracks(ctx, album.ID)
	if err != nil {
		return musicgraph.DeezerAlbum{}, nil, err
	}
	s.tracksMu.Lock()
	if len(s.tracks) >= maxTracks {
		clear(s.tracks)
	}
	s.tracks[k] = tracksEntry{at: now, album: album, tracks: tracks}
	s.tracksMu.Unlock()
	return album, tracks, nil
}

// Warm asks for artists' releases to be fetched in the background. Ones
// already waiting are skipped, and so is everything once the queue is
// full.
func (s *Service) Warm(as ...musicgraph.ArtistRef) {
	if !s.On() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range as {
		k := key(a)
		if k == "" || s.pending[k] {
			continue
		}
		select {
		case s.jobs <- a:
			s.pending[k] = true
		default:
			slog.Debug("releases: warm queue full, dropping", "artist", a.Name)
			return
		}
	}
}

// Run warms the cache, one artist at a time, and deletes what's expired
// hourly, until ctx is done.
func (s *Service) Run(ctx context.Context) {
	sweep := time.NewTicker(time.Hour)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			if err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("releases: sweeping expired releases", "err", err)
			}
		case a := <-s.jobs:
			if _, err := s.Get(ctx, a); err != nil && ctx.Err() == nil {
				slog.Debug("releases: warming an artist", "artist", a.Name, "err", err)
			}
			s.mu.Lock()
			delete(s.pending, key(a))
			s.mu.Unlock()
		}
	}
}

// Sweep deletes expired releases.
func (s *Service) Sweep(ctx context.Context) error {
	return s.db.DeleteExpiredArtistReleases(ctx, s.opts.Now())
}
