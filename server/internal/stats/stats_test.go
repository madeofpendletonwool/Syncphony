// SPDX-License-Identifier: AGPL-3.0-only

package stats_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/stats"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

var t0 = time.Date(2026, 10, 5, 20, 0, 0, 0, time.UTC)

// play makes a play of a 3-minute song by artists, queued by user,
// starting at minute m and lasting d minutes.
func play(n int, user, providerID, trackID, isrc string, m, d int, reason string, artists ...string) rooms.Played {
	t := provider.Track{Title: trackID, Duration: 3 * time.Minute, ISRC: isrc}
	for _, a := range artists {
		t.Artists = append(t.Artists, provider.ArtistCredit{Name: a})
	}
	meta, _ := json.Marshal(t)
	start := t0.Add(time.Duration(m) * time.Minute)
	return rooms.Played{
		Item: store.QueueItem{
			ID: fmt.Sprint("item", n), AddedBy: user, Provider: providerID, TrackID: trackID, Metadata: string(meta),
			UpdatedAt: start.Add(time.Duration(d) * time.Minute),
		},
		StartedAt: start, EndedAt: start.Add(time.Duration(d) * time.Minute), EndReason: reason,
	}
}

func tracks(tcs []stats.TrackCount) string {
	var s []string
	for _, tc := range tcs {
		s = append(s, fmt.Sprintf("%s×%d", tc.Item.TrackID, tc.Plays))
	}
	return strings.Join(s, " ")
}

func artists(acs []stats.ArtistCount) string {
	var s []string
	for _, ac := range acs {
		s = append(s, fmt.Sprintf("%s×%d", ac.Name, ac.Plays))
	}
	return strings.Join(s, " ")
}

func TestSummarize(t *testing.T) {
	plays := []rooms.Played{
		play(1, "ann", "navidrome", "song-a", "ISRC1", 0, 3, store.EndFinished, "Bowie"),
		play(2, "bob", "spotify", "sp-a", "ISRC1", 3, 3, store.EndFinished, "bowie", "Queen"),
		play(3, "ann", "navidrome", "song-b", "", 6, 1, store.EndSkipped, "Queen"),
		play(4, "bob", "spotify", "sp-c", "", 7, 0, store.EndError, "Nobody"),
		// Ran long (the speaker never said it ended): counts its length.
		play(5, "ann", "navidrome", "song-c", "", 8, 10, store.EndFinished, "Abba"),
	}
	s := stats.Summarize(plays)
	if s.Plays != 4 || s.Skipped != 1 || s.Listening != 10*time.Minute {
		t.Errorf("totals: %d plays, %d skipped, %v", s.Plays, s.Skipped, s.Listening)
	}
	// The same song on two services is one song; skips and failures don't count.
	if got, want := tracks(s.TopTracks), "sp-a×2 song-c×1"; got != want {
		t.Errorf("top tracks %q, want %q", got, want)
	}
	if got, want := artists(s.TopArtists), "Bowie×2 Abba×1 Queen×1"; got != want {
		t.Errorf("top artists %q, want %q", got, want)
	}
	if len(s.People) != 2 || s.People[0].UserID != "ann" || s.People[0].Plays != 3 || s.People[0].Skipped != 1 || s.People[1].Plays != 1 {
		t.Fatalf("people: %+v", s.People)
	}
	if got := tracks(s.People[0].TopTracks); got != "song-c×1 song-a×1" {
		t.Errorf("ann's top tracks %q", got)
	}
	if s.First.Item.ID != "item1" || s.Last.Item.ID != "item5" {
		t.Errorf("first %s, last %s", s.First.Item.ID, s.Last.Item.ID)
	}

	empty := stats.Summarize(nil)
	if empty.Plays != 0 || empty.First != nil || empty.TopTracks == nil || len(empty.People) != 0 {
		t.Errorf("empty: %+v", empty)
	}
}

func TestTopN(t *testing.T) {
	var plays []rooms.Played
	for i := range 8 {
		plays = append(plays, play(i, "ann", "p", fmt.Sprint("t", i), "", i*3, 3, store.EndFinished, fmt.Sprint("artist", i)))
	}
	s := stats.Summarize(plays)
	if len(s.TopTracks) != stats.TopN || len(s.TopArtists) != stats.TopN {
		t.Errorf("tops: %d tracks, %d artists", len(s.TopTracks), len(s.TopArtists))
	}
	// Ties go to what played most recently.
	if s.TopTracks[0].Item.TrackID != "t7" {
		t.Errorf("top track %s", s.TopTracks[0].Item.TrackID)
	}
}

func TestSessions(t *testing.T) {
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	spans := []stats.Span{
		{Start: at(0), End: at(3), User: "ann"},
		{Start: at(3), End: at(6), User: "bob"},
		{Start: at(6), End: at(9), User: "bob"},
		// 1h59m later: the same session.
		{Start: at(9 + 119), End: at(131), User: "cat"},
		// Two hours of quiet: a new one.
		{Start: at(131 + 120), End: at(254), User: "ann"},
	}
	got := stats.Sessions(spans)
	if len(got) != 2 {
		t.Fatalf("sessions: %+v", got)
	}
	if s := got[0]; !s.Start.Equal(at(251)) || !s.End.Equal(at(254)) || s.Plays != 1 || strings.Join(s.People, ",") != "ann" {
		t.Errorf("newest: %+v", s)
	}
	if s := got[1]; !s.Start.Equal(at(0)) || !s.End.Equal(at(131)) || s.Plays != 4 || strings.Join(s.People, ",") != "bob,ann,cat" {
		t.Errorf("oldest: %+v", s)
	}
	if len(stats.Sessions(nil)) != 0 {
		t.Error("sessions from nothing")
	}
}

func TestArtistTop(t *testing.T) {
	plays := []rooms.Played{
		play(1, "ann", "navidrome", "song-a", "ISRC1", 0, 3, store.EndFinished, "Bowie"),
		play(2, "bob", "spotify", "sp-a", "ISRC1", 3, 3, store.EndFinished, "Queen", "David Bowie"),
		play(3, "ann", "navidrome", "song-b", "", 6, 3, store.EndFinished, "bowie"),
		play(4, "ann", "navidrome", "song-a", "ISRC1", 9, 3, store.EndFinished, "Bowie"),
		play(5, "ann", "navidrome", "song-c", "", 12, 1, store.EndSkipped, "Bowie"),
		play(6, "ann", "navidrome", "song-d", "", 15, 3, store.EndFinished, "Abba"),
	}
	// Spelled any way, credited anywhere; skips don't count.
	if got, want := tracks(stats.ArtistTop(plays, "BOWIE", 10)), "song-a×2 song-b×1"; got != want {
		t.Errorf("Bowie's top %q, want %q", got, want)
	}
	if got := tracks(stats.ArtistTop(plays, "Bowie", 1)); got != "song-a×2" {
		t.Errorf("top 1: %q", got)
	}
	if got := stats.ArtistTop(plays, "Nobody", 10); got == nil || len(got) != 0 {
		t.Errorf("nobody: %v", got)
	}
}
