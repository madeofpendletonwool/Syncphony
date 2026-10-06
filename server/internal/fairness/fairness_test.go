// SPDX-License-Identifier: AGPL-3.0-only

package fairness_test

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/fairness"
)

var t0 = time.Date(2026, 10, 5, 20, 0, 0, 0, time.UTC)

// at returns t0 plus m minutes.
func at(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

// lanes builds lanes from "user: id@minute id@minute ..." lines. Each item
// is named after its user, e.g. "a1".
func lanes(spec ...string) map[string][]fairness.Item {
	out := map[string][]fairness.Item{}
	for _, line := range spec {
		user, items, _ := strings.Cut(line, ":")
		user = strings.TrimSpace(user)
		out[user] = []fairness.Item{}
		for f := range strings.FieldsSeq(items) {
			id, minute, _ := strings.Cut(f, "@")
			m, err := strconv.Atoi(minute)
			if err != nil {
				panic(fmt.Sprintf("bad item %q", f))
			}
			out[user] = append(out[user], fairness.Item{ID: id, User: user, AddedAt: at(m)})
		}
	}
	return out
}

func ids(items []fairness.Item) string {
	s := make([]string, len(items))
	for i, it := range items {
		s[i] = it.ID
	}
	return strings.Join(s, " ")
}

func TestRoundRobin(t *testing.T) {
	for _, tc := range []struct {
		name       string
		lanes      map[string][]fairness.Item
		lastPlayed map[string]time.Time
		playing    string
		want       string
	}{
		{name: "empty", lanes: nil, want: ""},
		{name: "empty lanes", lanes: lanes("a:", "b:"), want: ""},
		{name: "one lane keeps its order", lanes: lanes("a: a1@0 a2@1 a3@2"), want: "a1 a2 a3"},
		{
			name:  "nobody has played: whoever queued first starts",
			lanes: lanes("a: a1@1 a2@2 a3@3", "b: b1@0 b2@4"),
			want:  "b1 a1 b2 a2 a3",
		},
		{
			name:  "uneven lanes drain into the longest",
			lanes: lanes("a: a1@0 a2@0 a3@0 a4@0", "b: b1@1", "c: c1@2 c2@2"),
			want:  "a1 b1 c1 a2 c2 a3 a4",
		},
		{
			name:       "longest wait goes first",
			lanes:      lanes("a: a1@0 a2@0", "b: b1@9 b2@9"),
			lastPlayed: map[string]time.Time{"a": at(30), "b": at(20)},
			want:       "b1 a1 b2 a2",
		},
		{
			name:       "late joiner plays next, not after everyone",
			lanes:      lanes("a: a1@0 a2@0", "b: b1@1 b2@1", "c: c1@40"),
			lastPlayed: map[string]time.Time{"a": at(30), "b": at(35)},
			want:       "c1 a1 b1 a2 b2",
		},
		{
			name:    "whoever is playing goes to the back",
			lanes:   lanes("a: a1@0 a2@0", "b: b1@5"),
			playing: "a",
			want:    "b1 a1 a2",
		},
		{
			name:       "playing overrides an old last play",
			lanes:      lanes("a: a1@0", "b: b1@0"),
			lastPlayed: map[string]time.Time{"a": at(1), "b": at(10)},
			playing:    "a",
			want:       "b1 a1",
		},
		{
			name:       "someone joins mid-song",
			lanes:      lanes("a: a1@0", "b: b1@0 b2@0", "c: c1@50 c2@50"),
			lastPlayed: map[string]time.Time{"a": at(20), "b": at(40)},
			playing:    "b",
			want:       "c1 a1 b1 c2 b2",
		},
		{
			name:       "a user who left (empty lane) is skipped",
			lanes:      lanes("a: a1@0 a2@0", "b:", "c: c1@3"),
			lastPlayed: map[string]time.Time{"b": at(1)},
			want:       "a1 c1 a2",
		},
		{
			name:       "an emptied lane coming back keeps its place in line",
			lanes:      lanes("a: a1@0 a2@0", "b: b1@60"),
			lastPlayed: map[string]time.Time{"a": at(50), "b": at(10)},
			want:       "b1 a1 a2",
		},
		{
			name:  "lane order is the user's, not the add order",
			lanes: lanes("a: a3@3 a1@1 a2@2", "b: b1@0"),
			want:  "b1 a3 a1 a2",
		},
		{
			name:  "ties break by user",
			lanes: lanes("b: b1@0", "a: a1@0", "c: c1@0"),
			want:  "a1 b1 c1",
		},
		{
			name:  "never-played tie breaks by earliest item, wherever it is in the lane",
			lanes: lanes("a: a2@5 a1@1", "b: b1@2"),
			want:  "a2 b1 a1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fairness.State{Lanes: tc.lanes, LastPlayed: tc.lastPlayed, Playing: tc.playing}
			if got := ids(fairness.RoundRobin{}.Order(s)); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestRemovalMidSong(t *testing.T) {
	// b is playing. Removing a queued item changes the order only by
	// taking that item out.
	before := fairness.State{
		Lanes:      lanes("a: a1@0 a2@0", "b: b2@0", "c: c1@0 c2@0"),
		LastPlayed: map[string]time.Time{"a": at(10), "b": at(20), "c": at(5)},
		Playing:    "b",
	}
	if got, want := ids(fairness.RoundRobin{}.Order(before)), "c1 a1 b2 c2 a2"; got != want {
		t.Fatalf("before: got %q, want %q", got, want)
	}
	after := before
	after.Lanes = lanes("a: a1@0 a2@0", "b: b2@0", "c: c2@0")
	if got, want := ids(fairness.RoundRobin{}.Order(after)), "c2 a1 b2 a2"; got != want {
		t.Errorf("after removing c1: got %q, want %q", got, want)
	}
}

func TestFIFO(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lanes map[string][]fairness.Item
		want  string
	}{
		{"empty", nil, ""},
		{"by add time", lanes("a: a1@0 a2@3", "b: b1@1 b2@2"), "a1 b1 b2 a2"},
		{"lane order still holds", lanes("a: a2@3 a1@0", "b: b1@1"), "b1 a2 a1"},
		{"ties break by ID", lanes("a: a1@0", "b: b1@0"), "a1 b1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ids(fairness.FIFO{}.Order(fairness.State{Lanes: tc.lanes})); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOptions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   string
		lanes  map[string][]fairness.Item
		recent []string
		opts   fairness.Options
		want   string
	}{
		{
			name:  "FIFO caps songs in a row",
			mode:  "fifo",
			lanes: lanes("a: a1@0 a2@1 a3@2 a4@3", "b: b1@4 b2@5"),
			opts:  fairness.Options{MaxInARow: 2},
			want:  "a1 a2 b1 a3 a4 b2",
		},
		{
			name:   "the cap counts songs already played",
			mode:   "fifo",
			lanes:  lanes("a: a1@0 a2@1", "b: b1@4"),
			recent: []string{"a", "a"},
			opts:   fairness.Options{MaxInARow: 2},
			want:   "b1 a1 a2",
		},
		{
			name:  "the cap gives way when nobody else is waiting",
			mode:  "fifo",
			lanes: lanes("a: a1@0 a2@1 a3@2"),
			opts:  fairness.Options{MaxInARow: 1},
			want:  "a1 a2 a3",
		},
		{
			name:  "FIFO cooldown spaces one person's songs",
			mode:  "fifo",
			lanes: lanes("a: a1@0 a2@1 a3@2", "b: b1@3 b2@4", "c: c1@5"),
			opts:  fairness.Options{Cooldown: 2},
			want:  "a1 b1 c1 a2 b2 a3",
		},
		{
			name:   "cooldown counts songs already played",
			mode:   "fifo",
			lanes:  lanes("a: a1@0", "b: b1@3", "c: c1@5"),
			recent: []string{"b", "a"},
			opts:   fairness.Options{Cooldown: 2},
			want:   "c1 a1 b1",
		},
		{
			name:  "a weight of 2 is two songs a turn",
			lanes: lanes("a: a1@0 a2@0 a3@0 a4@0", "b: b1@1 b2@1 b3@1"),
			opts:  fairness.Options{Weights: map[string]int{"a": 2}},
			want:  "a1 a2 b1 a3 a4 b2 b3",
		},
		{
			name:   "a weighted turn already under way carries on",
			lanes:  lanes("a: a2@0 a3@0", "b: b1@1"),
			recent: []string{"a", "b"},
			opts:   fairness.Options{Weights: map[string]int{"a": 2}},
			want:   "a2 b1 a3",
		},
		{
			name:   "a finished weighted turn passes on",
			lanes:  lanes("a: a3@0", "b: b1@1"),
			recent: []string{"a", "a"},
			opts:   fairness.Options{Weights: map[string]int{"a": 2}},
			want:   "b1 a3",
		},
		{
			name:  "the cap cuts a weighted turn short",
			lanes: lanes("a: a1@0 a2@0 a3@0", "b: b1@1 b2@1"),
			opts:  fairness.Options{Weights: map[string]int{"a": 3}, MaxInARow: 2},
			want:  "a1 a2 b1 a3 b2",
		},
		{
			name:  "FIFO ignores weights",
			mode:  "fifo",
			lanes: lanes("a: a1@0 a2@2", "b: b1@1"),
			opts:  fairness.Options{Weights: map[string]int{"a": 2}},
			want:  "a1 b1 a2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fairness.State{Lanes: tc.lanes, Recent: tc.recent}
			if len(tc.recent) > 0 {
				s.Playing = tc.recent[0]
			}
			if got := ids(fairness.ForMode(tc.mode, tc.opts).Order(s)); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestReach(t *testing.T) {
	if got := (fairness.Options{}).Reach(); got != 1 {
		t.Errorf("plain: %d", got)
	}
	if got := (fairness.Options{MaxInARow: 3, Cooldown: 2, Weights: map[string]int{"a": 4}}).Reach(); got != 4 {
		t.Errorf("tuned: %d", got)
	}
}

func TestForMode(t *testing.T) {
	for mode, want := range map[string]string{"round_robin": "round_robin", "fifo": "fifo", "": "round_robin", "weighted": "round_robin"} {
		if got := fairness.ForMode(mode, fairness.Options{}).Mode(); got != want {
			t.Errorf("ForMode(%q) = %q, want %q", mode, got, want)
		}
	}
}

// randomState makes lanes for up to 6 users with up to 8 items each, and
// random history.
func randomState(r *rand.Rand) fairness.State {
	s := fairness.State{Lanes: map[string][]fairness.Item{}, LastPlayed: map[string]time.Time{}}
	n := 0
	for u := range r.IntN(7) {
		user := string(rune('a' + u))
		var lane []fairness.Item
		for range r.IntN(9) {
			n++
			lane = append(lane, fairness.Item{ID: fmt.Sprintf("%s%d", user, n), User: user, AddedAt: at(r.IntN(30))})
		}
		s.Lanes[user] = lane
		if r.IntN(2) == 0 {
			s.LastPlayed[user] = at(r.IntN(30))
		}
		if r.IntN(6) == 0 {
			s.Playing = user
		}
	}
	for range r.IntN(4) {
		s.Recent = append(s.Recent, string("abcdef"[r.IntN(6)]))
	}
	return s
}

func clone(s fairness.State) fairness.State {
	c := s
	c.Lanes = map[string][]fairness.Item{}
	for u, l := range s.Lanes {
		c.Lanes[u] = slices.Clone(l)
	}
	c.LastPlayed = maps.Clone(s.LastPlayed)
	c.Recent = slices.Clone(s.Recent)
	return c
}

// TestPolicyContract checks every policy against the Policy contract on
// random input: every item exactly once, lanes in order, deterministic,
// input untouched.
func TestPolicyContract(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // seeded on purpose, for repeatable tests
	tuned := fairness.Options{MaxInARow: 2, Cooldown: 1, Weights: map[string]int{"a": 3, "c": 2}}
	for _, p := range []fairness.Policy{fairness.RoundRobin{}, fairness.FIFO{}, fairness.RoundRobin{Options: tuned}, fairness.FIFO{Options: tuned}} {
		t.Run(fmt.Sprintf("%s %+v", p.Mode(), p), func(t *testing.T) {
			for range 500 {
				s := randomState(r)
				orig := clone(s)
				got := p.Order(s)
				if again := p.Order(s); ids(again) != ids(got) {
					t.Fatalf("not deterministic: %q then %q", ids(got), ids(again))
				}
				if fmt.Sprint(s) != fmt.Sprint(orig) {
					t.Fatalf("modified its input")
				}
				pos := map[string]int{}
				for i, it := range got {
					if _, dup := pos[it.ID]; dup {
						t.Fatalf("%s twice in %q", it.ID, ids(got))
					}
					pos[it.ID] = i
				}
				for _, lane := range s.Lanes {
					for i, it := range lane {
						p, ok := pos[it.ID]
						if !ok {
							t.Fatalf("%s missing from %q", it.ID, ids(got))
						}
						if i > 0 && p < pos[lane[i-1].ID] {
							t.Fatalf("lane order broken for %s in %q", it.ID, ids(got))
						}
					}
				}
			}
		})
	}
}

// TestRoundRobinTurns checks the fairness promise: while two users both
// have songs left, neither gets two turns ahead of the other.
func TestRoundRobinTurns(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // as above
	for range 500 {
		s := randomState(r)
		order := fairness.RoundRobin{}.Order(s)
		played := map[string]int{}
		left := map[string]int{}
		for u, l := range s.Lanes {
			left[u] = len(l)
		}
		for _, it := range order {
			played[it.User]++
			left[it.User]--
			for u := range s.Lanes {
				if u != it.User && left[u] > 0 && played[it.User] > played[u]+1 {
					t.Fatalf("%s took a 2nd turn ahead of %s, who's still waiting: %q", it.User, u, ids(order))
				}
			}
		}
	}
}

// TestLimits checks MaxInARow and Cooldown on random input: within the
// simulated order, neither is broken while someone else has songs waiting.
func TestLimits(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6)) //nolint:gosec // as above
	o := fairness.Options{MaxInARow: 2, Cooldown: 1, Weights: map[string]int{"a": 3}}
	for _, p := range []fairness.Policy{fairness.RoundRobin{Options: o}, fairness.FIFO{Options: o}} {
		for range 500 {
			s := randomState(r)
			s.Recent = nil
			order := p.Order(s)
			left := map[string]int{}
			for u, l := range s.Lanes {
				left[u] = len(l)
			}
			for i, it := range order {
				others := false
				for u, n := range left {
					others = others || (u != it.User && n > 0)
				}
				if others && i > 0 && order[i-1].User == it.User {
					t.Fatalf("%s: %s played again within the cooldown while others waited: %q", p.Mode(), it.User, ids(order))
				}
				left[it.User]--
			}
		}
	}
}
