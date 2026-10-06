// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

// passToken is the signed token at the end of a guest pass's link.
func passToken(t *testing.T, p httpapi.GuestPass) string {
	t.Helper()
	i := strings.LastIndex(p.Url, "/join/")
	if i < 0 {
		t.Fatalf("not a guest pass link: %q", p.Url)
	}
	return p.Url[i+len("/join/"):]
}

// joinAsGuest joins with a pass as a new guest.
func (e *env) joinAsGuest(token, name string) (*client, httpapi.Me) {
	e.t.Helper()
	c := e.client()
	var m httpapi.Me
	c.want(http.StatusCreated, "POST", "/guest/"+token, httpapi.JoinAsGuestRequest{DisplayName: name}).decode(e.t, &m)
	return c, m
}

func TestGuests(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	// Guest cookies expire with the pass, so the cookie jar needs the
	// server's clock near its own.
	e.advance(time.Since(e.clock()))
	bob := e.member(alice, "bob")
	var room, other httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Kitchen"}).decode(t, &other)
	base := "/rooms/" + room.Id
	tonight := e.clock().Add(4 * time.Hour)

	// Guests are off until the owner turns them on.
	if room.Guests != (httpapi.RoomGuests{Allowed: false, MaxSongs: 10, CanVote: true}) {
		t.Fatalf("default guests: %+v", room.Guests)
	}
	if r := bob.do("POST", base+"/guest-pass", httpapi.CreateGuestPassRequest{ExpiresAt: tonight}); r.status != http.StatusForbidden || r.code() != "guests_off" {
		t.Fatalf("pass with guests off: %d %s", r.status, r.body)
	}
	if r := bob.do("PATCH", base, httpapi.UpdateRoomRequest{Guests: &httpapi.RoomGuests{Allowed: true}}); r.status != http.StatusForbidden {
		t.Fatalf("bob changed alice's room: %d", r.status)
	}
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Guests: &httpapi.RoomGuests{Allowed: true, MaxSongs: 2, CanVote: false}}).decode(t, &room)
	if room.Guests != (httpapi.RoomGuests{Allowed: true, MaxSongs: 2, CanVote: false}) {
		t.Fatalf("guests: %+v", room.Guests)
	}

	// Any member can start a pass; it lasts 15 minutes to a day.
	if r := bob.do("POST", base+"/guest-pass", httpapi.CreateGuestPassRequest{ExpiresAt: e.clock().Add(48 * time.Hour)}); r.status != http.StatusBadRequest {
		t.Fatalf("a two-day pass: %d %s", r.status, r.body)
	}
	bob.want(http.StatusNotFound, "GET", base+"/guest-pass", nil)
	var pass httpapi.GuestPass
	bob.want(http.StatusCreated, "POST", base+"/guest-pass", httpapi.CreateGuestPassRequest{ExpiresAt: tonight}).decode(t, &pass)
	var shown httpapi.GuestPass
	alice.want(http.StatusOK, "GET", base+"/guest-pass", nil).decode(t, &shown)
	if shown.Url != pass.Url || !strings.HasPrefix(pass.Url, e.base+"/join/") {
		t.Fatalf("pass %q, shown %q", pass.Url, shown.Url)
	}
	token := passToken(t, pass)

	// Anyone can check a pass, but only a real one.
	var inv httpapi.GuestInvite
	e.client().want(http.StatusOK, "GET", "/guest/"+token, nil).decode(t, &inv)
	if inv.RoomId != room.Id || inv.RoomName != "Living room" || !inv.ExpiresAt.Equal(tonight.Truncate(time.Second)) {
		t.Fatalf("invite: %+v", inv)
	}
	id, _, _ := strings.Cut(token, ".")
	for _, bad := range []string{"nope", id, id + ".forged", strings.ToUpper(token)} {
		if r := e.client().do("GET", "/guest/"+bad, nil); r.status != http.StatusNotFound || r.code() != "guest_pass_invalid" {
			t.Errorf("pass %q: %d %s", bad, r.status, r.body)
		}
	}
	if r := e.client().do("POST", "/guest/"+token, httpapi.JoinAsGuestRequest{DisplayName: " "}); r.status != http.StatusBadRequest {
		t.Errorf("blank name: %d", r.status)
	}

	// A guest joins: a session until the pass expires, and nothing to set up.
	sam, samMe := e.joinAsGuest(token, "Sam")
	if samMe.Guest == nil || samMe.Guest.RoomId != room.Id || samMe.Guest.Ended || samMe.DisplayName != "Sam" || samMe.HasPassword {
		t.Fatalf("guest: %+v", samMe)
	}
	var seen []httpapi.User
	sam.want(http.StatusOK, "GET", "/users", nil).decode(t, &seen)
	for _, u := range seen {
		if u.Id != samMe.Id && u.Username != "" {
			t.Fatalf("guest sees %s's username", u.DisplayName)
		}
	}
	var rs []httpapi.Room
	sam.want(http.StatusOK, "GET", "/rooms", nil).decode(t, &rs)
	if len(rs) != 1 || rs[0].Id != room.Id {
		t.Fatalf("guest's rooms: %+v", rs)
	}
	// Their room only, and none of the members' things.
	for _, c := range []struct{ method, path string }{
		{"GET", "/rooms/" + other.Id},
		{"GET", "/rooms/" + other.Id + "/queue"},
		{"POST", "/rooms"},
		{"PATCH", base},
		{"PATCH", "/me"},
		{"GET", "/invites"},
		{"POST", "/links"},
		{"GET", base + "/guest-pass"},
		{"POST", base + "/guest-pass"},
		{"GET", base + "/guests"},
		{"PUT", base + "/player"},
		{"POST", base + "/nights"},
	} {
		if r := sam.do(c.method, c.path, map[string]any{}); r.status != http.StatusForbidden {
			t.Errorf("guest %s %s: %d %s", c.method, c.path, r.status, r.body)
		}
	}
	if _, status, err := sam.dialRoom(other.Id, ""); err == nil || status != http.StatusForbidden {
		t.Errorf("guest socket to another room: %d, %v", status, err)
	}
	conn, _, err := sam.dialRoom(room.Id, "")
	if err != nil {
		t.Fatal(err)
	}
	conn.expect("hello", nil)

	// They search and queue from shared services, in a lane of their own,
	// up to the room's limit.
	shared := linkFake(t, alice)
	alice.want(http.StatusOK, "PATCH", "/links/"+shared, httpapi.UpdateLinkRequest{Shared: true})
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", base+"/queue", addReq(shared, "t01", "t02"))
	sam.want(http.StatusOK, "POST", base+"/queue", addReq(shared, "t03")).decode(t, &snap)
	// Round robin: the guest's song takes its turn before alice's second.
	byID := map[string]string{}
	for _, it := range snap.Items {
		byID[it.Id] = it.AddedBy
	}
	if len(snap.UpNext) != 3 || byID[snap.UpNext[1]] != samMe.Id {
		t.Fatalf("up next: %v", titles(snap))
	}
	if r := sam.do("POST", base+"/queue", addReq(shared, "t04", "t05")); r.status != http.StatusConflict || r.code() != "guest_limit" {
		t.Fatalf("over the limit: %d %s", r.status, r.body)
	}
	sam.want(http.StatusOK, "POST", base+"/queue", addReq(shared, "t04"))
	if r := sam.do("POST", base+"/queue", addReq(shared, "t05")); r.status != http.StatusConflict {
		t.Fatalf("past the limit: %d %s", r.status, r.body)
	}
	if r := sam.do("POST", base+"/queue", addReq(linkFake(t, bob), "t05")); r.status != http.StatusForbidden && r.status != http.StatusNotFound {
		t.Fatalf("queued from bob's private link: %d %s", r.status, r.body)
	}

	// The room doesn't let guests vote: no skip votes, no pausing.
	if r := sam.do("POST", base+"/playback", httpapi.PlaybackCommand{Action: httpapi.Pause}); r.status != http.StatusForbidden {
		t.Errorf("guest paused: %d %s", r.status, r.body)
	}

	// Members see who's in, and the owner can remove a guest: they're signed
	// out, and their waiting songs go.
	var guests []httpapi.Guest
	bob.want(http.StatusOK, "GET", base+"/guests", nil).decode(t, &guests)
	if len(guests) != 1 || guests[0].User.Id != samMe.Id || guests[0].Songs != 2 {
		t.Fatalf("guests: %+v", guests)
	}
	if r := bob.do("DELETE", base+"/guests/"+samMe.Id, nil); r.status != http.StatusForbidden {
		t.Fatalf("bob removed a guest: %d", r.status)
	}
	alice.want(http.StatusNoContent, "DELETE", base+"/guests/"+samMe.Id, nil)
	if r := sam.do("GET", "/me", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("removed guest: %d", r.status)
	}
	alice.want(http.StatusOK, "GET", base+"/queue", nil).decode(t, &snap)
	for _, it := range snap.Items {
		if it.AddedBy == samMe.Id {
			t.Fatalf("removed guest's song still queued: %+v", it)
		}
	}
	// Their name stays, for the room's history.
	var users []httpapi.User
	alice.want(http.StatusOK, "GET", "/users", nil).decode(t, &users)
	found := false
	for _, u := range users {
		if u.Id == samMe.Id {
			found = u.DisplayName == "Sam" && u.Guest != nil && u.Guest.Ended
		}
	}
	if !found {
		t.Fatalf("users: %+v", users)
	}

	// A new pass replaces the old; revoking stops anyone else joining.
	var pass2 httpapi.GuestPass
	alice.want(http.StatusCreated, "POST", base+"/guest-pass", httpapi.CreateGuestPassRequest{ExpiresAt: tonight}).decode(t, &pass2)
	if r := e.client().do("POST", "/guest/"+token, httpapi.JoinAsGuestRequest{DisplayName: "Late"}); r.status != http.StatusNotFound {
		t.Fatalf("joined with a replaced pass: %d", r.status)
	}
	kim, kimMe := e.joinAsGuest(passToken(t, pass2), "Kim")
	kim.want(http.StatusOK, "POST", base+"/queue", addReq(shared, "t06"))
	if r := e.client().do("DELETE", base+"/guest-pass", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("revoked signed out: %d", r.status)
	}
	alice.want(http.StatusNoContent, "DELETE", base+"/guest-pass", nil)
	if r := e.client().do("GET", "/guest/"+passToken(t, pass2), nil); r.status != http.StatusNotFound {
		t.Fatalf("revoked pass: %d", r.status)
	}
	// Turning guests off stops passes too.
	kim.want(http.StatusOK, "GET", "/me", nil)

	// When the night's over, the guest expires: signed out, songs gone.
	e.advance(5 * time.Hour)
	if r := kim.do("GET", "/me", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("expired guest: %d", r.status)
	}
	if err := e.api.EndExpiredGuests(t.Context()); err != nil {
		t.Fatal(err)
	}
	alice.want(http.StatusOK, "GET", base+"/queue", nil).decode(t, &snap)
	for _, it := range snap.Items {
		if it.AddedBy == kimMe.Id {
			t.Fatalf("expired guest's song still queued: %+v", it)
		}
	}
	alice.want(http.StatusOK, "GET", base+"/guests", nil).decode(t, &guests)
	if len(guests) != 0 {
		t.Fatalf("guests after the night: %+v", guests)
	}
}

func TestGuestPassNeedsGuestsOn(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	// Guest cookies expire with the pass, so the cookie jar needs the
	// server's clock near its own.
	e.advance(time.Since(e.clock()))
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{
		Name: "Living room", Guests: &httpapi.RoomGuests{Allowed: true, MaxSongs: 0, CanVote: true},
	}).decode(t, &room)
	if room.Guests.MaxSongs != 0 {
		t.Fatalf("no limit: %+v", room.Guests)
	}
	base := "/rooms/" + room.Id
	var pass httpapi.GuestPass
	alice.want(http.StatusCreated, "POST", base+"/guest-pass", httpapi.CreateGuestPassRequest{ExpiresAt: e.clock().Add(time.Hour)}).decode(t, &pass)
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Guests: &httpapi.RoomGuests{Allowed: false, MaxSongs: 10, CanVote: true}})
	if r := e.client().do("POST", "/guest/"+passToken(t, pass), httpapi.JoinAsGuestRequest{DisplayName: "Sam"}); r.status != http.StatusNotFound {
		t.Fatalf("joined a room with guests off: %d %s", r.status, r.body)
	}
	alice.want(http.StatusNotFound, "GET", base+"/guest-pass", nil)
	if r := alice.do("PATCH", base, httpapi.UpdateRoomRequest{Guests: &httpapi.RoomGuests{Allowed: true, MaxSongs: 500}}); r.status != http.StatusBadRequest {
		t.Fatalf("500 songs: %d", r.status)
	}
}
