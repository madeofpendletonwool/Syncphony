// SPDX-License-Identifier: AGPL-3.0-only

package games

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Connect the artists (MAD-794): the big screen names two artists far
// apart in the music graph, and the room queues a chain of songs from one
// to the other, each by an artist similar to the last one's (either way
// round). The chain plays in the normal fair order, so it's also the
// playlist. Each link scores for whoever queued it; a hint names an artist
// on a shortest way from where the chain is, and links after it score
// less. With two teams, each has its own chain, and the shortest one to
// get there wins.

// Artists says how artists relate, for connect the artists, and what the
// night's songs give theme rounds to name. Sources is the production one.
type Artists interface {
	// Known are artists the room knows: tonight's, then its favorites'.
	Known(ctx context.Context, roomID string) ([]string, error)
	// Similar are artists like one, most alike first. fetch asks the
	// music graph's sources if it isn't cached.
	Similar(ctx context.Context, artist string, fetch bool) ([]Neighbour, error)
	// Themes are the producers and genres of the room's night.
	Themes(ctx context.Context, roomID string) quiz.ThemePool
}

// Neighbour is an artist like another, Score 0 to 1 alike.
type Neighbour struct {
	Name  string
	Score float64
}

// Connect is a connect the artists game.
type Connect struct {
	// From and To are the artists to connect.
	From, To string
	// Path is a shortest way between them, as the graph knew it when the
	// game began: From, the artists between, To. Screens get it at the end.
	Path []string
	// Chains are each team's chain; one without teams.
	Chains []Chain
	// Teams are who's on which team (an index into Chains).
	Teams map[string]int
	// Misses are the latest songs queued that didn't link, newest last.
	Misses []Miss
	// Winner is the winning team's chain, -1 for none (yet).
	Winner int

	graph *artistGraph
}

// Chain is a team's chain of links, from Connect.From.
type Chain struct {
	Links []Link
	// Hints are artists hinted on the way.
	Hints []string
	// Done is set once it reaches Connect.To, at DoneAt.
	Done   bool
	DoneAt time.Time
}

// Link is a song in a chain.
type Link struct {
	UserID, ItemID string
	Song           quiz.Song
	// Artist is the song's artist, the chain's newest; Score is how alike
	// they are to the one before, 0 to 1.
	Artist string
	Score  float64
	Points int
}

// Miss is a song queued during the game that didn't link.
type Miss struct {
	UserID, ItemID string
	Artist         string
	// After is the chain's artist it didn't link to; Reason says why.
	After, Reason string
}

// Connect points: a link, a link after a hint, reaching the end, and
// winning the race.
const (
	LinkPoints     = 200
	HintedPoints   = 100
	ConnectedBonus = 300
	RaceBonus      = 300
)

// How far apart the artists to connect are, in links, and how much of the
// graph is looked at to find them.
const (
	minHops       = 3
	maxHops       = 5
	exploreDepth  = 3
	exploreNodes  = 800
	neighboursTop = 12
	knownMax      = 40
	keptMisses    = 5
)

func (c *Connect) snapshot() *Connect {
	if c == nil {
		return nil
	}
	out := *c
	out.Path = slices.Clone(c.Path)
	out.Chains = make([]Chain, len(c.Chains))
	for i, ch := range c.Chains {
		out.Chains[i] = Chain{Links: slices.Clone(ch.Links), Hints: slices.Clone(ch.Hints), Done: ch.Done, DoneAt: ch.DoneAt}
	}
	out.Teams = make(map[string]int, len(c.Teams))
	for k, v := range c.Teams {
		out.Teams[k] = v
	}
	out.Misses = slices.Clone(c.Misses)
	out.graph = nil
	return &out
}

// last is the artist a chain ends on.
func (c *Connect) last(team int) string {
	if ls := c.Chains[team].Links; len(ls) > 0 {
		return ls[len(ls)-1].Artist
	}
	return c.From
}

// team is someone's team, picking one for them on their first link: the
// one with fewer people.
func (c *Connect) team(userID string) int {
	if t, ok := c.Teams[userID]; ok {
		return t
	}
	n := make([]int, len(c.Chains))
	for _, t := range c.Teams {
		n[t]++
	}
	t := 0
	for i := range n {
		if n[i] < n[t] {
			t = i
		}
	}
	c.Teams[userID] = t
	return t
}

// pair picks two artists the room knows, minHops to maxHops apart in the
// music graph.
func (e *Engine) pair(ctx context.Context, roomID string, teams int) (*Connect, error) {
	known, err := e.Artists.Known(ctx, roomID)
	if err != nil {
		return nil, err
	}
	known = known[:min(len(known), knownMax)]
	g := newArtistGraph()
	e.explore(ctx, g, known, false)
	e.mu.Lock()
	from, to, path, ok := g.pair(known, e.rng)
	e.mu.Unlock()
	if !ok {
		// Ask the sources about the room's own artists, then look again.
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		e.explore(ctx, g, known[:min(len(known), 8)], true)
		e.mu.Lock()
		from, to, path, ok = g.pair(known, e.rng)
		e.mu.Unlock()
	}
	if !ok {
		return nil, &InvalidInputError{"the music graph doesn't know tonight's artists well enough yet: play a few more songs"}
	}
	return &Connect{From: from, To: to, Path: path, Chains: make([]Chain, teams), Teams: map[string]int{}, Winner: -1, graph: g}, nil
}

// explore adds what's known about artists like these to the graph, out to
// exploreDepth. fetch asks the sources about the first ones.
func (e *Engine) explore(ctx context.Context, g *artistGraph, from []string, fetch bool) {
	frontier := from
	for depth := 0; depth < exploreDepth && len(frontier) > 0 && len(g.names) < exploreNodes; depth++ {
		var next []string
		for _, a := range frontier {
			if ctx.Err() != nil || len(g.names) >= exploreNodes {
				return
			}
			if g.done[g.add(a)] {
				continue
			}
			ns, err := e.Artists.Similar(ctx, a, fetch && depth == 0)
			if err != nil {
				continue
			}
			g.expand(a, ns)
			for _, n := range ns[:min(len(ns), neighboursTop)] {
				next = append(next, n.Name)
			}
		}
		frontier = next
	}
}

// link takes a song queued during the game: it extends its queuer's
// team's chain if its artist is like the chain's last, either way round.
// The caller holds the room's work lock.
func (e *Engine) link(ctx context.Context, g *QueueGame, it store.QueueItem) {
	t := queuedTrack(it)
	artist := mainArtist(t)
	if artist == "" {
		return
	}
	e.mu.Lock()
	c := g.Connect
	team := c.team(it.AddedBy)
	last := c.last(team)
	done := c.Chains[team].Done
	inChain := artistKey(artist) == artistKey(c.From) || slices.ContainsFunc(c.Chains[team].Links, func(l Link) bool {
		return artistKey(l.Artist) == artistKey(artist)
	})
	e.mu.Unlock()
	if done {
		return
	}
	miss := Miss{UserID: it.AddedBy, ItemID: it.ID, Artist: artist, After: last}
	score := 0.0
	switch {
	case artistKey(artist) == artistKey(last):
		miss.Reason = "the same artist"
	case inChain:
		miss.Reason = "already in the chain"
	default:
		score = e.alike(ctx, c.graph, last, artist)
		if score <= 0 {
			miss.Reason = "not close enough"
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if g.State != StateOpen || e.queueGame(g.RoomID, g.ID) != g {
		return
	}
	if score <= 0 {
		c.Misses = append(c.Misses, miss)
		c.Misses = c.Misses[max(0, len(c.Misses)-keptMisses):]
		e.publishQueue(g)
		return
	}
	ch := &c.Chains[team]
	points := LinkPoints
	if len(ch.Hints) > 0 {
		points = HintedPoints
	}
	ch.Links = append(ch.Links, Link{
		UserID: it.AddedBy, ItemID: it.ID, Song: quiz.Song{Title: t.Title, Artist: artist},
		Artist: artist, Score: math.Round(score*100) / 100, Points: points,
	})
	g.Points[it.AddedBy] += points
	if artistKey(artist) == artistKey(c.To) {
		ch.Done, ch.DoneAt = true, e.cfg.Now()
	}
	if !slices.ContainsFunc(c.Chains, func(ch Chain) bool { return !ch.Done }) {
		e.revealConnect(g)
		return
	}
	e.publishQueue(g)
}

// alike is how alike two artists are, 0 if the graph doesn't link them:
// from what's been looked at, else asking about both.
func (e *Engine) alike(ctx context.Context, g *artistGraph, a, b string) float64 {
	if s := g.edge(a, b); s > 0 {
		return s
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, x := range []string{b, a} {
		if g.done[g.add(x)] {
			continue
		}
		if ns, err := e.Artists.Similar(ctx, x, true); err == nil {
			g.expand(x, ns)
		} else if !errors.Is(err, context.Canceled) {
			slog.Debug("games: similar artists", "artist", x, "err", err)
		}
	}
	return g.edge(a, b)
}

// Hint names an artist on a shortest way from where someone's team's
// chain is to the end. Links after it score less.
func (e *Engine) Hint(_ context.Context, roomID, gameID string, by store.User, guest bool) (QueueGame, error) {
	e.mu.Lock()
	g := e.queueGame(roomID, gameID)
	var r *room
	if g != nil {
		r = e.byRoom[roomID]
	}
	e.mu.Unlock()
	if g == nil || g.Kind != rooms.GameConnect {
		return QueueGame{}, ErrNoGame
	}
	if guest && !g.Guests {
		return QueueGame{}, ErrGuestsCantPlay
	}
	r.work.Lock()
	defer r.work.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if g.State != StateOpen {
		return QueueGame{}, ErrNoGame
	}
	c := g.Connect
	team := c.team(by.ID)
	path := c.graph.path(c.last(team), c.To)
	if len(path) < 3 {
		return QueueGame{}, &InvalidInputError{"no hint: you're one song away, or the graph doesn't know a way from here"}
	}
	ch := &c.Chains[team]
	if !slices.Contains(ch.Hints, path[1]) {
		ch.Hints = append(ch.Hints, path[1])
		e.publishQueue(g)
	}
	return g.snapshot(), nil
}

// revealConnect ends the game: the shortest finished chain wins a race,
// and everyone who linked a finished chain scores the bonus. The caller
// holds mu.
func (e *Engine) revealConnect(g *QueueGame) {
	if g.State != StateOpen {
		return
	}
	c := g.Connect
	for i, ch := range c.Chains {
		if !ch.Done {
			continue
		}
		if w := c.Winner; w < 0 || len(ch.Links) < len(c.Chains[w].Links) ||
			(len(ch.Links) == len(c.Chains[w].Links) && ch.DoneAt.Before(c.Chains[w].DoneAt)) {
			c.Winner = i
		}
	}
	won := map[string]bool{}
	for i, ch := range c.Chains {
		if !ch.Done {
			continue
		}
		linked := map[string]bool{}
		for _, l := range ch.Links {
			linked[l.UserID] = true
		}
		for u := range linked {
			g.Points[u] += ConnectedBonus
			won[u] = true
			if len(c.Chains) > 1 && i == c.Winner {
				g.Points[u] += RaceBonus
			}
		}
	}
	e.saveQueue(g, "", g.StartedAt, g.Points, won)
	e.showResult(g)
}

// --- The graph -------------------------------------------------------------

// artistGraph is the part of the music graph a game has looked at, with
// similarity both ways round. Artists are keyed by match.Simplify.
type artistGraph struct {
	names map[string]string
	edges map[string]map[string]float64
	// done are the artists whose own similar artists have been added.
	done map[string]bool
}

func newArtistGraph() *artistGraph {
	return &artistGraph{names: map[string]string{}, edges: map[string]map[string]float64{}, done: map[string]bool{}}
}

func artistKey(name string) string { return match.Simplify(name) }

// add adds an artist, and returns its key.
func (g *artistGraph) add(name string) string {
	k := artistKey(name)
	if _, ok := g.names[k]; !ok {
		g.names[k] = name
	}
	return k
}

// expand adds an artist's similar artists.
func (g *artistGraph) expand(name string, ns []Neighbour) {
	k := g.add(name)
	g.done[k] = true
	for _, n := range ns[:min(len(ns), neighboursTop)] {
		nk := g.add(n.Name)
		if nk == k || nk == "" {
			continue
		}
		g.connect(k, nk, n.Score)
		g.connect(nk, k, n.Score)
	}
}

func (g *artistGraph) connect(a, b string, score float64) {
	if g.edges[a] == nil {
		g.edges[a] = map[string]float64{}
	}
	// A pair called alike with no score still links.
	g.edges[a][b] = max(g.edges[a][b], score, 0.01)
}

// edge is how alike two artists are, 0 if they don't link.
func (g *artistGraph) edge(a, b string) float64 { return g.edges[artistKey(a)][artistKey(b)] }

// bfs walks from an artist's key, and returns each reached key's
// distance and the key it was reached from.
func (g *artistGraph) bfs(from string) (dist map[string]int, prev map[string]string) {
	dist, prev = map[string]int{from: 0}, map[string]string{}
	queue := []string{from}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		// Sorted, so a path is the same each time.
		next := make([]string, 0, len(g.edges[k]))
		for n := range g.edges[k] {
			next = append(next, n)
		}
		slices.Sort(next)
		for _, n := range next {
			if _, seen := dist[n]; !seen {
				dist[n], prev[n] = dist[k]+1, k
				queue = append(queue, n)
			}
		}
	}
	return dist, prev
}

// path is a shortest way between two artists, by name, ends included;
// nil if there's none.
func (g *artistGraph) path(from, to string) []string {
	fk, tk := artistKey(from), artistKey(to)
	dist, prev := g.bfs(fk)
	if _, ok := dist[tk]; !ok {
		return nil
	}
	var out []string
	for k := tk; ; k = prev[k] {
		out = append(out, g.names[k])
		if k == fk {
			break
		}
	}
	slices.Reverse(out)
	return out
}

// pair picks two of the known artists minHops to maxHops apart, at
// random, and a shortest path between them.
func (g *artistGraph) pair(known []string, rng *rand.Rand) (from, to string, path []string, ok bool) {
	type cand struct{ a, b string }
	var cands []cand
	seen := map[string]bool{}
	var keys []string
	for _, a := range known {
		if k := artistKey(a); k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for i, a := range keys {
		dist, _ := g.bfs(a)
		for _, b := range keys[i+1:] {
			if d, ok := dist[b]; ok && d >= minHops && d <= maxHops {
				cands = append(cands, cand{a, b})
			}
		}
	}
	if len(cands) == 0 {
		return "", "", nil, false
	}
	c := cands[rng.IntN(len(cands))]
	if rng.IntN(2) == 0 {
		c.a, c.b = c.b, c.a
	}
	from, to = g.names[c.a], g.names[c.b]
	return from, to, g.path(from, to), true
}
