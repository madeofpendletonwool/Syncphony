// SPDX-License-Identifier: AGPL-3.0-only

package awards_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/awards"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

var t0 = time.Date(2026, 10, 8, 20, 0, 0, 0, time.UTC)

// night builds plays four minutes apart, each queued a minute before it.
func night(ps ...awards.Play) []awards.Play {
	for i := range ps {
		p := &ps[i]
		p.ItemID = fmt.Sprint("item", i)
		p.StartedAt = t0.Add(time.Duration(i) * 4 * time.Minute)
		if p.EndedAt.IsZero() {
			p.EndedAt = p.StartedAt.Add(4 * time.Minute)
		}
		if p.AddedAt.IsZero() {
			p.AddedAt = p.StartedAt.Add(-time.Minute)
		}
		if p.EndReason == "" {
			p.EndReason = store.EndFinished
		}
		if p.Song == "" {
			p.Song = p.Title
		}
	}
	return ps
}

func find(as []awards.Award, kind string) *awards.Award {
	for i := range as {
		if as[i].Kind == kind {
			return &as[i]
		}
	}
	return nil
}

func TestPick(t *testing.T) {
	plays := night(
		awards.Play{UserID: "ann", Title: "Opener", Artists: []string{"Bowie"}, BPM: 128, Energy: 0.8, Rank: 0.9, Year: 1977, Hearts: 3},
		awards.Play{UserID: "bob", Title: "Slow One", Artists: []string{"Sade"}, BPM: 70, Energy: 0.3, Rank: 0.05, Year: 1984},
		awards.Play{UserID: "cat", Title: "Banger", Artists: []string{"Daft Punk"}, BPM: 124, Energy: 0.9, Rank: 0.7, Year: 2001, Hearts: 1, Samples: 2},
		awards.Play{UserID: "ann", Title: "Newer", Artists: []string{"Bowie"}, BPM: 120, Energy: 0.7, Rank: 0.6, Year: 2016},
	)
	as := awards.Pick(plays, []awards.Score{{"bob", 2400}, {"cat", 900}})
	if len(as) > awards.Max {
		t.Fatalf("%d awards", len(as))
	}
	won := map[string]int{}
	for _, a := range as {
		won[a.UserID]++
		if a.Title == "" || a.Reason == "" {
			t.Errorf("%+v", a)
		}
	}
	for who, n := range won {
		if n > awards.MaxPerPerson {
			t.Errorf("%s won %d", who, n)
		}
	}
	if a := find(as, awards.TriviaChamp); a == nil || a.UserID != "bob" || a.Reason != "2,400 points at the games" {
		t.Errorf("trivia champ: %+v", a)
	}
	if a := find(as, awards.DeepestCut); a == nil || a.UserID != "bob" || a.ItemID != "item1" {
		t.Errorf("deepest cut: %+v", a)
	}
	if a := find(as, awards.DanceFloorMVP); a == nil || a.UserID != "ann" || a.ItemID != "item0" {
		t.Errorf("dance floor MVP: %+v", a)
	}
	// Bob already has two, so the whiplash into his slow song isn't his.
	if a := find(as, awards.TempoWhiplash); a != nil && a.UserID == "bob" {
		t.Errorf("bob won a third: %+v", a)
	}
}

func TestTempoWhiplash(t *testing.T) {
	as := awards.Pick(night(
		awards.Play{UserID: "ann", Title: "Fast", BPM: 128},
		awards.Play{UserID: "bob", Title: "Slow", BPM: 70},
		awards.Play{UserID: "ann", Title: "Mid", BPM: 100},
	), nil)
	if a := find(as, awards.TempoWhiplash); a == nil || a.UserID != "bob" || a.Reason != "Dropped from 128 to 70 BPM" {
		t.Errorf("%+v", a)
	}
}

func TestNoClearWinner(t *testing.T) {
	as := awards.Pick(night(
		awards.Play{UserID: "ann", Title: "A", Samples: 1, Year: 1970},
		awards.Play{UserID: "bob", Title: "B", Samples: 1, Year: 1970},
		awards.Play{UserID: "ann", Title: "C", Year: 1990},
		awards.Play{UserID: "bob", Title: "D", Year: 1990},
	), []awards.Score{{"ann", 100}, {"bob", 100}})
	for _, k := range []string{awards.SampleSnitch, awards.TimeTraveler, awards.TriviaChamp} {
		if a := find(as, k); a != nil {
			t.Errorf("a tie gave %+v", a)
		}
	}
}

func TestComebackAndTrendsetter(t *testing.T) {
	plays := night(
		awards.Play{UserID: "ann", Title: "Hit", Artists: []string{"Robyn"}},
		awards.Play{UserID: "bob", Title: "Another Robyn", Artists: []string{"Robyn"}},
		awards.Play{UserID: "cat", Title: "More Robyn", Artists: []string{"robyn"}},
		awards.Play{UserID: "dan", Title: "Hit", Artists: []string{"Robyn"}},
	)
	as := awards.Pick(plays, nil)
	if a := find(as, awards.Trendsetter); a == nil || a.UserID != "ann" || !strings.Contains(a.Reason, "3 people") {
		t.Errorf("trendsetter: %+v", a)
	}
	if a := find(as, awards.Comeback); a == nil || a.UserID != "dan" || a.ItemID != "item3" {
		t.Errorf("comeback: %+v", a)
	}
}

func TestAutopilotWinsNothing(t *testing.T) {
	as := awards.Pick(night(
		awards.Play{Title: "DJ pick", BPM: 170, Rank: 0.01},
		awards.Play{UserID: "ann", Title: "Mine", BPM: 80, Rank: 0.5},
		awards.Play{Title: "DJ pick 2", BPM: 170, Rank: 0.02},
	), nil)
	for _, a := range as {
		if a.UserID == "" {
			t.Errorf("autopilot won %+v", a)
		}
	}
}

func TestVibeKillerSkips(t *testing.T) {
	ps := night(
		awards.Play{UserID: "ann", Title: "A"},
		awards.Play{UserID: "bob", Title: "B"},
		awards.Play{UserID: "bob", Title: "C"},
		awards.Play{UserID: "ann", Title: "D"},
	)
	for _, i := range []int{1, 2} {
		ps[i].EndReason = store.EndSkipped
		ps[i].EndedAt = ps[i].StartedAt.Add(10 * time.Second)
	}
	if a := find(awards.Pick(ps, nil), awards.VibeKiller); a == nil || a.UserID != "bob" {
		t.Errorf("%+v", a)
	}
}

func TestNothingPlayed(t *testing.T) {
	if as := awards.Pick(nil, nil); len(as) != 0 {
		t.Errorf("%+v", as)
	}
}
