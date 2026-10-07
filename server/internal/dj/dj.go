// SPDX-License-Identifier: AGPL-3.0-only

// Package dj picks what a room should hear next. It reads the room's
// taste (Profile), walks the music graph out from the artists it likes
// (internal/musicgraph), scores what it finds on similarity, popularity
// and novelty, and finds the songs it picks on the room's services with
// cross-service matching. See docs/adr/0012-smart-dj.md.
//
// Autopilot uses it to fill a room whose queue ran dry. It knows nothing
// about queues or turns: it's given what the room did, and returns songs
// to try, best first.
package dj

import (
	"cmp"
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Graph is the music knowledge the DJ walks. musicgraph.Service is one.
type Graph interface {
	Artist(ctx context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, error)
	CachedArtist(ctx context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, bool, error)
	CachedTrack(ctx context.Context, s musicgraph.SongRef) (musicgraph.Track, bool, error)
	WarmArtist(as ...musicgraph.ArtistRef)
	WarmTrack(ss ...musicgraph.SongRef)
}

// Sessions opens links. suggest.Finder is one: it keeps the sessions it
// opens until it's closed.
type Sessions interface {
	Open(ctx context.Context, linkID string) (provider.Session, bool)
}

// How hard the DJ tries.
const (
	// walkTimeout bounds walking the graph, including fetching the room's
	// favorite artists that aren't cached yet; the rest of a fill's time
	// is for finding the picks on the room's services.
	walkTimeout = 12 * time.Second
	// linksPerSong is how many services each pick is looked for on.
	linksPerSong = 2
	// defaultWant is how many picks Choose returns, unless asked.
	defaultWant = 2
	// findAtOnce is how many picks are looked for on services at once.
	findAtOnce = 6
)

// Engine picks songs.
type Engine struct {
	Graph Graph
	// Rand returns a number in [0, n).
	Rand func(n int) int
}

// Request asks for songs for a room.
type Request struct {
	Input
	// Explore is how far to stray, from 0 to 100 (rooms.Autopilot.Explore).
	Explore int
	// Links are the services the songs may come from, best first: ones
	// that can search for tracks.
	Links []store.ServiceLink
	// Sessions opens them.
	Sessions Sessions
	// Fresh reports whether a song found on a service may play: not heard
	// lately, by service and ID, ISRC, or artist and title.
	Fresh func(provider.Track) bool
	// Want is how many picks to return. Default 2.
	Want int
	// LongTerm is the room's taste over past nights (Memory.Load): a weak
	// prior on the picks, and where throwbacks come from.
	LongTerm LongTerm
	// EnergyCurve follows the room's energy curve (rooms.Autopilot): a
	// gentle rise over a session, then settling, moved by the time of day.
	EnergyCurve bool
	// Member narrows the taste to one member's, for their own suggestions
	// ("your vibe", ADR 0010). The room's skips still turn artists away.
	Member string
	// Queued reads the taste from the members' songs playing and waiting
	// (Member's, if set), when there are any, so a queued change of vibe
	// is the vibe.
	Queued bool
}

// Pick is a song the DJ chose.
type Pick struct {
	Track provider.Track
	// ForUser is the member whose taste led to it, or "" if only
	// autopilot's own songs did.
	ForUser string
	// Seed is the member's song by the artist it leads from. Its ID is ""
	// if there's none.
	Seed store.QueueItem
	Why  Why
}

// Why says why the DJ chose a song: the parts of its score.
type Why struct {
	// Kind is KindArtist, KindSimilarArtist, KindTwoSteps, KindSimilarSong
	// or KindThrowback.
	Kind string
	// Via is the room's artist it leads from: for a throwback, the artist
	// the room loved.
	Via string
	// Similarity to the room's taste, popularity among the artist's songs,
	// novelty to the room, and how much the room's past nights favor it,
	// each 0 to 1; and the score they made.
	Similarity, Popularity, Novelty, Prior, Score float64
	// LovedAt is when the night ended that the room last liked a
	// throwback's artist.
	LovedAt time.Time
	// DeepCut is a loved artist's lesser-known song, picked for being one.
	DeepCut bool
	// Sources are the sources that led to it.
	Sources []string
	// Flow is how it fits the set (MAD-757).
	Flow Flow
	// Bridge is the members' tastes it's between, if it was picked as a
	// bridge; Turn, the member whose turn it was, if it wasn't (MAD-758).
	Bridge *Bridge
	Turn   string
}

// Choose returns songs for the room to hear next, best first, found on
// its services. It's empty when the room has nothing to go on yet, or the
// graph knows nothing near its taste; autopilot then falls back on its
// services' own recommendations.
func (e *Engine) Choose(ctx context.Context, r Request) []Pick {
	in := r.Input
	if in.Tags == nil {
		in.Tags = CachedTags(ctx, e.Graph, in)
	}
	if in.Related == nil {
		in.Related = Related(ctx, e.Graph)
	}
	p := NewProfile(in).narrow(in, r.Member, r.Queued)
	if len(p.Artists) == 0 || len(r.Links) == 0 {
		return nil
	}
	order := e.shortlist(ctx, p, r, in.Tags)
	want := cmp.Or(r.Want, defaultWant)
	sessions := &lockedSessions{s: r.Sessions}
	var out []Pick
	taken := map[provider.TrackRef]bool{}
	for len(order) > 0 && len(out) < want {
		if ctx.Err() != nil {
			break // out of time: play what was found
		}
		// Look for as many as are still wanted at once, in the draw's
		// order, so a fill tries the same candidates as one by one.
		batch := order[:min(len(order), want-len(out), findAtOnce)]
		order = order[len(batch):]
		found := make([]provider.Track, len(batch))
		ok := make([]bool, len(batch))
		var wg sync.WaitGroup
		for i, c := range batch {
			wg.Go(func() { found[i], ok[i] = e.find(ctx, r, sessions, p, c) })
		}
		wg.Wait()
		for i, c := range batch {
			if !ok[i] || taken[found[i].Ref] {
				continue
			}
			taken[found[i].Ref] = true
			out = append(out, p.pick(r, c, found[i]))
		}
	}
	return out
}

// pick is candidate c, found as t, with why it was chosen and whom for.
func (p Profile) pick(r Request, c candidate, t provider.Track) Pick {
	taste := p.Artists[c.via]
	pick := Pick{Track: t, Why: Why{
		Kind: c.kind, Similarity: c.similarity, Popularity: c.popularity, Novelty: c.novelty,
		Prior: c.prior, Score: c.score, DeepCut: c.deepCut, Sources: c.sources, LovedAt: c.lovedAt, Flow: c.flow,
		Bridge: c.bridge, Turn: c.turn,
	}}
	switch {
	case c.kind == KindThrowback:
		pick.Why.Via = r.LongTerm.Artists[c.via].Name
	case taste != nil:
		pick.ForUser, pick.Seed, pick.Why.Via = taste.Fan(), taste.Seed, taste.Artist.Name
	}
	// A bridge is for the one of its members served longer ago; a turn,
	// for its member, if the song is near their taste.
	switch {
	case c.bridge != nil:
		pick.ForUser = p.servedLongestAgo(c.bridge.Users[:])
		pick.Seed = p.memberSeed(pick.ForUser, c.reachVia[pick.ForUser])
	case c.turn != "" && c.reach[c.turn] >= bridgeReach:
		pick.ForUser = c.turn
		pick.Seed = p.memberSeed(c.turn, c.reachVia[c.turn])
	}
	return pick
}

// lockedSessions opens links one at a time, for finding picks at once:
// a Sessions like suggest.Finder isn't safe for concurrent use. The
// sessions it opens are.
type lockedSessions struct {
	mu sync.Mutex
	s  Sessions
}

func (l *lockedSessions) Open(ctx context.Context, linkID string) (provider.Session, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s.Open(ctx, linkID)
}

// shortlist returns the candidates to try, in order: drawn from the best
// the walk found, scored on how they flow from what the room just heard.
func (e *Engine) shortlist(ctx context.Context, p Profile, r Request, tags map[string][]musicgraph.Tag) []candidate {
	x := float64(min(max(r.Explore, 0), 100)) / 100
	var back []string
	if e.Rand(100) < throwbackChance(p) {
		back = Throwbacks(p, r.LongTerm)
	}
	wctx, cancel := context.WithTimeout(ctx, walkTimeout)
	cands := e.walk(wctx, p, r.LongTerm, x, back)
	cancel()
	deepCut := e.Rand(100) < deepCutPercent
	mx := mixFor(p)
	// A long list (suggestions) draws from a longer shortlist.
	n := max(drawn, 2*cmp.Or(r.Want, defaultWant))
	pool := e.flowing(ctx, p, tags, diverse(mixed(score(cands, p, r.LongTerm, x, deepCut), p, mx), max(shortlist, n)), r.EnergyCurve, r.Now)
	for i := range pool {
		pool[i].turn = mx.turn
	}
	return draw(pool, n, lerp(tempFamiliar, tempExplore, x), e.Rand)
}

// CachedTags returns the tags of the artists the room played lately, as
// far as the graph's cache knows them. It's for Input.Tags.
func CachedTags(ctx context.Context, g Graph, in Input) map[string][]musicgraph.Tag {
	out := map[string][]musicgraph.Tag{}
	look := func(it store.QueueItem) {
		name := artistOf(TrackOf(it))
		k := ArtistKey(name)
		if _, done := out[k]; done || k == "" {
			return
		}
		out[k] = nil
		if a, ok, err := g.CachedArtist(ctx, musicgraph.ArtistRef{Name: name}); ok && err == nil {
			out[k] = a.Tags
		}
	}
	for _, it := range in.Upcoming {
		look(it)
	}
	for _, h := range in.History[:min(len(in.History), profileReach)] {
		look(h.QueueItem)
	}
	return out
}

// find looks for a candidate on the room's services: first the one the
// song it leads from played from, then the others in order.
func (e *Engine) find(ctx context.Context, r Request, sessions Sessions, p Profile, c candidate) (provider.Track, bool) {
	want := provider.Track{Title: c.song.Title, ISRC: c.song.ISRC, MBID: c.song.MBID, Artists: []provider.ArtistCredit{{Name: c.song.Artist.Name}}}
	links := r.Links
	if taste := p.Artists[c.via]; taste != nil && taste.Seed.ID != "" {
		links = first(links, source(taste.Seed))
	}
	for _, l := range links[:min(len(links), linksPerSong)] {
		sess, ok := sessions.Open(ctx, l.ID)
		if !ok {
			continue
		}
		t, score, err := match.On(ctx, sess, want)
		if err != nil {
			slog.Debug("dj: finding a song", "link", l.ID, "title", c.song.Title, "err", err)
			continue
		}
		if score == 0 || (r.Fresh != nil && !r.Fresh(t)) || p.Heard[SongKey(artistOf(t), t.Title)] {
			continue
		}
		if t.Album.Title != "" && p.RecentAlbums[match.Simplify(t.Album.Title)] {
			continue
		}
		return t, true
	}
	return provider.Track{}, false
}

// source is the link an item played from: its stand-in's, or its own.
func source(it store.QueueItem) string {
	if it.ViaLinkID.Valid {
		return it.ViaLinkID.String
	}
	return it.LinkID.String
}

// first moves the link with id to the front, if it's there.
func first(ls []store.ServiceLink, id string) []store.ServiceLink {
	for i, l := range ls {
		if l.ID == id {
			out := append([]store.ServiceLink{l}, ls[:i]...)
			return append(out, ls[i+1:]...)
		}
	}
	return ls
}
