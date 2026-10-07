// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func userByID(t *testing.T, c *client, id string) httpapi.User {
	t.Helper()
	var us []httpapi.User
	c.want(http.StatusOK, "GET", "/users", nil).decode(t, &us)
	i := slices.IndexFunc(us, func(u httpapi.User) bool { return u.Id == id })
	if i < 0 {
		t.Fatalf("no user %s", id)
	}
	return us[i]
}

func auditActions(t *testing.T, c *client) []string {
	t.Helper()
	var log []httpapi.UserAuditEntry
	c.want(http.StatusOK, "GET", "/user-audit", nil).decode(t, &log)
	out := make([]string, len(log))
	for i, a := range log {
		out[i] = string(a.Action)
		if a.Role != nil {
			out[i] += ":" + string(*a.Role)
		}
	}
	return out
}

func TestManageUsers(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	carol := e.member(alice, "carol")
	aliceID, bobID, carolID := me(t, alice).Id, me(t, bob).Id, me(t, carol).Id
	admin, member := httpapi.Admin, httpapi.Member

	for _, tc := range []struct {
		name   string
		c      *client
		method string
		path   string
		body   any
		status int
		code   string
	}{
		{"a member changes a role", bob, "PATCH", "/users/" + carolID, httpapi.UpdateUserRequest{Role: &admin}, http.StatusForbidden, "forbidden"},
		{"a member removes someone", bob, "DELETE", "/users/" + carolID, nil, http.StatusForbidden, "forbidden"},
		{"a member reads the audit log", bob, "GET", "/user-audit", nil, http.StatusForbidden, "forbidden"},
		{"the last admin steps down", alice, "PATCH", "/users/" + aliceID, httpapi.UpdateUserRequest{Role: &member}, http.StatusConflict, "last_admin"},
		{"an admin disables themselves", alice, "PATCH", "/users/" + aliceID, httpapi.UpdateUserRequest{Disabled: ptr(true)}, http.StatusBadRequest, "invalid_input"},
		{"an admin removes themselves", alice, "DELETE", "/users/" + aliceID, nil, http.StatusBadRequest, "invalid_input"},
		{"nothing to change", alice, "PATCH", "/users/" + bobID, httpapi.UpdateUserRequest{}, http.StatusBadRequest, "invalid_input"},
		{"nobody", alice, "PATCH", "/users/nope", httpapi.UpdateUserRequest{Role: &admin}, http.StatusNotFound, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r := tc.c.do(tc.method, tc.path, tc.body); r.status != tc.status || r.code() != tc.code {
				t.Errorf("got %d %s", r.status, r.body)
			}
		})
	}

	// Promote carol and back. With two admins, either may step down.
	var u httpapi.User
	alice.want(http.StatusOK, "PATCH", "/users/"+carolID, httpapi.UpdateUserRequest{Role: &admin}).decode(t, &u)
	if u.Role != httpapi.Admin {
		t.Fatalf("promoted: %+v", u)
	}
	alice.want(http.StatusOK, "PATCH", "/users/"+carolID, httpapi.UpdateUserRequest{Role: &member})

	// Disabling bob signs him out everywhere, closes his room sockets, and
	// keeps him out; his songs stay.
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	bobLink := linkFake(t, bob)
	var snap httpapi.QueueSnapshot
	bob.want(http.StatusOK, "POST", "/rooms/"+room.Id+"/queue", addReq(bobLink, "t01", "t02")).decode(t, &snap)
	sock := bob.mustDial(room.Id, "")
	sock.expect("hello", nil)
	alice.want(http.StatusOK, "PATCH", "/users/"+bobID, httpapi.UpdateUserRequest{Disabled: ptr(true)}).decode(t, &u)
	if u.Disabled == nil || !*u.Disabled {
		t.Fatalf("disabled: %+v", u)
	}
	if code := sock.closeStatus(); code != 4001 {
		t.Errorf("socket closed with %d", code)
	}
	bob.want(http.StatusUnauthorized, "GET", "/me", nil)
	if r := e.client().do("POST", "/auth/login", httpapi.LoginRequest{Username: "bob", Password: "password-bob"}); r.status != http.StatusForbidden || r.code() != "account_disabled" {
		t.Fatalf("disabled login: %d %s", r.status, r.body)
	}
	alice.want(http.StatusOK, "GET", "/rooms/"+room.Id+"/queue", nil).decode(t, &snap)
	if len(snap.UpNext) != 2 {
		t.Fatalf("disabled bob's songs: %+v", snap.UpNext)
	}

	// Enabled again, he can sign in.
	alice.want(http.StatusOK, "PATCH", "/users/"+bobID, httpapi.UpdateUserRequest{Disabled: ptr(false)})
	bob.want(http.StatusOK, "POST", "/auth/login", httpapi.LoginRequest{Username: "bob", Password: "password-bob"})

	// Removing bob: his room becomes alice's, his songs leave the queue,
	// his links and sign-in go, and he's anonymized.
	var bobRoom httpapi.Room
	bob.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Bob's"}).decode(t, &bobRoom)
	alice.want(http.StatusNoContent, "DELETE", "/users/"+bobID, nil)
	bob.want(http.StatusUnauthorized, "GET", "/me", nil)
	if r := e.client().do("POST", "/auth/login", httpapi.LoginRequest{Username: "bob", Password: "password-bob"}); r.status != http.StatusUnauthorized {
		t.Fatalf("removed login: %d %s", r.status, r.body)
	}
	alice.want(http.StatusOK, "GET", "/rooms/"+bobRoom.Id, nil).decode(t, &bobRoom)
	if bobRoom.OwnerId != aliceID {
		t.Errorf("bob's room owner: %s", bobRoom.OwnerId)
	}
	alice.want(http.StatusOK, "GET", "/rooms/"+room.Id+"/queue", nil).decode(t, &snap)
	if len(snap.UpNext) != 0 {
		t.Errorf("removed bob's songs still queued: %+v", snap.UpNext)
	}
	if links, err := e.db.ListServiceLinks(t.Context(), bobID); err != nil || len(links) != 0 {
		t.Errorf("bob's links: %v, %v", links, err)
	}
	gone := userByID(t, alice, bobID)
	if gone.Removed == nil || !*gone.Removed || gone.DisplayName != "Former member" || gone.Role != httpapi.Member || strings.Contains(gone.Username, "bob") {
		t.Errorf("removed bob: %+v", gone)
	}
	alice.want(http.StatusNotFound, "DELETE", "/users/"+bobID, nil)
	// The username is free again.
	e.member(alice, "bob")

	want := []string{"removed", "enabled", "disabled", "role_changed:member", "role_changed:admin"}
	if got := auditActions(t, alice); !slices.Equal(got, want) {
		t.Errorf("audit: %v, want %v", got, want)
	}
	var log []httpapi.UserAuditEntry
	alice.want(http.StatusOK, "GET", "/user-audit", nil).decode(t, &log)
	if a := log[0]; a.ActorName != "Admin" || a.TargetName != "Bob" || a.ActorId == nil || *a.ActorId != aliceID {
		t.Errorf("removal entry: %+v", a)
	}
}

func TestDeleteMe(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	carol := e.member(alice, "carol")
	aliceID, carolID := me(t, alice).Id, me(t, carol).Id

	if r := bob.do("POST", "/me/delete", httpapi.DeleteMeRequest{}); r.code() != "invalid_input" {
		t.Fatalf("no confirmation: %d %s", r.status, r.body)
	}
	if r := bob.do("POST", "/me/delete", httpapi.DeleteMeRequest{Password: ptr("wrong")}); r.code() != "wrong_password" {
		t.Fatalf("wrong password: %d %s", r.status, r.body)
	}
	r := bob.want(http.StatusNoContent, "POST", "/me/delete", httpapi.DeleteMeRequest{Password: ptr("password-bob")})
	if !strings.Contains(r.header.Get("Set-Cookie"), "Max-Age=0") {
		t.Errorf("cookie not cleared: %q", r.header.Get("Set-Cookie"))
	}
	bob.want(http.StatusUnauthorized, "GET", "/me", nil)

	// The last admin can't leave; once carol is an admin, alice's rooms
	// go to her.
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	if r := alice.do("POST", "/me/delete", httpapi.DeleteMeRequest{Password: ptr("password-admin")}); r.code() != "last_admin" {
		t.Fatalf("last admin: %d %s", r.status, r.body)
	}
	admin := httpapi.Admin
	alice.want(http.StatusOK, "PATCH", "/users/"+carolID, httpapi.UpdateUserRequest{Role: &admin})
	alice.want(http.StatusNoContent, "POST", "/me/delete", httpapi.DeleteMeRequest{Password: ptr("password-admin")})
	carol.want(http.StatusOK, "GET", "/rooms/"+room.Id, nil).decode(t, &room)
	if room.OwnerId != carolID {
		t.Errorf("room owner: %s", room.OwnerId)
	}
	if got, want := auditActions(t, carol), []string{"deleted_self", "role_changed:admin", "deleted_self"}; !slices.Equal(got, want) {
		t.Errorf("audit: %v, want %v", got, want)
	}
	if u := userByID(t, carol, aliceID); u.Removed == nil || !*u.Removed {
		t.Errorf("alice: %+v", u)
	}
}

func TestDeleteMeWithPasskey(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	var inv httpapi.Invite
	alice.want(http.StatusCreated, "POST", "/invites", httpapi.CreateInviteRequest{}).decode(t, &inv)
	phone := newAuthenticator(t, e.base)
	dave := e.client()
	var cer httpapi.Ceremony
	dave.want(http.StatusOK, "POST", "/auth/signup/passkey/begin", httpapi.PasskeySignupRequest{Invite: inv.Code, Username: "dave", DisplayName: "Dave"}).decode(t, &cer)
	dave.want(http.StatusCreated, "POST", "/auth/signup/passkey/finish", httpapi.FinishCeremony{CeremonyId: cer.CeremonyId, Credential: phone.create(t, cer.Options)})

	// Without a password, a password won't do.
	if r := dave.do("POST", "/me/delete", httpapi.DeleteMeRequest{Password: ptr("anything")}); r.code() != "invalid_input" {
		t.Fatalf("password without one: %d %s", r.status, r.body)
	}
	// Alice has no passkey to confirm with.
	if r := alice.do("POST", "/me/reauth/begin", nil); r.code() != "invalid_input" {
		t.Fatalf("reauth without a passkey: %d %s", r.status, r.body)
	}
	// Alice can't use dave's ceremony.
	dave.want(http.StatusOK, "POST", "/me/reauth/begin", nil).decode(t, &cer)
	if r := alice.do("POST", "/me/delete", httpapi.DeleteMeRequest{CeremonyId: &cer.CeremonyId, Credential: ptr(phone.get(t, cer.Options))}); r.code() != "ceremony_expired" {
		t.Fatalf("someone else's ceremony: %d %s", r.status, r.body)
	}
	dave.want(http.StatusOK, "POST", "/me/reauth/begin", nil).decode(t, &cer)
	dave.want(http.StatusNoContent, "POST", "/me/delete", httpapi.DeleteMeRequest{CeremonyId: &cer.CeremonyId, Credential: ptr(phone.get(t, cer.Options))})
	dave.want(http.StatusUnauthorized, "GET", "/me", nil)
	if n, err := e.db.CountPasskeys(t.Context(), me(t, alice).Id); err != nil || n != 0 {
		t.Fatalf("alice's passkeys: %d, %v", n, err)
	}
}

func TestRoomManagement(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	// Guest cookies expire with the pass, so the cookie jar needs the
	// server's clock near its own.
	e.advance(time.Since(e.clock()))
	bob := e.member(alice, "bob")
	carol := e.member(alice, "carol")
	bobID, carolID := me(t, bob).Id, me(t, carol).Id

	var room httpapi.Room
	bob.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Bob's"}).decode(t, &room)
	base := "/rooms/" + room.Id

	// Carol can't touch bob's room; alice, an admin, can.
	carol.want(http.StatusForbidden, "PATCH", base, httpapi.UpdateRoomRequest{Name: ptr("Carol's")})
	carol.want(http.StatusForbidden, "PUT", base+"/owner", httpapi.TransferRoomRequest{UserId: carolID})
	carol.want(http.StatusForbidden, "DELETE", base, nil)
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Name: ptr("Den")}).decode(t, &room)
	if room.Name != "Den" {
		t.Fatalf("renamed: %+v", room)
	}

	// Transfer to carol; everyone in the room hears.
	sock := carol.mustDial(room.Id, "")
	sock.expect("hello", nil)
	if r := bob.do("PUT", base+"/owner", httpapi.TransferRoomRequest{UserId: "nope"}); r.code() != "invalid_input" {
		t.Fatalf("transfer to nobody: %d %s", r.status, r.body)
	}
	bob.want(http.StatusOK, "PUT", base+"/owner", httpapi.TransferRoomRequest{UserId: carolID}).decode(t, &room)
	if room.OwnerId != carolID {
		t.Fatalf("transferred: %+v", room)
	}
	var pushed httpapi.Room
	sock.await("room.updated", &pushed)
	if pushed.OwnerId != carolID {
		t.Fatalf("pushed: %+v", pushed)
	}
	bob.want(http.StatusForbidden, "PATCH", base, httpapi.UpdateRoomRequest{Name: ptr("Mine again")})

	// Not to someone disabled.
	alice.want(http.StatusOK, "PATCH", "/users/"+bobID, httpapi.UpdateUserRequest{Disabled: ptr(true)})
	if r := carol.do("PUT", base+"/owner", httpapi.TransferRoomRequest{UserId: bobID}); r.code() != "invalid_input" {
		t.Fatalf("transfer to disabled: %d %s", r.status, r.body)
	}

	// Deleting it tells everyone, closes their sockets, and takes its
	// guests with it.
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Guests: &httpapi.RoomGuests{Allowed: true, MaxSongs: 10, CanVote: true}})
	var pass httpapi.GuestPass
	carol.want(http.StatusCreated, "POST", base+"/guest-pass", httpapi.CreateGuestPassRequest{ExpiresAt: e.clock().Add(4 * time.Hour)}).decode(t, &pass)
	_, gm := e.joinAsGuest(passToken(t, pass), "Gus")
	carolLink := linkFake(t, carol)
	carol.want(http.StatusOK, "POST", base+"/queue", addReq(carolLink, "t01"))
	carol.want(http.StatusNoContent, "DELETE", base, nil)
	var gone struct {
		RoomID string `json:"roomId"`
	}
	sock.await("room.deleted", &gone)
	if gone.RoomID != room.Id {
		t.Errorf("deleted event: %+v", gone)
	}
	if code := sock.closeStatus(); code != 4004 {
		t.Errorf("socket closed with %d", code)
	}
	carol.want(http.StatusNotFound, "GET", base, nil)
	if _, err := e.db.GetUser(t.Context(), gm.Id); !store.IsNotFound(err) {
		t.Errorf("guest account: %v", err)
	}
}
