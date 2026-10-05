// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
)

var demoFields = map[string]string{"username": fake.Username, "password": fake.Password}

func TestProviders(t *testing.T) {
	e := newEnv(t)
	c := e.admin()
	var ps []httpapi.ProviderInfo
	c.want(http.StatusOK, "GET", "/providers", nil).decode(t, &ps)
	if len(ps) != 2 || ps[0].Id != "fake" || ps[0].LinkMethod != httpapi.Credentials || ps[1].LinkMethod != httpapi.Oauth2 {
		t.Fatalf("providers: %+v", ps)
	}
	if len(ps[0].Fields) != 2 || ps[0].Fields[1].Kind != httpapi.Secret || !ps[0].Fields[1].Required {
		t.Fatalf("fields: %+v", ps[0].Fields)
	}
	if len(ps[1].Fields) != 0 || len(ps[0].Capabilities.Search) != 4 || ps[0].Playback != httpapi.ProviderInfoPlaybackStream {
		t.Fatalf("oauth provider: %+v", ps[1])
	}
	if !ps[0].Capabilities.Shareable || ps[1].Capabilities.Shareable {
		t.Fatalf("shareable: %v, %v", ps[0].Capabilities.Shareable, ps[1].Capabilities.Shareable)
	}
	if r := e.client().do("GET", "/providers", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", r.status)
	}
}

func TestLinks(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")

	r := alice.do("POST", "/links", httpapi.CreateLinkRequest{Provider: "fake", Fields: map[string]string{"username": "demo", "password": "wrong"}})
	if r.status != http.StatusBadRequest || r.code() != "service_rejected_credentials" {
		t.Fatalf("wrong password: %d %s", r.status, r.body)
	}
	if r := alice.do("POST", "/links", httpapi.CreateLinkRequest{Provider: "fake", Fields: map[string]string{"username": "demo"}}); r.code() != "invalid_input" {
		t.Fatalf("missing field: %d %s", r.status, r.body)
	}
	if r := alice.do("POST", "/links", httpapi.CreateLinkRequest{Provider: "spotify", Fields: demoFields}); r.code() != "unknown_provider" {
		t.Fatalf("unknown provider: %d %s", r.status, r.body)
	}

	r = alice.want(http.StatusCreated, "POST", "/links", httpapi.CreateLinkRequest{Provider: "fake", Fields: demoFields})
	for _, leak := range []string{"token", "password", "credentials", "encrypted"} {
		if bytes.Contains(r.body, []byte(leak)) {
			t.Fatalf("response leaks %q: %s", leak, r.body)
		}
	}
	var l httpapi.ServiceLink
	r.decode(t, &l)
	if l.Provider != "fake" || l.Status != httpapi.ServiceLinkStatusOk || l.AccountLabel == "" || l.LastOkAt == nil {
		t.Fatalf("link: %+v", l)
	}

	var list []httpapi.ServiceLink
	alice.want(http.StatusOK, "GET", "/links", nil).decode(t, &list)
	if len(list) != 1 || list[0].Id != l.Id {
		t.Fatalf("alice's links: %+v", list)
	}
	bob.want(http.StatusOK, "GET", "/links", nil).decode(t, &list)
	if len(list) != 0 {
		t.Fatalf("bob sees alice's links: %+v", list)
	}
	if r := bob.do("DELETE", "/links/"+l.Id, nil); r.status != http.StatusNotFound {
		t.Fatalf("bob unlinking alice's link: %d", r.status)
	}
	if r := bob.do("PUT", "/links/"+l.Id, httpapi.RelinkRequest{Fields: demoFields}); r.status != http.StatusNotFound {
		t.Fatalf("bob relinking alice's link: %d", r.status)
	}

	// The service revokes access; using the link flags it.
	e.fake.Revoke(fake.Username)
	sess, err := e.links.Open(t.Context(), l.Id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Search(t.Context(), provider.SearchQuery{Text: "x"}); !errors.Is(err, provider.ErrAuthExpired) {
		t.Fatalf("revoked link: %v", err)
	}
	alice.want(http.StatusOK, "GET", "/links", nil).decode(t, &list)
	if list[0].Status != httpapi.ServiceLinkStatusNeedsRelink || list[0].StatusDetail == nil {
		t.Fatalf("after revocation: %+v", list[0])
	}
	alice.want(http.StatusOK, "PUT", "/links/"+l.Id, httpapi.RelinkRequest{Fields: demoFields}).decode(t, &l)
	if l.Status != httpapi.ServiceLinkStatusOk {
		t.Fatalf("after relink: %+v", l)
	}

	alice.want(http.StatusNoContent, "DELETE", "/links/"+l.Id, nil)
	alice.want(http.StatusOK, "GET", "/links", nil).decode(t, &list)
	if len(list) != 0 {
		t.Fatalf("after unlink: %+v", list)
	}
}

// noRedirects stops the client at the callback's redirect.
func noRedirects(c *client) *client {
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

func TestOAuthLink(t *testing.T) {
	e := newEnv(t)
	alice := noRedirects(e.admin())

	if r := alice.do("POST", "/links/oauth", httpapi.BeginOAuthLinkRequest{}); r.code() != "invalid_input" {
		t.Fatalf("no provider: %d %s", r.status, r.body)
	}
	if r := alice.do("POST", "/links/oauth", httpapi.BeginOAuthLinkRequest{Provider: ptr("fake")}); r.code() != "wrong_link_method" {
		t.Fatalf("form provider: %d %s", r.status, r.body)
	}
	begin := func() url.Values {
		t.Helper()
		var out httpapi.BeginOAuthLink200JSONResponse
		alice.want(http.StatusOK, "POST", "/links/oauth", httpapi.BeginOAuthLinkRequest{Provider: ptr("oauthy")}).decode(t, &out)
		u, err := url.Parse(out.AuthUrl)
		if err != nil {
			t.Fatal(err)
		}
		if got := u.Query().Get("redirect_uri"); got != e.base+"/api/links/oauth/callback" {
			t.Fatalf("redirect_uri %q", got)
		}
		return u.Query()
	}
	location := func(c *client, q url.Values) url.Values {
		t.Helper()
		r := c.do("GET", "/links/oauth/callback?"+q.Encode(), nil)
		if r.status != http.StatusSeeOther {
			t.Fatalf("callback: %d %s", r.status, r.body)
		}
		u, err := url.Parse(r.header.Get("Location"))
		if err != nil || u.Scheme+"://"+u.Host+u.Path != e.base+"/settings/services" {
			t.Fatalf("Location %q", r.header.Get("Location"))
		}
		return u.Query()
	}

	q := begin()
	if got := location(noRedirects(e.client()), url.Values{"state": {q.Get("state")}, "code": {fake.Code}}); got.Get("link_error") != "unauthenticated" {
		t.Fatalf("signed out: %v", got)
	}
	if got := location(alice, url.Values{"state": {q.Get("state")}, "error": {"access_denied"}}); got.Get("link_error") != "denied" {
		t.Fatalf("denied: %v", got)
	}
	if got := location(alice, url.Values{"state": {"forged"}, "code": {fake.Code}}); got.Get("link_error") != "oauth_state" {
		t.Fatalf("forged state: %v", got)
	}
	got := location(alice, url.Values{"state": {q.Get("state")}, "code": {fake.Code}})
	if got.Get("linked") == "" {
		t.Fatalf("success: %v", got)
	}
	var list []httpapi.ServiceLink
	alice.want(http.StatusOK, "GET", "/links", nil).decode(t, &list)
	if len(list) != 1 || list[0].Id != got.Get("linked") || list[0].Provider != "oauthy" {
		t.Fatalf("links: %+v", list)
	}
	// A personal subscription can't be shared.
	if r := alice.do("PATCH", "/links/"+list[0].Id, httpapi.UpdateLinkRequest{Shared: true}); r.status != http.StatusBadRequest || r.code() != "not_shareable" {
		t.Fatalf("sharing oauthy: %d %s", r.status, r.body)
	}
	// Re-linking by link ID.
	var out httpapi.BeginOAuthLink200JSONResponse
	alice.want(http.StatusOK, "POST", "/links/oauth", httpapi.BeginOAuthLinkRequest{LinkId: &list[0].Id}).decode(t, &out)
}

func TestSharedLinks(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	aliceID := me(t, alice).Id
	l := linkFake(t, alice)
	room := e.room(t, aliceID)
	queuePath := "/rooms/" + room.ID + "/queue"

	var link httpapi.ServiceLink
	listed := func(c *client, query string) []httpapi.ServiceLink {
		t.Helper()
		var ls []httpapi.ServiceLink
		c.want(http.StatusOK, "GET", "/links"+query, nil).decode(t, &ls)
		return ls
	}
	if ls := listed(alice, ""); len(ls) != 1 || ls[0].Shared || ls[0].OwnerId != aliceID {
		t.Fatalf("alice's links: %+v", ls)
	}

	// Not shared: bob can't see, browse or queue from it.
	if ls := listed(bob, "?include=shared"); len(ls) != 0 {
		t.Fatalf("bob sees an unshared link: %+v", ls)
	}
	if res := search(t, bob, "null"); len(res.Groups) != 0 {
		t.Fatalf("bob searches an unshared link: %+v", res.Groups)
	}
	if r := bob.do("POST", queuePath, addReq(l, "t01")); r.status != http.StatusNotFound {
		t.Fatalf("bob queues from an unshared link: %d %s", r.status, r.body)
	}
	// Nor share it himself.
	if r := bob.do("PATCH", "/links/"+l, httpapi.UpdateLinkRequest{Shared: true}); r.status != http.StatusNotFound {
		t.Fatalf("bob shares alice's link: %d %s", r.status, r.body)
	}

	// Shared: bob gets all of it, and the link still belongs to alice.
	alice.want(http.StatusOK, "PATCH", "/links/"+l, httpapi.UpdateLinkRequest{Shared: true}).decode(t, &link)
	if !link.Shared {
		t.Fatalf("shared: %+v", link)
	}
	if ls := listed(bob, ""); len(ls) != 0 {
		t.Fatalf("bob's own links include alice's: %+v", ls)
	}
	if ls := listed(bob, "?include=shared"); len(ls) != 1 || ls[0].Id != l || ls[0].OwnerId != aliceID || !ls[0].Shared {
		t.Fatalf("bob's usable links: %+v", ls)
	}
	res := search(t, bob, "null")
	if len(res.Groups) != 1 || res.Groups[0].LinkId != l || res.Groups[0].OwnerId != aliceID || len(res.Groups[0].Tracks) == 0 {
		t.Fatalf("bob's search: %+v", res.Groups)
	}
	tr := res.Groups[0].Tracks[0]
	bob.want(http.StatusOK, "GET", "/links/"+l+"/albums/"+*tr.Album.Id, nil)
	bob.want(http.StatusOK, "GET", "/links/"+l+"/artists/"+*tr.Artists[0].Id, nil)
	bob.want(http.StatusOK, "GET", "/links/"+l+"/artwork?ref="+url.QueryEscape(*tr.Artwork), nil)
	var snap httpapi.QueueSnapshot
	bob.want(http.StatusOK, "POST", queuePath, addReq(l, tr.TrackId)).decode(t, &snap)
	if len(snap.Items) != 1 || snap.Items[0].AddedBy == aliceID || snap.Items[0].Track.LinkId == nil || *snap.Items[0].Track.LinkId != l {
		t.Fatalf("bob's queued song: %+v", snap.Items)
	}
	if r := bob.do("DELETE", "/links/"+l, nil); r.status != http.StatusNotFound {
		t.Fatalf("bob unlinks alice's shared link: %d", r.status)
	}

	// Unshared again: gone for bob, but what he queued stays.
	alice.want(http.StatusOK, "PATCH", "/links/"+l, httpapi.UpdateLinkRequest{Shared: false})
	if res := search(t, bob, "null"); len(res.Groups) != 0 {
		t.Fatalf("bob searches an unshared link: %+v", res.Groups)
	}
	bob.want(http.StatusOK, "GET", queuePath, nil).decode(t, &snap)
	if len(snap.Items) != 1 {
		t.Fatalf("queue after unsharing: %+v", snap.Items)
	}
}
