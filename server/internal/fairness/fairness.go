// SPDX-License-Identifier: AGPL-3.0-only

// Package fairness decides a room's play order. Every member has a lane: the
// songs they queued, in the order they chose. A Policy interleaves the lanes
// into one "up next" list.
//
// Policies are pure functions of a State, with no I/O and no clock, so the
// same lanes and history always give the same order. A room picks a mode
// (round robin or FIFO) and tunes it with Options: weighted turns, a cap on
// songs in a row, and a cooldown between one person's songs.
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
	// Recent is who queued the songs that most recently started in this
	// room, newest first, starting with the one playing now. It needs to
	// reach back only as far as Options.Reach.
	Recent []string
}

// Options tune a policy. The zero value is the plain policy.
type Options struct {
	// MaxInARow caps how many songs one person gets in a row while
	// someone else has songs waiting. 0 means no cap.
	MaxInARow int
	// Cooldown is how many other songs must play between one person's
	// songs while someone else has songs waiting. 0 means none.
	Cooldown int
	// Weights gives some people more songs per turn in round robin: a
	// weight of 2 plays two of their songs each time their turn comes.
	// Missing users, and weights below 1, count as 1. Cooldown and
	// MaxInARow still apply, so they can cut a weighted turn short.
	Weights map[string]int
}

// Reach is how many recent plays the policy needs to see in State.Recent.
func (o Options) Reach() int {
	n := max(o.MaxInARow, o.Cooldown, 1)
	for _, w := range o.Weights {
		n = max(n, w)
	}
	return n
}

func (o Options) weight(user string) int { return max(o.Weights[user], 1) }

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

// ForMode returns the policy for a room's fairness mode, tuned by o.
// Unknown modes fall back to round robin.
func ForMode(mode string, o Options) Policy {
	if mode == ModeFIFO {
		return FIFO{o}
	}
	return RoundRobin{o}
}

// RoundRobin takes turns between users. The next turn goes to whoever has
// waited longest since their last song started; someone who hasn't had a
// song yet goes before anyone who has, so a late arrival plays soon rather
// than after every earlier lane drains. Between users who haven't played,
// whoever queued first goes first. A weighted user's turn is several songs.
type RoundRobin struct{ Options }

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

// lane is one user's queued items during a simulation.
type lane struct {
	user  string
	items []Item
	// last and waiting are round robin's: when the user last had a turn,
	// and when their earliest queued item was added (to break ties
	// between users who haven't played).
	last    lastTurn
	waiting time.Time
}

// Order implements Policy.
func (p RoundRobin) Order(s State) []Item {
	lanes := makeLanes(s)
	for _, l := range lanes {
		l.waiting = earliest(l.items)
		switch at, ok := s.LastPlayed[l.user]; {
		case l.user == s.Playing:
			l.last = lastTurn{tier: 2}
		case ok:
			l.last = lastTurn{tier: 1, at: at}
		}
	}
	return simulate(s, lanes, p.Options, func(run *lane, runLen int, eligible []*lane, step int) *lane {
		var next *lane
		if run != nil && runLen < p.weight(run.user) && slices.Contains(eligible, run) {
			next = run // a weighted turn carries on
		} else {
			for _, l := range eligible {
				if next == nil || cmp.Or(l.last.compare(next.last), l.waiting.Compare(next.waiting), cmp.Compare(l.user, next.user)) < 0 {
					next = l
				}
			}
		}
		next.last = lastTurn{tier: 2, step: step}
		return next
	})
}

// FIFO plays songs in the order they were queued, across everyone. Users
// can still reorder their own lane: it merges lanes by when each lane's
// next item was added. Weights don't apply.
type FIFO struct{ Options }

// Mode implements Policy.
func (FIFO) Mode() string { return ModeFIFO }

// Order implements Policy.
func (p FIFO) Order(s State) []Item {
	return simulate(s, makeLanes(s), p.Options, func(_ *lane, _ int, eligible []*lane, _ int) *lane {
		var best *lane
		for _, l := range eligible {
			if best == nil || cmp.Or(l.items[0].AddedAt.Compare(best.items[0].AddedAt), cmp.Compare(l.items[0].ID, best.items[0].ID)) < 0 {
				best = l
			}
		}
		return best
	})
}

func makeLanes(s State) []*lane {
	out := make([]*lane, 0, len(s.Lanes))
	for _, user := range users(s.Lanes) {
		out = append(out, &lane{user: user, items: s.Lanes[user]})
	}
	return out
}

// simulate plays the lanes out one song at a time. At each step, pick
// chooses from the lanes that may go next: those with songs left, less any
// that MaxInARow or Cooldown hold back (unless that would hold back all of
// them; the music never waits). run is the lane of whoever played last,
// and runLen how many songs in a row they've had.
func simulate(s State, lanes []*lane, o Options, pick func(run *lane, runLen int, eligible []*lane, step int) *lane) []Item {
	total := 0
	byUser := map[string]*lane{}
	for _, l := range lanes {
		total += len(l.items)
		byUser[l.user] = l
	}
	// history is who played, oldest first: the recent past, then the
	// simulated future.
	history := make([]string, 0, len(s.Recent)+total)
	for _, u := range slices.Backward(s.Recent) {
		history = append(history, u)
	}
	out := make([]Item, 0, total)
	eligible := make([]*lane, 0, len(lanes))
	for step := 1; len(out) < total; step++ {
		var run *lane
		runLen := 0
		if n := len(history); n > 0 {
			run = byUser[history[n-1]]
			for i := n - 1; i >= 0 && history[i] == history[n-1]; i-- {
				runLen++
			}
		}
		var waiting []*lane
		eligible = eligible[:0]
		for _, l := range lanes {
			if len(l.items) == 0 {
				continue
			}
			waiting = append(waiting, l)
			if o.MaxInARow > 0 && l == run && runLen >= o.MaxInARow {
				continue
			}
			if o.Cooldown > 0 && slices.Contains(history[max(len(history)-o.Cooldown, 0):], l.user) {
				continue
			}
			eligible = append(eligible, l)
		}
		if len(eligible) == 0 {
			eligible = append(eligible, waiting...)
		}
		next := pick(run, runLen, eligible, step)
		out = append(out, next.items[0])
		next.items = next.items[1:]
		history = append(history, next.user)
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
