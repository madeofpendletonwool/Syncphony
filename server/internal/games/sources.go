// SPDX-License-Identifier: AGPL-3.0-only

package games

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/analysis"
	"github.com/madeofpendletonwool/syncphony/server/internal/clips"
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
	// Providers say which services stream, for name that tune's clips.
	// Nil finds no tunes.
	Providers Providers
	// New finds songs a room has never played, by artists it likes (the
	// DJ's picks), for name that tune. Nil finds none.
	New func(ctx context.Context, roomID string) ([]provider.Track, error)
	// Now is the clock. Default store.Now.
	Now func() time.Time
}

// Providers looks providers up. links.Service is one.
type Providers interface {
	Provider(id string) (provider.Provider, error)
}

var (
	_ Facts = (*Sources)(nil)
	_ Tunes = (*Sources)(nil)
)

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
			if len(n.Release.Labels) > 0 {
				f.Label = n.Release.Labels[0]
			}
		}
		if n.Artist != nil && n.Artist.Area != "" {
			f.Origin, f.OriginLine = n.Artist.Area, n.Artist.About
			for _, fact := range n.Facts {
				if fact.Kind == linernotes.FactOrigin {
					f.OriginLine = fact.Text
				}
			}
		}
		if c := n.CoverOf; c != nil {
			f.CoverOf = &quiz.Original{Title: c.Title, Writers: c.Writers}
		}
		for _, r := range n.Samples {
			f.Samples = append(f.Samples, quiz.Song{Title: r.Title, Artist: r.Artist, ID: r.MBID})
		}
		for _, r := range n.SampledBy {
			f.SampledBy = append(f.SampledBy, quiz.Song{Title: r.Title, Artist: r.Artist, ID: r.MBID})
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
		if t.Album.Title != "" {
			p.Albums = append(p.Albums, t.Album.Title)
		}
		n, ok := s.notes(ctx, t, true)
		if !ok {
			continue
		}
		for _, c := range n.Credits {
			switch c.Role {
			case "Produced by", "Written by", "Music by", "Lyrics by":
				p.People = append(p.People, c.Names...)
			case "Arranged by", "Mixed by", "Engineered by", "Mastered by":
			default:
				p.Players = append(p.Players, c.Names...)
			}
		}
		if n.Release != nil {
			p.Labels = append(p.Labels, n.Release.Labels...)
		}
		if n.Artist != nil && n.Artist.Area != "" {
			p.Places = append(p.Places, n.Artist.Area)
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

// --- Tunes ---------------------------------------------------------------

// How far finding tunes looks: tonight's songs, the room's songs over
// every night, and the favorites drawn from them.
const (
	tunesTonight   = 60
	tunesEver      = 1000
	tunesFavorites = 30
)

// Tunes implements Tunes: songs from where the room's settings say, then,
// if there are none, from the other places in turn. Only songs whose
// service streams (or that play through a stand-in that does).
func (s *Sources) Tunes(ctx context.Context, roomID, from string) ([]Tune, error) {
	if s.DB == nil || s.Providers == nil {
		return nil, nil
	}
	order := append([]string{from}, slices.DeleteFunc(slices.Clone(rooms.TuneSources), func(f string) bool { return f == from })...)
	for _, f := range order {
		var items []store.QueueItem
		var err error
		switch f {
		case rooms.TuneTonight:
			items, err = s.tonight(ctx, roomID)
		case rooms.TuneFavorites:
			items, err = s.favorites(ctx, roomID)
		case rooms.TuneNew:
			items, err = s.newSongs(ctx, roomID)
		}
		if err != nil {
			return nil, err
		}
		if out := s.streamable(items); len(out) > 0 {
			return out, nil
		}
	}
	return nil, nil
}

// tonight are the songs the room played tonight, shuffled.
func (s *Sources) tonight(ctx context.Context, roomID string) ([]store.QueueItem, error) {
	since := time.Time{}
	if last, err := s.DB.LastNight(ctx, roomID); err == nil {
		since = last.EndedAt
	} else if !store.IsNotFound(err) {
		return nil, err
	}
	plays, err := s.DB.PlaysSince(ctx, store.PlaysSinceParams{RoomID: roomID, Since: since, Limit: tunesTonight})
	if err != nil {
		return nil, err
	}
	var out []store.QueueItem
	for _, pl := range plays {
		if it, err := s.DB.GetQueueItem(ctx, pl.QueueItemID); err == nil {
			out = append(out, it)
		}
	}
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] }) //nolint:gosec // picking songs, not secrets
	return out, nil
}

// favorites are the songs the room loved most over every night: played
// through most, and hearted (a heart counts as two plays). The top ones
// are shuffled, so a set isn't always the same.
func (s *Sources) favorites(ctx context.Context, roomID string) ([]store.QueueItem, error) {
	rows, err := s.DB.FavoriteItems(ctx, store.FavoriteItemsParams{RoomID: roomID, Limit: tunesEver})
	if err != nil {
		return nil, err
	}
	score := map[string]int64{}
	latest := map[string]store.QueueItem{}
	var keys []string
	for _, r := range rows {
		k := key(r.QueueItem)
		if _, ok := latest[k]; !ok {
			latest[k] = r.QueueItem
			keys = append(keys, k)
		}
		score[k] += 1 + 2*r.Hearts
	}
	slices.SortStableFunc(keys, func(a, b string) int { return cmp.Compare(score[b], score[a]) })
	keys = keys[:min(len(keys), tunesFavorites)]
	rand.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] }) //nolint:gosec // as above
	out := make([]store.QueueItem, 0, len(keys))
	for _, k := range keys {
		out = append(out, latest[k])
	}
	return out, nil
}

// newSongs are songs the room has never played, by artists it likes.
func (s *Sources) newSongs(ctx context.Context, roomID string) ([]store.QueueItem, error) {
	if s.New == nil {
		return nil, nil
	}
	ts, err := s.New(ctx, roomID)
	if err != nil {
		return nil, err
	}
	out := make([]store.QueueItem, 0, len(ts))
	for _, t := range ts {
		b, err := json.Marshal(t)
		if err != nil {
			continue
		}
		out = append(out, store.QueueItem{
			RoomID: roomID, Provider: t.Ref.Provider, LinkID: sql.NullString{String: t.Ref.LinkID, Valid: t.Ref.LinkID != ""},
			TrackID: t.Ref.ID, Metadata: string(b),
		})
	}
	return out, nil
}

// streamable are the items that can be clipped, once each.
func (s *Sources) streamable(items []store.QueueItem) []Tune {
	seen := map[string]bool{}
	var out []Tune
	for _, it := range items {
		k := key(it)
		if seen[k] {
			continue
		}
		seen[k] = true
		p, linkID, trackID := it.Provider, it.LinkID.String, it.TrackID
		if it.ViaLinkID.Valid {
			// A remote service's song with a stand-in that streams.
			p, linkID, trackID = it.ViaProvider.String, it.ViaLinkID.String, it.ViaTrackID.String
		}
		if linkID == "" {
			continue
		}
		prov, err := s.Providers.Provider(p)
		if err != nil || prov.Info().Capabilities.Playback != provider.PlaybackStream {
			continue
		}
		out = append(out, Tune{Item: it, Song: clips.Song{LinkID: linkID, TrackID: trackID}})
	}
	return out
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
