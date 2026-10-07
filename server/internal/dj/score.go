// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"cmp"
	"math"
	"slices"
)

// How candidates are scored and picked.
const (
	// The score's weights at explore 0 and 1; in between, they're mixed.
	// Near 0 the DJ plays the room's artists and their hits; near 1, new
	// artists, further out.
	simFamiliar, simExplore = 0.55, 0.30
	popFamiliar, popExplore = 0.40, 0.20
	novFamiliar, novExplore = 0.05, 0.50
	// Spacing: an artist heard in the last spaceNear songs loses
	// spaceNearCost; in the last recentReach, spaceFarCost.
	spaceNear     = 3
	spaceNearCost = 0.6
	spaceFarCost  = 0.25
	// deepCutPercent is how often the DJ reaches for a deep cut by an
	// artist the room loves (lovedPlays songs or more), instead of a hit.
	deepCutPercent = 15
	lovedPlays     = 3
	// shortlist is how many of the best candidates a pick is drawn from,
	// at most perArtist of any one artist, so a pick chooses between
	// artists rather than among one's songs; drawn is how many are drawn
	// to try in order.
	shortlist = 25
	perArtist = 2
	drawn     = 8
	// The softmax temperature at explore 0 and 1: low keeps to the best,
	// higher spreads the picks.
	tempFamiliar, tempExplore = 0.05, 0.15
	// priorShare caps the long-term taste's share of a candidate's score:
	// enough to tip a choice between near-equal candidates, never to
	// outvote what the room is playing now. A prior p raises the score by
	// p·priorShare/(1−priorShare), so its share is at most priorShare.
	// priorArtist is how much of the prior is the room's liking of the
	// artist, the rest of their tags.
	priorShare  = 0.12
	priorArtist = 0.6
	// vetoCost is what an artist the room skipped in past nights loses at
	// full veto; doubtCost, one it nearly turned away tonight. Both fade.
	vetoCost  = 0.3
	doubtCost = 0.3
)

// Throwbacks: on throwbackPercent of fills, artists the room loved in past
// nights but hasn't played lately compete for the pick, as near to the
// room as its nearest artist. Each throwback the room skipped lately
// halves the chance.
const (
	throwbackPercent = 10
	// throwbackRest is how many nights since the room last liked an artist
	// before they can come back; throwbackMin, how much it must have liked
	// them, against its favorite; throwbackTonight, how little it may like
	// them tonight.
	throwbackRest    = 3
	throwbackMin     = 0.03
	throwbackTonight = 0.2
	// throwbackArtists is how many artists one fill brings back to choose
	// from, the most loved first.
	throwbackArtists = 3
)

// novelty is how new a candidate's artist is to the room.
var novelty = map[int]float64{0: 0, 1: 0.6, 2: 1}

// score scores candidates for a room at an explore level from 0 to 1,
// best first. deepCut turns a loved artist's lesser-known songs up and
// their hits down, for this pick. The room's long-term taste (lt) is a
// weak prior, at most priorShare of a score.
func score(cands []candidate, p Profile, lt LongTerm, explore float64, deepCut bool) []candidate {
	most := 0.0
	for _, c := range cands {
		if c.kind != KindThrowback {
			most = max(most, c.affinity)
		}
	}
	if most == 0 {
		return nil
	}
	wSim := lerp(simFamiliar, simExplore, explore)
	wPop := lerp(popFamiliar, popExplore, explore)
	wNov := lerp(novFamiliar, novExplore, explore)
	out := slices.Clone(cands)
	for i := range out {
		c := &out[i]
		c.similarity = c.affinity / most
		if c.kind == KindThrowback {
			c.similarity = c.affinity // already against the most loved
		}
		c.novelty = novelty[c.hop]
		pop := c.popularity
		if t, ok := p.Artists[c.artist]; deepCut && ok && t.Plays >= lovedPlays {
			pop, c.deepCut = 1-pop, true
		}
		c.prior = lt.prior(c.artist, c.tags)
		c.score = (wSim*c.similarity + wPop*pop + wNov*c.novelty) * (1 + c.prior*priorShare/(1-priorShare))
		c.score -= vetoCost*lt.Artists[c.artist].Veto + doubtCost*p.Doubt[c.artist]
		if i, ok := p.RecentlyPlayed(c.artist); ok {
			if i < spaceNear {
				c.score -= spaceNearCost
			} else {
				c.score -= spaceFarCost
			}
		}
		c.score = math.Round(c.score*1000) / 1000
	}
	slices.SortStableFunc(out, func(a, b candidate) int { return cmp.Compare(b.score, a.score) })
	return out
}

// diverse keeps, in order, the best perArtist songs of each artist, up to
// shortlist songs.
func diverse(scored []candidate) []candidate {
	var out []candidate
	count := map[string]int{}
	for _, c := range scored {
		if count[c.artist] < perArtist {
			count[c.artist]++
			out = append(out, c)
		}
		if len(out) == shortlist {
			break
		}
	}
	return out
}

// draw orders up to n of the best candidates to try, each drawn with a
// probability that grows with its score (softmax at temp), without
// replacement. rand returns a number in [0, n).
func draw(scored []candidate, n int, temp float64, rand func(int) int) []candidate {
	pool := diverse(scored)
	var out []candidate
	for len(out) < n && len(pool) > 0 {
		top := pool[0].score
		for _, c := range pool {
			top = max(top, c.score)
		}
		weights := make([]float64, len(pool))
		total := 0.0
		for i, c := range pool {
			weights[i] = math.Exp((c.score - top) / temp)
			total += weights[i]
		}
		const steps = 1_000_000
		r := float64(rand(steps)) / steps * total
		i := 0
		for ; i < len(pool)-1; i++ {
			if r < weights[i] {
				break
			}
			r -= weights[i]
		}
		out = append(out, pool[i])
		pool = slices.Delete(pool, i, i+1)
	}
	return out
}

// throwbackChance is the percent chance this fill brings throwbacks.
func throwbackChance(p Profile) int {
	return throwbackPercent >> min(p.ThrowbacksSkipped, 8)
}

// Throwbacks returns the artists the room loved in past nights but hasn't
// played lately, most loved first, up to throwbackArtists.
func Throwbacks(p Profile, lt LongTerm) []string {
	var out []string
	for _, a := range lt.Top(len(lt.Artists)) {
		l := lt.Artists[a]
		if l.Weight*(1-l.Veto) < throwbackMin || l.Rest < throwbackRest || p.Avoid[a] || p.Doubt[a] > 0 {
			continue
		}
		if t, ok := p.Artists[a]; ok && t.Weight >= throwbackTonight {
			continue
		}
		if _, recent := p.RecentlyPlayed(a); recent {
			continue
		}
		out = append(out, a)
		if len(out) == throwbackArtists {
			break
		}
	}
	return out
}

func lerp(a, b, t float64) float64 { return a + (b-a)*min(max(t, 0), 1) }
