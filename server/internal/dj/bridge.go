// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"cmp"
	"encoding/json"
	"math"
	"slices"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Common ground (MAD-758): when members' tastes differ, the DJ looks for
// music in between instead of taking turns between extremes. The walk
// remembers how near each artist it reaches is to each member's taste. A
// song near two members' tastes, by a different artist of each, is a
// bridge, worth more the further apart their tastes are. Bridges take
// turns with members' own lanes, so everyone still hears their vibe.
const (
	// bridgeWeight is what a perfect bridge between members with nothing
	// in common adds to a score: it's bridgeWeight × how far apart they
	// are × how near the song is to the farther of them.
	bridgeWeight = 0.4
	// bridgeApart is how far apart, from 0 to 1, two members' tastes must
	// be to need a bridge; bridgeReach, how near (against each member's
	// nearest artist) a song must be to both to be one.
	bridgeApart = 0.3
	bridgeReach = 0.25
	// memberSeeds is how many of each member's own favorite artists the
	// walk also starts from, if the room's favorites leave them out.
	memberSeeds = 2
	// turnWeight is what a song near the member whose turn it is gains, on
	// a fill that isn't for a bridge, at their nearest artist.
	turnWeight = 0.2
)

// Bridge is a song between two members' tastes.
type Bridge struct {
	// Users are the members, Artists the artists of theirs it's between
	// (one each), and Apart how far apart their tastes are, 0 to 1.
	Users   [2]string
	Artists [2]string
	Apart   float64
}

// mix is how a fill serves the members of a mixed room: by a bridge
// between two of them, or by one member's turn.
type mix struct {
	// pairs are the members whose tastes are apart enough to bridge, if
	// this fill is for a bridge.
	pairs []pair
	// turn is the member whose turn it is, if it isn't.
	turn string
}

type pair struct {
	a, b  string
	apart float64
}

// mixed adds to candidates' scores for the fill's mix, best first: a
// bridge's bonus, or how near a song is to the member whose turn it is.
func mixed(scored []candidate, p Profile, mx mix) []candidate {
	if len(mx.pairs) == 0 && mx.turn == "" {
		return scored
	}
	out := slices.Clone(scored)
	for i := range out {
		c := &out[i]
		switch {
		case len(mx.pairs) > 0:
			if b, v := c.bridging(mx.pairs, p.Artists); v > 0 {
				c.score, c.bridge = math.Round((c.score+v)*1000)/1000, &b
			}
		case mx.turn != "":
			c.score = math.Round((c.score+turnWeight*c.reach[mx.turn])*1000) / 1000
		}
	}
	slices.SortStableFunc(out, func(a, b candidate) int { return cmp.Compare(b.score, a.score) })
	return out
}

// mixFor decides what a fill is for: a bridge, if the room's last pick by
// the DJ wasn't one and some members are apart enough; else the turn of
// the member autopilot served longest ago. A room of one member needs
// neither.
func mixFor(p Profile) mix {
	ms := p.mixMembers()
	if len(ms) < 2 {
		return mix{}
	}
	if !p.LastBridge {
		var out mix
		for i, a := range ms {
			for _, b := range ms[i+1:] {
				if d := 1 - cosine(p.memberVibes[a], p.memberVibes[b]); d >= bridgeApart {
					out.pairs = append(out.pairs, pair{a, b, d})
				}
			}
		}
		if len(out.pairs) > 0 {
			return out
		}
	}
	return mix{turn: p.servedLongestAgo(ms)}
}

// mixMembers are the members whose tastes the DJ mixes: the ones with a
// taste in the room, or of those, the ones here if two or more are.
func (p Profile) mixMembers() []string {
	var all, here []string
	for u := range p.memberVibes {
		all = append(all, u)
		if p.present[u] {
			here = append(here, u)
		}
	}
	if len(here) >= 2 {
		all = here
	}
	slices.Sort(all)
	return all
}

// servedLongestAgo is the member among ms that autopilot credited a song
// to longest ago, or never.
func (p Profile) servedLongestAgo(ms []string) string {
	best, at := "", -1
	for _, u := range ms {
		i, ok := p.served[u]
		if !ok {
			return u
		}
		if i > at {
			best, at = u, i
		}
	}
	return best
}

// bridge is the best bridge a candidate makes between a pair of members,
// and what it adds to its score.
func (c candidate) bridging(pairs []pair, artists map[string]*Taste) (Bridge, float64) {
	var out Bridge
	best := 0.0
	for _, pr := range pairs {
		ra, rb := c.reach[pr.a], c.reach[pr.b]
		va, vb := c.reachVia[pr.a], c.reachVia[pr.b]
		if min(ra, rb) < bridgeReach || va == "" || vb == "" || va == vb {
			continue
		}
		if v := bridgeWeight * pr.apart * min(ra, rb); v > best {
			best = v
			out = Bridge{Users: [2]string{pr.a, pr.b}, Artists: [2]string{nameOf(artists, va), nameOf(artists, vb)}, Apart: pr.apart}
		}
	}
	return out, best
}

func nameOf(artists map[string]*Taste, k string) string {
	if t, ok := artists[k]; ok {
		return t.Artist.Name
	}
	return k
}

// members notes each member's own vibe, who's here, and whom autopilot
// served lately, for mixing their tastes.
func (p *Profile) members(in Input) {
	p.memberVibes, p.present, p.served = map[string]map[string]float64{}, map[string]bool{}, map[string]int{}
	for a, t := range p.Artists {
		for u, w := range t.ByMember {
			if u == "" || w <= 0 {
				continue
			}
			if p.memberVibes[u] == nil {
				p.memberVibes[u] = map[string]float64{}
			}
			addVibe(p.memberVibes[u], a, w, in.Tags)
		}
	}
	for _, u := range in.Present {
		p.present[u] = true
	}
	for i, it := range in.Mine {
		if _, ok := p.served[it.AddedBy]; !ok {
			p.served[it.AddedBy] = i
		}
		if i == 0 {
			p.LastBridge = isBridge(it)
		}
	}
}

// memberSeed is a member's newest song by an artist (ArtistKey) the room
// liked; its ID is "" if there's none.
func (p Profile) memberSeed(u, artist string) store.QueueItem {
	if t, ok := p.Artists[artist]; ok {
		return t.seeds[u]
	}
	return store.QueueItem{}
}

// memberTop returns a member's own favorite artists, most first, up to n.
func (p Profile) memberTop(u string, n int) []*Taste {
	var out []*Taste
	for _, t := range p.Artists {
		if t.ByMember[u] > 0 {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b *Taste) int {
		if c := cmp.Compare(b.ByMember[u], a.ByMember[u]); c != 0 {
			return c
		}
		return cmp.Compare(a.Artist.Name, b.Artist.Name)
	})
	return out[:min(len(out), n)]
}

// seeds are the artists the walk starts from: the room's favorites, then
// each member's own favorites the room's leave out.
func (p Profile) seeds() []*Taste {
	out := p.Top(seedArtists)
	for _, u := range p.mixMembers() {
		for _, t := range p.memberTop(u, memberSeeds) {
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	return out
}

// shares are each member's share of the room's liking of an artist, 0 to
// 1, autopilot's own songs left out.
func (t *Taste) shares() map[string]float64 {
	total := 0.0
	for _, w := range t.ByMember {
		total += w
	}
	out := map[string]float64{}
	for u, w := range t.ByMember {
		if u != "" && w > 0 {
			out[u] = w / total
		}
	}
	return out
}

// isBridge reports whether autopilot picked an item as a bridge.
func isBridge(it store.QueueItem) bool {
	if !it.IsAutopilot() {
		return false
	}
	var info struct {
		Reason struct {
			Bridge json.RawMessage `json:"bridge"`
		} `json:"reason"`
	}
	_ = json.Unmarshal([]byte(it.Autopilot.String), &info)
	return len(info.Reason.Bridge) > 0 && string(info.Reason.Bridge) != "null"
}

// memberReach normalizes each member's reach to the artists the walk
// found: 1 at the member's nearest.
func memberReach(nodes map[string]*node) {
	most := map[string]float64{}
	for _, n := range nodes {
		for u, r := range n.byMember {
			most[u] = max(most[u], r)
		}
	}
	for _, n := range nodes {
		n.reach = map[string]float64{}
		for u, r := range n.byMember {
			if most[u] > 0 {
				n.reach[u] = r / most[u]
			}
		}
	}
}
