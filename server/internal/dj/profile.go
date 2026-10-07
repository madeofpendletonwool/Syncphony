// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"cmp"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// How the room's taste is read.
const (
	// halfLife is how long it takes a play to count half as much.
	halfLife = 2 * time.Hour
	// profileReach is how many of the room's plays the profile reads.
	profileReach = 100
	// recentReach is how many of the room's last songs count as recent,
	// for spacing.
	recentReach = 8
)

// What each thing the room did says about an artist, before decay.
const (
	finishedSignal = 1.0
	// queuedSignal is a member's song playing or waiting: they chose it.
	queuedSignal  = 0.8
	skippedSignal = -1.0
	// autopilotFinished is the room letting one of autopilot's songs play
	// through: a quieter yes than choosing it.
	autopilotFinished = 0.5
	// removedSignal is someone taking one of autopilot's songs off the
	// queue before it played.
	removedSignal = -0.5
)

// How much each voice counts in the room's taste. Each member's likes add
// up to the same, however many songs they've queued, so nobody's run of
// songs takes over; members in the room count more.
const (
	presentWeight = 1.5
	awayWeight    = 1.0
	// roomWeight is autopilot's songs the room let play through.
	roomWeight = 0.5
)

// Input is what the room has been doing.
type Input struct {
	// History is the room's plays, newest first.
	History []store.ListHistoryRow
	// Upcoming are the songs playing and waiting.
	Upcoming []store.QueueItem
	// Mine are autopilot's recent songs, newest first, removed ones too.
	Mine []store.QueueItem
	// Present are the members in the room.
	Present []string
	Now     time.Time
}

// Taste is how the room feels about one artist.
type Taste struct {
	Artist musicgraph.ArtistRef
	// Weight is how much the room likes them, from 0 to 1 against the
	// artist it likes most.
	Weight float64
	// Plays is how many of their songs members chose or let play through.
	Plays int
	// ByMember is each member's share of the liking; "" is autopilot's
	// songs the room let play.
	ByMember map[string]float64
	// Seed is the newest member song of theirs the room liked, for the
	// credit on a song it leads to. Its ID is "" if only autopilot played
	// them.
	Seed store.QueueItem
}

// Fan is the member who likes the artist most, or "" if none does.
func (t *Taste) Fan() string {
	var who string
	best := 0.0
	for u, w := range t.ByMember {
		if u != "" && (w > best || (w == best && u < who)) {
			who, best = u, w
		}
	}
	return who
}

// Profile is the room's taste: the artists it likes, how much, and who
// likes them; the artists it turned away; and what it just heard.
type Profile struct {
	// Artists the room likes, by ArtistKey.
	Artists map[string]*Taste
	// Avoid are artists the room skipped more than it liked, by ArtistKey.
	Avoid map[string]bool
	// Recent are the artists of the room's last songs, newest first.
	Recent []string
	// RecentAlbums are the simplified albums of the room's last songs.
	RecentAlbums map[string]bool
	// Heard are the songs the room heard or has waiting, by SongKey: the
	// same song in another version counts as heard.
	Heard map[string]bool
	// RecentSongs are the members' songs the room liked lately, newest
	// first, for songs like them.
	RecentSongs []store.QueueItem
}

// ArtistKey is how artists are compared: the simplified name.
func ArtistKey(name string) string { return match.Simplify(name) }

// SongKey is how songs are compared: artist and title, without
// qualifiers, so a remaster or a live take of a song is the same song.
func SongKey(artist, title string) string {
	t, _ := match.Title(title)
	return ArtistKey(artist) + "\x00" + t
}

// TrackOf returns the track an item snapshotted when it was queued.
func TrackOf(it store.QueueItem) provider.Track {
	var t provider.Track
	_ = json.Unmarshal([]byte(it.Metadata), &t)
	return t
}

func artistOf(t provider.Track) string {
	if len(t.Artists) == 0 {
		return ""
	}
	return t.Artists[0].Name
}

// NewProfile reads the room's taste from what it's been doing.
func NewProfile(in Input) Profile {
	p := Profile{
		Artists: map[string]*Taste{}, Avoid: map[string]bool{},
		RecentAlbums: map[string]bool{}, Heard: map[string]bool{},
	}
	// likes[who][artist] are positive signals, decayed; net[artist] adds
	// the negative ones too.
	likes := map[string]map[string]float64{}
	net := map[string]float64{}
	names := map[string]string{}
	like := func(who, artist string, v float64) {
		if likes[who] == nil {
			likes[who] = map[string]float64{}
		}
		likes[who][artist] += v
	}
	seen := map[string]bool{}
	note := func(it store.QueueItem, v float64, at time.Time) {
		t := TrackOf(it)
		name := artistOf(t)
		a := ArtistKey(name)
		if a == "" {
			return
		}
		names[a] = cmp.Or(names[a], name)
		v *= decay(in.Now.Sub(at))
		net[a] += v
		if v <= 0 {
			return
		}
		who := it.AddedBy
		if it.IsAutopilot() {
			who = ""
		}
		like(who, a, v)
		taste := p.taste(a, name)
		if who != "" {
			taste.Plays++
			if taste.Seed.ID == "" {
				taste.Seed = it
			}
		}
	}

	// What's playing and waiting: the room's freshest intent.
	for _, it := range in.Upcoming {
		seen[it.ID] = true
		t := TrackOf(it)
		p.Heard[SongKey(artistOf(t), t.Title)] = true
		if it.IsAutopilot() {
			continue
		}
		note(it, queuedSignal, in.Now)
		if it.State == store.ItemPlaying {
			p.recent(t)
		}
	}
	for i, h := range in.History {
		it := h.QueueItem
		t := TrackOf(it)
		p.Heard[SongKey(artistOf(t), t.Title)] = true
		if seen[it.ID] || i >= profileReach {
			continue // playing now, and counted above
		}
		seen[it.ID] = true
		p.recent(t)
		if !h.PlayHistory.EndedAt.Valid {
			continue
		}
		at := h.PlayHistory.StartedAt
		switch reason := h.PlayHistory.EndReason.String; {
		case reason == store.EndFinished && it.IsAutopilot():
			note(it, autopilotFinished, at)
		case reason == store.EndFinished:
			note(it, finishedSignal, at)
			if len(p.RecentSongs) < recentReach {
				p.RecentSongs = append(p.RecentSongs, it)
			}
		case reason == store.EndSkipped || reason == store.EndRemoved:
			note(it, skippedSignal, at)
		}
	}
	for _, it := range in.Mine {
		t := TrackOf(it)
		p.Heard[SongKey(artistOf(t), t.Title)] = true
		if it.State == store.ItemRemoved && !seen[it.ID] {
			note(it, removedSignal, it.UpdatedAt)
		}
	}

	present := map[string]bool{}
	for _, u := range in.Present {
		present[u] = true
	}
	most := 0.0
	for who, as := range likes {
		total := 0.0
		for _, v := range as {
			total += v
		}
		w := awayWeight
		switch {
		case who == "":
			w = roomWeight
		case present[who]:
			w = presentWeight
		}
		for a, v := range as {
			share := w * v / total
			t := p.Artists[a]
			t.Weight += share
			t.ByMember[who] += share
			most = max(most, t.Weight)
		}
	}
	for a, t := range p.Artists {
		if net[a] < 0 {
			p.Avoid[a] = true
			delete(p.Artists, a)
			continue
		}
		t.Weight /= most
	}
	for a, v := range net {
		if v < 0 {
			p.Avoid[a] = true
		}
	}
	return p
}

func (p *Profile) taste(key, name string) *Taste {
	t, ok := p.Artists[key]
	if !ok {
		t = &Taste{Artist: musicgraph.ArtistRef{Name: name}, ByMember: map[string]float64{}}
		p.Artists[key] = t
	}
	return t
}

// recent notes a song among the room's last few, newest first.
func (p *Profile) recent(t provider.Track) {
	if len(p.Recent) >= recentReach {
		return
	}
	p.Recent = append(p.Recent, ArtistKey(artistOf(t)))
	if len(p.Recent) <= 5 && t.Album.Title != "" {
		p.RecentAlbums[match.Simplify(t.Album.Title)] = true
	}
}

// Top returns the artists the room likes most, most first, up to n.
func (p Profile) Top(n int) []*Taste {
	out := make([]*Taste, 0, len(p.Artists))
	for _, t := range p.Artists {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b *Taste) int {
		if c := cmp.Compare(b.Weight, a.Weight); c != 0 {
			return c
		}
		return strings.Compare(a.Artist.Name, b.Artist.Name)
	})
	return out[:min(len(out), n)]
}

// RecentlyPlayed reports how many songs ago the room last heard an
// artist, and whether it did among its last few.
func (p Profile) RecentlyPlayed(artistKey string) (int, bool) {
	i := slices.Index(p.Recent, artistKey)
	return i, i >= 0
}

func decay(age time.Duration) float64 {
	if age <= 0 {
		return 1
	}
	return math.Pow(0.5, float64(age)/float64(halfLife))
}
