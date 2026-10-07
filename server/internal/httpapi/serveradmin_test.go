// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func TestServerAdmin(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	aliceID, bobID := me(t, alice).Id, me(t, bob).Id

	for _, path := range []string{"/admin/server", "/admin/links", "/admin/rooms", "/admin/sessions"} {
		if r := bob.do("GET", path, nil); r.status != http.StatusForbidden {
			t.Errorf("member GET %s: %d", path, r.status)
		}
	}
	bob.want(http.StatusForbidden, "POST", "/admin/backups", nil)
	bob.want(http.StatusForbidden, "PATCH", "/server-settings", httpapi.ServerSettingsUpdate{InstanceName: ptr("Mine")})

	// Settings: everyone reads them, admins change them, and new invites
	// last as long as they say.
	var st httpapi.ServerSettings
	bob.want(http.StatusOK, "GET", "/server-settings", nil).decode(t, &st)
	if st != (httpapi.ServerSettings{InstanceName: "", InviteExpiryHours: 168}) {
		t.Fatalf("defaults: %+v", st)
	}
	alice.want(http.StatusOK, "PATCH", "/server-settings", httpapi.ServerSettingsUpdate{InstanceName: ptr("The Den"), InviteExpiryHours: ptr(24)}).decode(t, &st)
	if st != (httpapi.ServerSettings{InstanceName: "The Den", InviteExpiryHours: 24}) {
		t.Fatalf("updated: %+v", st)
	}
	if r := alice.do("PATCH", "/server-settings", httpapi.ServerSettingsUpdate{InviteExpiryHours: ptr(0)}); r.code() != "invalid_input" {
		t.Errorf("zero expiry: %d %s", r.status, r.body)
	}
	var inv httpapi.Invite
	alice.want(http.StatusCreated, "POST", "/invites", httpapi.CreateInviteRequest{}).decode(t, &inv)
	if !inv.ExpiresAt.Equal(e.clock().Add(24 * time.Hour)) {
		t.Errorf("invite expires %s", inv.ExpiresAt)
	}

	// Server info and backups.
	var info httpapi.ServerInfo
	alice.want(http.StatusOK, "GET", "/admin/server", nil).decode(t, &info)
	if info.Version != "test" || info.DatabaseBytes == 0 || len(info.Backups) != 0 || info.StartedAt.IsZero() {
		t.Fatalf("info: %+v", info)
	}
	var b httpapi.Backup
	alice.want(http.StatusCreated, "POST", "/admin/backups", nil).decode(t, &b)
	alice.want(http.StatusOK, "GET", "/admin/server", nil).decode(t, &info)
	if len(info.Backups) != 1 || info.Backups[0].Name != b.Name || b.Bytes == 0 {
		t.Fatalf("after backup: %+v (made %+v)", info.Backups, b)
	}

	// Links, failing first.
	linkFake(t, alice)
	bobLink := linkFake(t, bob)
	if err := e.db.SetServiceLinkStatus(t.Context(), store.SetServiceLinkStatusParams{ID: bobLink, Status: store.LinkExpired, StatusDetail: "token revoked", UpdatedAt: e.clock()}); err != nil {
		t.Fatal(err)
	}
	var links []httpapi.ServiceLink
	alice.want(http.StatusOK, "GET", "/admin/links", nil).decode(t, &links)
	if len(links) != 2 || links[0].Id != bobLink || links[0].OwnerId != bobID || links[0].Status != "needs_relink" || links[1].OwnerId != aliceID {
		t.Fatalf("links: %+v", links)
	}

	// Rooms, with who's in them.
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Kitchen"})
	sock := bob.mustDial(room.Id, "")
	sock.expect("hello", nil)
	var rooms []httpapi.RoomActivity
	alice.want(http.StatusOK, "GET", "/admin/rooms", nil).decode(t, &rooms)
	if len(rooms) != 2 || rooms[0].RoomId != room.Id || len(rooms[0].Members) != 1 || rooms[0].Members[0] != bobID || rooms[0].State != "idle" {
		t.Fatalf("rooms: %+v", rooms)
	}

	// Everyone's sessions; an admin can sign anyone out.
	var sessions []httpapi.UserSession
	alice.want(http.StatusOK, "GET", "/admin/sessions", nil).decode(t, &sessions)
	var bobSession string
	for _, s := range sessions {
		if s.UserId == bobID {
			bobSession = s.Id
		}
		if s.UserId == aliceID && !s.Current {
			t.Errorf("alice's session isn't current: %+v", s)
		}
	}
	if len(sessions) != 2 || bobSession == "" {
		t.Fatalf("sessions: %+v", sessions)
	}
	alice.want(http.StatusNoContent, "DELETE", "/admin/sessions/"+bobSession, nil)
	bob.want(http.StatusUnauthorized, "GET", "/me", nil)
	alice.want(http.StatusNotFound, "DELETE", "/admin/sessions/"+bobSession, nil)
}
