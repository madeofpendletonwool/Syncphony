// SPDX-License-Identifier: AGPL-3.0-only

package nights

import (
	"database/sql"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func play(item string, start time.Time) store.PlayHistory {
	return store.PlayHistory{QueueItemID: item, StartedAt: start, EndedAt: sql.NullTime{Time: start.Add(3 * time.Minute), Valid: true}}
}

func TestTonight(t *testing.T) {
	at := time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC)
	plays := []store.PlayHistory{
		play("yesterday", at.Add(-24*time.Hour)),
		play("a", at),
		play("b", at.Add(time.Hour)), // an hour's lull isn't the end of the night
		play("c", at.Add(time.Hour+5*time.Minute)),
	}
	got := tonight(plays)
	if len(got) != 3 || got[0].QueueItemID != "a" {
		t.Fatalf("tonight: %+v", got)
	}
	if got := tonight(plays[1:2]); len(got) != 1 {
		t.Fatalf("one song: %+v", got)
	}
}

func TestCrown(t *testing.T) {
	at := time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC)
	plays := []store.PlayHistory{play("a", at), play("b", at.Add(5*time.Minute)), play("c", at.Add(10*time.Minute))}
	for _, tc := range []struct {
		name   string
		counts []store.HeartCountsSinceRow
		want   string
		hearts int64
	}{
		{"nothing hearted", nil, "", 0},
		{"most hearts", []store.HeartCountsSinceRow{{QueueItemID: "a", Hearts: 1}, {QueueItemID: "c", Hearts: 3}}, "c", 3},
		{"a tie goes to the earlier song", []store.HeartCountsSinceRow{{QueueItemID: "c", Hearts: 2}, {QueueItemID: "b", Hearts: 2}}, "b", 2},
		{"songs from another night don't count", []store.HeartCountsSinceRow{{QueueItemID: "old", Hearts: 9}, {QueueItemID: "a", Hearts: 1}}, "a", 1},
	} {
		got, hearts := crown(plays, tc.counts)
		if got != tc.want || hearts != tc.hearts {
			t.Errorf("%s: got %q with %d, want %q with %d", tc.name, got, hearts, tc.want, tc.hearts)
		}
	}
}
