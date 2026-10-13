// SPDX-License-Identifier: AGPL-3.0-only

package discover

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
)

// The map (MAD-820): a graph of artists around one artist, or around a
// set's seeds, in the spirit of music-map.com. It reads the music graph's
// cache and fetches little: what isn't cached yet is warmed, so the map
// fills out when it's loaded again.
const (
	// DefaultMapLimit is how many artists a map shows, MaxMapLimit and
	// MinMapLimit how many it may be asked for.
	DefaultMapLimit = 60
	MaxMapLimit     = 100
	MinMapLimit     = 10
	// mapFetches is how many artists that aren't cached a map fetches:
	// the artist it's of, or the first seeds. The rest are warmed.
	mapFetches = 3
	// mapFetchFor bounds fetching for a map; what isn't found by then is
	// warmed, and the map says it's partial.
	mapFetchFor = 5 * time.Second
	// mapEdges is how many of each artist's strongest likenesses a map
	// draws, so sixty artists don't make a hairball.
	mapEdges = 3
	// mapPerHop2 is how many of a neighbor's nearest artists a map two
	// steps out takes.
	mapPerHop2 = 4
	// mapTags is how many tags a node carries.
	mapTags = 3
	// songsFor bounds looking up an artist's songs.
	songsFor = 10 * time.Second
)

// MapQuery is what a map is of: an artist (by name, and MBID if known) or
// one of the user's sets.
type MapQuery struct {
	Artist, MBID string
	SetID        string
	// Depth is how many steps out from the artist or seeds: 1 or 2. 0 is
	// the default: 2 around an artist, 1 around a set.
	Depth int
	// Limit is how many artists, at most; 0 is DefaultMapLimit.
	Limit int
}

// MapNode is an artist on a map.
type MapNode struct {
	// Key is how the artist is compared (library.ArtistKey), and what
	// edges name.
	Key, Name, MBID string
	// Fans is how many fans they have on Deezer, Picture their picture's
	// ID there; zero if unknown.
	Fans    int64
	Picture string
	// Tags are their strongest tags.
	Tags []string
	// Owned is whether the user has anything of theirs. Seed is whether
	// they're one of the set's seeds, Center whether the map is of them.
	// Blocked is whether the user said they aren't for them: only ever an
	// artist the map is of, or a seed, since others blocked are left out.
	Owned, Seed, Center, Blocked bool
	// Hop is how many steps they are from the artist or seeds.
	Hop int
	// Seeds is how many of the set's seeds they're near.
	Seeds int
	// Score is how near they are, 0 to 1: 1 for the artist or seeds.
	Score float64
	// Known is whether the music graph knows them yet. One it doesn't is
	// being looked up, and has no picture, tags or neighbors yet.
	Known bool
}

// MapEdge says two artists on a map are alike.
type MapEdge struct {
	From, To string
	// Score is how alike, 0 to 1.
	Score   float64
	Sources []string
}

// Map is a graph of artists.
type Map struct {
	// Set is the set it's of, if it's of one.
	Set   *Set
	Nodes []MapNode
	Edges []MapEdge
	// Partial is whether some artists weren't known yet: they're being
	// looked up, and loading the map again shows more.
	Partial bool
	// NoGraph is whether the server knows nothing of how music relates.
	NoGraph bool
	// Library is how far the user's library is indexed: until it's done,
	// artists they have may not show as theirs.
	Library library.Status
}

// Map returns a graph around an artist, or around a set's seeds. Artists
// the user blocked are left out, unless the map is of them.
func (s *Service) Map(ctx context.Context, userID string, q MapQuery) (Map, error) {
	q.Artist = strings.Join(strings.Fields(q.Artist), " ")
	switch {
	case (q.Artist == "") == (q.SetID == ""):
		return Map{}, &InvalidInputError{"a map is of an artist or a set"}
	case q.Depth < 0 || q.Depth > 2:
		return Map{}, &InvalidInputError{"depth is 1 or 2"}
	case q.Limit != 0 && (q.Limit < MinMapLimit || q.Limit > MaxMapLimit):
		return Map{}, &InvalidInputError{"limit is 10 to 100"}
	}
	limit := cmp.Or(q.Limit, DefaultMapLimit)
	var out Map
	var roots []musicgraph.ArtistRef
	if q.SetID != "" {
		set, err := s.Get(ctx, userID, q.SetID)
		if err != nil {
			return Map{}, err
		}
		out.Set = &set
		for _, a := range set.Artists {
			roots = append(roots, musicgraph.ArtistRef{Name: a.Name, MBID: a.Mbid.String})
		}
		q.Depth = cmp.Or(q.Depth, 1)
	} else {
		roots = []musicgraph.ArtistRef{{Name: q.Artist, MBID: strings.ToLower(strings.TrimSpace(q.MBID))}}
		q.Depth = cmp.Or(q.Depth, 2)
	}

	var owned *library.Owned
	if s.Library != nil {
		o, err := s.Library.For(ctx, userID)
		if err != nil {
			return Map{}, err
		}
		owned, out.Library = o, o.Status()
	}
	blocks, err := s.db.ListDiscoveryBlocks(ctx, userID)
	if err != nil {
		return Map{}, err
	}
	blocked := map[string]bool{}
	for _, b := range blocks {
		blocked[b.ArtistKey] = true
	}

	m := &mapper{roots: map[string]*node{}, nodes: map[string]*node{}, blocked: blocked}
	if s.Graph == nil {
		out.NoGraph = true
	} else {
		m.lk = &looker{g: s.Graph, ctx: ctx, budget: mapFetches, fetchBy: time.Now().Add(mapFetchFor)}
	}
	chosen := m.walk(roots, q.Depth, limit)

	// The nodes, the roots first, and what the graph knows of each.
	top := 0.0
	for _, n := range chosen {
		top = max(top, n.pull())
	}
	center := q.SetID == ""
	add := func(n *node, root bool) {
		k := library.ArtistKey(n.ref.Name)
		mn := MapNode{
			Key: k, Name: n.ref.Name, MBID: n.ref.MBID, Owned: owned.KnowsArtist(n.ref.Name),
			Seed: root && !center, Center: root && center, Blocked: blocked[k], Hop: n.hop, Seeds: len(n.near), Score: 1,
		}
		if root {
			mn.Seeds = 0
		} else {
			mn.Score = n.pull() / top
		}
		if a, ok := m.get(n.ref); ok {
			mn.Known, mn.Fans, mn.Picture = true, a.Fans, a.Picture
			mn.Name = cmp.Or(a.Ref.Name, mn.Name)
			mn.MBID = cmp.Or(mn.MBID, a.Ref.MBID)
			for _, t := range a.Tags[:min(len(a.Tags), mapTags)] {
				mn.Tags = append(mn.Tags, t.Name)
			}
		}
		out.Nodes = append(out.Nodes, mn)
	}
	for _, n := range m.order {
		add(n, true)
	}
	for _, n := range chosen {
		add(n, false)
	}
	out.Edges = m.edges(out.Nodes)
	if m.lk != nil {
		out.Partial = m.lk.warm()
	}
	return out, nil
}

// mapper walks the graph for a map.
type mapper struct {
	lk *looker
	// roots are the artist the map is of, or the seeds, by key; order is
	// them as given.
	roots map[string]*node
	order []*node
	// nodes are the artists reached, not roots.
	nodes   map[string]*node
	blocked map[string]bool
}

// get returns what the graph knows of an artist. Only the roots are
// fetched if they aren't cached; anyone else is warmed.
func (m *mapper) get(ref musicgraph.ArtistRef) (musicgraph.Artist, bool) {
	if m.lk == nil {
		return musicgraph.Artist{}, false
	}
	_, root := m.roots[library.ArtistKey(ref.Name)]
	return m.lk.get(ref, root)
}

// reach records that a walk reached an artist: near root by near, hop
// steps out. Roots and blocked artists are passed over.
func (m *mapper) reach(into map[string]*node, ref musicgraph.ArtistRef, root string, near float64, hop int) {
	k := library.ArtistKey(ref.Name)
	if _, isRoot := m.roots[k]; k == "" || isRoot || m.blocked[k] {
		return
	}
	n, ok := into[k]
	if !ok {
		n = &node{ref: ref, near: map[string]float64{}, hop: hop}
		into[k] = n
	}
	n.near[root] = max(n.near[root], near)
	n.hop = min(n.hop, hop)
	n.ref.MBID = cmp.Or(n.ref.MBID, ref.MBID)
}

// walk returns the artists to show besides the roots, up to limit in all
// (and always some, however many roots): the roots' neighbors near the
// most roots first, and, two steps out, their nearest neighbors' too.
func (m *mapper) walk(roots []musicgraph.ArtistRef, depth, limit int) []*node {
	for _, ref := range roots {
		k := library.ArtistKey(ref.Name)
		if _, dup := m.roots[k]; k == "" || dup {
			continue
		}
		n := &node{ref: ref, near: map[string]float64{k: 1}, seed: true}
		m.roots[k] = n
		m.order = append(m.order, n)
	}
	slots := max(limit-len(m.order), MinMapLimit)
	for _, n := range m.order {
		k := library.ArtistKey(n.ref.Name)
		a, ok := m.get(n.ref)
		if !ok {
			continue
		}
		for _, sim := range a.Similar[:min(len(a.Similar), max(slots, similarPerSeed))] {
			m.reach(m.nodes, sim.Artist, k, sim.Score, 1)
		}
	}
	hop1 := byOverlap(m.nodes)
	if depth < 2 {
		return hop1[:min(len(hop1), slots)]
	}
	// Two steps out: half the room for the nearest neighbors, the rest
	// for their own nearest, then more neighbors if that's not enough.
	chosen := hop1[:min(len(hop1), (slots+1)/2)]
	taken := map[string]bool{}
	for _, n := range chosen {
		taken[library.ArtistKey(n.ref.Name)] = true
	}
	far := map[string]*node{}
	for _, n := range chosen {
		a, ok := m.get(n.ref)
		if !ok {
			continue
		}
		root, pull := strongest(n.near)
		got := 0
		for _, sim := range a.Similar {
			if got == mapPerHop2 {
				break
			}
			k := library.ArtistKey(sim.Artist.Name)
			if _, isRoot := m.roots[k]; isRoot || taken[k] || m.blocked[k] {
				continue
			}
			m.reach(far, sim.Artist, root, pull*sim.Score*hop2Damping, 2)
			got++
		}
	}
	for _, n := range byOverlap(far) {
		if len(chosen) == slots {
			break
		}
		chosen = append(chosen, n)
		taken[library.ArtistKey(n.ref.Name)] = true
	}
	for _, n := range hop1 {
		if len(chosen) == slots {
			break
		}
		if !taken[library.ArtistKey(n.ref.Name)] {
			chosen = append(chosen, n)
		}
	}
	return chosen
}

// byOverlap returns nodes near the most roots first, then nearest.
func byOverlap(nodes map[string]*node) []*node {
	out := ranked(nodes)
	slices.SortStableFunc(out, func(a, b *node) int { return cmp.Compare(len(b.near), len(a.near)) })
	return out
}

// edges are the likenesses the graph knows between a map's artists, each
// pair once, as alike as the most alike source says: those among either
// artist's mapEdges strongest.
func (m *mapper) edges(nodes []MapNode) []MapEdge {
	on := map[string]bool{}
	for _, n := range nodes {
		on[n.Key] = true
	}
	type pair struct{ a, b string }
	found := map[pair]*MapEdge{}
	var out []*MapEdge
	for _, n := range nodes {
		if !n.Known {
			continue
		}
		a, _ := m.get(musicgraph.ArtistRef{Name: n.Name, MBID: n.MBID})
		for _, sim := range a.Similar {
			k := library.ArtistKey(sim.Artist.Name)
			if !on[k] || k == n.Key {
				continue
			}
			p := pair{n.Key, k}
			if p.b < p.a {
				p.a, p.b = p.b, p.a
			}
			e, ok := found[p]
			if !ok {
				e = &MapEdge{From: n.Key, To: k}
				found[p] = e
				out = append(out, e)
			}
			e.Score = max(e.Score, sim.Score)
			for _, src := range sim.Sources {
				if !slices.Contains(e.Sources, src) {
					e.Sources = append(e.Sources, src)
				}
			}
		}
	}
	// Each artist's strongest.
	byNode := map[string][]*MapEdge{}
	for _, e := range out {
		byNode[e.From] = append(byNode[e.From], e)
		byNode[e.To] = append(byNode[e.To], e)
	}
	keep := map[*MapEdge]bool{}
	for _, es := range byNode {
		slices.SortStableFunc(es, func(a, b *MapEdge) int { return cmp.Compare(b.Score, a.Score) })
		for _, e := range es[:min(len(es), mapEdges)] {
			keep[e] = true
		}
	}
	edges := make([]MapEdge, 0, len(keep))
	for _, e := range out {
		if !keep[e] {
			continue
		}
		if e.Sources == nil {
			e.Sources = []string{}
		}
		edges = append(edges, *e)
	}
	return edges
}

// Songs are an artist's songs the user doesn't have.
type Songs struct {
	Items   []Item
	Library library.Status
	NoGraph bool
}

// ArtistSongs returns an artist's best-known songs that the user doesn't
// have, hasn't turned down and doesn't want yet: for a map's artist.
// Nothing if they blocked the artist.
func (s *Service) ArtistSongs(ctx context.Context, userID, name, mbid string) (Songs, error) {
	name = strings.Join(strings.Fields(name), " ")
	if library.ArtistKey(name) == "" {
		return Songs{}, &InvalidInputError{"an artist's name is needed"}
	}
	out := Songs{Items: []Item{}, NoGraph: s.Graph == nil}
	keep, status, err := s.filter(ctx, userID)
	if err != nil {
		return Songs{}, err
	}
	out.Library = status
	if s.Graph == nil {
		return out, nil
	}
	lctx, cancel := context.WithTimeout(ctx, songsFor)
	defer cancel()
	lk := &looker{g: s.Graph, ctx: lctx, budget: 1}
	a, ok := lk.get(musicgraph.ArtistRef{Name: name, MBID: strings.ToLower(strings.TrimSpace(mbid))}, true)
	lk.warm()
	if !ok {
		return out, nil
	}
	for _, so := range a.Top {
		if len(out.Items) == songsPerArtist {
			break
		}
		artist := cmp.Or(so.Artist.Name, a.Ref.Name, name)
		if _, variants := match.Title(so.Title); len(variants) > 0 || so.Title == "" {
			continue
		}
		it := Item{
			Key: SongKey(artist, so.Title), Title: so.Title, Artist: artist, Album: so.Album,
			MBID: so.MBID, ISRC: so.ISRC, DeezerID: so.DeezerID, Cover: so.Cover, Because: []string{}, Score: so.Score,
		}
		if !keep(it) || slices.ContainsFunc(out.Items, func(o Item) bool { return o.Key == it.Key }) {
			continue
		}
		out.Items = append(out.Items, it)
	}
	s.resolve(ctx, nil, out.Items)
	return out, nil
}
