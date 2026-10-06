// SPDX-License-Identifier: AGPL-3.0-only

// Package lyrics finds lyrics for tracks: from the track's own provider
// when it has them (Navidrome reads tags and .lrc files), and otherwise
// from LRCLIB, for any provider. Results, misses included, are cached in
// the database.
package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// SourceLRCLIB is the Source of lyrics from LRCLIB. Lyrics from a
// provider have its ID as their Source.
const SourceLRCLIB = "lrclib"

// fetchTimeout bounds finding one track's lyrics, provider and LRCLIB both.
const fetchTimeout = 20 * time.Second

// Result is a track's lyrics and where they came from.
type Result struct {
	provider.Lyrics
	Source string
	// Instrumental means the track is known to have no words. Plain and
	// Synced are empty.
	Instrumental bool
}

// Options configure a Service. The zero value uses only providers' own lyrics.
type Options struct {
	// LRCLIB is asked when the provider has no lyrics. Nil turns it off.
	LRCLIB *LRCLIB
	// FoundTTL is how long found lyrics are kept. Default 30 days.
	FoundTTL time.Duration
	// MissTTL is how long a miss is kept before asking again. Default 1 day.
	MissTTL time.Duration
	// Now is the clock.
	Now func() time.Time
}

// Service finds lyrics. It's safe for concurrent use.
type Service struct {
	db   *store.Store
	opts Options
	// Everyone in a room asks for the new song's lyrics at once.
	group singleflight.Group
}

// New returns a Service.
func New(db *store.Store, opts Options) *Service {
	if opts.FoundTTL <= 0 {
		opts.FoundTTL = 30 * 24 * time.Hour
	}
	if opts.MissTTL <= 0 {
		opts.MissTTL = 24 * time.Hour
	}
	if opts.Now == nil {
		opts.Now = store.Now
	}
	return &Service{db: db, opts: opts}
}

// Get returns t's lyrics, or provider.ErrNotFound if there are none.
// t.Ref names the track. sess, its link's session, may be nil if the link
// is gone; then only LRCLIB is asked. If t has no title, it is looked up
// through sess when LRCLIB needs it.
func (s *Service) Get(ctx context.Context, sess provider.Session, t provider.Track) (Result, error) {
	key := t.Ref.Provider + "\x00" + t.Ref.ID
	v, err, _ := s.group.Do(key, func() (any, error) {
		// Shared by every caller waiting here, so not cut short by
		// whichever happened to ask first.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
		defer cancel()
		return s.get(ctx, sess, t)
	})
	if err != nil {
		return Result{}, err
	}
	r := v.(Result)
	if r.Source == "" {
		return Result{}, fmt.Errorf("lyrics for %s %q: %w", t.Ref.Provider, t.Ref.ID, provider.ErrNotFound)
	}
	return r, nil
}

// get finds lyrics, caching what it learns. A Result with no Source is a miss.
func (s *Service) get(ctx context.Context, sess provider.Session, t provider.Track) (Result, error) {
	now := s.opts.Now()
	row, err := s.db.GetCachedLyrics(ctx, store.GetCachedLyricsParams{Provider: t.Ref.Provider, TrackID: t.Ref.ID, Now: now})
	switch {
	case err == nil:
		return fromRow(row)
	case !store.IsNotFound(err):
		return Result{}, err
	}

	r, sure, err := s.fetch(ctx, sess, t)
	if err != nil {
		return Result{}, err
	}
	if r.Source != "" || sure {
		if err := s.put(ctx, t.Ref, r, sure, now); err != nil {
			slog.Warn("caching lyrics", "provider", t.Ref.Provider, "track", t.Ref.ID, "err", err)
		}
	}
	return r, nil
}

// fetch asks the provider, then LRCLIB. sure says a miss is a real one,
// not a sign that something was down, and may be cached.
func (s *Service) fetch(ctx context.Context, sess provider.Session, t provider.Track) (r Result, sure bool, err error) {
	sure = true
	if ly, ok := sess.(provider.Lyricist); ok {
		l, err := ly.Lyrics(ctx, t.Ref.ID)
		switch {
		case err == nil && len(l.Synced) > 0:
			return Result{Lyrics: l, Source: t.Ref.Provider}, true, nil
		case err == nil && l.Plain != "":
			// Plain only; LRCLIB may have them synced.
			r = Result{Lyrics: l, Source: t.Ref.Provider}
		case err == nil, errors.Is(err, provider.ErrNotFound):
		case ctx.Err() != nil:
			return Result{}, false, ctx.Err()
		default:
			slog.Debug("provider lyrics", "provider", t.Ref.Provider, "track", t.Ref.ID, "err", err)
			sure = false
		}
	}
	if s.opts.LRCLIB == nil {
		return r, sure, nil
	}
	if t.Title == "" && sess != nil {
		full, err := sess.Track(ctx, t.Ref.ID)
		if err != nil {
			if errors.Is(err, provider.ErrNotFound) && r.Source == "" {
				return Result{}, true, nil
			}
			slog.Debug("looking up a track for LRCLIB", "provider", t.Ref.Provider, "track", t.Ref.ID, "err", err)
			return r, false, nil
		}
		t = full
	}
	f, err := s.opts.LRCLIB.Get(ctx, t)
	switch {
	case errors.Is(err, errNoMatch):
		return r, sure, nil
	case err != nil:
		if ctx.Err() != nil {
			return Result{}, false, ctx.Err()
		}
		slog.Debug("LRCLIB lyrics", "provider", t.Ref.Provider, "track", t.Ref.ID, "err", err)
		return r, false, nil
	case r.Source != "" && len(f.Synced) == 0:
		// The provider's plain lyrics are as good, and closer to home.
		return r, true, nil
	}
	return Result{Lyrics: f.Lyrics, Source: SourceLRCLIB, Instrumental: f.Instrumental}, true, nil
}

// syncedLine is how synced lines are stored.
type syncedLine struct {
	MS   int64  `json:"ms"`
	Text string `json:"text"`
}

// put caches r. Misses, and finds that might be bettered once a service
// is back, are kept only for MissTTL.
func (s *Service) put(ctx context.Context, ref provider.TrackRef, r Result, sure bool, now time.Time) error {
	lines := make([]syncedLine, len(r.Synced))
	for i, l := range r.Synced {
		lines[i] = syncedLine{MS: l.At.Milliseconds(), Text: l.Text}
	}
	synced, err := json.Marshal(lines)
	if err != nil {
		return err
	}
	ttl := s.opts.MissTTL
	if r.Source != "" && sure {
		ttl = s.opts.FoundTTL
	}
	return s.db.PutCachedLyrics(ctx, store.PutCachedLyricsParams{
		Provider: ref.Provider, TrackID: ref.ID, Source: r.Source, Instrumental: r.Instrumental,
		Plain: r.Plain, Synced: string(synced), FetchedAt: now, ExpiresAt: now.Add(ttl),
	})
}

func fromRow(row store.LyricsCache) (Result, error) {
	var lines []syncedLine
	if err := json.Unmarshal([]byte(row.Synced), &lines); err != nil {
		return Result{}, fmt.Errorf("cached lyrics for %s %q: %w", row.Provider, row.TrackID, err)
	}
	r := Result{Source: row.Source, Instrumental: row.Instrumental, Lyrics: provider.Lyrics{Plain: row.Plain}}
	for _, l := range lines {
		r.Synced = append(r.Synced, provider.LyricLine{At: time.Duration(l.MS) * time.Millisecond, Text: l.Text})
	}
	return r, nil
}

// Sweep deletes expired cache entries.
func (s *Service) Sweep(ctx context.Context) error {
	return s.db.DeleteExpiredLyrics(ctx, s.opts.Now())
}
