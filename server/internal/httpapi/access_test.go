// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// adminRoomOps mirrors httpapi's: what an admin may do to a room they
// can't open.
var adminRoomOps = map[string]bool{"updateRoom": true, "deleteRoom": true, "transferRoom": true, "joinRoomAsAdmin": true}

// TestRoomsOutsidersCantSee calls every operation about a room, as someone
// who can't open it, and expects the room not to be there. A new room
// endpoint that skipped the check would fail here.
func TestRoomsOutsidersCantSee(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	bob := e.member(admin, "bob")
	carol := e.member(admin, "carol")
	for _, visibility := range []httpapi.RoomVisibility{httpapi.Unlisted, httpapi.Private} {
		var room httpapi.Room
		bob.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Bob's", Visibility: &visibility}).decode(t, &room)
		if room.Visibility != visibility {
			t.Fatalf("visibility %s, want %s", room.Visibility, visibility)
		}

		spec, err := openapi3.NewLoader().LoadFromFile("../../../api/openapi.yaml")
		if err != nil {
			t.Fatal(err)
		}
		checked := 0
		for path, item := range spec.Paths.Map() {
			if !strings.Contains(path, "{roomId}") {
				continue
			}
			for method, op := range item.Operations() {
				u := requestPath(path, room.Id, item.Parameters, op.Parameters)
				var body any
				if op.RequestBody != nil {
					body = map[string]any{}
				}
				for who, c := range map[string]*client{"carol": carol, "admin": admin} {
					if who == "admin" && adminRoomOps[op.OperationID] {
						continue
					}
					if r := c.do(method, u, body); r.status != http.StatusNotFound || r.code() != "not_found" {
						t.Errorf("%s room, %s: %s %s (%s): %d %s", visibility, who, method, u, op.OperationID, r.status, r.body)
					}
				}
				checked++
			}
		}
		if checked < 45 {
			t.Fatalf("only %d room operations checked", checked)
		}
		if _, status, err := carol.dialRoom(room.Id, ""); err == nil || status != http.StatusNotFound {
			t.Errorf("%s room: carol's socket: %d, %v", visibility, status, err)
		}
		var rs []httpapi.Room
		carol.want(http.StatusOK, "GET", "/rooms", nil).decode(t, &rs)
		admin.want(http.StatusOK, "GET", "/rooms", nil).decode(t, &rs)
		if len(rs) != 0 {
			t.Errorf("%s room listed for others: %+v", visibility, rs)
		}
		bob.want(http.StatusOK, "GET", "/rooms", nil).decode(t, &rs)
		if !slices.ContainsFunc(rs, func(r httpapi.Room) bool { return r.Id == room.Id }) {
			t.Errorf("%s room not listed for its owner", visibility)
		}
		bob.want(http.StatusNoContent, "DELETE", "/rooms/"+room.Id, nil)
	}
}

// requestPath fills in an operation's path and required query parameters
// with values that parse.
func requestPath(path, roomID string, params ...openapi3.Parameters) string {
	q := url.Values{}
	path = strings.ReplaceAll(path, "{roomId}", roomID)
	for _, ps := range params {
		for _, p := range ps {
			v := "x"
			if s := p.Value.Schema; s != nil && s.Value != nil {
				switch {
				case s.Value.Type.Is("integer"), s.Value.Type.Is("number"):
					v = "1"
				case s.Value.Type.Is("boolean"):
					v = "true"
				case s.Value.Format == "date-time":
					v = "2026-10-05T12:00:00Z"
				case s.Value.Format == "date":
					v = "2026-10-05"
				case len(s.Value.Enum) > 0:
					v = s.Value.Enum[0].(string)
				}
			}
			switch p.Value.In {
			case "path":
				path = strings.ReplaceAll(path, "{"+p.Value.Name+"}", v)
			case "query":
				if p.Value.Required {
					q.Set(p.Value.Name, v)
				}
			}
		}
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return path
}

func TestRoomMembersAndInvites(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	bob := e.member(admin, "bob")
	carol, dave, erin := e.member(admin, "carol"), e.member(admin, "dave"), e.member(admin, "erin")
	bobID, carolID, daveID, erinID := me(t, bob).Id, me(t, carol).Id, me(t, dave).Id, me(t, erin).Id
	private := httpapi.Private
	var room httpapi.Room
	bob.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Bob's", Visibility: &private}).decode(t, &room)
	base := "/rooms/" + room.Id

	// Only the owner shares a private room's invites.
	bob.want(http.StatusOK, "GET", base+"/members", nil)
	var inv httpapi.RoomInvite
	bob.want(http.StatusCreated, "POST", base+"/invites", httpapi.CreateRoomInviteRequest{}).decode(t, &inv)
	if !strings.HasPrefix(inv.Url, e.base+"/room-invite/") || inv.ExpiresAt != nil || inv.MaxUses != nil {
		t.Fatalf("invite: %+v", inv)
	}
	var preview httpapi.RoomInvitePreview
	carol.want(http.StatusOK, "GET", "/room-invites/"+inv.Code, nil).decode(t, &preview)
	if preview.RoomId != room.Id || preview.RoomName != "Bob's" || preview.Approval || preview.Status != httpapi.RoomInvitePreviewStatusNone {
		t.Fatalf("preview: %+v", preview)
	}
	var res httpapi.RoomInviteResult
	carol.want(http.StatusOK, "POST", "/room-invites/"+inv.Code, nil).decode(t, &res)
	if res.Status != httpapi.RoomInviteResultStatusMember {
		t.Fatalf("carol: %+v", res)
	}
	carol.want(http.StatusOK, "GET", base, nil)
	// Using it again, already in, uses nothing up.
	carol.want(http.StatusOK, "POST", "/room-invites/"+inv.Code, nil)
	var invs []httpapi.RoomInvite
	bob.want(http.StatusOK, "GET", base+"/invites", nil).decode(t, &invs)
	if len(invs) != 1 || invs[0].Uses != 1 {
		t.Fatalf("invites: %+v", invs)
	}
	// A member of a private room can't share it.
	carol.want(http.StatusForbidden, "GET", base+"/invites", nil)
	carol.want(http.StatusForbidden, "POST", base+"/invites", httpapi.CreateRoomInviteRequest{})
	carol.want(http.StatusForbidden, "DELETE", base+"/members/"+bobID, nil)
	if r := e.client().do("GET", "/room-invites/"+inv.Code, nil); r.status != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", r.status)
	}
	if r := carol.do("GET", "/room-invites/nope", nil); r.status != http.StatusNotFound || r.code() != "room_invite_invalid" {
		t.Fatalf("wrong code: %d %s", r.status, r.body)
	}

	// With approval on, dave asks; carol doesn't see the request, bob does.
	bob.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{ApproveJoins: ptr(true)}).decode(t, &room)
	if !room.ApproveJoins {
		t.Fatal("approveJoins not set")
	}
	dave.want(http.StatusOK, "POST", "/room-invites/"+inv.Code, nil).decode(t, &res)
	if res.Status != httpapi.RoomInviteResultStatusPending || !res.Room.Approval {
		t.Fatalf("dave: %+v", res)
	}
	dave.want(http.StatusNotFound, "GET", base, nil)
	dave.want(http.StatusOK, "GET", "/room-invites/"+inv.Code, nil).decode(t, &preview)
	if preview.Status != httpapi.RoomInvitePreviewStatusPending {
		t.Fatalf("dave's preview: %+v", preview)
	}
	members := func(c *client) map[string]httpapi.RoomMemberStatus {
		t.Helper()
		var ms []httpapi.RoomMember
		c.want(http.StatusOK, "GET", base+"/members", nil).decode(t, &ms)
		out := map[string]httpapi.RoomMemberStatus{}
		for _, m := range ms {
			out[m.User.Id] = m.Status
		}
		return out
	}
	if got := members(bob); len(got) != 2 || got[carolID] != httpapi.RoomMemberStatusMember || got[daveID] != httpapi.RoomMemberStatusPending {
		t.Fatalf("bob sees %v", got)
	}
	if got := members(carol); len(got) != 1 || got[carolID] != httpapi.RoomMemberStatusMember {
		t.Fatalf("carol sees %v", got)
	}
	bob.want(http.StatusNoContent, "PUT", base+"/members/"+daveID, nil)
	dave.want(http.StatusOK, "GET", base, nil)

	// Erin asks, then takes it back.
	erin.want(http.StatusOK, "POST", "/room-invites/"+inv.Code, nil)
	erin.want(http.StatusNoContent, "DELETE", base+"/members/"+erinID, nil)
	erin.want(http.StatusNotFound, "DELETE", base+"/members/"+erinID, nil)
	if _, ok := members(bob)[erinID]; ok {
		t.Fatal("erin's request is still there")
	}

	// Removing dave closes his connection and takes out his songs.
	e.queue(t, storeRoom(t, e, room.Id), daveID, "Dave's song")
	s := dave.mustDial(room.Id, "")
	s.expect("hello", nil)
	bob.want(http.StatusNoContent, "DELETE", base+"/members/"+daveID, nil)
	if code := s.closeStatus(); code != 4003 {
		t.Fatalf("close code %d, want 4003", code)
	}
	dave.want(http.StatusNotFound, "GET", base, nil)
	var q httpapi.QueueSnapshot
	bob.want(http.StatusOK, "GET", base+"/queue", nil).decode(t, &q)
	if len(q.UpNext) != 0 {
		t.Fatalf("dave's song is still queued: %+v", q)
	}
	// The owner can't be removed.
	bob.want(http.StatusBadRequest, "DELETE", base+"/members/"+bobID, nil)

	// An admin looks after the room without seeing in, until they join.
	admin.want(http.StatusNotFound, "GET", base, nil)
	admin.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Name: ptr("Bob's place")})
	var activity []httpapi.RoomActivity
	admin.want(http.StatusOK, "GET", "/admin/rooms", nil).decode(t, &activity)
	if len(activity) != 1 || activity[0].CanEnter || activity[0].Visibility != httpapi.Private {
		t.Fatalf("admin rooms: %+v", activity)
	}
	carolSock := carol.mustDial(room.Id, "")
	carolSock.expect("hello", nil)
	admin.want(http.StatusOK, "POST", base+"/admin-join", nil)
	var notice httpapi.PlaybackNotice
	carolSock.await("playback.notice", &notice)
	if !strings.Contains(notice.Message, "joined as a server admin") {
		t.Fatalf("notice: %+v", notice)
	}
	admin.want(http.StatusOK, "GET", base, nil)
	bob.want(http.StatusForbidden, "POST", base+"/admin-join", nil)

	// Handing the room over keeps bob in it.
	bob.want(http.StatusOK, "PUT", base+"/owner", httpapi.TransferRoomRequest{UserId: carolID})
	bob.want(http.StatusOK, "GET", base, nil)

	// Limited invites run out.
	var once httpapi.RoomInvite
	carol.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{ApproveJoins: ptr(false)})
	carol.want(http.StatusCreated, "POST", base+"/invites", httpapi.CreateRoomInviteRequest{MaxUses: ptr(1)}).decode(t, &once)
	dave.want(http.StatusOK, "POST", "/room-invites/"+once.Code, nil)
	if r := erin.do("POST", "/room-invites/"+once.Code, nil); r.status != http.StatusNotFound || r.code() != "room_invite_invalid" {
		t.Fatalf("used-up invite: %d %s", r.status, r.body)
	}
	if r := carol.do("POST", base+"/invites", httpapi.CreateRoomInviteRequest{ExpiresAt: ptr(e.clock().Add(-time.Hour))}); r.status != http.StatusBadRequest {
		t.Fatalf("expired invite: %d", r.status)
	}

	// In an unlisted room, anyone in it shares it. Changing who can join
	// revokes the old links.
	unlisted := httpapi.Unlisted
	carol.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Visibility: &unlisted})
	var link httpapi.RoomInvite
	dave.want(http.StatusCreated, "POST", base+"/invites", httpapi.CreateRoomInviteRequest{}).decode(t, &link)
	carol.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Visibility: &private})
	if r := erin.do("POST", "/room-invites/"+link.Code, nil); r.status != http.StatusNotFound || r.code() != "room_invite_invalid" {
		t.Fatalf("invite after closing: %d %s", r.status, r.body)
	}

	// Open rooms need no invites, or members.
	open := httpapi.Open
	carol.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Visibility: &open})
	erin.want(http.StatusOK, "GET", base, nil)
	carol.want(http.StatusBadRequest, "POST", base+"/invites", httpapi.CreateRoomInviteRequest{})
	carol.want(http.StatusBadRequest, "PUT", base+"/members/"+erinID, nil)
}

// TestClosingARoomKeepsWhosThere: making an open room private keeps whoever
// has it open, or has songs waiting, so a party isn't cut off mid-song.
func TestClosingARoomKeepsWhosThere(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	bob, carol, dave := e.member(admin, "bob"), e.member(admin, "carol"), e.member(admin, "dave")
	var room httpapi.Room
	bob.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Party"}).decode(t, &room)
	base := "/rooms/" + room.Id
	e.queue(t, storeRoom(t, e, room.Id), me(t, carol).Id, "Carol's song")
	s := dave.mustDial(room.Id, "")
	s.expect("hello", nil)

	private := httpapi.Private
	bob.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Visibility: &private})
	carol.want(http.StatusOK, "GET", base, nil)
	dave.want(http.StatusOK, "GET", base, nil)
	admin.want(http.StatusNotFound, "GET", base, nil)

	// Guests are in by their pass, whatever the room's visibility.
	e.advance(time.Since(e.clock()))
	bob.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Guests: &httpapi.RoomGuests{Allowed: true, MaxSongs: 10, CanVote: true}})
	var pass httpapi.GuestPass
	bob.want(http.StatusCreated, "POST", base+"/guest-pass", httpapi.CreateGuestPassRequest{ExpiresAt: e.clock().Add(2 * time.Hour)}).decode(t, &pass)
	sam, _ := e.joinAsGuest(passToken(t, pass), "Sam")
	sam.want(http.StatusOK, "GET", base, nil)
	var rs []httpapi.Room
	sam.want(http.StatusOK, "GET", "/rooms", nil).decode(t, &rs)
	if len(rs) != 1 || rs[0].Id != room.Id {
		t.Fatalf("guest's rooms: %+v", rs)
	}
	sam.want(http.StatusForbidden, "GET", base+"/members", nil)
}

func storeRoom(t *testing.T, e *env, id string) store.Room {
	t.Helper()
	r, err := e.db.GetRoom(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
