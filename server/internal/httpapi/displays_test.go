// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

// pairTV pairs a fresh client as a display of roomID, by way of someone in the room.
func pairTV(t *testing.T, e *env, by *client, roomID string) *client {
	t.Helper()
	tv := e.client()
	var p httpapi.DisplayPairing
	tv.want(http.StatusCreated, "POST", "/display/pairing", nil).decode(t, &p)
	var d httpapi.Display
	// Typed in as people would: lowercase, with a space.
	by.want(http.StatusCreated, "POST", "/rooms/"+roomID+"/displays", httpapi.PairDisplayRequest{
		Code: strings.ToLower(p.Code[:3] + " " + p.Code[3:]), Name: ptr("Living room TV"),
	}).decode(t, &d)
	var st httpapi.DisplayPairingStatus
	tv.want(http.StatusOK, "GET", "/display/pairing", nil).decode(t, &st)
	if st.Status != httpapi.Paired || st.RoomId == nil || *st.RoomId != roomID {
		t.Fatalf("pairing status: %+v", st)
	}
	return tv
}

func TestDisplayPairing(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	carol := e.member(alice, "carol")
	room := e.room(t, me(t, alice).Id)
	other := e.room(t, me(t, alice).Id)
	item := e.queue(t, room, me(t, bob).Id, "Concert Pitch")

	// Waiting for a code to be typed in.
	tv := e.client()
	if r := tv.do("GET", "/display", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("unpaired display: %d", r.status)
	}
	var p httpapi.DisplayPairing
	tv.want(http.StatusCreated, "POST", "/display/pairing", nil).decode(t, &p)
	if len(p.Code) != 6 {
		t.Fatalf("code: %q", p.Code)
	}
	var st httpapi.DisplayPairingStatus
	tv.want(http.StatusOK, "GET", "/display/pairing", nil).decode(t, &st)
	if st.Status != httpapi.Waiting || st.Code != p.Code {
		t.Fatalf("waiting: %+v", st)
	}
	if r := bob.do("POST", "/rooms/"+room.ID+"/displays", httpapi.PairDisplayRequest{Code: "ZZZZZZ"}); r.status != http.StatusNotFound || r.code() != "pairing_invalid" {
		t.Fatalf("wrong code: %d %s", r.status, r.body)
	}
	// Codes time out.
	e.advance(11 * time.Minute)
	if r := tv.do("GET", "/display/pairing", nil); r.status != http.StatusGone || r.code() != "pairing_expired" {
		t.Fatalf("expired: %d %s", r.status, r.body)
	}

	tv = pairTV(t, e, bob, room.ID)
	var dm httpapi.DisplayMe
	tv.want(http.StatusOK, "GET", "/display", nil).decode(t, &dm)
	if dm.Room.Id != room.ID || dm.Display.Name != "Living room TV" || dm.Display.PairedBy == nil || *dm.Display.PairedBy != me(t, bob).Id {
		t.Fatalf("display: %+v", dm)
	}
	// The code was used up.
	if r := tv.do("GET", "/display/pairing", nil); r.status != http.StatusGone {
		t.Fatalf("pairing after paired: %d", r.status)
	}

	// It reads its own room...
	for _, path := range []string{"/rooms/" + room.ID, "/rooms/" + room.ID + "/queue", "/rooms/" + room.ID + "/playback", "/users"} {
		tv.want(http.StatusOK, "GET", path, nil)
	}
	// (The test song has no artwork or lyrics, but the display may ask.)
	for _, path := range []string{"/artwork", "/lyrics", "/liner-notes"} {
		if r := tv.do("GET", "/rooms/"+room.ID+"/queue/"+item.ID+path, nil); r.status != http.StatusNotFound {
			t.Fatalf("GET %s: %d %s", path, r.status, r.body)
		}
	}
	// ...and nothing else.
	if r := tv.do("GET", "/rooms/"+other.ID+"/queue", nil); r.status != http.StatusForbidden {
		t.Fatalf("other room: %d", r.status)
	}
	for _, path := range []string{"/rooms", "/me", "/links", "/rooms/" + room.ID + "/history"} {
		if r := tv.do("GET", path, nil); r.status != http.StatusUnauthorized {
			t.Fatalf("GET %s: %d", path, r.status)
		}
	}
	if r := tv.do("POST", "/rooms/"+room.ID+"/reactions", httpapi.ReactionRequest{Emoji: "🔥"}); r.status != http.StatusUnauthorized {
		t.Fatalf("display reacting: %d", r.status)
	}

	var ds []httpapi.Display
	carol.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/displays", nil).decode(t, &ds)
	if len(ds) != 1 || ds[0].Id != dm.Display.Id {
		t.Fatalf("displays: %+v", ds)
	}
	// Only the owner, whoever paired it, or an admin may unpair it.
	if r := carol.do("DELETE", "/rooms/"+room.ID+"/displays/"+ds[0].Id, nil); r.status != http.StatusForbidden {
		t.Fatalf("carol unpairing: %d", r.status)
	}
	if r := bob.do("DELETE", "/rooms/"+other.ID+"/displays/"+ds[0].Id, nil); r.status != http.StatusNotFound {
		t.Fatalf("unpairing through another room: %d", r.status)
	}
	bob.want(http.StatusNoContent, "DELETE", "/rooms/"+room.ID+"/displays/"+ds[0].Id, nil)
	if r := tv.do("GET", "/rooms/"+room.ID+"/queue", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("unpaired display reading: %d", r.status)
	}

	// A display can unpair itself.
	tv = pairTV(t, e, alice, room.ID)
	tv.want(http.StatusNoContent, "DELETE", "/display", nil)
	if r := tv.do("GET", "/display", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("after leaving: %d", r.status)
	}
}

func TestDisplaySocketAndReactions(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	aliceMe := me(t, alice)
	room := e.room(t, aliceMe.Id)
	other := e.room(t, aliceMe.Id)
	tv := pairTV(t, e, alice, room.ID)

	a := alice.mustDial(room.ID, "")
	a.expect("hello", nil)

	// The display watches without being in the room.
	d := tv.mustDial(room.ID, "")
	var hello httpapi.RoomHello
	d.expect("hello", &hello)
	if hello.You != "" || len(hello.Members) != 1 || hello.ServerTime.IsZero() {
		t.Fatalf("display hello: %+v", hello)
	}
	d.expect("queue.updated", nil)
	d.expect("nowplaying.updated", nil)
	if _, status, err := tv.dialRoom(other.ID, ""); err == nil || status != http.StatusForbidden {
		t.Fatalf("display dialing another room: %d %v", status, err)
	}
	// So does a signed-in user's big screen.
	big := alice.mustDial(room.ID, "display=1")
	big.expect("hello", nil)

	// Reactions reach everyone.
	bob := e.member(alice, "bob")
	bobMe := me(t, bob)
	if r := bob.do("POST", "/rooms/"+room.ID+"/reactions", map[string]string{"emoji": "💩"}); r.status != http.StatusBadRequest {
		t.Fatalf("unlisted emoji: %d", r.status)
	}
	var sent httpapi.Reaction
	bob.want(http.StatusAccepted, "POST", "/rooms/"+room.ID+"/reactions", httpapi.ReactionRequest{Emoji: "🔥"}).decode(t, &sent)
	var got httpapi.Reaction
	d.await("reaction.sent", &got)
	if got.Id != sent.Id || got.UserId != bobMe.Id || got.Emoji != "🔥" {
		t.Fatalf("reaction: %+v", got)
	}
	a.await("reaction.sent", nil)

	// A burst is fine; a flood isn't.
	limited := false
	for range 20 {
		if r := bob.do("POST", "/rooms/"+room.ID+"/reactions", httpapi.ReactionRequest{Emoji: "🎉"}); r.status == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("reactions weren't rate limited")
	}

	// Unpairing ends the display's socket.
	var ds []httpapi.Display
	alice.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/displays", nil).decode(t, &ds)
	alice.want(http.StatusNoContent, "DELETE", "/rooms/"+room.ID+"/displays/"+ds[0].Id, nil)
	if code := d.closeStatus(); code != websocket.StatusCode(4001) {
		t.Fatalf("display socket closed with %d", code)
	}
}
