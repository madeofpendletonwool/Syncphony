// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/clips"
	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/transcode"
)

// fakeFacts knows every song came out in 1977.
type fakeFacts struct{}

func (fakeFacts) Song(context.Context, store.QueueItem, bool) (quiz.Facts, error) {
	return quiz.Facts{Song: quiz.Song{Title: "Song", Artist: "Someone"}, Year: 1977}, nil
}

func (fakeFacts) Pool(context.Context, string, store.QueueItem, quiz.Facts) quiz.Pool {
	return quiz.Pool{}
}

func TestGamesAPI(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Party"}).decode(t, &room)
	base := "/rooms/" + room.Id
	if g := room.Games; g.Level != "off" || g.Scores != "off" || len(g.Enabled) != 0 || room.Permissions.StartRounds != "owner" {
		t.Fatalf("a new room's games: %+v", g)
	}
	alice.want(http.StatusNoContent, "GET", base+"/games/round", nil)
	if r := alice.do("POST", base+"/games/rounds", nil); r.status != http.StatusConflict || r.code() != "games_off" {
		t.Fatalf("start with games off: %d %s", r.status, r.body)
	}

	// Rounds only when someone starts one.
	level, zero := httpapi.RoomGamesChangeLevel("rounds"), 0
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Games: &httpapi.RoomGamesChange{Level: &level, Frequency: &zero}}).decode(t, &room)
	if g := room.Games; g.Level != "rounds" || g.Frequency != 0 || g.Scores != "board" || !g.Enabled["year"] || len(g.Enabled) != 4 {
		t.Fatalf("rounds: %+v", g)
	}
	// Just a level: the rest is that level's.
	night := httpapi.RoomGamesChangeLevel("gamenight")
	var other httpapi.Room
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Games: &httpapi.RoomGamesChange{Level: &night}}).decode(t, &other)
	if g := other.Games; g.Frequency != 2 || g.BreaksPerHour != 4 || !g.Enabled["tune"] {
		t.Fatalf("game night: %+v", g)
	}
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Games: &httpapi.RoomGamesChange{Level: &level, Frequency: &zero}})
	if r := alice.do("PATCH", base, map[string]any{"games": map[string]any{"level": "party"}}); r.status != http.StatusBadRequest {
		t.Fatalf("a bad level: %d %s", r.status, r.body)
	}

	if r := bob.do("POST", base+"/games/rounds", nil); r.status != http.StatusForbidden {
		t.Fatalf("bob starts a round: %d %s", r.status, r.body)
	}
	if r := alice.do("POST", base+"/games/rounds", nil); r.status != http.StatusConflict || r.code() != "nothing_playing" {
		t.Fatalf("nothing playing: %d %s", r.status, r.body)
	}
	aliceLink := linkFake(t, alice)
	alice.want(http.StatusOK, "POST", base+"/queue", addReq(aliceLink, "t01"))
	alice.want(http.StatusOK, "PUT", base+"/player", httpapi.ClaimPlayerRequest{DeviceId: "phone", Name: "Alice's phone"})

	var rd httpapi.GameRound
	alice.want(http.StatusCreated, "POST", base+"/games/rounds", httpapi.StartGameRoundRequest{}).decode(t, &rd)
	if rd.State != "announce" || rd.Kind != "year" || rd.Correct != nil || rd.Results != nil || rd.StartedBy == nil {
		t.Fatalf("started: %+v", rd)
	}
	if r := alice.do("POST", base+"/games/rounds", nil); r.status != http.StatusConflict || r.code() != "round_running" {
		t.Fatalf("a second round: %d %s", r.status, r.body)
	}
	// The year is in the liner notes, so they wait for the reveal.
	if r := bob.do("GET", base+"/queue/"+rd.ItemId+"/liner-notes", nil); r.status != http.StatusConflict || r.code() != "hidden_for_round" {
		t.Fatalf("liner notes during the round: %d %s", r.status, r.body)
	}
	year := 1977
	if r := bob.do("POST", base+"/games/rounds/"+rd.Id+"/answers", httpapi.GameAnswerRequest{Number: &year}); r.status != http.StatusConflict || r.code() != "round_closed" {
		t.Fatalf("an answer before it opened: %d %s", r.status, r.body)
	}
	time.Sleep(time.Until(rd.OpensAt) + 20*time.Millisecond)
	bob.want(http.StatusOK, "POST", base+"/games/rounds/"+rd.Id+"/answers", httpapi.GameAnswerRequest{Number: &year})
	time.Sleep(time.Until(rd.ClosesAt) + 100*time.Millisecond)

	alice.want(http.StatusOK, "GET", base+"/games/round", nil).decode(t, &rd)
	if rd.State != "reveal" || rd.Correct == nil || *rd.Correct != "1977" || rd.Results == nil || len(*rd.Results) != 1 || !(*rd.Results)[0].Correct {
		t.Fatalf("revealed: %+v", rd)
	}
	var scores httpapi.GameScores
	for range 50 {
		alice.want(http.StatusOK, "GET", base+"/games/scores", nil).decode(t, &scores)
		if len(scores.Players) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if scores.Mode != "board" || len(scores.Players) != 1 || scores.Players[0].Points < 500 {
		t.Fatalf("scores: %+v", scores)
	}
}

func TestGameClipsAPI(t *testing.T) {
	e := newEnv(t)
	if e.api.Clips == nil {
		t.Skip("ffmpeg not installed")
	}
	alice := e.admin()
	bob := e.member(alice, "bob")
	var room, other httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Party"}).decode(t, &room)
	bob.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Bob's"}).decode(t, &other)
	base := "/rooms/" + room.Id

	// Name that tune's setup round-trips, defaults filled in.
	night := httpapi.RoomGamesChangeLevel("gamenight")
	from, clip, typed := httpapi.RoomGamesTuneChangeFrom("favorites"), httpapi.RoomGamesTuneChangeClip("outro"), true
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Games: &httpapi.RoomGamesChange{
		Level: &night, Tune: &httpapi.RoomGamesTuneChange{From: &from, Clip: &clip, Typed: &typed},
	}}).decode(t, &room)
	if tune := room.Games.Tune; tune.From != "favorites" || tune.Clip != "outro" || !tune.Typed {
		t.Fatalf("tune settings: %+v", tune)
	}
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Games: &httpapi.RoomGamesChange{Level: &night}}).decode(t, &room)
	if tune := room.Games.Tune; tune.From != "tonight" || tune.Clip != "chorus" || tune.Typed {
		t.Fatalf("default tune settings: %+v", tune)
	}
	if r := alice.do("POST", base+"/games/rounds", map[string]any{"kind": "year", "set": 5}); r.status != http.StatusBadRequest {
		t.Fatalf("a set of years: %d %s", r.status, r.body)
	}

	link := linkFake(t, alice)
	ids := e.api.Clips.Cut(room.Id, clips.Song{LinkID: link, TrackID: "t01"}, []transcode.Cut{{Start: 5 * time.Second, Length: 2 * time.Second}})
	r := alice.do("GET", base+"/games/clips/"+ids[0], nil)
	if r.status != http.StatusOK || r.header.Get("Content-Type") != "audio/mpeg" || len(r.body) < 1000 || !strings.HasPrefix(r.header.Get("Cache-Control"), "private") {
		t.Fatalf("the clip: %d %v, %d bytes", r.status, r.header, len(r.body))
	}
	// Another room's clips aren't there.
	if r := bob.do("GET", "/rooms/"+other.Id+"/games/clips/"+ids[0], nil); r.status != http.StatusNotFound {
		t.Fatalf("from another room: %d %s", r.status, r.body)
	}
	if r := alice.do("GET", base+"/games/clips/nope", nil); r.status != http.StatusNotFound {
		t.Fatalf("no such clip: %d %s", r.status, r.body)
	}
}
