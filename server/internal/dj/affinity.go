// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"cmp"
	"database/sql"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// How the room's long-term taste is kept. Tonight's taste (Profile)
// drives the picks; the long-term taste is only a weak prior on them.
const (
	// nightFade is how much each night counts against the one after it,
	// so last weekend still counts and three months ago is faint, however
	// many idle days fall in between.
	nightFade = 0.7
	// forgetBelow drops what has faded to almost nothing.
	forgetBelow = 0.005
	// throwbackSkip is how much more a skipped throwback counts against
	// its artist in the long run than an ordinary skip.
	throwbackSkip = 2.0
	// A long-term veto, from 0 to 1, is skips beyond vetoForgive times the
	// likes, over vetoFull. It fades with the nights, like the rest.
	vetoForgive = 0.5
	vetoFull    = 3.0
)

// Kinds of long-term affinity.
const (
	affinityArtist = "artist"
	affinityTag    = "tag"
)

// LongTerm is the room's taste over past nights.
type LongTerm struct {
	// Nights is how many nights it has learned from.
	Nights int
	// Artists the room liked or skipped, by ArtistKey.
	Artists map[string]Lasting
	// Tags the room liked, by lowercased name, from 0 to 1 against the
	// strongest.
	Tags map[string]float64
}

// Lasting is how the room felt about an artist over past nights.
type Lasting struct {
	Name string
	// Weight is how much the room liked them, from 0 to 1 against the
	// artist it liked most, each member's taste counting the same.
	Weight float64
	// Veto is how much the room turned them away, from 0 to 1.
	Veto float64
	// Rest is how many nights ago the room last liked them: 0 for the
	// last night. LovedAt is when that night ended; zero if it never did.
	Rest    int
	LovedAt time.Time
}

// Top returns the artists the room liked most over past nights, most
// first, up to n.
func (lt LongTerm) Top(n int) []string {
	var out []string
	for a, l := range lt.Artists {
		if l.Weight > 0 {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		return cmp.Or(cmp.Compare(lt.Artists[b].Weight, lt.Artists[a].Weight), strings.Compare(a, b))
	})
	return out[:min(len(out), n)]
}

// TopTags returns the tags the room liked most, most first, up to n.
func (lt LongTerm) TopTags(n int) []string {
	out := make([]string, 0, len(lt.Tags))
	for t := range lt.Tags {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b string) int { return cmp.Or(cmp.Compare(lt.Tags[b], lt.Tags[a]), strings.Compare(a, b)) })
	return out[:min(len(out), n)]
}

// prior is how much the room's past nights favor an artist with tags,
// from 0 to 1: its own liking of them, and of their tags.
func (lt LongTerm) prior(artist string, tags []musicgraph.Tag) float64 {
	a := lt.Artists[artist].Weight
	var dot, total float64
	for _, t := range tags {
		dot += t.Weight * lt.Tags[strings.ToLower(t.Name)]
		total += t.Weight
	}
	if total == 0 {
		return a
	}
	return priorArtist*a + (1-priorArtist)*dot/total
}

// readLongTerm reads the room's long-term taste from its stored rows.
// Like the profile, each member's likes add up to the same, so a member
// who's been around for months doesn't outweigh someone new; autopilot's
// songs the room let play count roomWeight.
func readLongTerm(room store.DjRoom, rows []store.DjAffinity) LongTerm {
	lt := LongTerm{Nights: int(room.Nights), Artists: map[string]Lasting{}, Tags: map[string]float64{}}
	totals := map[[2]string]float64{} // kind, member
	for _, r := range rows {
		totals[[2]string{r.Kind, r.Member}] += r.Likes
	}
	likes, skips := map[string]float64{}, map[string]float64{}
	weights := map[string]float64{}
	mostArtist, mostTag := 0.0, 0.0
	for _, r := range rows {
		share := 0.0
		if t := totals[[2]string{r.Kind, r.Member}]; t > 0 {
			share = r.Likes / t
			if r.Member == "" {
				share *= roomWeight
			}
		}
		if r.Kind == affinityTag {
			lt.Tags[r.Key] += share
			mostTag = max(mostTag, lt.Tags[r.Key])
			continue
		}
		l := lt.Artists[r.Key]
		l.Name = cmp.Or(l.Name, r.Name)
		if r.LastAt.Valid && r.LastAt.Time.After(l.LovedAt) {
			l.LovedAt, l.Rest = r.LastAt.Time, int(room.Nights-r.LastNight)
		}
		lt.Artists[r.Key] = l
		weights[r.Key] += share
		likes[r.Key] += r.Likes
		skips[r.Key] += r.Skips
		mostArtist = max(mostArtist, weights[r.Key])
	}
	for a, l := range lt.Artists {
		if mostArtist > 0 {
			l.Weight = weights[a] / mostArtist
		}
		l.Veto = min(max((skips[a]-vetoForgive*likes[a])/vetoFull, 0), 1)
		lt.Artists[a] = l
	}
	for t, w := range lt.Tags {
		lt.Tags[t] = w / mostTag
	}
	return lt
}

// fold folds one finished night into a room's stored rows: everything
// before it fades by nightFade, then the night's likes and skips are
// added. nights is the night's number, counting from 1, and ended is when
// it ended. tags returns an artist's tags, by name, if known.
func fold(roomID string, rows []store.DjAffinity, night Input, nights int64, ended time.Time, tags func(artist string) []musicgraph.Tag) []store.DjAffinity {
	type key struct{ kind, key, member string }
	byKey := map[key]*store.DjAffinity{}
	var order []key
	get := func(k key, name string) *store.DjAffinity {
		r, ok := byKey[k]
		if !ok {
			r = &store.DjAffinity{RoomID: roomID, Kind: k.kind, Key: k.key, Member: k.member, Name: name}
			byKey[k] = r
			order = append(order, k)
		}
		return r
	}
	for _, r := range rows {
		r.Likes *= nightFade
		r.Skips *= nightFade
		k := key{r.Kind, r.Key, r.Member}
		byKey[k] = &r
		order = append(order, k)
	}
	loved := func(r *store.DjAffinity) {
		r.LastNight, r.LastAt = nights, sql.NullTime{Time: ended, Valid: true}
	}
	for _, re := range reactions(night, math.MaxInt) {
		name, a := re.name, re.artist
		if a == "" {
			continue
		}
		if re.v < 0 {
			v := -re.v
			if re.throwback {
				v *= throwbackSkip
			}
			get(key{affinityArtist, a, ""}, name).Skips += v
			continue
		}
		r := get(key{affinityArtist, a, re.who}, name)
		r.Likes += re.v
		loved(r)
		for _, t := range tags(name) {
			tk := strings.ToLower(t.Name)
			r := get(key{affinityTag, tk, re.who}, t.Name)
			r.Likes += re.v * t.Weight
			loved(r)
		}
	}
	out := make([]store.DjAffinity, 0, len(order))
	for _, k := range order {
		r := byKey[k]
		if r.Likes >= forgetBelow || r.Skips >= forgetBelow {
			out = append(out, *r)
		}
	}
	return out
}
