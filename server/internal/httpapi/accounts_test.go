// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/admin"
	"github.com/madeofpendletonwool/syncphony/server/internal/artwork"
	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/avatar"
	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/linernotes"
	"github.com/madeofpendletonwool/syncphony/server/internal/links"
	"github.com/madeofpendletonwool/syncphony/server/internal/lyrics"
	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/nights"
	"github.com/madeofpendletonwool/syncphony/server/internal/palette"
	"github.com/madeofpendletonwool/syncphony/server/internal/playback"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/suggest"
	"github.com/madeofpendletonwool/syncphony/server/internal/vault"
)

// env is a running API server with a fresh database.
type env struct {
	t        *testing.T
	srv      *httptest.Server
	base     string // the public base URL the server believes it has
	svc      *auth.Service
	server   *admin.Service
	links    *links.Service
	db       *store.Store
	bus      *realtime.Local
	rooms    *rooms.Service
	playback *playback.Engine
	mb       *musicbrainz.Service
	palettes *palette.Service
	nights   *nights.Service
	api      *httpapi.Server
	fake     *fake.Provider // links with a form
	oauth    *fake.Provider // links with OAuth2
	setupURL string

	mu  sync.Mutex
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	var h http.Handler
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	t.Cleanup(e.srv.Close)
	// Passkeys need a domain, not an IP, as the relying party.
	e.base = strings.Replace(e.srv.URL, "127.0.0.1", "localhost", 1)

	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e.svc, err = auth.New(db, auth.Config{
		BaseURL:      e.base,
		Now:          e.clock,
		PasswordCost: &auth.PasswordCost{MemoryKiB: 64, Time: 1, Threads: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.setupURL, err = e.svc.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.fake = fake.New(fake.Options{})
	// OAuthy stands in for a personal subscription: its links can't be shared.
	e.oauth = fake.New(fake.Options{ID: "oauthy", Name: "OAuthy", Link: provider.LinkOAuth2, Private: true, Now: e.clock})
	reg, err := provider.NewRegistry(e.fake, e.oauth)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := vault.ParseKey(vault.GenerateKey())
	e.db, e.bus = db, realtime.NewLocal()
	e.server = admin.New(db, filepath.Join(t.TempDir(), "backups"))
	e.rooms = rooms.New(db, e.bus)
	e.links = links.New(db, vault.New(key), reg, links.Config{BaseURL: e.base, Now: e.clock, Notifier: links.BusNotifier{Bus: e.bus}})
	qs := queue.New(db, e.rooms, e.links)
	mbURL := newMusicBrainz(t).URL
	e.mb = musicbrainz.New(db, musicbrainz.Options{BaseURL: mbURL, CoverArtURL: mbURL, Interval: -1, Now: e.clock})
	art := artwork.New(e.links, e.mb)
	e.palettes = palette.New(db, art)
	qs.OnAdd = func(ts []provider.Track) {
		e.mb.Enqueue(ts...)
		e.palettes.Enqueue(ts...)
	}
	go e.mb.Run(t.Context())
	go e.palettes.Run(t.Context())
	presence := realtime.NewPresence()
	e.playback = playback.New(db, e.rooms, qs, e.links, playback.Config{Now: e.clock, Presence: presence, Matcher: match.New(db, e.links, presence)})
	t.Cleanup(e.playback.Close)
	ly := lyrics.New(db, lyrics.Options{LRCLIB: &lyrics.LRCLIB{BaseURL: newLRCLIB(t).URL}, Now: e.clock})
	notes := linernotes.New(db, e.mb, linernotes.Options{WikipediaURL: mbURL, WikidataURL: mbURL, Now: e.clock})
	e.nights = nights.New(db, e.bus)
	e.nights.Now = e.clock
	api := &httpapi.Server{
		Version: "test", StartedAt: e.clock(), Admin: e.server, Auth: e.svc, Links: e.links, Lyrics: ly, LinerNotes: notes, Artwork: art, Palettes: e.palettes, Rooms: e.rooms, Queue: qs, Playback: e.playback, Nights: e.nights, Bus: e.bus, Presence: presence,
		Suggest: suggest.New(db, e.rooms, e.links),
		BaseURL: e.base, PingEvery: 50 * time.Millisecond,
	}
	e.api = api
	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler())
	mux.Handle("GET /ws/rooms/{id}", api.RoomSocket())
	h = mux
	return e
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = e.now.Add(d)
}

// client is a browser-ish client with its own cookie jar.
type client struct {
	e    *env
	http *http.Client
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, http: &http.Client{Jar: jar}}
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decoding %s: %v", r.body, err)
	}
}

func (r response) code() string {
	var e httpapi.Error
	_ = json.Unmarshal(r.body, &e)
	return e.Code
}

func (c *client) do(method, path string, body any, headers ...string) response {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(c.e.t.Context(), method, c.e.srv.URL+"/api"+path, rd)
	if err != nil {
		c.e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		c.e.t.Fatal(err)
	}
	return response{status: res.StatusCode, header: res.Header, body: b}
}

func (c *client) want(status int, method, path string, body any) response {
	c.e.t.Helper()
	r := c.do(method, path, body)
	if r.status != status {
		c.e.t.Fatalf("%s %s: status %d, want %d: %s", method, path, r.status, status, r.body)
	}
	return r
}

func inviteCode(t *testing.T, link string) string {
	t.Helper()
	i := strings.LastIndex(link, "/invite/")
	if i < 0 {
		t.Fatalf("not an invite link: %q", link)
	}
	return link[i+len("/invite/"):]
}

// signup creates an account with a password through c.
func (c *client) signup(invite, username string) httpapi.Me {
	c.e.t.Helper()
	var me httpapi.Me
	c.want(http.StatusCreated, "POST", "/auth/signup", httpapi.SignupRequest{
		Invite: invite, Username: username, DisplayName: strings.ToUpper(username[:1]) + username[1:], Password: "password-" + username,
	}).decode(c.e.t, &me)
	return me
}

// admin signs up the first user with the setup link.
func (e *env) admin() *client {
	e.t.Helper()
	c := e.client()
	c.signup(inviteCode(e.t, e.setupURL), "admin")
	return c
}

// member creates an invite as admin and signs up a member with it.
func (e *env) member(admin *client, username string) *client {
	e.t.Helper()
	var inv httpapi.Invite
	admin.want(http.StatusCreated, "POST", "/invites", httpapi.CreateInviteRequest{}).decode(e.t, &inv)
	c := e.client()
	c.signup(inv.Code, username)
	return c
}

func TestHealth(t *testing.T) {
	e := newEnv(t)
	var h httpapi.Health
	e.client().want(http.StatusOK, "GET", "/healthz", nil).decode(t, &h)
	if h.Status != httpapi.HealthStatusOk || h.Version != "test" {
		t.Fatalf("got %+v", h)
	}
}

func TestSetupAndInvites(t *testing.T) {
	e := newEnv(t)
	code := inviteCode(t, e.setupURL)
	if !strings.HasPrefix(e.setupURL, e.base+"/invite/") {
		t.Fatalf("setup link %q", e.setupURL)
	}
	anon := e.client()
	var info httpapi.InviteInfo
	anon.want(http.StatusOK, "GET", "/invites/"+code, nil).decode(t, &info)
	if info.Role != httpapi.Admin {
		t.Fatalf("setup invite role %q", info.Role)
	}

	admin := e.client()
	r := admin.want(http.StatusCreated, "POST", "/auth/signup", httpapi.SignupRequest{
		Invite: code, Username: "  Admin ", DisplayName: "The Admin", Password: "correct horse",
	})
	var me httpapi.Me
	r.decode(t, &me)
	if me.Role != httpapi.Admin || me.Username != "admin" || !me.HasPassword || me.PasskeyCount != 0 || me.Color == "" {
		t.Fatalf("admin: %+v", me)
	}
	cookie := r.header.Get("Set-Cookie")
	for _, want := range []string{httpapi.SessionCookie + "=", "HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("cookie %q lacks %q", cookie, want)
		}
	}
	if strings.Contains(cookie, "Secure") {
		t.Errorf("cookie %q is Secure on an http:// base URL", cookie)
	}
	if link, err := e.svc.Bootstrap(t.Context()); err != nil || link != "" {
		t.Fatalf("Bootstrap with users: %q, %v", link, err)
	}

	// The setup invite is used up.
	if r := anon.do("GET", "/invites/"+code, nil); r.status != http.StatusNotFound || r.code() != "invite_invalid" {
		t.Fatalf("used setup invite: %d %s", r.status, r.body)
	}
	if r := anon.do("POST", "/auth/signup", httpapi.SignupRequest{Invite: code, Username: "mallory", DisplayName: "M", Password: "password123"}); r.status != http.StatusNotFound {
		t.Fatalf("signup with used invite: %d %s", r.status, r.body)
	}

	// Admin invites a member.
	var inv httpapi.Invite
	admin.want(http.StatusCreated, "POST", "/invites", httpapi.CreateInviteRequest{}).decode(t, &inv)
	if inv.Role != httpapi.Member || inv.Url != e.base+"/invite/"+inv.Code || !inv.ExpiresAt.Equal(e.clock().Add(7*24*time.Hour)) {
		t.Fatalf("invite: %+v", inv)
	}
	bob := e.client()
	if r := bob.do("POST", "/auth/signup", httpapi.SignupRequest{Invite: inv.Code, Username: "admin", DisplayName: "Bob", Password: "password123"}); r.code() != "username_taken" {
		t.Fatalf("taken username: %d %s", r.status, r.body)
	}
	if r := bob.do("POST", "/auth/signup", httpapi.SignupRequest{Invite: inv.Code, Username: "b", DisplayName: "Bob", Password: "password123"}); r.code() != "invalid_input" {
		t.Fatalf("short username: %d %s", r.status, r.body)
	}
	bobMe := bob.signup(inv.Code, "bob")
	if bobMe.Role != httpapi.Member || bobMe.Color == me.Color {
		t.Fatalf("bob: %+v (admin color %s)", bobMe, me.Color)
	}

	// Members can't manage invites.
	if r := bob.do("POST", "/invites", httpapi.CreateInviteRequest{}); r.status != http.StatusForbidden {
		t.Fatalf("member creating invite: %d", r.status)
	}
	if r := bob.do("GET", "/invites", nil); r.status != http.StatusForbidden {
		t.Fatalf("member listing invites: %d", r.status)
	}

	var invites []httpapi.Invite
	admin.want(http.StatusOK, "GET", "/invites", nil).decode(t, &invites)
	i := slices.IndexFunc(invites, func(x httpapi.Invite) bool { return x.Code == inv.Code })
	if len(invites) != 2 || i < 0 || invites[i].UsedBy == nil || *invites[i].UsedBy != bobMe.Id {
		t.Fatalf("invites: %+v", invites)
	}
	var users []httpapi.User
	bob.want(http.StatusOK, "GET", "/users", nil).decode(t, &users)
	if len(users) != 2 || users[0].Username != "admin" {
		t.Fatalf("users: %+v", users)
	}

	// Revoking.
	admin.want(http.StatusCreated, "POST", "/invites", httpapi.CreateInviteRequest{Role: ptr(httpapi.Admin), ExpiresInHours: ptr(1)}).decode(t, &inv)
	admin.want(http.StatusNoContent, "DELETE", "/invites/"+inv.Code, nil)
	if r := anon.do("GET", "/invites/"+inv.Code, nil); r.status != http.StatusNotFound {
		t.Fatalf("revoked invite: %d", r.status)
	}
	// Expiry.
	admin.want(http.StatusCreated, "POST", "/invites", httpapi.CreateInviteRequest{ExpiresInHours: ptr(1)}).decode(t, &inv)
	e.advance(time.Hour)
	if r := anon.do("GET", "/invites/"+inv.Code, nil); r.status != http.StatusNotFound {
		t.Fatalf("expired invite: %d", r.status)
	}
}

func ptr[T any](v T) *T { return &v }

func TestUnauthenticated(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	if r := c.do("GET", "/me", nil); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
		t.Fatalf("no cookie: %d %s", r.status, r.body)
	}
	r := c.do("GET", "/me", nil, "Cookie", httpapi.SessionCookie+"=forged")
	if r.status != http.StatusUnauthorized || !strings.Contains(r.header.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("bad cookie: %d, Set-Cookie %q", r.status, r.header.Get("Set-Cookie"))
	}
}

func TestLoginAndRateLimit(t *testing.T) {
	e := newEnv(t)
	e.admin()
	c := e.client()
	var me httpapi.Me
	c.want(http.StatusOK, "POST", "/auth/login", httpapi.LoginRequest{Username: "ADMIN", Password: "password-admin"}).decode(t, &me)
	if me.Username != "admin" {
		t.Fatalf("login: %+v", me)
	}
	c.want(http.StatusOK, "GET", "/me", nil)

	if r := c.do("POST", "/auth/login", httpapi.LoginRequest{Username: "nobody", Password: "whatever1"}); r.code() != "invalid_credentials" {
		t.Fatalf("unknown user: %d %s", r.status, r.body)
	}
	for range 10 {
		if r := c.do("POST", "/auth/login", httpapi.LoginRequest{Username: "admin", Password: "wrong-password"}); r.status != http.StatusUnauthorized {
			t.Fatalf("wrong password: %d %s", r.status, r.body)
		}
	}
	r := c.do("POST", "/auth/login", httpapi.LoginRequest{Username: "admin", Password: "password-admin"})
	if r.status != http.StatusTooManyRequests || r.header.Get("Retry-After") == "" {
		t.Fatalf("after 10 failures: %d, Retry-After %q", r.status, r.header.Get("Retry-After"))
	}
	e.advance(15 * time.Minute)
	c.want(http.StatusOK, "POST", "/auth/login", httpapi.LoginRequest{Username: "admin", Password: "password-admin"})
}

func TestLogout(t *testing.T) {
	e := newEnv(t)
	c := e.admin()
	u := mustURL(t, e.srv.URL)
	token := c.http.Jar.Cookies(u)[0].Value
	r := c.want(http.StatusNoContent, "POST", "/auth/logout", nil)
	if !strings.Contains(r.header.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout didn't clear the cookie: %q", r.header.Get("Set-Cookie"))
	}
	if r := e.client().do("GET", "/me", nil, "Cookie", httpapi.SessionCookie+"="+token); r.status != http.StatusUnauthorized {
		t.Fatalf("old token after logout: %d", r.status)
	}
}

func TestSessionSlides(t *testing.T) {
	e := newEnv(t)
	c := e.admin()
	if r := c.want(http.StatusOK, "GET", "/me", nil); r.header.Get("Set-Cookie") != "" {
		t.Fatal("fresh session re-sent its cookie")
	}
	e.advance(29 * 24 * time.Hour)
	if r := c.want(http.StatusOK, "GET", "/me", nil); r.header.Get("Set-Cookie") == "" {
		t.Fatal("aging session wasn't extended")
	}
	e.advance(29 * 24 * time.Hour) // 58 days after sign-in, 29 after last use
	c.want(http.StatusOK, "GET", "/me", nil)
	e.advance(31 * 24 * time.Hour)
	if r := c.do("GET", "/me", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("idle session: %d", r.status)
	}
}

func TestSetPassword(t *testing.T) {
	e := newEnv(t)
	a := e.admin()
	b := e.client()
	b.want(http.StatusOK, "POST", "/auth/login", httpapi.LoginRequest{Username: "admin", Password: "password-admin"})

	if r := a.do("PUT", "/me/password", httpapi.SetPasswordRequest{NewPassword: "new-password"}); r.code() != "invalid_input" {
		t.Fatalf("no current password: %d %s", r.status, r.body)
	}
	if r := a.do("PUT", "/me/password", httpapi.SetPasswordRequest{CurrentPassword: ptr("nope-nope"), NewPassword: "new-password"}); r.code() != "wrong_password" {
		t.Fatalf("wrong current password: %d %s", r.status, r.body)
	}
	a.want(http.StatusNoContent, "PUT", "/me/password", httpapi.SetPasswordRequest{CurrentPassword: ptr("password-admin"), NewPassword: "new-password"})
	a.want(http.StatusOK, "GET", "/me", nil)
	if r := b.do("GET", "/me", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("other session survived a password change: %d", r.status)
	}
	e.client().want(http.StatusOK, "POST", "/auth/login", httpapi.LoginRequest{Username: "admin", Password: "new-password"})
	if r := a.do("DELETE", "/me/password", nil); r.code() != "last_credential" {
		t.Fatalf("removing the only credential: %d %s", r.status, r.body)
	}
}

func TestProfile(t *testing.T) {
	e := newEnv(t)
	c := e.admin()
	if r := c.do("PATCH", "/me", httpapi.ProfileUpdate{Color: ptr("red")}); r.code() != "invalid_input" {
		t.Fatalf("bad color: %d %s", r.status, r.body)
	}
	if r := c.do("PATCH", "/me", httpapi.ProfileUpdate{Avatar: ptr("javascript:alert(1)")}); r.code() != "invalid_input" {
		t.Fatalf("bad avatar: %d %s", r.status, r.body)
	}
	var me httpapi.Me
	c.want(http.StatusOK, "PATCH", "/me", httpapi.ProfileUpdate{
		DisplayName: ptr("  Big Boss "), Color: ptr("#ABCDEF"), Avatar: ptr("https://example.com/me.png"),
	}).decode(t, &me)
	if me.DisplayName != "Big Boss" || me.Color != "#abcdef" || me.Avatar == nil || *me.Avatar != "https://example.com/me.png" {
		t.Fatalf("updated: %+v", me)
	}
	var cleared httpapi.Me
	c.want(http.StatusOK, "PATCH", "/me", httpapi.ProfileUpdate{Avatar: ptr("")}).decode(t, &cleared)
	if cleared.Avatar != nil || cleared.DisplayName != "Big Boss" {
		t.Fatalf("after clearing avatar: %+v", cleared)
	}
}

func TestCrossOriginBlocked(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	login := httpapi.LoginRequest{Username: "x", Password: "y"}
	if r := c.do("POST", "/auth/login", login, "Sec-Fetch-Site", "cross-site"); r.status != http.StatusForbidden || r.code() != "cross_origin" {
		t.Fatalf("cross-site POST: %d %s", r.status, r.body)
	}
	if r := c.do("POST", "/auth/login", login, "Origin", "https://evil.example"); r.status != http.StatusForbidden {
		t.Fatalf("foreign Origin: %d %s", r.status, r.body)
	}
	if r := c.do("POST", "/auth/login", login, "Origin", e.base); r.status != http.StatusUnauthorized {
		t.Fatalf("own origin: %d %s", r.status, r.body)
	}
	if r := c.do("GET", "/healthz", nil, "Sec-Fetch-Site", "cross-site"); r.status != http.StatusOK {
		t.Fatalf("cross-site GET: %d", r.status)
	}
}

func TestPasskeys(t *testing.T) {
	e := newEnv(t)
	code := inviteCode(t, e.setupURL)
	phone := newAuthenticator(t, e.base)
	c := e.client()

	// Sign up with a passkey and no password.
	var cer httpapi.Ceremony
	c.want(http.StatusOK, "POST", "/auth/signup/passkey/begin", httpapi.PasskeySignupRequest{Invite: code, Username: "alice", DisplayName: "Alice"}).decode(t, &cer)
	finish := httpapi.FinishCeremony{CeremonyId: cer.CeremonyId, Credential: phone.create(t, cer.Options), Name: ptr("Phone")}
	var me httpapi.Me
	c.want(http.StatusCreated, "POST", "/auth/signup/passkey/finish", finish).decode(t, &me)
	if me.Username != "alice" || me.Role != httpapi.Admin || me.HasPassword || me.PasskeyCount != 1 {
		t.Fatalf("passkey signup: %+v", me)
	}
	if r := c.do("POST", "/auth/signup/passkey/finish", finish); r.code() != "ceremony_expired" {
		t.Fatalf("replayed ceremony: %d %s", r.status, r.body)
	}

	var pks []httpapi.Passkey
	c.want(http.StatusOK, "GET", "/me/passkeys", nil).decode(t, &pks)
	if len(pks) != 1 || pks[0].Name != "Phone" || pks[0].Id != b64.EncodeToString(phone.credID) {
		t.Fatalf("passkeys: %+v", pks)
	}
	if r := c.do("DELETE", "/me/passkeys/"+pks[0].Id, nil); r.code() != "last_credential" {
		t.Fatalf("deleting the only passkey: %d %s", r.status, r.body)
	}

	// Sign in with it, no username.
	other := e.client()
	other.want(http.StatusOK, "POST", "/auth/passkey/begin", nil).decode(t, &cer)
	other.want(http.StatusOK, "POST", "/auth/passkey/finish", httpapi.FinishCeremony{CeremonyId: cer.CeremonyId, Credential: phone.get(t, cer.Options)}).decode(t, &me)
	if me.Username != "alice" {
		t.Fatalf("passkey login: %+v", me)
	}

	// A forged signature fails.
	other.want(http.StatusOK, "POST", "/auth/passkey/begin", nil).decode(t, &cer)
	forged := phone.get(t, cer.Options)
	forged["response"].(map[string]any)["signature"] = b64.EncodeToString([]byte("not a signature"))
	if r := other.do("POST", "/auth/passkey/finish", httpapi.FinishCeremony{CeremonyId: cer.CeremonyId, Credential: forged}); r.code() != "invalid_credentials" {
		t.Fatalf("forged assertion: %d %s", r.status, r.body)
	}

	// Add a second passkey, rename it, then remove the first.
	laptop := newAuthenticator(t, e.base)
	c.want(http.StatusOK, "POST", "/me/passkeys/begin", nil).decode(t, &cer)
	var pk httpapi.Passkey
	c.want(http.StatusCreated, "POST", "/me/passkeys/finish", httpapi.FinishCeremony{CeremonyId: cer.CeremonyId, Credential: laptop.create(t, cer.Options)}).decode(t, &pk)
	if pk.Name != "Passkey" {
		t.Fatalf("default name %q", pk.Name)
	}
	if string(laptop.userHandle) != me.Id {
		t.Fatalf("added passkey has user handle %q, want %q", laptop.userHandle, me.Id)
	}
	c.want(http.StatusNoContent, "PATCH", "/me/passkeys/"+pk.Id, map[string]string{"name": "Laptop"})
	c.want(http.StatusNoContent, "DELETE", "/me/passkeys/"+pks[0].Id, nil)
	c.want(http.StatusOK, "GET", "/me/passkeys", nil).decode(t, &pks)
	if len(pks) != 1 || pks[0].Name != "Laptop" {
		t.Fatalf("after changes: %+v", pks)
	}

	// Someone else can't touch alice's passkeys.
	bob := e.member(c, "bob")
	if r := bob.do("DELETE", "/me/passkeys/"+pk.Id, nil); r.status != http.StatusNotFound {
		t.Fatalf("deleting someone else's passkey: %d", r.status)
	}

	// The removed passkey no longer signs in.
	other.want(http.StatusOK, "POST", "/auth/passkey/begin", nil).decode(t, &cer)
	if r := other.do("POST", "/auth/passkey/finish", httpapi.FinishCeremony{CeremonyId: cer.CeremonyId, Credential: phone.get(t, cer.Options)}); r.status != http.StatusUnauthorized {
		t.Fatalf("removed passkey: %d %s", r.status, r.body)
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSetPasswordKeepsSessions(t *testing.T) {
	e := newEnv(t)
	a := e.admin()
	b := e.client()
	b.want(http.StatusOK, "POST", "/auth/login", httpapi.LoginRequest{Username: "admin", Password: "password-admin"})
	a.want(http.StatusNoContent, "PUT", "/me/password", httpapi.SetPasswordRequest{
		CurrentPassword: ptr("password-admin"), NewPassword: "new-password", SignOutOtherSessions: ptr(false),
	})
	b.want(http.StatusOK, "GET", "/me", nil)
}

func TestSignedInDevices(t *testing.T) {
	e := newEnv(t)
	a := e.admin()
	login := func(ua string) *client {
		c := e.client()
		r := c.do("POST", "/auth/login", httpapi.LoginRequest{Username: "admin", Password: "password-admin"}, "User-Agent", ua)
		if r.status != http.StatusOK {
			t.Fatalf("login: %d %s", r.status, r.body)
		}
		return c
	}
	e.advance(time.Minute)
	phone := login("Phone/1.0")
	e.advance(time.Minute)
	laptop := login("Laptop/1.0")
	// Someone else's sessions aren't listed or revocable.
	other := e.member(a, "bob")

	var list []httpapi.SignedInSession
	phone.want(http.StatusOK, "GET", "/me/sessions", nil).decode(t, &list)
	if len(list) != 3 {
		t.Fatalf("sessions: %+v", list)
	}
	var current, laptopID string
	for _, s := range list {
		if s.Current {
			if current != "" {
				t.Fatalf("two current sessions: %+v", list)
			}
			current = s.Id
			if s.UserAgent != "Phone/1.0" {
				t.Fatalf("current session is %q, want the phone", s.UserAgent)
			}
		}
		if s.UserAgent == "Laptop/1.0" {
			laptopID = s.Id
		}
	}
	if current == "" || laptopID == "" {
		t.Fatalf("sessions: %+v", list)
	}

	if r := other.do("DELETE", "/me/sessions/"+laptopID, nil); r.status != http.StatusNotFound {
		t.Fatalf("revoking someone else's session: %d %s", r.status, r.body)
	}
	if r := phone.do("DELETE", "/me/sessions/not-base64!", nil); r.status != http.StatusNotFound {
		t.Fatalf("revoking a bad id: %d %s", r.status, r.body)
	}
	phone.want(http.StatusNoContent, "DELETE", "/me/sessions/"+laptopID, nil)
	if r := laptop.do("GET", "/me", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("revoked session still works: %d", r.status)
	}
	a.want(http.StatusOK, "GET", "/me", nil)

	phone.want(http.StatusNoContent, "DELETE", "/me/sessions", nil)
	if r := a.do("GET", "/me", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("other session survived sign out everywhere else: %d", r.status)
	}
	phone.want(http.StatusOK, "GET", "/me/sessions", nil).decode(t, &list)
	if len(list) != 1 || !list[0].Current {
		t.Fatalf("after signing out elsewhere: %+v", list)
	}
	other.want(http.StatusOK, "GET", "/me", nil)
}

func (c *client) upload(path, contentType string, data []byte) response {
	c.e.t.Helper()
	req, err := http.NewRequestWithContext(c.e.t.Context(), "PUT", c.e.srv.URL+"/api"+path, bytes.NewReader(data))
	if err != nil {
		c.e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	res, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return response{status: res.StatusCode, header: res.Header, body: b}
}

func TestAvatarUpload(t *testing.T) {
	e := newEnv(t)
	a := e.admin()
	bob := e.member(a, "bob")

	if r := a.upload("/me/avatar", "image/png", []byte("not an image")); r.code() != "invalid_input" {
		t.Fatalf("not an image: %d %s", r.status, r.body)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 600, 400))); err != nil {
		t.Fatal(err)
	}
	var me httpapi.Me
	r := a.upload("/me/avatar", "image/png", buf.Bytes())
	if r.status != http.StatusOK {
		t.Fatalf("upload: %d %s", r.status, r.body)
	}
	r.decode(t, &me)
	if me.Avatar == nil || !strings.HasPrefix(*me.Avatar, "/api/users/"+me.Id+"/avatar?v=") {
		t.Fatalf("avatar after upload: %v", me.Avatar)
	}

	// Others can see it.
	img := bob.want(http.StatusOK, "GET", strings.TrimPrefix(*me.Avatar, "/api"), nil)
	if ct := img.header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Fatalf("avatar content type %q", ct)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(img.body))
	if err != nil || cfg.Width != avatar.Size || cfg.Height != avatar.Size {
		t.Fatalf("served avatar: %+v %v", cfg, err)
	}

	// Removing it deletes the upload.
	var cleared httpapi.Me
	a.want(http.StatusOK, "PATCH", "/me", httpapi.ProfileUpdate{Avatar: ptr("")}).decode(t, &cleared)
	if cleared.Avatar != nil {
		t.Fatalf("avatar after removing: %v", *cleared.Avatar)
	}
	if r := bob.do("GET", "/users/"+me.Id+"/avatar", nil); r.status != http.StatusNotFound {
		t.Fatalf("removed avatar: %d", r.status)
	}
}
