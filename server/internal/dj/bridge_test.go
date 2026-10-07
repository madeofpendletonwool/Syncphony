// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"slices"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// mixedRoom is Sam, who plays Radiohead, and Jo, who plays Massive
// Attack. Each has a nearer neighbor of their own (Thom Yorke, Tricky),
// and Portishead is like both, less so.
func mixedRoom(mine ...store.QueueItem) (*graph, Input) {
	g := newGraph(
		artist("Radiohead", map[string]float64{"Thom Yorke": 0.9, "Portishead": 0.4}, "Creep"),
		artist("Massive Attack", map[string]float64{"Tricky": 0.9, "Portishead": 0.4}, "Teardrop"),
		artist("Thom Yorke", nil, "Black Swan"),
		artist("Tricky", nil, "Overcome"),
		artist("Portishead", nil, "Glory Box"),
	)
	h := []store.ListHistoryRow{
		played(item("sam", "Radiohead", "Airbag"), now.Add(-4*time.Minute), store.EndFinished),
		played(item("jo", "Massive Attack", "Angel"), now.Add(-8*time.Minute), store.EndFinished),
		played(item("sam", "Radiohead", "Nude"), now.Add(-12*time.Minute), store.EndFinished),
		played(item("jo", "Massive Attack", "Safe from Harm"), now.Add(-16*time.Minute), store.EndFinished),
	}
	return g, Input{History: h, Present: []string{"sam", "jo"}, Mine: mine, Now: now}
}

func shortlistFor(t *testing.T, g *graph, in Input) []candidate {
	t.Helper()
	e := &Engine{Graph: g, Rand: func(int) int { return 0 }}
	return e.shortlist(t.Context(), NewProfile(in), Request{Input: in, Explore: 25}, nil)
}

func TestBridgeBetweenMembers(t *testing.T) {
	g, in := mixedRoom()
	order := shortlistFor(t, g, in)
	if len(order) == 0 || order[0].song.Title != "Glory Box" {
		t.Fatalf("order %v: want Portishead's Glory Box, between Sam's and Jo's tastes, first", titles(order))
	}
	b := order[0].bridge
	if b == nil || b.Apart < 0.9 {
		t.Fatalf("Glory Box's bridge = %+v: want one between tastes far apart", b)
	}
	got := map[string]string{b.Users[0]: b.Artists[0], b.Users[1]: b.Artists[1]}
	if got["sam"] != "Radiohead" || got["jo"] != "Massive Attack" {
		t.Errorf("bridge between %v: want Sam's Radiohead and Jo's Massive Attack", got)
	}
	for _, c := range order {
		if c.bridge != nil && c.song.Title != "Glory Box" {
			t.Errorf("%s is a bridge %+v: only Portishead is near both", c.song.Title, c.bridge)
		}
	}
}

// After a bridge, it's the turn of the member autopilot served longest
// ago: Jo, since the bridge was Sam's.
func TestTurnAfterBridge(t *testing.T) {
	last := item("", "Portishead", "Roads")
	last.AddedBy = "sam"
	last.Autopilot.String = `{"reason":{"kind":"similar-artist","bridge":{"between":[{"userId":"sam","artist":"Radiohead"},{"userId":"jo","artist":"Massive Attack"}],"apart":1}}}`
	g, in := mixedRoom(last)
	p := NewProfile(in)
	if mx := mixFor(p); !p.LastBridge || len(mx.pairs) > 0 || mx.turn != "jo" {
		t.Fatalf("mix after a bridge for Sam = %+v (last bridge %v): want Jo's turn", mx, p.LastBridge)
	}
	order := shortlistFor(t, g, in)
	if len(order) == 0 || order[0].song.Title != "Overcome" {
		t.Fatalf("order %v: want Tricky, near Jo's taste, first", titles(order))
	}
	if order[0].bridge != nil || order[0].turn != "jo" {
		t.Errorf("Overcome = bridge %+v, turn %q: want Jo's turn", order[0].bridge, order[0].turn)
	}
}

// Members who like the same music need no bridge.
func TestNoBridgeForAlikeMembers(t *testing.T) {
	g, in := mixedRoom()
	in.History = []store.ListHistoryRow{
		played(item("sam", "Radiohead", "Airbag"), now.Add(-4*time.Minute), store.EndFinished),
		played(item("jo", "Radiohead", "Nude"), now.Add(-8*time.Minute), store.EndFinished),
	}
	if mx := mixFor(NewProfile(in)); len(mx.pairs) > 0 {
		t.Errorf("mix = %+v: want no bridge between members who both play Radiohead", mx)
	}
	for _, c := range shortlistFor(t, g, in) {
		if c.bridge != nil {
			t.Errorf("%s is a bridge %+v", c.song.Title, c.bridge)
		}
	}
}

// A room of one member mixes nothing, and the members' own favorites seed
// the walk even when the room's favorites leave them out.
func TestMixMembers(t *testing.T) {
	_, in := mixedRoom()
	in.History = in.History[:1]
	in.Present = []string{"sam"}
	if mx := mixFor(NewProfile(in)); len(mx.pairs) > 0 || mx.turn != "" {
		t.Errorf("one member: mix = %+v", mx)
	}

	var h []store.ListHistoryRow
	for i, a := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I"} {
		h = append(h, played(item("sam", a, "x"), now.Add(-time.Duration(i)*time.Minute), store.EndFinished))
	}
	h = append(h, played(item("jo", "Jo's Own", "y"), now.Add(-3*time.Hour), store.EndFinished))
	p := NewProfile(Input{History: h, Present: []string{"sam", "jo"}, Now: now})
	var seeds []string
	for _, t := range p.seeds() {
		seeds = append(seeds, t.Artist.Name)
	}
	if !slices.Contains(seeds, "Jo's Own") {
		t.Errorf("seeds %v: want Jo's favorite", seeds)
	}
}

func TestServedLongestAgo(t *testing.T) {
	mine := func(user string) store.QueueItem {
		it := item("", "X", "x")
		it.AddedBy = user
		return it
	}
	p := NewProfile(Input{Mine: []store.QueueItem{mine("a"), mine("b"), mine("a")}, Now: now})
	if got := p.servedLongestAgo([]string{"a", "b", "c"}); got != "c" {
		t.Errorf("served longest ago = %q, want c, never served", got)
	}
	if got := p.servedLongestAgo([]string{"a", "b"}); got != "b" {
		t.Errorf("served longest ago = %q, want b", got)
	}
}
