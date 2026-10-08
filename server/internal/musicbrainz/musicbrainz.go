// SPDX-License-Identifier: AGPL-3.0-only

// Package musicbrainz resolves queued tracks to MusicBrainz recordings,
// releases and artists, and serves their releases' cover art from the
// Cover Art Archive, for when a service's own artwork is small or missing.
//
// MusicBrainz allows one request a second, so tracks are resolved one at
// a time by a background worker and the results, misses included, are
// cached in the database.
package musicbrainz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/artcache"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// queueSize is how many tracks may wait to be resolved. More are dropped,
// and resolved the next time they're asked about.
const queueSize = 512

// Options configure a Service.
type Options struct {
	// UserAgent identifies us, as MusicBrainz requires: name/version (contact).
	UserAgent string
	// BaseURL is the MusicBrainz server (a mirror, say). Default DefaultURL.
	BaseURL string
	// CoverArtURL is the Cover Art Archive. Default DefaultCoverArtURL.
	CoverArtURL string
	// Client makes the requests. Default http.DefaultClient.
	Client *http.Client
	// Interval is the least time between MusicBrainz requests. Default 1s;
	// negative means none, for tests.
	Interval time.Duration
	// FoundTTL is how long resolved IDs are kept. Default 90 days.
	FoundTTL time.Duration
	// MissTTL is how long a miss is kept before trying again. Default 7 days.
	MissTTL time.Duration
	// ArtCacheBytes bounds the cover art cache. Default 64 MiB.
	ArtCacheBytes int64
	// Now is the clock.
	Now func() time.Time
}

// Service resolves tracks and serves cover art. Call Run to start resolving.
type Service struct {
	db   *store.Store
	mb   *client
	caa  *coverArt
	opts Options

	jobs    chan provider.Track
	mu      sync.Mutex
	pending map[string]bool
	// releases are recordings' releases, for their covers: "" for none.
	releases map[string]IDs
}

// New returns a Service.
func New(db *store.Store, opts Options) *Service {
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultURL
	}
	if opts.CoverArtURL == "" {
		opts.CoverArtURL = DefaultCoverArtURL
	}
	if opts.Client == nil {
		opts.Client = http.DefaultClient
	}
	if opts.Interval == 0 {
		opts.Interval = time.Second
	}
	opts.Interval = max(opts.Interval, 0)
	if opts.FoundTTL <= 0 {
		opts.FoundTTL = 90 * 24 * time.Hour
	}
	if opts.MissTTL <= 0 {
		opts.MissTTL = 7 * 24 * time.Hour
	}
	if opts.ArtCacheBytes == 0 {
		opts.ArtCacheBytes = 64 << 20
	}
	if opts.Now == nil {
		opts.Now = store.Now
	}
	return &Service{
		db:   db,
		mb:   &client{base: strings.TrimRight(opts.BaseURL, "/"), userAgent: opts.UserAgent, http: opts.Client, interval: opts.Interval},
		caa:  &coverArt{base: strings.TrimRight(opts.CoverArtURL, "/"), userAgent: opts.UserAgent, http: opts.Client, cache: artcache.New[artKey](opts.ArtCacheBytes, artTTL, opts.Now)},
		opts: opts, jobs: make(chan provider.Track, queueSize), pending: map[string]bool{},
	}
}

func key(ref provider.TrackRef) string { return ref.Provider + "\x00" + ref.ID }

// Enqueue asks for tracks to be resolved in the background. Tracks
// already waiting are skipped, and so are all of them once the queue is full.
func (s *Service) Enqueue(ts ...provider.Track) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range ts {
		k := key(t.Ref)
		if t.Ref.ID == "" || s.pending[k] {
			continue
		}
		select {
		case s.jobs <- t:
			s.pending[k] = true
		default:
			slog.Debug("musicbrainz: queue full, dropping a track", "provider", t.Ref.Provider, "track", t.Ref.ID)
		}
	}
}

// Run resolves queued tracks, one at a time, until ctx is done.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-s.jobs:
			if _, err := s.Resolve(ctx, t); err != nil && ctx.Err() == nil {
				slog.Debug("musicbrainz: resolving", "provider", t.Ref.Provider, "track", t.Ref.ID, "err", err)
			}
			s.mu.Lock()
			delete(s.pending, key(t.Ref))
			s.mu.Unlock()
		}
	}
}

// Resolve finds t's IDs, from the cache or MusicBrainz, and caches what it
// learns. It returns provider.ErrNotFound if MusicBrainz doesn't have it.
func (s *Service) Resolve(ctx context.Context, t provider.Track) (IDs, error) {
	if ids, ok, err := s.Lookup(ctx, t.Ref); err != nil || ok {
		return ids, err
	}
	ids, err := s.mb.resolve(ctx, t)
	found := err == nil
	if err != nil && !errors.Is(err, errNotFound) {
		// Down or busy: try again next time.
		return IDs{}, err
	}
	now := s.opts.Now()
	ttl := s.opts.MissTTL
	if found {
		ttl = s.opts.FoundTTL
	}
	if err := s.db.PutMusicBrainzTrack(ctx, store.PutMusicBrainzTrackParams{
		Provider: t.Ref.Provider, TrackID: t.Ref.ID,
		RecordingMbid: ids.Recording, ReleaseMbid: ids.Release, ReleaseGroupMbid: ids.ReleaseGroup, ArtistMbid: ids.Artist,
		Method: ids.Method, ResolvedAt: now, ExpiresAt: now.Add(ttl),
	}); err != nil {
		return IDs{}, err
	}
	if !found {
		return IDs{}, notFound(t.Ref)
	}
	return ids, nil
}

// Lookup returns what's cached for a track. ok is false if it hasn't been
// resolved yet; a cached miss is provider.ErrNotFound.
func (s *Service) Lookup(ctx context.Context, ref provider.TrackRef) (IDs, bool, error) {
	row, err := s.db.GetMusicBrainzTrack(ctx, store.GetMusicBrainzTrackParams{Provider: ref.Provider, TrackID: ref.ID, Now: s.opts.Now()})
	switch {
	case store.IsNotFound(err):
		return IDs{}, false, nil
	case err != nil:
		return IDs{}, false, err
	case row.Method == "":
		return IDs{}, true, notFound(ref)
	}
	return IDs{
		Recording: row.RecordingMbid, Release: row.ReleaseMbid, ReleaseGroup: row.ReleaseGroupMbid,
		Artist: row.ArtistMbid, Method: row.Method,
	}, true, nil
}

func notFound(ref provider.TrackRef) error {
	return fmt.Errorf("musicbrainz: %s %q: %w", ref.Provider, ref.ID, provider.ErrNotFound)
}

// CoverArt returns the front cover of t's release from the Cover Art
// Archive, at least size pixels wide if it can (up to 1200). It's
// provider.ErrNotFound if there's none, or t hasn't been resolved yet, in
// which case it's queued to be.
func (s *Service) CoverArt(ctx context.Context, t provider.Track, size int) (artcache.Image, error) {
	ids, ok, err := s.Lookup(ctx, t.Ref)
	if err != nil {
		return artcache.Image{}, err
	}
	if !ok {
		s.Enqueue(t)
		return artcache.Image{}, notFound(t.Ref)
	}
	if ids.Release == "" {
		return artcache.Image{}, notFound(t.Ref)
	}
	return s.caa.front(ctx, ids, size)
}

// Sweep deletes expired cache entries.
func (s *Service) Sweep(ctx context.Context) error {
	return s.db.DeleteExpiredMusicBrainzTracks(ctx, s.opts.Now())
}
