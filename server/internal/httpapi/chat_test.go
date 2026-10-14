// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

func TestChat(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	carol := e.member(alice, "carol")
	bobMe := me(t, bob)
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	base := "/rooms/" + room.Id
	tv := pairTV(t, e, alice, room.Id)
	d := tv.mustDial(room.Id, "")
	d.expect("hello", nil)

	// A message reaches the room, and remembers the song playing.
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", base+"/queue", addReq(linkFake(t, alice), "t01")).decode(t, &snap)
	alice.want(http.StatusOK, "PUT", base+"/player", httpapi.ClaimPlayerRequest{DeviceId: "phone", Name: "Alice's phone"})
	alice.want(http.StatusOK, "POST", base+"/playback", httpapi.PlaybackCommand{Action: httpapi.Play})
	var sent httpapi.ChatMessage
	bob.want(http.StatusCreated, "POST", base+"/messages", httpapi.ChatMessageRequest{Body: "  who queued this?  "}).decode(t, &sent)
	if sent.Body != "who queued this?" || sent.UserId != bobMe.Id || sent.ItemId == nil || *sent.ItemId != snap.Items[0].Id || sent.Track == nil || sent.Track.Title == "" {
		t.Fatalf("sent: %+v", sent)
	}
	var got httpapi.ChatMessage
	d.await("chat.message", &got)
	if got.Id != sent.Id || got.Body != sent.Body || got.Track == nil {
		t.Fatalf("on the socket: %+v", got)
	}

	// Plain text, 1-500 characters.
	for _, body := range []string{"", "   \n\t ", strings.Repeat("x", 501)} {
		if r := bob.do("POST", base+"/messages", httpapi.ChatMessageRequest{Body: body}); r.status != http.StatusBadRequest {
			t.Errorf("body of %d: %d", len(body), r.status)
		}
	}
	bob.want(http.StatusCreated, "POST", base+"/messages", httpapi.ChatMessageRequest{Body: strings.Repeat("é", 500)})

	// Pages, newest first.
	for i := range 3 {
		alice.want(http.StatusCreated, "POST", base+"/messages", httpapi.ChatMessageRequest{Body: "hi " + string(rune('a'+i))})
	}
	var page []httpapi.ChatMessage
	carol.want(http.StatusOK, "GET", base+"/messages?limit=2", nil).decode(t, &page)
	if len(page) != 2 || page[0].Body != "hi c" || page[1].Body != "hi b" {
		t.Fatalf("first page: %+v", page)
	}
	var older []httpapi.ChatMessage
	carol.want(http.StatusOK, "GET", base+"/messages?limit=10&before="+page[1].Id, nil).decode(t, &older)
	if len(older) != 3 || older[0].Body != "hi a" || older[2].Id != sent.Id {
		t.Fatalf("older: %+v", older)
	}
	// The big screen reads along.
	tv.want(http.StatusOK, "GET", base+"/messages", nil).decode(t, &page)
	if len(page) != 5 {
		t.Fatalf("display reads %d", len(page))
	}
	if r := tv.do("POST", base+"/messages", httpapi.ChatMessageRequest{Body: "hello"}); r.status != http.StatusUnauthorized {
		t.Fatalf("display posted: %d", r.status)
	}

	// Your own, or anyone's if it's your room.
	carol.want(http.StatusForbidden, "DELETE", base+"/messages/"+sent.Id, nil)
	bob.want(http.StatusNoContent, "DELETE", base+"/messages/"+sent.Id, nil)
	var gone httpapi.ChatMessageDeleted
	d.await("chat.deleted", &gone)
	if gone.Id != sent.Id || gone.RoomId != room.Id {
		t.Fatalf("deleted: %+v", gone)
	}
	bob.want(http.StatusNotFound, "DELETE", base+"/messages/"+sent.Id, nil)
	alice.want(http.StatusNoContent, "DELETE", base+"/messages/"+older[1].Id, nil)
	carol.want(http.StatusOK, "GET", base+"/messages", nil).decode(t, &page)
	if len(page) != 3 {
		t.Fatalf("after deleting: %+v", page)
	}

	// A room you can't open has no chat.
	var private httpapi.Room
	carol.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Carol's", Visibility: ptr(httpapi.RoomVisibilityPrivate)}).decode(t, &private)
	bob.want(http.StatusNotFound, "GET", "/rooms/"+private.Id+"/messages", nil)
	bob.want(http.StatusNotFound, "POST", "/rooms/"+private.Id+"/messages", httpapi.ChatMessageRequest{Body: "hi"})

	// A burst is fine; a flood isn't.
	limited := false
	for range 10 {
		if r := carol.do("POST", base+"/messages", httpapi.ChatMessageRequest{Body: "spam"}); r.status == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("chat wasn't rate limited")
	}

	// The night's chat stays with it.
	e.play(room.Id, snap.Items[0].Id, e.clock().Add(-10*time.Minute))
	var n httpapi.Night
	alice.want(http.StatusCreated, "POST", base+"/nights", nil).decode(t, &n)
	e.advance(time.Minute)
	alice.want(http.StatusCreated, "POST", base+"/messages", httpapi.ChatMessageRequest{Body: "tomorrow"})
	alice.want(http.StatusOK, "GET", base+"/messages", nil).decode(t, &page)
	if page[0].NightId != nil {
		t.Errorf("after the night: %+v", page[0])
	}
	for _, m := range page[1:] {
		if m.NightId == nil || *m.NightId != n.Id {
			t.Errorf("tonight's message %q has night %v", m.Body, m.NightId)
		}
	}
}

func TestChatGuests(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	e.advance(time.Since(e.clock()))
	var room, other httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Kitchen"}).decode(t, &other)
	base := "/rooms/" + room.Id
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Guests: &httpapi.RoomGuests{Allowed: true, MaxSongs: 2, CanVote: true}})
	var pass httpapi.GuestPass
	alice.want(http.StatusCreated, "POST", base+"/guest-pass", httpapi.CreateGuestPassRequest{ExpiresAt: e.clock().Add(4 * time.Hour)}).decode(t, &pass)
	guest, _ := e.joinAsGuest(passToken(t, pass), "Sam")

	var m httpapi.ChatMessage
	guest.want(http.StatusCreated, "POST", base+"/messages", httpapi.ChatMessageRequest{Body: "hey all"}).decode(t, &m)
	var page []httpapi.ChatMessage
	guest.want(http.StatusOK, "GET", base+"/messages", nil).decode(t, &page)
	if len(page) != 1 {
		t.Fatalf("guest reads %+v", page)
	}
	guest.want(http.StatusForbidden, "GET", "/rooms/"+other.Id+"/messages", nil)
	guest.want(http.StatusNoContent, "DELETE", base+"/messages/"+m.Id, nil)
}
