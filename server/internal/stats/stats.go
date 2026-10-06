// SPDX-License-Identifier: AGPL-3.0-only

// Package stats sums up what a room played: who played what, top tracks
// and artists, and listening sessions (a night's hangout). Like fairness,
// it's pure: plays in, numbers out, no I/O and no clock.
package stats

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// TopN is how many top tracks and artists a summary lists.
const TopN = 5

// SessionGap is how long a room must go quiet for the next song to start
// a new session.
const SessionGap = 2 * time.Hour

// TrackCount is a song and how many times it played to the end. Item is
// its most recent play, for showing it and queueing it again.
type TrackCount struct {
	Item  store.QueueItem
	Plays int
}

// ArtistCount is an artist and how many of their songs played to the end.
type ArtistCount struct {
	Name  string
	Plays int
}

// Totals are the numbers for a room, or for one person's songs in it.
type Totals struct {
	// Plays counts songs that played, to the end or until skipped. Songs
	// that failed or were removed don't count anywhere.
	Plays   int
	Skipped int
	// Listening is how long songs played for.
	Listening  time.Duration
	TopTracks  []TrackCount
	TopArtists []ArtistCount
}

// Person is one person's totals: their songs, whoever skipped them.
type Person struct {
	UserID string
	Totals
}

// Summary sums up a room's plays.
type Summary struct {
	Totals
	// People are everyone whose songs played, most plays first.
	People []Person
	// First and Last are the first and last songs that played, if any.
	First, Last *rooms.Played
}

// Summarize sums up plays, in the order they started.
func Summarize(plays []rooms.Played) Summary {
	var s Summary
	room := newTally()
	people := map[string]*tally{}
	for i := range plays {
		p := &plays[i]
		if p.EndReason != store.EndFinished && p.EndReason != store.EndSkipped {
			continue
		}
		if s.First == nil {
			s.First = p
		}
		s.Last = p
		t := people[p.Item.AddedBy]
		if t == nil {
			t = newTally()
			people[p.Item.AddedBy] = t
		}
		var track provider.Track
		_ = json.Unmarshal([]byte(p.Item.Metadata), &track)
		room.add(p, track)
		t.add(p, track)
	}
	s.Totals = room.totals()
	for user, t := range people {
		s.People = append(s.People, Person{UserID: user, Totals: t.totals()})
	}
	slices.SortFunc(s.People, func(a, b Person) int {
		return cmp.Or(cmp.Compare(b.Plays, a.Plays), cmp.Compare(b.Listening, a.Listening), cmp.Compare(a.UserID, b.UserID))
	})
	return s
}

type tally struct {
	plays, skipped int
	listening      time.Duration
	tracks         map[string]*TrackCount
	artists        map[string]*ArtistCount
}

func newTally() *tally {
	return &tally{tracks: map[string]*TrackCount{}, artists: map[string]*ArtistCount{}}
}

func (t *tally) add(p *rooms.Played, track provider.Track) {
	t.plays++
	d := p.EndedAt.Sub(p.StartedAt)
	if track.Duration > 0 {
		d = min(d, track.Duration)
	}
	t.listening += max(d, 0)
	if p.EndReason == store.EndSkipped {
		t.skipped++
		return
	}
	// Tops count songs that played to the end: a skip isn't a vote for it.
	key := p.Item.Provider + "\x00" + p.Item.TrackID
	if track.ISRC != "" {
		key = "isrc\x00" + track.ISRC // the same song, whichever service
	}
	if tc := t.tracks[key]; tc != nil {
		tc.Plays++
		tc.Item = p.Item
	} else {
		t.tracks[key] = &TrackCount{Item: p.Item, Plays: 1}
	}
	for _, a := range track.Artists {
		key := strings.ToLower(strings.TrimSpace(a.Name))
		if key == "" {
			continue
		}
		if ac := t.artists[key]; ac != nil {
			ac.Plays++
		} else {
			t.artists[key] = &ArtistCount{Name: strings.TrimSpace(a.Name), Plays: 1}
		}
	}
}

func (t *tally) totals() Totals {
	out := Totals{Plays: t.plays, Skipped: t.skipped, Listening: t.listening, TopTracks: []TrackCount{}, TopArtists: []ArtistCount{}}
	for _, tc := range t.tracks {
		out.TopTracks = append(out.TopTracks, *tc)
	}
	// Ties go to the song that played most recently, then by ID, so the
	// order is stable.
	slices.SortFunc(out.TopTracks, func(a, b TrackCount) int {
		return cmp.Or(cmp.Compare(b.Plays, a.Plays), b.Item.UpdatedAt.Compare(a.Item.UpdatedAt), cmp.Compare(a.Item.ID, b.Item.ID))
	})
	for _, ac := range t.artists {
		out.TopArtists = append(out.TopArtists, *ac)
	}
	slices.SortFunc(out.TopArtists, func(a, b ArtistCount) int {
		return cmp.Or(cmp.Compare(b.Plays, a.Plays), cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)))
	})
	out.TopTracks = out.TopTracks[:min(len(out.TopTracks), TopN)]
	out.TopArtists = out.TopArtists[:min(len(out.TopArtists), TopN)]
	return out
}

// Span is one play's time and whose song it was.
type Span struct {
	Start, End time.Time
	User       string
}

// Session is a stretch of listening with no gap of SessionGap or more.
type Session struct {
	Start, End time.Time
	Plays      int
	// People are whose songs played, most first.
	People []string
}

// Sessions groups plays (oldest first) into sessions, newest first.
func Sessions(spans []Span) []Session {
	var out []Session
	var counts map[string]int
	flush := func() {
		if len(out) == 0 {
			return
		}
		s := &out[len(out)-1]
		for u := range counts {
			s.People = append(s.People, u)
		}
		slices.SortFunc(s.People, func(a, b string) int { return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b)) })
	}
	for _, sp := range spans {
		if len(out) == 0 || sp.Start.Sub(out[len(out)-1].End) >= SessionGap {
			flush()
			out = append(out, Session{Start: sp.Start, End: sp.End})
			counts = map[string]int{}
		}
		s := &out[len(out)-1]
		s.Plays++
		if sp.End.After(s.End) {
			s.End = sp.End
		}
		counts[sp.User]++
	}
	flush()
	slices.Reverse(out)
	return out
}
