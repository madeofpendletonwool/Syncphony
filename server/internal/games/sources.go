// SPDX-License-Identifier: AGPL-3.0-only

package games

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/analysis"
	"github.com/madeofpendletonwool/syncphony/server/internal/linernotes"
	"github.com/madeofpendletonwool/syncphony/server/internal/lyrics"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Sources finds songs' facts from what the server already knows: liner
// notes, lyrics, beat maps and the music graph. Each is optional.
type Sources struct {
	DB         *store.Store
	LinerNotes *linernotes.Service
	Lyrics     *lyrics.Service
	Graph      *musicgraph.Service
	// Now is the clock. Default store.Now.
	Now func() time.Time
}

var _ Facts = (*Sources)(nil)

func (s *Sources) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return store.Now()
}

// Song implements Facts.
func (s *Sources) Song(ctx context.Context, it store.QueueItem, cachedOnly bool) (quiz.Facts, error) {
	t := queuedTrack(it)
	f := quiz.Facts{Song: quiz.Song{Title: t.Title, Artist: mainArtist(t)}, Album: t.Album.Title, DurationMs: t.Duration.Milliseconds()}
	if n, ok := s.notes(ctx, t, cachedOnly); ok {
		f.Year = n.Year
		if n.Release != nil {
			f.ReleaseYear = year(n.Release.Date)
		}
		if c := n.CoverOf; c != nil {
			f.CoverOf = &quiz.Original{Title: c.Title, Writers: c.Writers}
		}
		for _, r := range n.Samples {
			f.Samples = append(f.Samples, quiz.Song{Title: r.Title, Artist: r.Artist})
		}
		for _, r := range n.SampledBy {
			f.SampledBy = append(f.SampledBy, quiz.Song{Title: r.Title, Artist: r.Artist})
		}
		for _, c := range n.Credits {
			f.Credits = append(f.Credits, quiz.Credit{Role: c.Role, Names: c.Names})
		}
	}
	if s.Lyrics != nil && !cachedOnly {
		if l, err := s.Lyrics.Get(ctx, nil, t); err == nil {
			for _, line := range l.Synced {
				f.Lyrics = append(f.Lyrics, quiz.Line{Ms: line.At.Milliseconds(), Text: line.Text})
			}
		}
	}
	if m, ok := s.beatMap(ctx, it); ok {
		f.BPM, f.Energy = m.BPM, m.Features.Energy
		if m.DurationMs > 0 {
			f.DurationMs = m.DurationMs
		}
		for _, sec := range m.Sections {
			f.Sections = append(f.Sections, quiz.Section{StartMs: sec.StartMs, Energy: sec.Energy})
		}
		for i := m.Downbeat; i >= 0 && i < len(m.BeatsMs); i += 4 {
			f.Bars = append(f.Bars, m.BeatsMs[i])
		}
	}
	if s.Graph != nil {
		if tr, ok, err := s.Graph.CachedTrack(ctx, songRef(t)); ok && err == nil {
			f.Rank = tr.Rank
			if f.Year == 0 {
				f.Year = tr.Year
			}
		}
		if a, ok, err := s.Graph.CachedArtist(ctx, musicgraph.ArtistRef{Name: f.Artist}); ok && err == nil {
			for _, tag := range a.Tags {
				f.Tags = append(f.Tags, tag.Name)
			}
		}
	}
	return f, nil
}

// notes are a song's liner notes: from the cache, or written now unless
// cachedOnly.
func (s *Sources) notes(ctx context.Context, t provider.Track, cachedOnly bool) (linernotes.Notes, bool) {
	if !cachedOnly && s.LinerNotes != nil {
		n, err := s.LinerNotes.Get(ctx, t)
		return n, err == nil
	}
	if s.DB == nil {
		return linernotes.Notes{}, false
	}
	row, err := s.DB.GetCachedLinerNotes(ctx, store.GetCachedLinerNotesParams{Provider: t.Ref.Provider, TrackID: t.Ref.ID, Now: s.now()})
	if err != nil || !row.Found {
		return linernotes.Notes{}, false
	}
	var n linernotes.Notes
	return n, json.Unmarshal([]byte(row.Notes), &n) == nil
}

// beatMap is a song's beat map, if it's been worked out. It's never
// worked out here: package analysis does that as songs are queued.
func (s *Sources) beatMap(ctx context.Context, it store.QueueItem) (analysis.Map, bool) {
	if s.DB == nil {
		return analysis.Map{}, false
	}
	p, id := it.Provider, it.TrackID
	if it.ViaLinkID.Valid {
		p, id = it.ViaProvider.String, it.ViaTrackID.String
	}
	row, err := s.DB.GetBeatMap(ctx, store.GetBeatMapParams{Provider: p, TrackID: id})
	if err != nil || !row.Found {
		return analysis.Map{}, false
	}
	var m analysis.Map
	return m, json.Unmarshal([]byte(row.Map), &m) == nil
}

// Most of each source a pool draws on.
const (
	poolArtists = 8
	poolTonight = 40
)

// Pool implements Facts: wrong answers from the song's neighbourhood,
// as already cached. Similar artists and their top songs come from the
// music graph; the night's songs and credits from the room's history.
func (s *Sources) Pool(ctx context.Context, roomID string, it store.QueueItem, f quiz.Facts) quiz.Pool {
	var p quiz.Pool
	if s.Graph != nil {
		if a, ok, err := s.Graph.CachedArtist(ctx, musicgraph.ArtistRef{Name: f.Artist}); ok && err == nil {
			for _, sim := range a.Similar[:min(len(a.Similar), poolArtists)] {
				p.Artists = append(p.Artists, sim.Artist.Name)
				p.Songs = append(p.Songs, s.top(ctx, sim.Artist)...)
			}
		}
		// Songs like the ones it samples, for sample questions.
		for _, smp := range slices.Concat(f.Samples, f.SampledBy) {
			a, ok, err := s.Graph.CachedArtist(ctx, musicgraph.ArtistRef{Name: smp.Artist})
			if !ok || err != nil {
				continue
			}
			p.Era = append(p.Era, s.top(ctx, a.Ref)...)
			for _, sim := range a.Similar[:min(len(a.Similar), 4)] {
				p.Era = append(p.Era, s.top(ctx, sim.Artist)...)
			}
		}
	}
	if s.DB == nil {
		return p
	}
	// The night's other songs, and who made them.
	since := time.Time{}
	if last, err := s.DB.LastNight(ctx, roomID); err == nil {
		since = last.EndedAt
	}
	plays, err := s.DB.PlaysSince(ctx, store.PlaysSinceParams{RoomID: roomID, Since: since, Limit: poolTonight})
	if err != nil {
		return p
	}
	for _, pl := range plays {
		if pl.QueueItemID == it.ID {
			continue
		}
		other, err := s.DB.GetQueueItem(ctx, pl.QueueItemID)
		if err != nil {
			continue
		}
		t := queuedTrack(other)
		p.Songs = append(p.Songs, quiz.Song{Title: t.Title, Artist: mainArtist(t)})
		if n, ok := s.notes(ctx, t, true); ok {
			for _, c := range n.Credits {
				if c.Role == "Produced by" || c.Role == "Written by" || c.Role == "Music by" || c.Role == "Lyrics by" {
					p.People = append(p.People, c.Names...)
				}
			}
		}
	}
	return p
}

// top are an artist's cached top songs.
func (s *Sources) top(ctx context.Context, a musicgraph.ArtistRef) []quiz.Song {
	art, ok, err := s.Graph.CachedArtist(ctx, a)
	if !ok || err != nil {
		return nil
	}
	var out []quiz.Song
	for _, t := range art.Top[:min(len(art.Top), 5)] {
		out = append(out, quiz.Song{Title: t.Title, Artist: cmpOr(t.Artist.Name, a.Name)})
	}
	return out
}

func mainArtist(t provider.Track) string {
	if len(t.Artists) == 0 {
		return ""
	}
	return t.Artists[0].Name
}

func songRef(t provider.Track) musicgraph.SongRef {
	return musicgraph.SongRef{Title: t.Title, Artist: musicgraph.ArtistRef{Name: mainArtist(t)}, ISRC: t.ISRC}
}

func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- Readiness -----------------------------------------------------------

// readiness caches what's known about songs coming up, so a round never
// waits on a service, and never starts on a song that can't answer it.
type readiness struct {
	mu    sync.Mutex
	songs map[string]readyEntry
}

type readyEntry struct {
	facts quiz.Facts
	at    time.Time
}

// Readiness limits: songs kept, and for how long.
const (
	maxReady = 500
	readyTTL = 6 * time.Hour
)

func newReadiness() *readiness { return &readiness{songs: map[string]readyEntry{}} }

func (r *readiness) get(k string) (quiz.Facts, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.songs[k]
	if !ok || time.Since(e.at) > readyTTL {
		return quiz.Facts{}, false
	}
	return e.facts, true
}

func (r *readiness) put(k string, f quiz.Facts) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.songs) >= maxReady {
		// Forget the oldest.
		var oldest string
		for k, e := range r.songs {
			if oldest == "" || e.at.Before(r.songs[oldest].at) {
				oldest = k
			}
		}
		delete(r.songs, oldest)
	}
	r.songs[k] = readyEntry{facts: f, at: time.Now()}
}

// prepareAhead is how many songs coming up are worked out ahead.
const prepareAhead = 4

// QueueChanged works out what's known about the songs coming up in a
// room that plays games, like beat maps are for queued songs, so rounds
// are ready when they play. Call it after a room's queue changes.
func (e *Engine) QueueChanged(roomID string) {
	if e.Facts == nil || e.ctx.Err() != nil {
		return
	}
	e.background(func(ctx context.Context) {
		row, err := e.rooms.Get(ctx, roomID)
		if err != nil || !rooms.ParseSettings(row.Settings).Games.Plays() {
			return
		}
		snap, err := e.rooms.QueueSnapshot(ctx, roomID)
		if err != nil {
			return
		}
		byID := map[string]store.QueueItem{}
		var ids []string
		for _, it := range snap.Items {
			byID[it.ID] = it
			if it.State == store.ItemPlaying {
				ids = append(ids, it.ID)
			}
		}
		ids = append(ids, snap.UpNext[:min(len(snap.UpNext), prepareAhead)]...)
		for _, id := range ids {
			it, ok := byID[id]
			if !ok {
				continue
			}
			if _, ok := e.ready.get(key(it)); ok {
				continue
			}
			f, err := e.Facts.Song(ctx, it, false)
			if errors.Is(err, context.Canceled) {
				return
			} else if err != nil {
				slog.Debug("games: song facts", "item", it.ID, "err", err)
				continue
			}
			e.ready.put(key(it), f)
		}
	})
}
