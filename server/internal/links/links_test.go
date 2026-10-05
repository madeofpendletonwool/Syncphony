// SPDX-License-Identifier: AGPL-3.0-only

package links

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/providertest"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/vault"
)

type harness struct {
	db       *store.Store
	key      *vault.Key
	vault    *vault.Vault
	svc      *Service
	notified []string
	mu       sync.Mutex
	now      time.Time
	user     store.User
}

func newHarness(t *testing.T, ps ...provider.Provider) *harness {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg, err := provider.NewRegistry(ps...)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := vault.ParseKey(vault.GenerateKey())
	h := &harness{db: db, key: k, vault: vault.New(k), now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	h.svc = New(db, h.vault, reg, Config{BaseURL: "https://syncphony.example.com/", Notifier: h, Now: h.clock})
	h.user = h.newUser(t, "alice")
	return h
}

func (h *harness) newUser(t *testing.T, name string) store.User {
	t.Helper()
	u, err := h.db.CreateUser(t.Context(), store.CreateUserParams{
		ID: store.NewID(), Username: name, DisplayName: name, Color: "#000000", Role: store.RoleMember, CreatedAt: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
}

func (h *harness) NeedsRelink(_ context.Context, l store.ServiceLink) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notified = append(h.notified, l.ID)
}

var demo = map[string]string{"username": fake.Username, "password": fake.Password}

func (h *harness) link(t *testing.T, providerID string) store.ServiceLink {
	t.Helper()
	l, err := h.svc.LinkWithCredentials(t.Context(), h.user.ID, providerID, demo)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// linked is a provider whose sessions come from Service.Open, so the
// conformance suite exercises the vault and the health wrapper.
type linked struct {
	*fake.Provider
	h *harness
}

func (l linked) Open(ctx context.Context, link provider.Link) (provider.Session, error) {
	return l.h.svc.Open(ctx, link.ID)
}

func TestConformanceThroughLinks(t *testing.T) {
	for _, mode := range []provider.PlaybackMode{provider.PlaybackStream, provider.PlaybackRemote} {
		t.Run(string(mode), func(t *testing.T) {
			f := fake.New(fake.Options{Playback: mode})
			h := newHarness(t, f)
			providertest.Run(t, providertest.Harness{
				Provider: linked{f, h},
				Link: func(t *testing.T) provider.Link {
					row := h.link(t, "fake")
					return provider.Link{ID: row.ID, Account: provider.AccountInfo{ID: row.AccountID, Name: row.AccountLabel}, Credentials: row.EncryptedCredentials}
				},
				Query:  "zero",
				Expire: func(*testing.T, provider.Link) { f.Revoke(fake.Username) },
			})
		})
	}
}

func TestCredentialsAreSealed(t *testing.T) {
	h := newHarness(t, fake.New(fake.Options{}))
	l := h.link(t, "fake")
	if l.AccountID != fake.Username || l.Status != store.LinkOK || !l.LastOkAt.Valid {
		t.Fatalf("link: %+v", l)
	}
	if bytes.Contains(l.EncryptedCredentials, []byte("token")) {
		t.Fatal("credentials stored in the clear")
	}
	plain, err := h.vault.Open(l.ID, h.user.ID, l.EncryptedCredentials)
	if err != nil || !bytes.Contains(plain, []byte("token")) {
		t.Fatalf("sealed credentials don't open: %v", err)
	}

	// Linking the same account again updates the link instead of duplicating it.
	again := h.link(t, "fake")
	if again.ID != l.ID || bytes.Equal(again.EncryptedCredentials, l.EncryptedCredentials) {
		t.Fatalf("relinking the same account: id %s vs %s", again.ID, l.ID)
	}
	if rows, _ := h.svc.List(t.Context(), h.user.ID); len(rows) != 1 {
		t.Fatalf("%d links, want 1", len(rows))
	}
}

func TestLinkErrors(t *testing.T) {
	h := newHarness(t, fake.New(fake.Options{}), fake.New(fake.Options{ID: "oauthy", Link: provider.LinkOAuth2}))
	ctx := t.Context()
	var invalid *InvalidInputError
	if _, err := h.svc.LinkWithCredentials(ctx, h.user.ID, "fake", map[string]string{"username": "demo"}); !errors.As(err, &invalid) || invalid.Field != "password" {
		t.Errorf("missing field: %v", err)
	}
	if _, err := h.svc.LinkWithCredentials(ctx, h.user.ID, "fake", map[string]string{"username": "demo", "password": "nope"}); !errors.Is(err, provider.ErrInvalidCredentials) {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := h.svc.LinkWithCredentials(ctx, h.user.ID, "nope", demo); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("unknown provider: %v", err)
	}
	if _, err := h.svc.LinkWithCredentials(ctx, h.user.ID, "oauthy", demo); !errors.Is(err, ErrWrongMethod) {
		t.Errorf("form for an OAuth provider: %v", err)
	}
	if _, err := h.svc.BeginOAuth(ctx, h.user.ID, "fake", ""); !errors.Is(err, ErrWrongMethod) {
		t.Errorf("OAuth for a form provider: %v", err)
	}
}

func TestCheckFields(t *testing.T) {
	spec := []provider.LinkField{
		{Name: "server", Label: "Server", Kind: provider.FieldURL, Required: true},
		{Name: "password", Label: "Password", Kind: provider.FieldSecret, Required: true},
		{Name: "note", Label: "Note", Kind: provider.FieldText},
	}
	got, err := checkFields(spec, map[string]string{"server": " https://music.example.com ", "password": " pass ", "extra": "dropped"})
	if err != nil || got["server"] != "https://music.example.com" || got["password"] != " pass " || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{"music.example.com", "ftp://music.example.com", "https://"} {
		if _, err := checkFields(spec, map[string]string{"server": bad, "password": "p"}); err == nil {
			t.Errorf("server %q accepted", bad)
		}
	}
}

func TestHealth(t *testing.T) {
	f := fake.New(fake.Options{})
	h := newHarness(t, f)
	ctx := t.Context()
	l := h.link(t, "fake")
	sess, err := h.svc.Open(ctx, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.Revoke(fake.Username)
	for range 2 {
		if _, err := sess.Track(ctx, "t01"); !errors.Is(err, provider.ErrAuthExpired) {
			t.Fatalf("revoked: %v", err)
		}
	}
	row, _ := h.db.GetServiceLink(ctx, l.ID)
	if row.Status != store.LinkExpired || row.StatusDetail == "" {
		t.Fatalf("after expiry: %+v", row)
	}
	if len(h.notified) != 1 || h.notified[0] != l.ID {
		t.Fatalf("notified %v, want once for %s", h.notified, l.ID)
	}
	if _, err := h.svc.Open(ctx, l.ID); !errors.Is(err, provider.ErrAuthExpired) {
		t.Fatalf("opening an expired link: %v", err)
	}

	// Outages say nothing about the credentials.
	relinked, err := h.svc.RelinkWithCredentials(ctx, h.user.ID, l.ID, demo)
	if err != nil || relinked.Status != store.LinkOK || relinked.ID != l.ID {
		t.Fatalf("relink: %+v, %v", relinked, err)
	}
	sess, _ = h.svc.Open(ctx, l.ID)
	f.Fail(provider.ErrUnavailable)
	if _, err := sess.Search(ctx, provider.SearchQuery{Text: "x"}); !errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("outage: %v", err)
	}
	if row, _ := h.db.GetServiceLink(ctx, l.ID); row.Status != store.LinkOK {
		t.Fatalf("outage changed status to %s", row.Status)
	}
}

func TestLastOKThrottled(t *testing.T) {
	h := newHarness(t, fake.New(fake.Options{}))
	ctx := t.Context()
	l := h.link(t, "fake")
	sess, _ := h.svc.Open(ctx, l.ID)
	h.advance(time.Minute)
	if _, err := sess.Track(ctx, "t01"); err != nil {
		t.Fatal(err)
	}
	if row, _ := h.db.GetServiceLink(ctx, l.ID); !row.LastOkAt.Time.Equal(l.LastOkAt.Time) {
		t.Fatal("last_ok_at written on every call")
	}
	h.advance(okEvery)
	if _, err := sess.Track(ctx, "t01"); err != nil {
		t.Fatal(err)
	}
	if row, _ := h.db.GetServiceLink(ctx, l.ID); !row.LastOkAt.Time.Equal(h.clock()) {
		t.Fatalf("last_ok_at %v, want %v", row.LastOkAt.Time, h.clock())
	}
}

func TestOAuth(t *testing.T) {
	var h *harness
	f := fake.New(fake.Options{ID: "oauthy", Link: provider.LinkOAuth2, TokenTTL: time.Hour, Now: func() time.Time { return h.clock() }})
	h = newHarness(t, f)
	ctx := t.Context()
	authURL, err := h.svc.BeginOAuth(ctx, h.user.ID, "oauthy", "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	state := u.Query().Get("state")
	if u.Query().Get("redirect_uri") != "https://syncphony.example.com"+CallbackPath || state == "" {
		t.Fatalf("auth URL %s", authURL)
	}
	bob := h.newUser(t, "bob")
	if _, err := h.svc.CompleteOAuth(ctx, bob.ID, state, fake.Code); !errors.Is(err, ErrOAuthState) {
		t.Fatalf("someone else's state: %v", err)
	}
	if _, err := h.svc.CompleteOAuth(ctx, h.user.ID, state, "denied"); !errors.Is(err, provider.ErrInvalidCredentials) {
		t.Fatalf("denied code: %v", err)
	}
	if _, err := h.svc.CompleteOAuth(ctx, h.user.ID, state, fake.Code); !errors.Is(err, ErrOAuthState) {
		t.Fatalf("reused state: %v", err)
	}

	authURL, _ = h.svc.BeginOAuth(ctx, h.user.ID, "oauthy", "")
	u, _ = url.Parse(authURL)
	l, err := h.svc.CompleteOAuth(ctx, h.user.ID, u.Query().Get("state"), fake.Code)
	if err != nil {
		t.Fatal(err)
	}

	// The access token expires; the session refreshes it and the sink
	// re-seals the new credentials.
	sess, _ := h.svc.Open(ctx, l.ID)
	h.advance(2 * time.Hour)
	if _, err := sess.Track(ctx, "t01"); err != nil {
		t.Fatal(err)
	}
	row, _ := h.db.GetServiceLink(ctx, l.ID)
	if bytes.Equal(row.EncryptedCredentials, l.EncryptedCredentials) {
		t.Fatal("rotated credentials weren't saved")
	}
	sess2, err := h.svc.Open(ctx, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess2.Track(ctx, "t01"); err != nil {
		t.Fatalf("rotated credentials don't work: %v", err)
	}

	// Expired states are rejected.
	authURL, _ = h.svc.BeginOAuth(ctx, h.user.ID, "", l.ID)
	u, _ = url.Parse(authURL)
	h.advance(oauthTTL + time.Second)
	if _, err := h.svc.CompleteOAuth(ctx, h.user.ID, u.Query().Get("state"), fake.Code); !errors.Is(err, ErrOAuthState) {
		t.Fatalf("expired state: %v", err)
	}
}

func TestUnlink(t *testing.T) {
	h := newHarness(t, fake.New(fake.Options{}))
	ctx := t.Context()
	l := h.link(t, "fake")
	bob := h.newUser(t, "bob")
	if err := h.svc.Unlink(ctx, bob.ID, l.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unlinking someone else's link: %v", err)
	}
	if _, err := h.svc.RelinkWithCredentials(ctx, bob.ID, l.ID, demo); !errors.Is(err, ErrNotFound) {
		t.Fatalf("relinking someone else's link: %v", err)
	}
	if err := h.svc.Unlink(ctx, h.user.ID, l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.GetServiceLink(ctx, l.ID); !store.IsNotFound(err) {
		t.Fatalf("link still stored: %v", err)
	}
}

func TestRotateAndLostKey(t *testing.T) {
	f := fake.New(fake.Options{})
	h := newHarness(t, f)
	ctx := t.Context()
	l1 := h.link(t, "fake")
	bob := h.newUser(t, "bob")
	l3, err := h.svc.LinkWithCredentials(ctx, bob.ID, "fake", demo)
	if err != nil {
		t.Fatal(err)
	}

	// Rotate to a new key, keeping the old one as a fallback.
	newKey, _ := vault.ParseKey(vault.GenerateKey())
	oldKey := h.key
	reg, _ := provider.NewRegistry(f)
	rotated := New(h.db, vault.New(newKey, oldKey), reg, Config{})
	n, failed, err := rotated.Rotate(ctx)
	if err != nil || n != 2 || len(failed) != 0 {
		t.Fatalf("Rotate = %d, %v, %v", n, failed, err)
	}
	if n, _, _ := rotated.Rotate(ctx); n != 0 {
		t.Fatalf("second Rotate changed %d", n)
	}
	onlyNew := New(h.db, vault.New(newKey), reg, Config{})
	for _, id := range []string{l1.ID, l3.ID} {
		sess, err := onlyNew.Open(ctx, id)
		if err != nil {
			t.Fatalf("open %s with only the new key: %v", id, err)
		}
		if _, err := sess.Track(ctx, "t01"); err != nil {
			t.Fatal(err)
		}
	}

	// A server started with the wrong key can't decrypt, and flags the link.
	wrongKey, _ := vault.ParseKey(vault.GenerateKey())
	lost := New(h.db, vault.New(wrongKey), reg, Config{})
	if _, err := lost.Open(ctx, l1.ID); !errors.Is(err, provider.ErrAuthExpired) {
		t.Fatalf("wrong key: %v", err)
	}
	if row, _ := h.db.GetServiceLink(ctx, l1.ID); row.Status != store.LinkExpired {
		t.Fatalf("status %s after a decryption failure", row.Status)
	}
}
