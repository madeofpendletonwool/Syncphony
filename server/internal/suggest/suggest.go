// SPDX-License-Identifier: AGPL-3.0-only

// Package suggest finds songs to keep a room's vibe going: songs like the
// ones the room has been playing and has queued. Search shows them before
// you type, as "your vibe" (like your own songs) and "group vibe" (like
// everyone's). A song the room skipped doesn't seed, and turns its artist
// away, unless the room finished or queued one of theirs. Autopilot finds
// its songs with the same Finder. See docs/adr/0010-suggestions.md.
//
// Suggestions only come from links the asker can add from, so every one
// can be queued with a tap. Services that recommend (provider.Recommender)
// give songs like each seed; services that only search give more by the
// seed's artist.
package suggest

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/dj"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Scope is whose songs suggestions are like.
type Scope string

const (
	// ScopeMine is like the asker's own songs.
	ScopeMine Scope = "mine"
	// ScopeGroup is like everyone's, members taking turns.
	ScopeGroup Scope = "group"
)

// ErrScope is a scope that isn't ScopeMine or ScopeGroup.
var ErrScope = errors.New("suggest: unknown scope")

// Origin is where a list's vibe is read from.
type Origin string

const (
	// OriginHistory reads what's playing and what played through.
	OriginHistory Origin = "history"
	// OriginQueue reads what's playing and waiting, so a queued change
	// of vibe is reflected. What played through stands in when nothing's
	// queued.
	OriginQueue Origin = "queue"
)

// ErrOrigin is an origin that isn't OriginHistory or OriginQueue.
var ErrOrigin = errors.New("suggest: unknown origin")

// UsableLinks opens links, and lists the ones a user may add songs from.
// It's links.Service.
type UsableLinks interface {
	Links
	Usable(ctx context.Context, userID string) ([]store.ServiceLink, error)
}

// How far suggestions look, and how hard they try.
const (
	// historyReach is how many of the room's plays are read: the songs not
	// to suggest, and where played seeds come from.
	historyReach = 200
	// maxSeeds is how many songs one list is built from.
	maxSeeds = 6
	// linksPerSeed is how many services are asked about each seed.
	linksPerSeed = 2
	// window is how near the top of a seed's candidates its picks stay.
	window = SimilarTop * 2
	// MaxLimit is the most suggestions one list has.
	MaxLimit = 50
)

// Suggestion is a song to queue, and the song it's like.
type Suggestion struct {
	Track provider.Track
	Seed  store.QueueItem
}

// Query asks for one list of suggestions.
type Query struct {
	RoomID, UserID string
	Scope          Scope
	// Origin is where the vibe is read from. The zero value reads as
	// history.
	Origin Origin
	Limit  int
	// Refresh skips the cached list, for a new shuffle.
	Refresh bool
}

// Service suggests songs to queue.
type Service struct {
	db    *store.Store
	rooms *rooms.Service
	links UsableLinks
	// TTL is how long a list is reused while the queue doesn't change.
	// Default 2m.
	TTL time.Duration
	// Timeout bounds building one list. Default 15s.
	Timeout time.Duration
	// Rand returns a number in [0, n). Default math/rand/v2's IntN.
	Rand func(n int) int
	// Now is the clock. Default time.Now.
	Now func() time.Time

	mu    sync.Mutex
	cache map[cacheKey]cached
}

// cacheKey is one list: a version of a room's queue, for one person.
type cacheKey struct {
	room, user string
	scope      Scope
	origin     Origin
	version    int64
	limit      int
}

type cached struct {
	at  time.Time
	out []Suggestion
}

// New returns a Service.
func New(db *store.Store, rs *rooms.Service, links UsableLinks) *Service {
	return &Service{
		db: db, rooms: rs, links: links,
		TTL: 2 * time.Minute, Timeout: 15 * time.Second, Rand: rand.IntN, Now: time.Now,
		cache: map[cacheKey]cached{},
	}
}

// Suggest returns songs like the room's, best first, mixing seeds so no
// one song decides the list. It's empty when there's nothing to go on yet,
// or no service the asker can add from has anything new.
func (s *Service) Suggest(ctx context.Context, q Query) ([]Suggestion, error) {
	if q.Scope != ScopeMine && q.Scope != ScopeGroup {
		return nil, ErrScope
	}
	if q.Origin == "" {
		q.Origin = OriginHistory
	}
	if q.Origin != OriginHistory && q.Origin != OriginQueue {
		return nil, ErrOrigin
	}
	q.Limit = min(max(q.Limit, 1), MaxLimit)
	snap, err := s.rooms.QueueSnapshot(ctx, q.RoomID)
	if err != nil {
		return nil, err
	}
	key := cacheKey{room: q.RoomID, user: q.UserID, scope: q.Scope, origin: q.Origin, version: snap.Version, limit: q.Limit}
	if !q.Refresh {
		if out, ok := s.cached(key); ok {
			return out, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	history, err := s.db.ListHistory(ctx, store.ListHistoryParams{RoomID: q.RoomID, Limit: historyReach})
	if err != nil {
		return nil, err
	}
	out, err := s.build(ctx, q, snap, history)
	if err != nil {
		return nil, err
	}
	s.store(key, out)
	return out, nil
}

func (s *Service) cached(k cacheKey) ([]Suggestion, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cache[k]
	if !ok || s.Now().Sub(c.at) >= s.TTL {
		return nil, false
	}
	return c.out, true
}

func (s *Service) store(k cacheKey, out []Suggestion) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	for old, c := range s.cache {
		if now.Sub(c.at) >= s.TTL || (old.room == k.room && old.user == k.user && old.scope == k.scope && old.origin == k.origin) {
			delete(s.cache, old)
		}
	}
	s.cache[k] = cached{at: now, out: out}
}

func (s *Service) build(ctx context.Context, q Query, snap rooms.QueueSnapshot, history []store.ListHistoryRow) ([]Suggestion, error) {
	seen := Seen{}
	for _, it := range snap.Items {
		seen.Item(it)
	}
	for _, h := range history {
		seen.Item(h.QueueItem)
	}
	seeds := Seeds(q.Scope, q.Origin, q.UserID, snap, history)
	if len(seeds) == 0 {
		return nil, nil
	}
	turned := TurnedAway(snap, history, s.Now())
	links, err := s.links.Usable(ctx, q.UserID)
	if err != nil {
		return nil, err
	}
	var recommend, search []store.ServiceLink
	for _, l := range links {
		p, err := s.links.Provider(l.Provider)
		switch {
		case err != nil:
		case p.Info().Capabilities.Recommendations:
			recommend = append(recommend, l)
		case p.Info().Capabilities.CanSearch(provider.KindTrack):
			search = append(search, l)
		}
	}
	if len(recommend)+len(search) == 0 {
		return nil, nil
	}

	f := NewFinder(s.links, s.Rand)
	defer f.Close()
	perSeed := (q.Limit + len(seeds) - 1) / len(seeds)
	lists := make([][]Suggestion, len(seeds))
	for i, sd := range seeds {
		if ctx.Err() != nil {
			break // out of time: suggest what was found
		}
		taken := Seen{}
		for _, l := range linksFor(sd, recommend, search) {
			var cands []provider.Track
			if slices.ContainsFunc(recommend, func(r store.ServiceLink) bool { return r.ID == l.ID }) {
				rec, sess, ok := f.Recommender(ctx, l.ID)
				if !ok {
					continue
				}
				cands = f.Similar(ctx, rec, sess, l, sd, rooms.AdventureSimilar)
			} else if sess, ok := f.Open(ctx, l.ID); ok {
				cands = f.ByArtist(ctx, sess, l, sd)
			}
			var fresh []provider.Track
			for _, t := range cands {
				if !seen.Has(t) && !taken.Has(t) && !turned[djArtist(t)] {
					taken.Track(t)
					fresh = append(fresh, t)
				}
			}
			for _, t := range s.sample(fresh, perSeed-len(lists[i])) {
				lists[i] = append(lists[i], Suggestion{Track: t, Seed: sd.Item})
			}
			if len(lists[i]) >= perSeed {
				break
			}
		}
	}

	// Take turns between seeds, so the list mixes them.
	var out []Suggestion
	dup := Seen{}
	for n := 0; len(out) < q.Limit; n++ {
		more := false
		for _, l := range lists {
			if n >= len(l) {
				continue
			}
			more = true
			if !dup.Has(l[n].Track) && len(out) < q.Limit {
				dup.Track(l[n].Track)
				out = append(out, l[n])
			}
		}
		if !more {
			break
		}
	}
	return out, nil
}

// sample picks up to n of the candidates near the top of the list, in
// their order, at random.
func (s *Service) sample(cands []provider.Track, n int) []provider.Track {
	pool := cands[:min(len(cands), max(window, 2*n))]
	if n <= 0 || len(pool) == 0 {
		return nil
	}
	idx := make([]int, len(pool))
	for i := range idx {
		idx[i] = i
	}
	var keep []int
	for range min(n, len(pool)) {
		j := s.Rand(len(idx))
		keep = append(keep, idx[j])
		idx = slices.Delete(idx, j, j+1)
	}
	slices.Sort(keep)
	out := make([]provider.Track, len(keep))
	for i, k := range keep {
		out[i] = pool[k]
	}
	return out
}

// Seeds returns the songs a list is like, best first: the song playing,
// then what's waiting in fair order, then what played through, newest
// first. The queue origin stops after what's playing and waiting, so a
// queued change of vibe is reflected; what played through stands in when
// nothing's queued. Mine is only the user's own; group takes turns
// between members, so nobody's taste takes over. Autopilot's songs and
// songs the room skipped don't seed.
func Seeds(scope Scope, origin Origin, userID string, snap rooms.QueueSnapshot, history []store.ListHistoryRow) []Seed {
	var all []store.QueueItem
	byID := map[string]store.QueueItem{}
	for _, it := range snap.Items {
		byID[it.ID] = it
		if it.State == store.ItemPlaying {
			all = append(all, it)
		}
	}
	for _, id := range snap.UpNext {
		all = append(all, byID[id])
	}
	if origin == OriginQueue {
		if out := pickSeeds(scope, userID, all); len(out) > 0 {
			return out
		}
	}
	for _, h := range history {
		if h.PlayHistory.EndedAt.Valid && h.PlayHistory.EndReason.String == store.EndFinished {
			all = append(all, h.QueueItem)
		}
	}
	return pickSeeds(scope, userID, all)
}

// TurnedAway returns the artists (dj.ArtistKey) the room turned away: as
// the DJ does (dj.Profile, ADR 0012), those whose decayed signal, skips
// against plays, hearts and queued songs, is below the DJ's threshold.
// A skip long ago, faded to almost nothing, doesn't turn an artist away
// (MAD-762). Songs by them aren't suggested. MAD-759 moves the rest of
// suggestions onto the DJ.
func TurnedAway(snap rooms.QueueSnapshot, history []store.ListHistoryRow, now time.Time) map[string]bool {
	return dj.NewProfile(dj.Input{History: history, Upcoming: snap.Items, Now: now}).Avoid
}

// pickSeeds picks the seeds from items: each member's songs take turns,
// up to maxSeeds.
func pickSeeds(scope Scope, userID string, items []store.QueueItem) []Seed {
	var users []string
	byUser := map[string][]Seed{}
	seen := Seen{}
	for _, it := range items {
		if it.ID == "" || it.IsAutopilot() || (scope == ScopeMine && it.AddedBy != userID) {
			continue
		}
		sd := SeedOf(it)
		if sd.Track.Title == "" || seen.Has(sd.Track) {
			continue
		}
		seen.Track(sd.Track)
		if _, ok := byUser[it.AddedBy]; !ok {
			users = append(users, it.AddedBy)
		}
		byUser[it.AddedBy] = append(byUser[it.AddedBy], sd)
	}
	var out []Seed
	for n := 0; len(out) < maxSeeds; n++ {
		more := false
		for _, u := range users {
			if n < len(byUser[u]) && len(out) < maxSeeds {
				out = append(out, byUser[u][n])
				more = true
			}
		}
		if !more {
			break
		}
	}
	return out
}

// linksFor orders the links to ask about a seed: services that recommend
// first, each kind starting with the one the seed played from.
func linksFor(sd Seed, recommend, search []store.ServiceLink) []store.ServiceLink {
	from, _ := Source(sd.Item)
	first := func(ls []store.ServiceLink) []store.ServiceLink {
		out := slices.Clone(ls)
		slices.SortStableFunc(out, func(a, b store.ServiceLink) int {
			switch {
			case a.ID == from && b.ID != from:
				return -1
			case b.ID == from && a.ID != from:
				return 1
			}
			return 0
		})
		return out
	}
	out := append(first(recommend), first(search)...)
	return out[:min(len(out), linksPerSeed)]
}

// djArtist is a track's artist as the DJ keys it.
func djArtist(t provider.Track) string {
	if len(t.Artists) == 0 {
		return ""
	}
	return dj.ArtistKey(t.Artists[0].Name)
}
