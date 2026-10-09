// SPDX-License-Identifier: AGPL-3.0-only

// Package recap makes a night's Syncphony Wrapped (MAD-722): the
// shareable story of a session, beyond its stats. Who brought the most,
// whose songs got the hearts, the song the room kept skipping, the longest
// run of someone's songs played through, the genres and decades it
// spanned, and which friends' tastes met.
//
// Like stats it's pure: plays and what's known about their music in,
// numbers out. Genres and years come from Facts, which is only ever asked
// about what's already cached, so a recap never waits on the internet.
package recap

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/stats"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Limits on what a recap lists.
const (
	// TopShares is how many genres and decades a mix lists; the rest are
	// left out.
	TopShares = 5
	// TopOverlaps is how many pairs of friends' tastes are listed.
	TopOverlaps = 3
	// MinStreak is the shortest streak worth telling.
	MinStreak = 3
	// tagsPerArtist is how many of an artist's strongest tags count
	// toward the genre mix.
	tagsPerArtist = 2
)

// Tag is a genre and how strongly it applies, from 0 to 1.
type Tag struct {
	Name   string
	Weight float64
}

// Facts is what's known about the music, from a cache. musicgraph in
// production. Either may answer nothing.
type Facts interface {
	// Tags are an artist's genres, strongest first.
	Tags(ctx context.Context, artist string) []Tag
	// Year is when a song came out, or 0.
	Year(ctx context.Context, t provider.Track) int
}

// Count is a person and how many.
type Count struct {
	UserID string
	N      int
}

// ItemCount is a song and how many.
type ItemCount struct {
	Item store.QueueItem
	N    int
}

// Share is part of a mix: a genre or decade, and how many songs.
type Share struct {
	Name  string
	Plays int
}

// Overlap is two people whose songs shared artists.
type Overlap struct {
	UserIDs [2]string
	// Artists they both brought, most played first.
	Artists []string
}

// Recap is a night's Wrapped.
type Recap struct {
	stats.Summary
	// TopAdder is whoever's songs played most. MostHearted is whoever's
	// songs got the most hearts. Nil when no one's did.
	TopAdder, MostHearted *Count
	// MostSkipped is the song skipped most, nil if none was.
	MostSkipped *ItemCount
	// Streak is the longest run of one person's songs played through
	// without a skip, at least MinStreak.
	Streak *Count
	// Genres and Decades are the mix, biggest first; decades in order.
	Genres, Decades []Share
	// Overlaps are pairs of friends who brought the same artists, most
	// shared first.
	Overlaps []Overlap
}

// Make makes a recap of plays, in the order they started. hearts are the
// hearts on each song, by item ID.
func Make(ctx context.Context, plays []rooms.Played, hearts map[string]int, facts Facts) Recap {
	r := Recap{Summary: stats.Summarize(plays), Genres: []Share{}, Decades: []Share{}, Overlaps: []Overlap{}}
	if len(r.People) > 0 {
		r.TopAdder = &Count{UserID: r.People[0].UserID, N: r.People[0].Plays}
	}
	counted := counts(plays)
	r.MostHearted = mostHearted(counted, hearts)
	r.MostSkipped = mostSkipped(counted)
	r.Streak = streak(counted)
	r.Overlaps = overlaps(counted)
	if facts != nil {
		r.Genres, r.Decades = mix(ctx, counted, facts)
	}
	return r
}

// counts are the plays that count: songs that finished or were skipped.
func counts(plays []rooms.Played) []rooms.Played {
	out := make([]rooms.Played, 0, len(plays))
	for _, p := range plays {
		if p.EndReason == store.EndFinished || p.EndReason == store.EndSkipped {
			out = append(out, p)
		}
	}
	return out
}

// person is whose song an item is: "" for autopilot's.
func person(it store.QueueItem) string {
	if it.IsAutopilot() {
		return ""
	}
	return it.AddedBy
}

// best returns the key with the highest count, the earliest seen on a
// tie, or "" if none counted above zero.
func best(order []string, n map[string]int) string {
	top := ""
	for _, k := range order {
		if n[k] > n[top] {
			top = k
		}
	}
	return top
}

func mostHearted(plays []rooms.Played, hearts map[string]int) *Count {
	n := map[string]int{}
	var order []string
	seen := map[string]bool{}
	for _, p := range plays {
		who := person(p.Item)
		if who == "" || seen[p.Item.ID] {
			continue
		}
		seen[p.Item.ID] = true
		if _, ok := n[who]; !ok {
			order = append(order, who)
		}
		n[who] += hearts[p.Item.ID]
	}
	if top := best(order, n); top != "" {
		return &Count{UserID: top, N: n[top]}
	}
	return nil
}

func mostSkipped(plays []rooms.Played) *ItemCount {
	n := map[string]int{}
	items := map[string]store.QueueItem{}
	var order []string
	for _, p := range plays {
		if p.EndReason != store.EndSkipped {
			continue
		}
		k := songKey(p.Item)
		if _, ok := n[k]; !ok {
			order = append(order, k)
		}
		n[k]++
		items[k] = p.Item
	}
	if top := best(order, n); top != "" {
		return &ItemCount{Item: items[top], N: n[top]}
	}
	return nil
}

// streak finds the longest run of one person's songs, in the order theirs
// played, that each played to the end.
func streak(plays []rooms.Played) *Count {
	run := map[string]int{}
	var top *Count
	for _, p := range plays {
		who := person(p.Item)
		if who == "" {
			continue
		}
		if p.EndReason != store.EndFinished {
			run[who] = 0
			continue
		}
		run[who]++
		if run[who] >= MinStreak && (top == nil || run[who] > top.N) {
			top = &Count{UserID: who, N: run[who]}
		}
	}
	return top
}

// overlaps finds pairs of people who brought songs by the same artists.
func overlaps(plays []rooms.Played) []Overlap {
	// artists[user][artist key] = plays; names keeps a display name.
	artists := map[string]map[string]int{}
	names := map[string]string{}
	var people []string
	for _, p := range plays {
		who := person(p.Item)
		if who == "" {
			continue
		}
		if artists[who] == nil {
			artists[who] = map[string]int{}
			people = append(people, who)
		}
		for _, a := range trackOf(p.Item).Artists {
			k := strings.ToLower(strings.TrimSpace(a.Name))
			if k == "" {
				continue
			}
			artists[who][k]++
			names[k] = cmp.Or(names[k], a.Name)
		}
	}
	slices.Sort(people)
	out := []Overlap{}
	for i, a := range people {
		for _, b := range people[i+1:] {
			var shared []string
			for k := range artists[a] {
				if artists[b][k] > 0 {
					shared = append(shared, k)
				}
			}
			if len(shared) == 0 {
				continue
			}
			slices.SortFunc(shared, func(x, y string) int {
				return cmp.Or(cmp.Compare(artists[a][y]+artists[b][y], artists[a][x]+artists[b][x]), cmp.Compare(x, y))
			})
			o := Overlap{UserIDs: [2]string{a, b}}
			for _, k := range shared {
				o.Artists = append(o.Artists, names[k])
			}
			out = append(out, o)
		}
	}
	slices.SortStableFunc(out, func(x, y Overlap) int { return cmp.Compare(len(y.Artists), len(x.Artists)) })
	return out[:min(len(out), TopOverlaps)]
}

// mix sums up the genres (each song's artist's strongest tags, by
// weight) and decades of the songs that played.
func mix(ctx context.Context, plays []rooms.Played, facts Facts) (genres, decades []Share) {
	tags := map[string][]Tag{}
	genreN := map[string]float64{}
	decadeN := map[int]int{}
	for _, p := range plays {
		t := trackOf(p.Item)
		if len(t.Artists) > 0 {
			name := t.Artists[0].Name
			k := strings.ToLower(name)
			ts, ok := tags[k]
			if !ok {
				ts = facts.Tags(ctx, name)
				tags[k] = ts
			}
			ts = ts[:min(len(ts), tagsPerArtist)]
			var sum float64
			for _, tg := range ts {
				sum += max(tg.Weight, 0.01)
			}
			for _, tg := range ts {
				genreN[strings.ToLower(tg.Name)] += max(tg.Weight, 0.01) / sum
			}
		}
		year := t.Year
		if year == 0 {
			year = facts.Year(ctx, t)
		}
		if year >= 1900 {
			decadeN[year/10*10]++
		}
	}
	genres = []Share{}
	for name, n := range genreN {
		if plays := int(n + 0.5); plays > 0 {
			genres = append(genres, Share{Name: name, Plays: plays})
		}
	}
	slices.SortFunc(genres, func(a, b Share) int { return cmp.Or(cmp.Compare(b.Plays, a.Plays), cmp.Compare(a.Name, b.Name)) })
	genres = genres[:min(len(genres), TopShares)]

	decades = []Share{}
	top := slices.SortedFunc(maps.Keys(decadeN), func(a, b int) int { return cmp.Or(cmp.Compare(decadeN[b], decadeN[a]), cmp.Compare(a, b)) })
	top = top[:min(len(top), TopShares)]
	slices.Sort(top)
	for _, d := range top {
		decades = append(decades, Share{Name: fmt.Sprintf("%ds", d), Plays: decadeN[d]})
	}
	return genres, decades
}

// trackOf reads an item's track snapshot.
func trackOf(it store.QueueItem) provider.Track {
	var t provider.Track
	_ = json.Unmarshal([]byte(it.Metadata), &t)
	return t
}

// songKey is the same song, across plays: its ISRC if known.
func songKey(it store.QueueItem) string {
	if t := trackOf(it); t.ISRC != "" {
		return "isrc\x00" + t.ISRC
	}
	return it.Provider + "\x00" + it.TrackID
}
