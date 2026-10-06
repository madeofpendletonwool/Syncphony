// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// play records an item playing in a room from start for a minute, as the
// playback engine would.
func (e *env) play(roomID, itemID string, start time.Time) {
	e.t.Helper()
	ctx := e.t.Context()
	if _, err := e.db.StartPlay(ctx, store.StartPlayParams{ID: store.NewID(), RoomID: roomID, QueueItemID: itemID, StartedAt: start}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.db.EndOpenPlays(ctx, store.EndOpenPlaysParams{
		EndedAt: sql.NullTime{Time: start.Add(time.Minute), Valid: true}, EndReason: sql.NullString{String: store.EndFinished, Valid: true}, RoomID: roomID,
	}); err != nil {
		e.t.Fatal(err)
	}
}

func TestSongOfTheNight(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	carol := e.member(alice, "carol")
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	base := "/rooms/" + room.Id
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", base+"/queue", addReq(linkFake(t, alice), "t01", "t02"))
	bob.want(http.StatusOK, "POST", base+"/queue", addReq(linkFake(t, bob), "t03", "t04")).decode(t, &snap)
	owner := map[string]string{}
	for _, it := range snap.Items {
		owner[it.Id] = it.AddedBy
	}
	alicesFirst, bobsFirst, alicesSecond, unplayed := snap.UpNext[0], snap.UpNext[1], snap.UpNext[2], snap.UpNext[3]
	if owner[alicesFirst] != me(t, alice).Id || owner[bobsFirst] != me(t, bob).Id {
		t.Fatalf("order: %v", snap.UpNext)
	}

	ws, _, err := alice.dialRoom(room.Id, "")
	if err != nil {
		t.Fatal(err)
	}
	ws.expect("hello", nil)

	// Tonight: three songs.
	night := e.clock().Add(-time.Hour)
	for i, id := range []string{alicesFirst, bobsFirst, alicesSecond} {
		e.play(room.Id, id, night.Add(time.Duration(i)*5*time.Minute))
	}
	heart := func(c *client, item string, status int) httpapi.Hearts {
		t.Helper()
		var h httpapi.Hearts
		r := c.want(status, "PUT", base+"/queue/"+item+"/hearts", nil)
		if status == http.StatusOK {
			r.decode(t, &h)
		}
		return h
	}

	// Hearts are for other people's songs, once they've played.
	heart(alice, alicesFirst, http.StatusBadRequest)
	if r := carol.do("PUT", base+"/queue/"+unplayed+"/hearts", nil); r.status != http.StatusConflict || r.code() != "not_tonight" {
		t.Fatalf("hearting a song that hasn't played: %d %s", r.status, r.body)
	}
	heart(bob, alicesFirst, http.StatusOK)
	h := heart(carol, alicesFirst, http.StatusOK)
	if len(h.UserIds) != 2 {
		t.Fatalf("hearts: %+v", h)
	}
	heart(carol, alicesFirst, http.StatusOK) // again: still one heart from carol
	var ev httpapi.Hearts
	ws.await("hearts.updated", &ev) // bob's
	ws.await("hearts.updated", &ev) // carol's
	if ev.ItemId != alicesFirst || len(ev.UserIds) != 2 {
		t.Fatalf("hearts event: %+v", ev)
	}
	// A later song with as many hearts doesn't steal the crown.
	heart(alice, bobsFirst, http.StatusOK)
	heart(carol, bobsFirst, http.StatusOK)
	heart(bob, alicesSecond, http.StatusOK)
	// Taking a heart back.
	var got httpapi.Hearts
	bob.want(http.StatusOK, "DELETE", base+"/queue/"+alicesSecond+"/hearts", nil).decode(t, &got)
	if len(got.UserIds) != 0 {
		t.Fatalf("after unhearting: %+v", got)
	}
	bob.want(http.StatusOK, "GET", base+"/queue/"+bobsFirst+"/hearts", nil).decode(t, &got)
	if len(got.UserIds) != 2 {
		t.Fatalf("bob's hearts: %+v", got)
	}

	// Only the host ends the night; the song with the most hearts, first to
	// get them, is crowned.
	if r := bob.do("POST", base+"/nights", nil); r.status != http.StatusForbidden {
		t.Fatalf("bob ended the night: %d", r.status)
	}
	var n httpapi.Night
	alice.want(http.StatusCreated, "POST", base+"/nights", nil).decode(t, &n)
	if n.EndedBy != httpapi.NightEndedByHost || n.Plays != 3 || !n.StartedAt.Equal(night) || n.SongOfTheNight == nil ||
		n.SongOfTheNight.Item.Id != alicesFirst || n.SongOfTheNight.Hearts != 2 {
		t.Fatalf("night: %+v", n)
	}
	var ended httpapi.Night
	ws.await("night.ended", &ended)
	if ended.Id != n.Id || ended.SongOfTheNight == nil {
		t.Fatalf("night event: %+v", ended)
	}
	if r := alice.do("POST", base+"/nights", nil); r.status != http.StatusConflict || r.code() != "nothing_played" {
		t.Fatalf("ending an empty night: %d %s", r.status, r.body)
	}
	// Last night's songs can't get hearts any more.
	if r := bob.do("PUT", base+"/queue/"+alicesSecond+"/hearts", nil); r.status != http.StatusConflict {
		t.Fatalf("hearting last night's song: %d", r.status)
	}

	// The recap carries the night.
	var sessions []httpapi.ListeningSession
	bob.want(http.StatusOK, "GET", base+"/sessions", nil).decode(t, &sessions)
	if len(sessions) != 1 || sessions[0].Night == nil || sessions[0].Night.SongOfTheNight == nil {
		t.Fatalf("sessions: %+v", sessions)
	}

	// The next night ends on its own once the room goes quiet. Nothing got
	// a heart, so nothing is crowned.
	e.play(room.Id, unplayed, e.clock().Add(time.Minute))
	e.advance(time.Hour)
	if err := e.nights.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	var ns []httpapi.Night
	bob.want(http.StatusOK, "GET", base+"/nights", nil).decode(t, &ns)
	if len(ns) != 1 {
		t.Fatalf("ended before the room went quiet: %+v", ns)
	}
	e.advance(2 * time.Hour)
	if err := e.nights.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	ns = nil
	bob.want(http.StatusOK, "GET", base+"/nights", nil).decode(t, &ns)
	if len(ns) != 2 || ns[0].EndedBy != httpapi.NightEndedByIdle || ns[0].Plays != 1 || ns[0].SongOfTheNight != nil || ns[1].Id != n.Id {
		b, _ := json.Marshal(ns)
		t.Fatalf("nights: %s", b)
	}
}

func TestGuestHearts(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	e.advance(time.Since(e.clock())) // guest cookies need a clock near the jar's
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{
		Name: "Living room", Guests: &httpapi.RoomGuests{Allowed: true, MaxSongs: 5, CanVote: true},
	}).decode(t, &room)
	base := "/rooms/" + room.Id
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", base+"/queue", addReq(linkFake(t, alice), "t01")).decode(t, &snap)
	e.play(room.Id, snap.UpNext[0], e.clock().Add(-time.Minute))
	var pass httpapi.GuestPass
	alice.want(http.StatusCreated, "POST", base+"/guest-pass", httpapi.CreateGuestPassRequest{ExpiresAt: e.clock().Add(time.Hour)}).decode(t, &pass)
	sam, _ := e.joinAsGuest(passToken(t, pass), "Sam")

	sam.want(http.StatusOK, "PUT", base+"/queue/"+snap.UpNext[0]+"/hearts", nil)
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Guests: &httpapi.RoomGuests{Allowed: true, MaxSongs: 5, CanVote: false}})
	if r := sam.do("PUT", base+"/queue/"+snap.UpNext[0]+"/hearts", nil); r.status != http.StatusForbidden {
		t.Fatalf("guest hearted with voting off: %d %s", r.status, r.body)
	}
	// The host's crown counts the heart sam gave while they could.
	var n httpapi.Night
	alice.want(http.StatusCreated, "POST", base+"/nights", nil).decode(t, &n)
	if n.SongOfTheNight == nil || n.SongOfTheNight.Hearts != 1 {
		t.Fatalf("night: %+v", n)
	}
}
