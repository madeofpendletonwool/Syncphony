// SPDX-License-Identifier: AGPL-3.0-only

package discover_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/discover"
	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

func node(m discover.Map, name string) (discover.MapNode, bool) {
	i := slices.IndexFunc(m.Nodes, func(n discover.MapNode) bool { return n.Name == name })
	if i < 0 {
		return discover.MapNode{}, false
	}
	return m.Nodes[i], true
}

func edge(m discover.Map, a, b string) bool {
	ka, kb := library.ArtistKey(a), library.ArtistKey(b)
	return slices.ContainsFunc(m.Edges, func(e discover.MapEdge) bool {
		return (e.From == ka && e.To == kb) || (e.From == kb && e.To == ka)
	})
}

func TestMapOfASet(t *testing.T) {
	e := newEnv(t)
	credit := []provider.ArtistCredit{{Name: "Lush"}}
	e.svc.Library = owned{library.Holding(provider.Track{Title: "Sweetness and Light", Artists: credit})}
	if _, err := e.svc.Block(t.Context(), e.alice, "Shiner"); err != nil {
		t.Fatal(err)
	}
	set := e.set(t, "Slowdive", "Hum", "Duster")
	m, err := e.svc.Map(t.Context(), e.alice, discover.MapQuery{SetID: set.ID})
	if err != nil {
		t.Fatal(err)
	}
	if m.Set == nil || m.Set.ID != set.ID {
		t.Fatalf("set: %+v", m.Set)
	}
	// The seeds first, ringed; then the artist near all three.
	for i, name := range []string{"Slowdive", "Hum", "Duster"} {
		if n := m.Nodes[i]; n.Name != name || !n.Seed || n.Center || n.Score != 1 {
			t.Errorf("node %d: %+v", i, n)
		}
	}
	if n := m.Nodes[3]; n.Name != "Swirlies" || n.Seeds != 3 || n.Hop != 1 {
		t.Errorf("first neighbor: %+v", n)
	}
	if n, ok := node(m, "Failure"); !ok || n.Seeds != 2 {
		t.Errorf("Failure, near Hum and Duster: %+v", n)
	}
	if n, _ := node(m, "Lush"); !n.Owned {
		t.Error("Lush isn't shown as owned")
	}
	if n, _ := node(m, "Ride"); n.Owned || !n.Known || n.Fans != 100_000 {
		t.Errorf("Ride: %+v", n)
	}
	if _, ok := node(m, "Shiner"); ok {
		t.Error("a blocked artist is on the map")
	}
	if !edge(m, "Slowdive", "Swirlies") || !edge(m, "Hum", "Swirlies") || !edge(m, "Slowdive", "Hum") {
		t.Errorf("edges: %+v", m.Edges)
	}
	if m.Partial {
		t.Error("partial, though every artist is cached")
	}
	// Bob can't map alice's set.
	if _, err := e.svc.Map(t.Context(), e.bob, discover.MapQuery{SetID: set.ID}); err == nil {
		t.Error("bob mapped alice's set")
	}
}

func TestMapOfAnArtistIsBounded(t *testing.T) {
	e := newEnv(t)
	// A big neighborhood: Center has 80 neighbors, each with 10 of its own.
	var sims []any
	for i := range 80 {
		name := fmt.Sprintf("Near %d", i)
		sims = append(sims, name, 0.9-float64(i)*0.01)
		var far []any
		for j := range 10 {
			far = append(far, fmt.Sprintf("Far %d-%d", i, j), 0.8-float64(j)*0.05)
		}
		e.graph.artist(name, 1000, far...)
	}
	e.graph.artist("Center", 5_000_000, sims...)
	start := time.Now()
	m, err := e.svc.Map(t.Context(), e.alice, discover.MapQuery{Artist: " Center "})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s", d)
	}
	if len(m.Nodes) != discover.DefaultMapLimit {
		t.Fatalf("%d nodes", len(m.Nodes))
	}
	if c := m.Nodes[0]; c.Name != "Center" || !c.Center || c.Seed {
		t.Errorf("center: %+v", c)
	}
	hops := map[int]int{}
	for _, n := range m.Nodes[1:] {
		hops[n.Hop]++
	}
	if hops[1] == 0 || hops[2] == 0 {
		t.Errorf("hops: %v", hops)
	}
	// Two steps out, from the nearest neighbors: their own nearest.
	if !edge(m, "Near 0", "Far 0-0") {
		t.Error("no edge to a neighbor's neighbor")
	}
	// Far artists aren't cached: they're warmed, and the map says so.
	if !m.Partial || !slices.Contains(e.graph.warmed, "Far 0-0") {
		t.Errorf("partial %v, warmed %v", m.Partial, e.graph.warmed)
	}
	// Only the center is fetched, never the neighbors.
	if len(e.graph.fetched) != 0 {
		t.Errorf("fetched %v", e.graph.fetched)
	}

	one, err := e.svc.Map(t.Context(), e.alice, discover.MapQuery{Artist: "Center", Depth: 1, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Nodes) != 20 || slices.ContainsFunc(one.Nodes, func(n discover.MapNode) bool { return n.Hop > 1 }) {
		t.Errorf("depth 1: %d nodes", len(one.Nodes))
	}
}

func TestMapOfAnUnknownArtist(t *testing.T) {
	e := newEnv(t)
	m, err := e.svc.Map(t.Context(), e.alice, discover.MapQuery{Artist: "Nobody Knows"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Nodes) != 1 || m.Nodes[0].Known || !m.Nodes[0].Center {
		t.Errorf("nodes: %+v", m.Nodes)
	}
	if !slices.Contains(e.graph.fetched, "Nobody Knows") {
		t.Errorf("not fetched: %v", e.graph.fetched)
	}
	for _, q := range []discover.MapQuery{{}, {Artist: "x", SetID: "y"}, {Artist: "x", Depth: 3}, {Artist: "x", Limit: 500}} {
		if _, err := e.svc.Map(t.Context(), e.alice, q); !isInvalid(err) {
			t.Errorf("%+v: %v", q, err)
		}
	}
}

func TestArtistSongs(t *testing.T) {
	e := newEnv(t)
	credit := []provider.ArtistCredit{{Name: "Hum"}}
	e.svc.Library = owned{library.Holding(provider.Track{Title: "Hum song 1", Artists: credit})}
	if err := e.svc.Thumb(t.Context(), e.alice, "", discover.ThumbDown, discover.Song{Title: "Hum song 2", Artist: "Hum"}); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.ArtistSongs(t.Context(), e.alice, "hum", "")
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, it := range got.Items {
		titles = append(titles, it.Title)
	}
	if !slices.Equal(titles, []string{"Hum song 3", "Hum song 4", "Hum song 5"}) {
		t.Errorf("songs: %v", titles)
	}
	if got.Items[0].DeezerID == 0 {
		t.Error("no Deezer ID")
	}
	if _, err := e.svc.Block(t.Context(), e.alice, "Hum"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.ArtistSongs(t.Context(), e.alice, "Hum", ""); len(got.Items) != 0 {
		t.Errorf("a blocked artist's songs: %+v", got.Items)
	}
}
