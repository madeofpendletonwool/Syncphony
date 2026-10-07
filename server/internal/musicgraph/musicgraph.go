// SPDX-License-Identifier: AGPL-3.0-only

// Package musicgraph knows how music relates, apart from whichever
// service plays it: which artists are alike, which of an artist's songs
// are their hits, what an artist sounds like (tags), and a song's similar
// songs, tempo and year. See docs/adr/0012-smart-dj.md.
//
// It asks several sources (Last.fm, ListenBrainz, Deezer, MusicBrainz),
// each optional, and merges their answers: an artist that more sources
// call similar ranks higher. Answers are cached in the database, misses
// too, and a background worker warms the cache with the artists a room
// queues and their nearest neighbors, so autopilot reads the cache instead
// of waiting on the network.
//
// It says what should play, not where: songs are names, MBIDs and ISRCs,
// found on a room's services by internal/match.
package musicgraph

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// ArtistRef names an artist, and gives their MusicBrainz ID when known.
type ArtistRef struct {
	Name string `json:"name"`
	MBID string `json:"mbid,omitempty"`
}

// SongRef names a song.
type SongRef struct {
	Title  string    `json:"title"`
	Artist ArtistRef `json:"artist"`
	// MBID is the MusicBrainz recording ID, ISRC the song's ISRC, when known.
	MBID string `json:"mbid,omitempty"`
	ISRC string `json:"isrc,omitempty"`
}

// Similar is an artist like another.
type Similar struct {
	Artist ArtistRef `json:"artist"`
	// Score is how alike, from 0 to 1, merged across sources.
	Score float64 `json:"score"`
	// Sources are the sources that called them similar.
	Sources []string `json:"sources"`
}

// Song is one of an artist's songs, or a song like another.
type Song struct {
	SongRef
	// Score is, for an artist's top songs, how popular the song is among
	// the artist's own (1 is their biggest); for similar songs, how alike.
	// From 0 to 1, merged across sources.
	Score   float64  `json:"score"`
	Sources []string `json:"sources"`
}

// Tag is a genre or descriptive tag.
type Tag struct {
	Name string `json:"name"`
	// Weight is how strongly it applies, from 0 to 1.
	Weight float64 `json:"weight"`
}

// Artist is what the sources know about an artist.
type Artist struct {
	Ref ArtistRef `json:"ref"`
	// Similar are artists like them, most alike first.
	Similar []Similar `json:"similar,omitempty"`
	// Top are their most popular songs, most popular first.
	Top []Song `json:"top,omitempty"`
	// Tags describe them, strongest first.
	Tags []Tag `json:"tags,omitempty"`
	// Sources are the sources that knew them.
	Sources []string `json:"sources,omitempty"`
}

// Track is what the sources know about a song.
type Track struct {
	Ref SongRef `json:"ref"`
	// Similar are songs like it, most alike first.
	Similar []Song `json:"similar,omitempty"`
	// BPM is its tempo, or 0 if unknown.
	BPM float64 `json:"bpm,omitempty"`
	// Year is when it first came out, or 0 if unknown.
	Year int `json:"year,omitempty"`
	// Rank is how popular it is across all music, from 0 to 1, or 0 if
	// unknown.
	Rank float64 `json:"rank,omitempty"`
	// Sources are the sources that knew it.
	Sources []string `json:"sources,omitempty"`
}

// Source is somewhere that knows about music. A source answers what it
// can, leaving the rest empty; provider.ErrNotFound if it knows nothing
// about an artist or song, provider.ErrUnsupported if it can't say (it
// needs an MBID, say).
type Source interface {
	Name() string
	Artist(ctx context.Context, a ArtistRef) (Artist, error)
	Track(ctx context.Context, s SongRef) (Track, error)
}

// ArtistFinder finds an artist's MusicBrainz ID by name.
// musicbrainz.Service is one.
type ArtistFinder interface {
	FindArtist(ctx context.Context, name string) (string, error)
}

// weights are how much each source's opinion counts when merging. A
// source that isn't listed counts 1.
var weights = map[string]float64{
	"lastfm":       1,
	"listenbrainz": 0.8,
	"musicbrainz":  0.8,
	"deezer":       0.6,
}

func weight(source string) float64 {
	if w, ok := weights[source]; ok {
		return w
	}
	return 1
}

// How much is kept, and how far warming reaches.
const (
	maxSimilar = 100
	maxTop     = 50
	maxTags    = 20
	// warmSimilar is how many of a queued artist's nearest neighbors are
	// warmed too, so a walk of the graph finds them cached.
	warmSimilar = 5
	queueSize   = 512
)

// Options configure a Service.
type Options struct {
	// Sources are asked about every artist and song, at once.
	Sources []Source
	// Finder, if set, finds an artist's MBID when it isn't known, for the
	// sources that need one.
	Finder ArtistFinder
	// TTL is how long answers are kept. Default 7 days.
	TTL time.Duration
	// MissTTL is how long a miss, or an answer some source failed to give,
	// is kept before asking again. Default 1 day.
	MissTTL time.Duration
	// Timeout bounds asking every source about one artist or song.
	// Default 30s.
	Timeout time.Duration
	// Now is the clock.
	Now func() time.Time
}

// Service answers what's known about artists and songs, from the cache or
// the sources. Call Run to warm the cache in the background.
type Service struct {
	db   *store.Store
	opts Options

	jobs    chan job
	mu      sync.Mutex
	pending map[string]bool
	flights map[string]*flight
}

// job is something to warm: an artist, with how many hops it is from one
// that was queued, or a song.
type job struct {
	artist ArtistRef
	hop    int
	song   *SongRef
}

func (j job) key() string {
	if j.song != nil {
		return "t\x00" + songKey(*j.song)
	}
	return "a\x00" + artistKey(j.artist)
}

// flight is a fetch in progress, which others asking the same can wait on.
type flight struct {
	done chan struct{}
}

// New returns a Service.
func New(db *store.Store, opts Options) *Service {
	if opts.TTL <= 0 {
		opts.TTL = 7 * 24 * time.Hour
	}
	if opts.MissTTL <= 0 {
		opts.MissTTL = 24 * time.Hour
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.Now == nil {
		opts.Now = store.Now
	}
	return &Service{
		db: db, opts: opts,
		jobs: make(chan job, queueSize), pending: map[string]bool{}, flights: map[string]*flight{},
	}
}

// Sources names the sources the Service asks.
func (s *Service) Sources() []string {
	out := make([]string, len(s.opts.Sources))
	for i, src := range s.opts.Sources {
		out[i] = src.Name()
	}
	return out
}

// artistKey is how an artist is cached: their simplified name.
func artistKey(a ArtistRef) string { return match.Simplify(a.Name) }

// songKey is how a song is cached: its simplified artist and title.
func songKey(s SongRef) string {
	return match.Simplify(s.Artist.Name) + "\x00" + match.Simplify(s.Title)
}

func notFound(what string) error { return fmt.Errorf("musicgraph: %s: %w", what, provider.ErrNotFound) }

// Artist returns what's known about a, from the cache or, on a miss, the
// sources. It's provider.ErrNotFound if no source knows them.
func (s *Service) Artist(ctx context.Context, a ArtistRef) (Artist, error) {
	if out, ok, err := s.CachedArtist(ctx, a); ok || err != nil {
		return out, err
	}
	if artistKey(a) == "" {
		// Sources are asked by name, and answers cached by it.
		return Artist{}, notFound("artist " + a.MBID + " with no name")
	}
	key := "a\x00" + artistKey(a)
	if !s.lead(ctx, key) {
		// Someone else just fetched it. If that failed, try again.
		if out, ok, err := s.CachedArtist(ctx, a); ok || err != nil {
			return out, err
		}
		return s.fetchArtist(ctx, a)
	}
	defer s.land(key)
	return s.fetchArtist(ctx, a)
}

// CachedArtist returns what's cached about a, without asking the sources.
// ok is false if nothing is; a cached miss is provider.ErrNotFound.
func (s *Service) CachedArtist(ctx context.Context, a ArtistRef) (Artist, bool, error) {
	if strings.TrimSpace(a.Name) == "" && a.MBID == "" {
		return Artist{}, true, notFound("an artist with no name")
	}
	now := s.opts.Now()
	var row store.MusicgraphArtist
	err := sql.ErrNoRows // looked up by MBID only when there is one
	if a.MBID != "" {
		row, err = s.db.GetMusicGraphArtistByMBID(ctx, store.GetMusicGraphArtistByMBIDParams{Mbid: a.MBID, Now: now})
	}
	if store.IsNotFound(err) && a.Name != "" {
		row, err = s.db.GetMusicGraphArtist(ctx, store.GetMusicGraphArtistParams{Key: artistKey(a), Now: now})
	}
	switch {
	case store.IsNotFound(err):
		return Artist{}, false, nil
	case err != nil:
		return Artist{}, false, err
	case !row.Found:
		return Artist{}, true, notFound("artist " + a.Name)
	}
	var out Artist
	if err := json.Unmarshal([]byte(row.Facts), &out); err != nil {
		return Artist{}, false, fmt.Errorf("musicgraph: reading cached artist %q: %w", row.Key, err)
	}
	return out, true, nil
}

func (s *Service) fetchArtist(ctx context.Context, a ArtistRef) (Artist, error) {
	// The sources share a deadline; saving the answer doesn't.
	fctx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()
	// failed is whether something that might have known failed, so the
	// answer is kept only briefly. Without an MBID, the sources that need
	// one can't answer.
	failed := false
	if a.MBID == "" && s.opts.Finder != nil && a.Name != "" {
		switch mbid, err := s.opts.Finder.FindArtist(fctx, a.Name); {
		case err == nil:
			a.MBID = mbid
		case !errors.Is(err, provider.ErrNotFound):
			failed = true
			slog.Debug("musicgraph: finding an artist's MBID", "artist", a.Name, "err", err)
		}
	}
	answers := make([]Artist, len(s.opts.Sources))
	errs := make([]error, len(s.opts.Sources))
	var wg sync.WaitGroup
	for i, src := range s.opts.Sources {
		wg.Go(func() {
			answers[i], errs[i] = src.Artist(fctx, a)
		})
	}
	wg.Wait()
	var got []Artist
	for i, err := range errs {
		switch {
		case err == nil:
			answers[i].Sources = []string{s.opts.Sources[i].Name()}
			got = append(got, answers[i])
		case errors.Is(err, provider.ErrNotFound), errors.Is(err, provider.ErrUnsupported):
		default:
			failed = true
			slog.Debug("musicgraph: asking about an artist", "source", s.opts.Sources[i].Name(), "artist", a.Name, "err", err)
		}
	}
	if len(got) == 0 && failed {
		return Artist{}, fmt.Errorf("musicgraph: artist %q: every source that might know failed: %w", a.Name, provider.ErrUnavailable)
	}
	out := mergeArtist(a, got)
	ttl := s.opts.TTL
	if failed || len(got) == 0 {
		ttl = s.opts.MissTTL
	}
	if err := s.putArtist(ctx, out, len(got) > 0, ttl); err != nil {
		return Artist{}, err
	}
	if len(got) == 0 {
		return Artist{}, notFound("artist " + a.Name)
	}
	return out, nil
}

func (s *Service) putArtist(ctx context.Context, a Artist, found bool, ttl time.Duration) error {
	facts, err := json.Marshal(a)
	if err != nil {
		return err
	}
	now := s.opts.Now()
	return s.db.PutMusicGraphArtist(ctx, store.PutMusicGraphArtistParams{
		Key: artistKey(a.Ref), Mbid: a.Ref.MBID, Found: found, Facts: string(facts),
		FetchedAt: now, ExpiresAt: now.Add(ttl),
	})
}

// Track returns what's known about a song, from the cache or, on a miss,
// the sources. It's provider.ErrNotFound if no source knows it.
func (s *Service) Track(ctx context.Context, sr SongRef) (Track, error) {
	if out, ok, err := s.CachedTrack(ctx, sr); ok || err != nil {
		return out, err
	}
	key := "t\x00" + songKey(sr)
	if !s.lead(ctx, key) {
		// Someone else just fetched it. If that failed, try again.
		if out, ok, err := s.CachedTrack(ctx, sr); ok || err != nil {
			return out, err
		}
		return s.fetchTrack(ctx, sr)
	}
	defer s.land(key)
	return s.fetchTrack(ctx, sr)
}

// CachedTrack returns what's cached about a song, without asking the
// sources. ok is false if nothing is; a cached miss is provider.ErrNotFound.
func (s *Service) CachedTrack(ctx context.Context, sr SongRef) (Track, bool, error) {
	if strings.TrimSpace(sr.Title) == "" {
		return Track{}, true, notFound("a song with no title")
	}
	row, err := s.db.GetMusicGraphTrack(ctx, store.GetMusicGraphTrackParams{Key: songKey(sr), Now: s.opts.Now()})
	switch {
	case store.IsNotFound(err):
		return Track{}, false, nil
	case err != nil:
		return Track{}, false, err
	case !row.Found:
		return Track{}, true, notFound("song " + sr.Title)
	}
	var out Track
	if err := json.Unmarshal([]byte(row.Facts), &out); err != nil {
		return Track{}, false, fmt.Errorf("musicgraph: reading cached song %q: %w", row.Key, err)
	}
	return out, true, nil
}

func (s *Service) fetchTrack(ctx context.Context, sr SongRef) (Track, error) {
	// The sources share a deadline; saving the answer doesn't.
	fctx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()
	answers := make([]Track, len(s.opts.Sources))
	errs := make([]error, len(s.opts.Sources))
	var wg sync.WaitGroup
	for i, src := range s.opts.Sources {
		wg.Go(func() {
			answers[i], errs[i] = src.Track(fctx, sr)
		})
	}
	wg.Wait()
	var got []Track
	failed := false
	for i, err := range errs {
		switch {
		case err == nil:
			answers[i].Sources = []string{s.opts.Sources[i].Name()}
			got = append(got, answers[i])
		case errors.Is(err, provider.ErrNotFound), errors.Is(err, provider.ErrUnsupported):
		default:
			failed = true
			slog.Debug("musicgraph: asking about a song", "source", s.opts.Sources[i].Name(), "title", sr.Title, "err", err)
		}
	}
	if len(got) == 0 && failed {
		return Track{}, fmt.Errorf("musicgraph: song %q: every source that might know failed: %w", sr.Title, provider.ErrUnavailable)
	}
	out := mergeTrack(sr, got)
	ttl := s.opts.TTL
	if failed || len(got) == 0 {
		ttl = s.opts.MissTTL
	}
	facts, err := json.Marshal(out)
	if err != nil {
		return Track{}, err
	}
	now := s.opts.Now()
	if err := s.db.PutMusicGraphTrack(ctx, store.PutMusicGraphTrackParams{
		Key: songKey(sr), Found: len(got) > 0, Facts: string(facts), FetchedAt: now, ExpiresAt: now.Add(ttl),
	}); err != nil {
		return Track{}, err
	}
	if len(got) == 0 {
		return Track{}, notFound("song " + sr.Title)
	}
	return out, nil
}

// lead starts a fetch of key and returns true, unless one is already in
// progress: then it waits for that one and returns false. The leader
// lands when done.
func (s *Service) lead(ctx context.Context, key string) bool {
	s.mu.Lock()
	f, ok := s.flights[key]
	if !ok {
		s.flights[key] = &flight{done: make(chan struct{})}
		s.mu.Unlock()
		return true
	}
	s.mu.Unlock()
	select {
	case <-f.done:
	case <-ctx.Done():
	}
	return false
}

// land ends the fetch of key that lead started.
func (s *Service) land(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.flights[key]; ok {
		close(f.done)
		delete(s.flights, key)
	}
}

// Warm asks for queued tracks' artists and songs to be fetched in the
// background, so they're cached by the time autopilot needs them. Ones
// already waiting are skipped, and so is everything once the queue is
// full.
func (s *Service) Warm(ts ...provider.Track) {
	for _, t := range ts {
		if len(t.Artists) == 0 || t.Title == "" {
			continue
		}
		a := ArtistRef{Name: t.Artists[0].Name}
		s.enqueue(job{artist: a})
		s.enqueue(job{song: &SongRef{Title: t.Title, Artist: a, MBID: t.MBID, ISRC: t.ISRC}})
	}
}

// WarmArtist asks for artists to be fetched in the background, with
// their nearest neighbors: ones a fill wanted and found nothing cached for.
func (s *Service) WarmArtist(as ...ArtistRef) {
	for _, a := range as {
		if artistKey(a) != "" {
			s.enqueue(job{artist: a})
		}
	}
}

func (s *Service) enqueue(j job) {
	k := j.key()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[k] {
		return
	}
	select {
	case s.jobs <- j:
		s.pending[k] = true
	default:
		slog.Debug("musicgraph: warm queue full, dropping", "job", k)
	}
}

// Run warms the cache, one artist or song at a time, until ctx is done. A
// queued artist's nearest neighbors are warmed after them.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.jobs:
			s.run(ctx, j)
			s.mu.Lock()
			delete(s.pending, j.key())
			s.mu.Unlock()
		}
	}
}

func (s *Service) run(ctx context.Context, j job) {
	if j.song != nil {
		if _, err := s.Track(ctx, *j.song); err != nil && !errors.Is(err, provider.ErrNotFound) && ctx.Err() == nil {
			slog.Debug("musicgraph: warming a song", "title", j.song.Title, "err", err)
		}
		return
	}
	a, err := s.Artist(ctx, j.artist)
	if err != nil {
		if !errors.Is(err, provider.ErrNotFound) && ctx.Err() == nil {
			slog.Debug("musicgraph: warming an artist", "artist", j.artist.Name, "err", err)
		}
		return
	}
	if j.hop > 0 {
		return
	}
	for _, sim := range a.Similar[:min(len(a.Similar), warmSimilar)] {
		s.enqueue(job{artist: sim.Artist, hop: j.hop + 1})
	}
}

// Repaired says what Repair did.
type Repaired struct {
	// Artists and Tracks are how many were fetched again; Complete, how
	// many of them every source answered this time.
	Artists, Tracks, Complete int
}

// Repair fetches again every cached answer kept only until MissTTL: ones
// some source failed to give (it was down, or throttled us), and misses.
// The server runs it once at startup, so a fix to a source fills in what
// it missed instead of waiting a day for those answers to expire. It goes
// one at a time, oldest first, at the sources' rate limits, and stops when
// ctx is done.
func (s *Service) Repair(ctx context.Context) (Repaired, error) {
	var out Repaired
	if len(s.opts.Sources) == 0 {
		return out, nil
	}
	now := s.opts.Now()
	artists, err := s.db.ListMusicGraphArtists(ctx, now)
	if err != nil {
		return out, err
	}
	tracks, err := s.db.ListMusicGraphTracks(ctx, now)
	if err != nil {
		return out, err
	}
	for _, row := range artists {
		if row.ExpiresAt.Sub(row.FetchedAt) >= s.opts.TTL || ctx.Err() != nil {
			continue
		}
		var a Artist
		if err := json.Unmarshal([]byte(row.Facts), &a); err != nil || artistKey(a.Ref) == "" {
			continue
		}
		key := "a\x00" + artistKey(a.Ref)
		if !s.lead(ctx, key) {
			continue // someone just fetched it
		}
		_, err := s.fetchArtist(ctx, a.Ref)
		s.land(key)
		out.Artists++
		if err == nil && s.complete(ctx, a.Ref) {
			out.Complete++
		}
	}
	for _, row := range tracks {
		if row.ExpiresAt.Sub(row.FetchedAt) >= s.opts.TTL || ctx.Err() != nil {
			continue
		}
		var t Track
		if err := json.Unmarshal([]byte(row.Facts), &t); err != nil || t.Ref.Title == "" {
			continue
		}
		key := "t\x00" + songKey(t.Ref)
		if !s.lead(ctx, key) {
			continue
		}
		_, err := s.fetchTrack(ctx, t.Ref)
		s.land(key)
		out.Tracks++
		if err == nil && s.completeTrack(ctx, t.Ref) {
			out.Complete++
		}
	}
	return out, ctx.Err()
}

// complete reports whether an artist's cached answer is kept the full TTL:
// every source that might know answered.
func (s *Service) complete(ctx context.Context, a ArtistRef) bool {
	row, err := s.db.GetMusicGraphArtist(ctx, store.GetMusicGraphArtistParams{Key: artistKey(a), Now: s.opts.Now()})
	return err == nil && row.ExpiresAt.Sub(row.FetchedAt) >= s.opts.TTL
}

// completeTrack is complete, for a song.
func (s *Service) completeTrack(ctx context.Context, sr SongRef) bool {
	row, err := s.db.GetMusicGraphTrack(ctx, store.GetMusicGraphTrackParams{Key: songKey(sr), Now: s.opts.Now()})
	return err == nil && row.ExpiresAt.Sub(row.FetchedAt) >= s.opts.TTL
}

// Sweep deletes expired cache entries.
func (s *Service) Sweep(ctx context.Context) error {
	now := s.opts.Now()
	if err := s.db.DeleteExpiredMusicGraphArtists(ctx, now); err != nil {
		return err
	}
	return s.db.DeleteExpiredMusicGraphTracks(ctx, now)
}

// Merging. Every score from a source is 0 to 1; the merged score is the
// sources' weighted average, a source that answered but didn't list
// something counting as 0 for it. So what more sources agree on ranks
// higher.

func mergeArtist(ref ArtistRef, got []Artist) Artist {
	out := Artist{Ref: ref}
	var similar, top, tags []scored
	var simW, topW, tagW float64
	for _, a := range got {
		src := a.Sources[0]
		w := weight(src)
		out.Sources = append(out.Sources, src)
		if out.Ref.MBID == "" {
			out.Ref.MBID = a.Ref.MBID
		}
		if len(a.Similar) > 0 {
			simW += w
			for _, sim := range a.Similar {
				similar = append(similar, scored{key: artistKey(sim.Artist), score: sim.Score * w, source: src, artist: sim.Artist})
			}
		}
		if len(a.Top) > 0 {
			topW += w
			for _, so := range a.Top {
				top = append(top, scored{key: match.Simplify(so.Title), score: so.Score * w, source: src, song: so.SongRef})
			}
		}
		if len(a.Tags) > 0 {
			tagW += w
			for _, t := range a.Tags {
				tags = append(tags, scored{key: strings.ToLower(strings.TrimSpace(t.Name)), score: t.Weight * w, source: src})
			}
		}
	}
	self := artistKey(ref)
	for _, m := range combine(similar, simW, maxSimilar) {
		if m.key == self {
			continue
		}
		out.Similar = append(out.Similar, Similar{Artist: m.artist, Score: m.score, Sources: m.sources})
	}
	for _, m := range combine(top, topW, maxTop) {
		so := m.song
		so.Artist = out.Ref
		out.Top = append(out.Top, Song{SongRef: so, Score: m.score, Sources: m.sources})
	}
	for _, m := range combine(tags, tagW, maxTags) {
		out.Tags = append(out.Tags, Tag{Name: m.key, Weight: m.score})
	}
	return out
}

func mergeTrack(ref SongRef, got []Track) Track {
	out := Track{Ref: ref}
	var similar []scored
	var simW float64
	for _, t := range got {
		src := t.Sources[0]
		out.Sources = append(out.Sources, src)
		if out.BPM == 0 && t.BPM > 0 {
			out.BPM = t.BPM
		}
		// MusicBrainz knows when a recording first came out; other sources
		// give the date of the release they have, often a reissue.
		if t.Year > 0 && (out.Year == 0 || src == "musicbrainz") {
			out.Year = t.Year
		}
		if t.Rank > out.Rank {
			out.Rank = t.Rank
		}
		if len(t.Similar) > 0 {
			w := weight(src)
			simW += w
			for _, so := range t.Similar {
				similar = append(similar, scored{key: songKey(so.SongRef), score: so.Score * w, source: src, song: so.SongRef})
			}
		}
	}
	self := songKey(ref)
	for _, m := range combine(similar, simW, maxSimilar) {
		if m.key != self {
			out.Similar = append(out.Similar, Song{SongRef: m.song, Score: m.score, Sources: m.sources})
		}
	}
	return out
}

// scored is one source's weighted score for something, while merging.
type scored struct {
	key    string
	score  float64
	source string
	artist ArtistRef
	song   SongRef
}

// merged is something's combined score.
type merged struct {
	scored
	sources []string
}

// combine adds up each thing's weighted scores, divides by the total
// weight of the sources that answered, and returns the best n, best
// first. The first source to give a field (an MBID, an ISRC) fills it.
func combine(in []scored, total float64, n int) []merged {
	if total == 0 {
		return nil
	}
	byKey := map[string]*merged{}
	var order []*merged
	for _, s := range in {
		if s.key == "" {
			continue
		}
		m, ok := byKey[s.key]
		if !ok {
			m = &merged{scored: s}
			m.score = 0
			byKey[s.key] = m
			order = append(order, m)
		}
		if slices.Contains(m.sources, s.source) {
			continue // a source listing something twice counts once
		}
		m.score += s.score
		m.sources = append(m.sources, s.source)
		m.artist.MBID = cmp.Or(m.artist.MBID, s.artist.MBID)
		m.song.MBID = cmp.Or(m.song.MBID, s.song.MBID)
		m.song.ISRC = cmp.Or(m.song.ISRC, s.song.ISRC)
		m.song.Artist.MBID = cmp.Or(m.song.Artist.MBID, s.song.Artist.MBID)
	}
	out := make([]merged, len(order))
	for i, m := range order {
		m.score = math.Round(m.score/total*1000) / 1000
		out[i] = *m
	}
	slices.SortStableFunc(out, func(a, b merged) int { return cmp.Compare(b.score, a.score) })
	return out[:min(len(out), n)]
}

// share scores a count (plays, listens) against the biggest, from 0 to 1.
// The square root spreads an artist's songs out: a song with a quarter of
// the hit's plays scores 0.5, a deep cut with 1% scores 0.1. A log would
// squeeze them all near 1; a plain ratio, all but the hit near 0.
func share(n, top float64) float64 {
	if n <= 0 || top <= 0 {
		return 0
	}
	return min(math.Sqrt(n/top), 1)
}

// byRank scores the ith of n things in a ranked list that gives no
// score of its own: 1 for the first, down to 0.5 for the last.
func byRank(i, n int) float64 {
	if n <= 1 {
		return 1
	}
	return 1 - 0.5*float64(i)/float64(n-1)
}
