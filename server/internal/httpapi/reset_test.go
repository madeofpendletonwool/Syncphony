// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

func resetCode(t *testing.T, link httpapi.ResetLink) string {
	t.Helper()
	if link.Url == nil {
		t.Fatalf("new reset link has no URL: %+v", link)
	}
	i := strings.LastIndex(*link.Url, "/reset/")
	if i < 0 {
		t.Fatalf("not a reset link: %q", *link.Url)
	}
	return (*link.Url)[i+len("/reset/"):]
}

func TestResetPassword(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	bob := e.member(admin, "bob")
	var bobMe httpapi.Me
	bob.want(http.StatusOK, "GET", "/me", nil).decode(t, &bobMe)

	// Only admins make reset links.
	if r := bob.do("POST", "/users/"+bobMe.Id+"/reset-link", nil); r.status != http.StatusForbidden {
		t.Fatalf("member making a reset link: %d %s", r.status, r.body)
	}
	if r := admin.do("POST", "/users/nobody/reset-link", nil); r.status != http.StatusNotFound {
		t.Fatalf("reset link for nobody: %d %s", r.status, r.body)
	}

	var link httpapi.ResetLink
	admin.want(http.StatusCreated, "POST", "/users/"+bobMe.Id+"/reset-link", httpapi.CreateResetLinkRequest{ExpiresInHours: ptr(2)}).decode(t, &link)
	if link.UserId != bobMe.Id || !link.ExpiresAt.Equal(e.clock().Add(2*time.Hour)) || !strings.HasPrefix(*link.Url, e.base+"/reset/") {
		t.Fatalf("reset link: %+v", link)
	}
	code := resetCode(t, link)

	var pending []httpapi.ResetLink
	admin.want(http.StatusOK, "GET", "/reset-links", nil).decode(t, &pending)
	if len(pending) != 1 || pending[0].Id != link.Id || pending[0].Url != nil {
		t.Fatalf("pending links: %+v", pending)
	}

	// Anyone with the link can see whose it is.
	stranger := e.client()
	var info httpapi.ResetLinkInfo
	stranger.want(http.StatusOK, "GET", "/reset-links/"+code, nil).decode(t, &info)
	if info.Username != "bob" || info.DisplayName != "Bob" {
		t.Fatalf("reset link info: %+v", info)
	}
	if r := stranger.do("GET", "/reset-links/not-a-code", nil); r.code() != "reset_link_invalid" {
		t.Fatalf("bad code: %d %s", r.status, r.body)
	}
	if r := stranger.do("POST", "/reset-links/"+code+"/password", httpapi.ResetPasswordRequest{NewPassword: "short"}); r.code() != "invalid_input" {
		t.Fatalf("short password: %d %s", r.status, r.body)
	}

	var me httpapi.Me
	r := stranger.want(http.StatusOK, "POST", "/reset-links/"+code+"/password", httpapi.ResetPasswordRequest{NewPassword: "bobs-new-password"})
	r.decode(t, &me)
	if me.Id != bobMe.Id || r.header.Get("Set-Cookie") == "" {
		t.Fatalf("after reset: %+v, cookie %q", me, r.header.Get("Set-Cookie"))
	}
	stranger.want(http.StatusOK, "GET", "/me", nil)
	// Bob's old sessions are gone, and the old password doesn't work.
	if r := bob.do("GET", "/me", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("old session survived a reset: %d", r.status)
	}
	if r := e.client().do("POST", "/auth/login", httpapi.LoginRequest{Username: "bob", Password: "password-bob"}); r.status != http.StatusUnauthorized {
		t.Fatalf("old password after reset: %d", r.status)
	}
	e.client().want(http.StatusOK, "POST", "/auth/login", httpapi.LoginRequest{Username: "bob", Password: "bobs-new-password"})
	admin.want(http.StatusOK, "GET", "/me", nil)

	// It works once.
	if r := e.client().do("POST", "/reset-links/"+code+"/password", httpapi.ResetPasswordRequest{NewPassword: "another-password"}); r.code() != "reset_link_invalid" {
		t.Fatalf("reused link: %d %s", r.status, r.body)
	}
	admin.want(http.StatusOK, "GET", "/reset-links", nil).decode(t, &pending)
	if len(pending) != 0 {
		t.Fatalf("used link still pending: %+v", pending)
	}
}

func TestResetLinkReplaceRevokeExpire(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	bob := e.member(admin, "bob")
	var bobMe httpapi.Me
	bob.want(http.StatusOK, "GET", "/me", nil).decode(t, &bobMe)

	var first, second httpapi.ResetLink
	admin.want(http.StatusCreated, "POST", "/users/"+bobMe.Id+"/reset-link", nil).decode(t, &first)
	admin.want(http.StatusCreated, "POST", "/users/"+bobMe.Id+"/reset-link", nil).decode(t, &second)
	if r := e.client().do("GET", "/reset-links/"+resetCode(t, first), nil); r.code() != "reset_link_invalid" {
		t.Fatalf("replaced link still works: %d %s", r.status, r.body)
	}
	e.client().want(http.StatusOK, "GET", "/reset-links/"+resetCode(t, second), nil)

	admin.want(http.StatusNoContent, "DELETE", "/users/"+bobMe.Id+"/reset-link", nil)
	if r := e.client().do("GET", "/reset-links/"+resetCode(t, second), nil); r.code() != "reset_link_invalid" {
		t.Fatalf("revoked link still works: %d %s", r.status, r.body)
	}
	if r := admin.do("DELETE", "/users/"+bobMe.Id+"/reset-link", nil); r.status != http.StatusNotFound {
		t.Fatalf("revoking twice: %d %s", r.status, r.body)
	}

	var third httpapi.ResetLink
	admin.want(http.StatusCreated, "POST", "/users/"+bobMe.Id+"/reset-link", nil).decode(t, &third)
	e.advance(25 * time.Hour)
	if r := e.client().do("POST", "/reset-links/"+resetCode(t, third)+"/password", httpapi.ResetPasswordRequest{NewPassword: "too-late-now"}); r.code() != "reset_link_invalid" {
		t.Fatalf("expired link: %d %s", r.status, r.body)
	}
	if r := admin.do("POST", "/users/"+bobMe.Id+"/reset-link", httpapi.CreateResetLinkRequest{ExpiresInHours: ptr(500)}); r.code() != "invalid_input" {
		t.Fatalf("too long: %d %s", r.status, r.body)
	}
}

func TestResetPasskey(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	bob := e.member(admin, "bob")
	var bobMe httpapi.Me
	bob.want(http.StatusOK, "GET", "/me", nil).decode(t, &bobMe)
	var link httpapi.ResetLink
	admin.want(http.StatusCreated, "POST", "/users/"+bobMe.Id+"/reset-link", nil).decode(t, &link)
	code := resetCode(t, link)

	phone := newAuthenticator(t, e.base)
	c := e.client()
	var cer httpapi.Ceremony
	c.want(http.StatusOK, "POST", "/reset-links/"+code+"/passkey/begin", nil).decode(t, &cer)
	var me httpapi.Me
	c.want(http.StatusOK, "POST", "/reset-links/"+code+"/passkey/finish", httpapi.FinishCeremony{
		CeremonyId: cer.CeremonyId, Credential: phone.create(t, cer.Options), Name: ptr("Phone"),
	}).decode(t, &me)
	if me.Id != bobMe.Id || me.PasskeyCount != 1 || !me.HasPassword {
		t.Fatalf("after passkey reset: %+v", me)
	}
	if string(phone.userHandle) != bobMe.Id {
		t.Fatalf("passkey user handle %q, want %q", phone.userHandle, bobMe.Id)
	}
	if r := bob.do("GET", "/me", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("old session survived a passkey reset: %d", r.status)
	}
	// The link is used up.
	if r := e.client().do("POST", "/reset-links/"+code+"/passkey/begin", nil); r.code() != "reset_link_invalid" {
		t.Fatalf("reused link: %d %s", r.status, r.body)
	}
	// And the passkey signs in.
	other := e.client()
	other.want(http.StatusOK, "POST", "/auth/passkey/begin", nil).decode(t, &cer)
	other.want(http.StatusOK, "POST", "/auth/passkey/finish", httpapi.FinishCeremony{CeremonyId: cer.CeremonyId, Credential: phone.get(t, cer.Options)}).decode(t, &me)
	if me.Username != "bob" {
		t.Fatalf("passkey login after reset: %+v", me)
	}
}

func TestResetPasskeyLinkUsedMeanwhile(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	bob := e.member(admin, "bob")
	var bobMe httpapi.Me
	bob.want(http.StatusOK, "GET", "/me", nil).decode(t, &bobMe)
	var link httpapi.ResetLink
	admin.want(http.StatusCreated, "POST", "/users/"+bobMe.Id+"/reset-link", nil).decode(t, &link)
	code := resetCode(t, link)

	phone := newAuthenticator(t, e.base)
	c := e.client()
	var cer httpapi.Ceremony
	c.want(http.StatusOK, "POST", "/reset-links/"+code+"/passkey/begin", nil).decode(t, &cer)
	// The link is used for a password before the passkey finishes.
	e.client().want(http.StatusOK, "POST", "/reset-links/"+code+"/password", httpapi.ResetPasswordRequest{NewPassword: "bobs-new-password"})
	if r := c.do("POST", "/reset-links/"+code+"/passkey/finish", httpapi.FinishCeremony{CeremonyId: cer.CeremonyId, Credential: phone.create(t, cer.Options)}); r.code() != "reset_link_invalid" {
		t.Fatalf("finishing with a used link: %d %s", r.status, r.body)
	}
	var me httpapi.Me
	e.client().want(http.StatusOK, "POST", "/auth/login", httpapi.LoginRequest{Username: "bob", Password: "bobs-new-password"}).decode(t, &me)
	if me.PasskeyCount != 0 {
		t.Fatalf("passkey saved with a used link: %+v", me)
	}
}

func TestResetLinkNotForGuests(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	e.advance(time.Since(e.clock())) // guest cookies expire with the pass
	var room httpapi.Room
	admin.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	admin.want(http.StatusOK, "PATCH", "/rooms/"+room.Id, httpapi.UpdateRoomRequest{Guests: &httpapi.RoomGuests{Allowed: true, MaxSongs: 5, CanVote: true}})
	var pass httpapi.GuestPass
	admin.want(http.StatusCreated, "POST", "/rooms/"+room.Id+"/guest-pass", httpapi.CreateGuestPassRequest{ExpiresAt: e.clock().Add(4 * time.Hour)}).decode(t, &pass)
	guest, guestMe := e.joinAsGuest(passToken(t, pass), "Sam")

	if r := admin.do("POST", "/users/"+guestMe.Id+"/reset-link", nil); r.code() != "invalid_input" {
		t.Fatalf("reset link for a guest: %d %s", r.status, r.body)
	}
	if r := guest.do("GET", "/reset-links", nil); r.status != http.StatusForbidden {
		t.Fatalf("guest listing reset links: %d %s", r.status, r.body)
	}
}
