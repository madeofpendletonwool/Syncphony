// SPDX-License-Identifier: AGPL-3.0-only

// Package awards hands out a night's awards (MAD-787): Deepest Cut, Tempo
// Whiplash, Trivia Champ and the rest, shown in the recap and on the big
// screen when the night ends, beside song of the night. Like stats it's
// pure: the night's plays, hearts, beat maps, music graph and game scores
// in, awards out.
//
// Each award names a person and, mostly, a song, with a one-line reason.
// An award with no clear winner is skipped, nobody takes more than
// MaxPerPerson, and there are at most Max.
package awards

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Award kinds.
const (
	DeepestCut    = "deepest_cut"
	DanceFloorMVP = "dance_floor_mvp"
	VibeKiller    = "vibe_killer"
	Trendsetter   = "trendsetter"
	TimeTraveler  = "time_traveler"
	TempoWhiplash = "tempo_whiplash"
	SampleSnitch  = "sample_snitch"
	Comeback      = "comeback"
	Opener        = "opener"
	Closer        = "closer"
	TriviaChamp   = "trivia_champ"
)

// Titles are the awards' names, by kind.
var Titles = map[string]string{
	DeepestCut:    "Deepest Cut",
	DanceFloorMVP: "Dance Floor MVP",
	VibeKiller:    "Vibe Killer",
	Trendsetter:   "Trendsetter",
	TimeTraveler:  "Time Traveler",
	TempoWhiplash: "Tempo Whiplash",
	SampleSnitch:  "Sample Snitch",
	Comeback:      "The Comeback",
	Opener:        "The Opener",
	Closer:        "The Closer",
	TriviaChamp:   "Trivia Champ",
}

// Limits.
const (
	// Max is the most awards a night gets.
	Max = 6
	// MaxPerPerson is the most awards one person takes.
	MaxPerPerson = 2
	// ComebackWindow is how soon after it played a song queued again
	// makes a comeback.
	ComebackWindow = 3 * time.Hour
	// FollowWindow is how long after a song ends someone else queueing
	// the same artist still counts as following it.
	FollowWindow = 15 * time.Minute
	// QuickSkip is how soon a skip has to come to count against a song.
	QuickSkip = 30 * time.Second
)

// Play is one song the night played, and what's known about it. Zero
// values are unknown.
type Play struct {
	ItemID string
	// UserID queued it; empty for autopilot, which wins nothing.
	UserID  string
	Title   string
	Artists []string
	// Song identifies the song across queue items (a repeat is the same
	// song queued again).
	Song                        string
	AddedAt, StartedAt, EndedAt time.Time
	// EndReason is how it ended: store.EndFinished, store.EndSkipped...
	EndReason string
	Hearts    int
	// BPM and Energy (0–1) are from its beat map.
	BPM, Energy float64
	// Rank is how popular it is across all music, 0–1.
	Rank float64
	// Year it first came out.
	Year int
	// Samples is how many songs it samples.
	Samples int
}

// Score is someone's game score for the night.
type Score struct {
	UserID string
	Points int
}

// Award is one award.
type Award struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	UserID string `json:"userId"`
	// ItemID is the song it's for, if it's for one.
	ItemID string `json:"itemId,omitempty"`
	Reason string `json:"reason"`
}

// order is which awards win a place when there are too many: the ones
// that say the most about the night first, the opener and closer last.
var order = []func([]Play, []Score) (Award, bool){
	triviaChamp, deepestCut, danceFloorMVP, tempoWhiplash, timeTraveler,
	trendsetter, sampleSnitch, comeback, vibeKiller, opener, closer,
}

// Pick hands out the night's awards. plays are the night's, in the order
// they started.
func Pick(plays []Play, scores []Score) []Award {
	plays = slices.DeleteFunc(slices.Clone(plays), func(p Play) bool {
		return p.EndReason != store.EndFinished && p.EndReason != store.EndSkipped
	})
	won := map[string]int{}
	var out []Award
	for _, f := range order {
		if len(out) == Max {
			break
		}
		a, ok := f(plays, scores)
		if !ok || a.UserID == "" || won[a.UserID] >= MaxPerPerson {
			continue
		}
		a.Title = Titles[a.Kind]
		won[a.UserID]++
		out = append(out, a)
	}
	return out
}

// people are the plays someone queued, not autopilot's.
func people(plays []Play) []Play {
	return slices.DeleteFunc(slices.Clone(plays), func(p Play) bool { return p.UserID == "" })
}

// best returns the one item with the highest score above floor, false if
// there's none or a tie for it.
func best[T any](items []T, score func(T) float64, floor float64) (T, bool) {
	var top T
	topScore, tied, found := math.Inf(-1), false, false
	for _, it := range items {
		s := score(it)
		if s <= floor {
			continue
		}
		switch {
		case s > topScore:
			top, topScore, tied, found = it, s, false, true
		case s == topScore:
			tied = true
		}
	}
	return top, found && !tied
}

func triviaChamp(_ []Play, scores []Score) (Award, bool) {
	s, ok := best(scores, func(s Score) float64 { return float64(s.Points) }, 0)
	return Award{Kind: TriviaChamp, UserID: s.UserID, Reason: fmt.Sprintf("%s points at the games", thousands(s.Points))}, ok
}

func deepestCut(plays []Play, _ []Score) (Award, bool) {
	eligible := slices.DeleteFunc(people(plays), func(p Play) bool {
		return p.Rank <= 0 || (p.EndReason != store.EndFinished && p.Hearts == 0)
	})
	// Two songs or more, or it's not deep, it's just the only one known.
	if len(eligible) < 2 {
		return Award{}, false
	}
	p, ok := best(eligible, func(p Play) float64 { return -p.Rank }, math.Inf(-1))
	reason := "The least-known song of the night, and it played right through"
	if p.Hearts > 0 {
		reason = "The least-known song of the night, and it still got " + plural(p.Hearts, "heart")
	}
	return Award{Kind: DeepestCut, UserID: p.UserID, ItemID: p.ItemID, Reason: reason}, ok
}

// High energy: a loud, fast song.
const (
	highEnergy = 0.6
	highBPM    = 115
)

func danceFloorMVP(plays []Play, _ []Score) (Award, bool) {
	hearts := map[string]int{}
	top := map[string]Play{}
	for _, p := range people(plays) {
		if p.Energy < highEnergy || p.BPM < highBPM || p.Hearts == 0 {
			continue
		}
		hearts[p.UserID] += p.Hearts
		if t, ok := top[p.UserID]; !ok || p.Hearts > t.Hearts {
			top[p.UserID] = p
		}
	}
	who, ok := best(keys(hearts), func(u string) float64 { return float64(hearts[u]) }, 0)
	return Award{Kind: DanceFloorMVP, UserID: who, ItemID: top[who].ItemID, Reason: plural(hearts[who], "heart") + " on the night's dance floor fillers"}, ok
}

func vibeKiller(plays []Play, _ []Score) (Award, bool) {
	// The biggest energy drop from the song before.
	type drop struct {
		p    Play
		from float64
	}
	var drops []drop
	for i := 1; i < len(plays); i++ {
		a, b := plays[i-1], plays[i]
		if b.UserID != "" && a.Energy > 0 && b.Energy > 0 && a.Energy-b.Energy >= 0.3 {
			drops = append(drops, drop{b, a.Energy})
		}
	}
	if d, ok := best(drops, func(d drop) float64 { return d.from - d.p.Energy }, 0); ok {
		return Award{
			Kind: VibeKiller, UserID: d.p.UserID, ItemID: d.p.ItemID,
			Reason: fmt.Sprintf("Took the energy from %d%% to %d%%", pct(d.from), pct(d.p.Energy)),
		}, true
	}
	// Or the most quick skips on your songs.
	skips := map[string]int{}
	last := map[string]string{}
	for _, p := range people(plays) {
		if p.EndReason == store.EndSkipped && p.EndedAt.Sub(p.StartedAt) < QuickSkip {
			skips[p.UserID]++
			last[p.UserID] = p.ItemID
		}
	}
	who, ok := best(keys(skips), func(u string) float64 { return float64(skips[u]) }, 1)
	return Award{Kind: VibeKiller, UserID: who, ItemID: last[who], Reason: plural(skips[who], "song") + " skipped in under 30 seconds"}, ok
}

func trendsetter(plays []Play, _ []Score) (Award, bool) {
	type lead struct {
		p         Play
		followers int
	}
	var leads []lead
	for i, p := range plays {
		if p.UserID == "" {
			continue
		}
		who := map[string]bool{}
		for _, q := range plays[i+1:] {
			if q.UserID == "" || q.UserID == p.UserID || q.AddedAt.Before(p.StartedAt) || q.AddedAt.After(p.EndedAt.Add(FollowWindow)) {
				continue
			}
			if shareArtist(p, q) {
				who[q.UserID] = true
			}
		}
		if len(who) > 0 {
			leads = append(leads, lead{p, len(who)})
		}
	}
	l, ok := best(leads, func(l lead) float64 { return float64(l.followers) }, 0)
	reason := fmt.Sprintf("%s had %s queueing more %s", quote(l.p.Title), plural(l.followers, "person"), first(l.p.Artists))
	return Award{Kind: Trendsetter, UserID: l.p.UserID, ItemID: l.p.ItemID, Reason: reason}, ok
}

func timeTraveler(plays []Play, _ []Score) (Award, bool) {
	oldest, newest := map[string]Play{}, map[string]Play{}
	for _, p := range people(plays) {
		if p.Year <= 0 {
			continue
		}
		if o, ok := oldest[p.UserID]; !ok || p.Year < o.Year {
			oldest[p.UserID] = p
		}
		if n, ok := newest[p.UserID]; !ok || p.Year > n.Year {
			newest[p.UserID] = p
		}
	}
	spread := func(u string) float64 { return float64(newest[u].Year - oldest[u].Year) }
	who, ok := best(keys(oldest), spread, 9)
	o, n := oldest[who], newest[who]
	return Award{Kind: TimeTraveler, UserID: who, ItemID: o.ItemID, Reason: fmt.Sprintf("From %d to %d, %d years apart", o.Year, n.Year, n.Year-o.Year)}, ok
}

func tempoWhiplash(plays []Play, _ []Score) (Award, bool) {
	type jump struct {
		p    Play
		from float64
	}
	var jumps []jump
	for i := 1; i < len(plays); i++ {
		a, b := plays[i-1], plays[i]
		if b.UserID != "" && a.BPM > 0 && b.BPM > 0 && math.Abs(a.BPM-b.BPM) >= 30 {
			jumps = append(jumps, jump{b, a.BPM})
		}
	}
	j, ok := best(jumps, func(j jump) float64 { return math.Round(math.Abs(j.from - j.p.BPM)) }, 0)
	verb := "Dropped"
	if j.p.BPM > j.from {
		verb = "Jumped"
	}
	return Award{
		Kind: TempoWhiplash, UserID: j.p.UserID, ItemID: j.p.ItemID,
		Reason: fmt.Sprintf("%s from %.0f to %.0f BPM", verb, j.from, j.p.BPM),
	}, ok
}

func sampleSnitch(plays []Play, _ []Score) (Award, bool) {
	n := map[string]int{}
	song := map[string]string{}
	for _, p := range people(plays) {
		if p.Samples > 0 {
			n[p.UserID]++
			if song[p.UserID] == "" {
				song[p.UserID] = p.ItemID
			}
		}
	}
	who, ok := best(keys(n), func(u string) float64 { return float64(n[u]) }, 0)
	return Award{Kind: SampleSnitch, UserID: who, ItemID: song[who], Reason: plural(n[who], "song") + " built on samples"}, ok
}

func comeback(plays []Play, _ []Score) (Award, bool) {
	type back struct {
		p     Play
		after time.Duration
	}
	var backs []back
	for i, p := range plays {
		for _, q := range plays[i+1:] {
			if q.UserID == "" || q.Song == "" || q.Song != p.Song || q.ItemID == p.ItemID {
				continue
			}
			if d := q.AddedAt.Sub(p.StartedAt); d > 0 && d <= ComebackWindow {
				backs = append(backs, back{q, q.AddedAt.Sub(p.EndedAt)})
				break
			}
		}
	}
	// The quickest return.
	b, ok := best(backs, func(b back) float64 { return -b.after.Minutes() }, math.Inf(-1))
	return Award{Kind: Comeback, UserID: b.p.UserID, ItemID: b.p.ItemID, Reason: quote(b.p.Title) + " was back " + ago(b.after) + " after it played"}, ok
}

func opener(plays []Play, _ []Score) (Award, bool) {
	ps := people(plays)
	if len(ps) < 2 {
		return Award{}, false
	}
	p := ps[0]
	return Award{Kind: Opener, UserID: p.UserID, ItemID: p.ItemID, Reason: quote(p.Title) + " got the night started"}, true
}

func closer(plays []Play, _ []Score) (Award, bool) {
	ps := people(plays)
	if len(ps) < 2 {
		return Award{}, false
	}
	p := ps[len(ps)-1]
	return Award{Kind: Closer, UserID: p.UserID, ItemID: p.ItemID, Reason: quote(p.Title) + " played us out"}, true
}

// --- Helpers -------------------------------------------------------------

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func shareArtist(a, b Play) bool {
	for _, x := range a.Artists {
		for _, y := range b.Artists {
			if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(y)) && x != "" {
				return true
			}
		}
	}
	return false
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if word == "person" {
		return fmt.Sprintf("%d people", n)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func quote(title string) string { return "“" + cmp.Or(title, "A song") + "”" }

func first(ss []string) string {
	if len(ss) == 0 {
		return "of the same"
	}
	return ss[0]
}

func pct(x float64) int { return int(math.Round(x * 100)) }

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	}
	return fmt.Sprintf("%.1f hours", d.Hours())
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
