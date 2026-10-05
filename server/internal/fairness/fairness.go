// SPDX-License-Identifier: AGPL-3.0-only

// Package fairness decides a room's play order. Every member has a lane: the
// songs they queued, in the order they chose. A Policy interleaves the lanes
// into one "up next" list.
//
// Policies are pure functions of a State, with no I/O and no clock, so the
// same lanes and history always give the same order. Adding a mode (weighted
// turns, "at most N in a row", per-user cooldowns) means adding a Policy;
// the queue engine doesn't change.
package fairness

import (
	"cmp"
	"slices"
	"time"
)

// Item is a queued song, as far as ordering is concerned.
type Item struct {
	ID   string
	User string
	// AddedAt is when the item was queued.
	AddedAt time.Time
}

// State is everything a policy may look at.
type State struct {
	// Lanes holds each user's queued items, in the user's order. Users
	// with nothing queued may be absent or have an empty lane.
	Lanes map[string][]Item
	// LastPlayed is when each user's most recent song started playing in
	// this room. Users who haven't had a song played are absent.
	LastPlayed map[string]time.Time
	// Playing is the user whose song is playing now, or "". They count as
	// having just had their turn.
	Playing string
}

// Policy turns lanes into a play order.
type Policy interface {
	// Mode is the room setting that selects the policy, e.g. "round_robin".
	Mode() string
	// Order returns every queued item, in the order they will play. It must
	// be deterministic, keep each lane's order, and not modify s.
	Order(s State) []Item
}

// Modes.
const (
	ModeRoundRobin = "round_robin"
	ModeFIFO       = "fifo"
)

// ForMode returns the policy for a room's fairness mode. Unknown modes fall
// back to round robin.
func ForMode(mode string) Policy {
	if mode == ModeFIFO {
		return FIFO{}
	}
	return RoundRobin{}
}

// RoundRobin takes turns between users. The next turn goes to whoever has
// waited longest since their last song started; someone who hasn't had a
// song yet goes before anyone who has, so a late arrival plays soon rather
// than after every earlier lane drains. Between users who haven't played,
// whoever queued first goes first.
type RoundRobin struct{}

// Mode implements Policy.
func (RoundRobin) Mode() string { return ModeRoundRobin }

// lastTurn orders users by how recently they had a turn. Smaller is longer ago.
type lastTurn struct {
	// tier is 0 for never played, 1 for a real play at time at, and 2 for
	// a turn in the simulated order (or the song playing now) at step.
	tier int
	at   time.Time
	step int
}

func (a lastTurn) compare(b lastTurn) int {
	return cmp.Or(cmp.Compare(a.tier, b.tier), a.at.Compare(b.at), cmp.Compare(a.step, b.step))
}

// Order implements Policy.
func (RoundRobin) Order(s State) []Item {
	type lane struct {
		user  string
		items []Item
		last  lastTurn
		// waiting is when the user's earliest queued item was added, to
		// break ties between users who haven't played.
		waiting time.Time
	}
	var lanes []*lane
	total := 0
	for _, user := range users(s.Lanes) {
		items := s.Lanes[user]
		l := &lane{user: user, items: items, waiting: earliest(items)}
		switch at, ok := s.LastPlayed[user]; {
		case user == s.Playing:
			l.last = lastTurn{tier: 2}
		case ok:
			l.last = lastTurn{tier: 1, at: at}
		}
		lanes = append(lanes, l)
		total += len(items)
	}
	out := make([]Item, 0, total)
	for step := 1; len(out) < total; step++ {
		var next *lane
		for _, l := range lanes {
			if len(l.items) == 0 {
				continue
			}
			if next == nil {
				next = l
				continue
			}
			c := cmp.Or(l.last.compare(next.last), l.waiting.Compare(next.waiting), cmp.Compare(l.user, next.user))
			if c < 0 {
				next = l
			}
		}
		out = append(out, next.items[0])
		next.items = next.items[1:]
		next.last = lastTurn{tier: 2, step: step}
	}
	return out
}

// FIFO plays songs in the order they were queued, across everyone. Users
// can still reorder their own lane: it merges lanes by when each lane's
// next item was added.
type FIFO struct{}

// Mode implements Policy.
func (FIFO) Mode() string { return ModeFIFO }

// Order implements Policy.
func (FIFO) Order(s State) []Item {
	lanes := make([][]Item, 0, len(s.Lanes))
	total := 0
	for _, user := range users(s.Lanes) {
		lanes = append(lanes, s.Lanes[user])
		total += len(s.Lanes[user])
	}
	out := make([]Item, 0, total)
	for len(out) < total {
		best := -1
		for i, l := range lanes {
			if len(l) == 0 {
				continue
			}
			if best < 0 || cmp.Or(l[0].AddedAt.Compare(lanes[best][0].AddedAt), cmp.Compare(l[0].ID, lanes[best][0].ID)) < 0 {
				best = i
			}
		}
		out = append(out, lanes[best][0])
		lanes[best] = lanes[best][1:]
	}
	return out
}

// users returns the lanes' users in a fixed order, so map iteration order
// can't leak into results.
func users(lanes map[string][]Item) []string {
	out := make([]string, 0, len(lanes))
	for u := range lanes {
		out = append(out, u)
	}
	slices.Sort(out)
	return out
}

func earliest(items []Item) time.Time {
	var t time.Time
	for i, it := range items {
		if i == 0 || it.AddedAt.Before(t) {
			t = it.AddedAt
		}
	}
	return t
}
