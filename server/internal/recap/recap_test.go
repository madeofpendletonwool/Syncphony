// SPDX-License-Identifier: AGPL-3.0-only

package recap_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/recap"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

var t0 = time.Date(2026, 10, 9, 20, 0, 0, 0, time.UTC)

// play makes the nth play: user's song trackID by artist, from year.
func play(n int, user, trackID, artist string, year int, reason string) rooms.Played {
	meta, _ := json.Marshal(provider.Track{Title: trackID, Duration: 3 * time.Minute, Year: year, Artists: []provider.ArtistCredit{{Name: artist}}})
	start := t0.Add(time.Duration(n) * 3 * time.Minute)
	return rooms.Played{
		Item:      store.QueueItem{ID: fmt.Sprint("item", n), AddedBy: user, Provider: "fake", TrackID: trackID, Metadata: string(meta)},
		StartedAt: start, EndedAt: start.Add(3 * time.Minute), EndReason: reason,
	}
}

type facts map[string][]recap.Tag

func (f facts) Tags(_ context.Context, artist string) []recap.Tag { return f[artist] }
func (facts) Year(context.Context, provider.Track) int            { return 1977 }

func TestMake(t *testing.T) {
	plays := []rooms.Played{
		play(1, "ann", "a1", "Bowie", 1972, store.EndFinished),
		play(2, "bob", "b1", "Queen", 1975, store.EndSkipped),
		play(3, "ann", "a2", "Queen", 1980, store.EndFinished),
		play(4, "bob", "b1", "Queen", 1975, store.EndSkipped),
		play(5, "ann", "a3", "Bowie", 0, store.EndFinished),
		play(6, "bob", "b2", "Bowie", 1983, store.EndFinished),
		play(7, "ann", "a4", "Abba", 1976, store.EndError),
		play(8, "ann", "a5", "Abba", 1976, store.EndFinished),
	}
	// Autopilot's song is nobody's.
	pilot := play(9, "bob", "p1", "Bowie", 1972, store.EndFinished)
	pilot.Item.Autopilot = sql.NullString{String: "{}", Valid: true}
	plays = append(plays, pilot)

	f := facts{"Bowie": {{"glam rock", 1}, {"art rock", 0.5}}, "Queen": {{"rock", 1}}}
	r := recap.Make(t.Context(), plays, map[string]int{"item6": 3, "item1": 1, "item3": 1}, f)

	if r.TopAdder == nil || r.TopAdder.UserID != "ann" || r.TopAdder.N != 4 {
		t.Errorf("top adder %+v", r.TopAdder)
	}
	if r.MostHearted == nil || r.MostHearted.UserID != "bob" || r.MostHearted.N != 3 {
		t.Errorf("most hearted %+v", r.MostHearted)
	}
	if r.MostSkipped == nil || r.MostSkipped.Item.TrackID != "b1" || r.MostSkipped.N != 2 {
		t.Errorf("most skipped %+v", r.MostSkipped)
	}
	// Ann's songs that played all played through; the one that failed
	// doesn't break the run.
	if r.Streak == nil || r.Streak.UserID != "ann" || r.Streak.N != 4 {
		t.Errorf("streak %+v", r.Streak)
	}
	if len(r.Overlaps) != 1 || r.Overlaps[0].UserIDs != [2]string{"ann", "bob"} || len(r.Overlaps[0].Artists) != 2 {
		t.Errorf("overlaps %+v", r.Overlaps)
	}
	if len(r.Genres) == 0 || r.Genres[0].Name != "rock" && r.Genres[0].Name != "glam rock" {
		t.Errorf("genres %+v", r.Genres)
	}
	// a3 has no year of its own, so Facts says 1977.
	want := []recap.Share{{"1970s", 6}, {"1980s", 2}}
	if fmt.Sprint(r.Decades) != fmt.Sprint(want) {
		t.Errorf("decades %+v, want %+v", r.Decades, want)
	}
}

func TestMakeQuietNight(t *testing.T) {
	r := recap.Make(t.Context(), []rooms.Played{play(1, "ann", "a1", "Bowie", 0, store.EndSkipped)}, nil, nil)
	if r.MostHearted != nil || r.Streak != nil || len(r.Genres) != 0 || len(r.Decades) != 0 || len(r.Overlaps) != 0 {
		t.Errorf("recap %+v", r)
	}
	if r.MostSkipped == nil {
		t.Error("no most skipped")
	}
}
