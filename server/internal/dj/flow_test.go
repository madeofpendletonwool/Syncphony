// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func TestSoundOf(t *testing.T) {
	tags := []musicgraph.Tag{{Name: "synthpop", Weight: 1}, {Name: "80s", Weight: 0.6}, {Name: "seen live", Weight: 0.9}}
	cases := []struct {
		name      string
		tr        musicgraph.Track
		bpm, year int
		tags      []musicgraph.Tag
		want      sound
	}{
		{"nothing known", musicgraph.Track{}, 0, 0, nil, unknownSound},
		{"the graph first", musicgraph.Track{BPM: 120, Year: 1983}, 90, 2015, nil, sound{bpm: 120, year: 1983, energy: 0.5}},
		{"the service's tags", musicgraph.Track{}, 90, 2015, nil, sound{bpm: 90, year: 2015, energy: 0.2}},
		{"a tagging error isn't a tempo", musicgraph.Track{BPM: 999}, 12, 0, nil, unknownSound},
		{"the decade and energy from tags", musicgraph.Track{}, 0, 0, tags, sound{year: 1985, energy: 0.65}},
		{"tags and tempo mix", musicgraph.Track{BPM: 170}, 0, 1984, tags, sound{bpm: 170, year: 1984, energy: 0.6*0.65 + 0.4}},
	}
	for _, c := range cases {
		got := soundOf(c.tr, c.bpm, c.year, c.tags)
		if got.bpm != c.want.bpm || got.year != c.want.year || math.Abs(got.energy-c.want.energy) > 1e-9 {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	if y := decadeOf([]musicgraph.Tag{{Name: "1990s", Weight: 0.2}, {Name: "00s", Weight: 0.7}}); y != 2005 {
		t.Errorf("decade = %d, want the most tagged, 2005", y)
	}
}

func TestFit(t *testing.T) {
	at := func(bpm float64, year int, energy float64) sound { return sound{bpm: bpm, year: year, energy: energy} }
	cases := []struct {
		name   string
		a, b   sound
		target float64
		want   float64 // the mean fit
	}{
		{"nothing known", unknownSound, at(120, 1990, 0.5), 0.6, 0.5}, // only the curve: 0.1 off
		{"within 8%", at(120, 0, -1), at(128, 0, -1), -1, 1},
		{"double time mixes", at(85, 0, -1), at(172, 0, -1), -1, 1},
		{"a tempo far off", at(120, 0, -1), at(160, 0, -1), -1, 0},
		{"the same decade", at(0, 1981, -1), at(0, 1989, -1), -1, 1},
		{"a few years across a decade", at(0, 1979, -1), at(0, 1982, -1), -1, 1},
		{"decades apart", at(0, 1975, -1), at(0, 2015, -1), -1, 0},
		{"a gradual change", at(0, 0, 0.5), at(0, 0, 0.6), -1, 1},
		{"a jump", at(0, 0, 0.1), at(0, 0, 0.9), -1, 0},
	}
	for _, c := range cases {
		adj, mean := fit(c.a, c.b, c.target)
		if c.name == "nothing known" {
			c.want = ease(0.1, 0, curveSpan)
		}
		if math.Abs(mean-c.want) > 1e-9 {
			t.Errorf("%s: fit %v, want %v", c.name, mean, c.want)
		}
		if (mean > 0.5) != (adj > 0) {
			t.Errorf("%s: adj %v doesn't follow fit %v", c.name, adj, mean)
		}
	}
	if adj, mean := fit(unknownSound, unknownSound, -1); adj != 0 || mean != -1 {
		t.Errorf("all unknown: %v, %v; want no change", adj, mean)
	}
}

func TestCurve(t *testing.T) {
	day := func(h, m int) time.Time { return time.Date(2026, 10, 9, h, m, 0, 0, time.Local) }
	start := day(19, 0)
	early, peak, late := curve(start, start), curve(start, day(21, 0)), curve(start, day(23, 59))
	if !(early < peak) {
		t.Errorf("curve: start %.2f, 2h in %.2f: want a rise", early, peak)
	}
	if !(late < peak) {
		t.Errorf("curve: 2h in %.2f, 5h in %.2f: want it to settle", peak, late)
	}
	// The same time into a session is calmer in the small hours than in
	// the evening.
	if small, evening := curve(day(3, 0), day(4, 30)), curve(day(19, 0), day(20, 30)); !(small < evening) {
		t.Errorf("curve: 4:30am %.2f, 8:30pm %.2f: want the small hours calmer", small, evening)
	}
	for m := 0; m < 48*60; m += 7 {
		if e := curve(start, start.Add(time.Duration(m)*time.Minute)); e < 0.2 || e > 0.9 {
			t.Fatalf("curve %d minutes in = %v, out of bounds", m, e)
		}
	}
}

// flowRoom is a room whose last song is New Order's Hero, with two artists
// like them as near as each other: Depeche Mode's song is 122 BPM from
// 1984, Skrillex's 175 BPM from 2011.
func flowRoom(heroBPM float64, heroYear int) (*graph, Input) {
	g := newGraph(
		artist("New Order", map[string]float64{"Depeche Mode": 0.8, "Skrillex": 0.8}, "Hero"),
		artist("Depeche Mode", nil, "Enjoy"),
		artist("Skrillex", nil, "Bangarang"),
	)
	g.tracks[SongKey("New Order", "Hero")] = musicgraph.Track{BPM: heroBPM, Year: heroYear}
	g.tracks[SongKey("Depeche Mode", "Enjoy")] = musicgraph.Track{BPM: 122, Year: 1984}
	g.tracks[SongKey("Skrillex", "Bangarang")] = musicgraph.Track{BPM: 175, Year: 2011}
	h := []store.ListHistoryRow{played(item("alice", "New Order", "Hero"), now.Add(-3*time.Minute), store.EndFinished)}
	return g, Input{History: h, Now: now}
}

func TestFlowFollowsTheSong(t *testing.T) {
	for _, c := range []struct {
		bpm  float64
		year int
		want string
	}{
		{120, 1985, "Enjoy"},
		{172, 2012, "Bangarang"},
	} {
		g, in := flowRoom(c.bpm, c.year)
		e := &Engine{Graph: g, Rand: func(int) int { return 0 }}
		order := e.shortlist(t.Context(), NewProfile(in), Request{Input: in}, nil)
		if len(order) != 2 || order[0].song.Title != c.want {
			t.Fatalf("after %v BPM from %d: %v, want %s first", c.bpm, c.year, titles(order), c.want)
		}
		fl := order[0].flow
		if fl.Fit < 0.9 || fl.Target != -1 || fl.BPM == 0 || fl.Year == 0 {
			t.Errorf("%s's flow = %+v: want a close fit, no curve", c.want, fl)
		}
		if len(fl.Ahead) != 1 || order[1].flow.Fit > 0.2 {
			t.Errorf("flows %+v, %+v: want the other planned after, fitting poorly", fl, order[1].flow)
		}
	}
}

// TestFlowPlansAhead checks that of two songs that follow the last one
// equally well, the one that leads somewhere is played: the 2020 song,
// with others from around then to follow, over the 1990 one.
func TestFlowPlansAhead(t *testing.T) {
	g := newGraph(
		artist("Room", map[string]float64{"A": 0.8, "B": 0.8, "C": 0.8, "D": 0.8, "E": 0.8}, "Now"),
		artist("A", nil, "Nineties"),
		artist("B", nil, "Twenties"),
		artist("C", nil, "Late Tens"),
		artist("D", nil, "Early Twenties"),
		artist("E", nil, "Mid Tens"),
	)
	for k, y := range map[string]int{"Room|Now": 2005, "A|Nineties": 1990, "B|Twenties": 2020, "C|Late Tens": 2019, "D|Early Twenties": 2021, "E|Mid Tens": 2017} {
		ar, ti, _ := strings.Cut(k, "|")
		g.tracks[SongKey(ar, ti)] = musicgraph.Track{Year: y}
	}
	in := Input{History: []store.ListHistoryRow{played(item("alice", "Room", "Now"), now.Add(-3*time.Minute), store.EndFinished)}, Now: now}
	e := &Engine{Graph: g, Rand: func(int) int { return 0 }}
	order := e.shortlist(t.Context(), NewProfile(in), Request{Input: in}, nil)
	got := titles(order)
	if slices.Index(got, "Twenties") > slices.Index(got, "Nineties") {
		t.Errorf("order %v: want Twenties, which leads on, before Nineties, which leads nowhere", got)
	}
	tw, _ := find(order, "Twenties")
	if len(tw.flow.Ahead) != planAhead-1 {
		t.Errorf("Twenties plans %v: want %d songs after it", tw.flow.Ahead, planAhead-1)
	}
}

func TestFlowWarmsUnknownSongs(t *testing.T) {
	g, in := flowRoom(120, 1985)
	delete(g.tracks, SongKey("Skrillex", "Bangarang"))
	e := &Engine{Graph: g, Rand: func(int) int { return 0 }}
	order := e.shortlist(t.Context(), NewProfile(in), Request{Input: in}, nil)
	if !slices.Contains(g.warmedSongs, "Bangarang") {
		t.Errorf("warmed %v: want the song the DJ knew nothing about", g.warmedSongs)
	}
	if b, ok := find(order, "Bangarang"); !ok || b.flow.Fit != -1 {
		t.Errorf("Bangarang = %+v, %v: want it still a candidate, its fit unknown", b, ok)
	}
}

func TestFlowCurve(t *testing.T) {
	// Two songs follow the last one equally well, a little calmer and a
	// little livelier. Half an hour into a 3am session the curve wants the
	// calmer; two hours into a 10pm one, the livelier.
	g := newGraph(
		withTags(artist("Room", map[string]float64{"Calm": 0.8, "Lively": 0.8}, "Now"), "shoegaze"),
		withTags(artist("Calm", nil, "Hush"), "folk"),
		withTags(artist("Lively", nil, "Roar"), "rock"),
	)
	for _, c := range []struct {
		start time.Time
		songs int
		want  string
	}{
		{time.Date(2026, 10, 9, 3, 30, 0, 0, time.Local), 8, "Hush"},
		{time.Date(2026, 10, 9, 20, 0, 0, 0, time.Local), 30, "Roar"},
	} {
		var h []store.ListHistoryRow
		for i := range c.songs {
			h = append(h, played(item("alice", "Room", "Now"), c.start.Add(time.Duration(c.songs-1-i)*4*time.Minute), store.EndFinished))
		}
		at := c.start.Add(time.Duration(c.songs) * 4 * time.Minute)
		in := Input{History: h, Now: at}
		in.Tags = CachedTags(t.Context(), g, in)
		e := &Engine{Graph: g, Rand: func(int) int { return 0 }}
		for _, on := range []bool{true, false} {
			order := e.shortlist(t.Context(), NewProfile(in), Request{Input: in, EnergyCurve: on}, in.Tags)
			if len(order) != 2 {
				t.Fatalf("%s, curve %v: %v", at, on, titles(order))
			}
			hush, _ := find(order, "Hush")
			roar, _ := find(order, "Roar")
			switch {
			case on && order[0].song.Title != c.want:
				t.Errorf("%s: %v (Hush %+v, Roar %+v): want %s first", at.Format("15:04"), titles(order), hush.flow, roar.flow, c.want)
			case !on && (hush.flow.Target != -1 || hush.score != roar.score):
				t.Errorf("curve off: Hush %.3f %+v, Roar %.3f %+v: want no target, an even fit", hush.score, hush.flow, roar.score, roar.flow)
			}
		}
	}
}

func withTags(a musicgraph.Artist, names ...string) musicgraph.Artist {
	for _, n := range names {
		a.Tags = append(a.Tags, musicgraph.Tag{Name: n, Weight: 1})
	}
	return a
}
