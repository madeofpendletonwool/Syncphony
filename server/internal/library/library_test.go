// SPDX-License-Identifier: AGPL-3.0-only

package library_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/navidrome"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// subsonic is a tiny Subsonic server whose library is songs, listed by
// search3 with an empty query, a page at a time.
type subsonic struct {
	*httptest.Server
	mu       sync.Mutex
	songs    []string // JSON song objects
	lastScan string
	searches int
}

func newSubsonic(t *testing.T, songs []string) *subsonic {
	s := &subsonic{songs: songs, lastScan: "2026-10-01T10:00:00Z"}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *subsonic) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	ok := func(fields string) {
		fmt.Fprintf(w, `{"subsonic-response":{"status":"ok","version":"1.16.1","type":"navidrome","openSubsonic":true,%s}}`, fields)
	}
	switch strings.TrimPrefix(r.URL.Path, "/rest/") {
	case "getScanStatus":
		ok(fmt.Sprintf(`"scanStatus":{"scanning":false,"count":%d,"lastScan":%q}`, len(s.songs), s.lastScan))
	case "search3":
		if q.Get("query") != "" || q.Get("albumCount") != "0" || q.Get("artistCount") != "0" {
			http.Error(w, "not a catalog listing", http.StatusBadRequest)
			return
		}
		s.searches++
		n, _ := strconv.Atoi(q.Get("songCount"))
		off, _ := strconv.Atoi(q.Get("songOffset"))
		page := s.songs[min(off, len(s.songs)):min(off+n, len(s.songs))]
		ok(`"searchResult3":{"song":[` + strings.Join(page, ",") + `]}`)
	default:
		http.NotFound(w, r)
	}
}

func song(id, artist, title, album, isrc string) string {
	b, _ := json.Marshal(map[string]any{
		"id": id, "title": title, "artist": artist, "album": album, "isrc": []string{isrc},
		"artists": []map[string]string{{"name": artist}},
	})
	return string(b)
}

// links opens Navidrome links straight from their stored credentials.
type links struct {
	db    *store.Store
	p     *navidrome.Provider
	creds map[string][]byte
}

func (l *links) List(ctx context.Context, userID string) ([]store.ServiceLink, error) {
	return l.db.ListServiceLinks(ctx, userID)
}

func (l *links) Open(ctx context.Context, linkID string) (provider.Session, error) {
	return l.p.Open(ctx, provider.Link{ID: linkID, Credentials: l.creds[linkID]})
}

type env struct {
	db    *store.Store
	links *links
	svc   *library.Service
	user  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	u, err := db.CreateUser(t.Context(), store.CreateUserParams{
		ID: store.NewID(), Username: "alice", DisplayName: "Alice", Color: "#000", Role: store.RoleMember, CreatedAt: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{db: db, user: u.ID, links: &links{db: db, p: navidrome.New(navidrome.Options{}), creds: map[string][]byte{}}}
	e.svc = library.New(db, e.links)
	return e
}

// link links a Subsonic server to alice.
func (e *env) link(t *testing.T, srv *subsonic) string {
	t.Helper()
	l, err := e.db.CreateServiceLink(t.Context(), store.CreateServiceLinkParams{
		ID: store.NewID(), UserID: e.user, Provider: navidrome.ID, AccountID: srv.URL, AccountLabel: "alice",
		EncryptedCredentials: []byte("sealed"), Now: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.links.creds[l.ID], _ = json.Marshal(map[string]string{"url": srv.URL, "username": "alice", "password": "pw"})
	return l.ID
}

func TestHasWhatsOnTheServer(t *testing.T) {
	e := newEnv(t)
	srv := newSubsonic(t, []string{
		song("1", "Slowdive", "Alison - 2010 Remaster", "Souvlaki", "GBAAA9300001"),
		song("2", "The Hum", "Stars", "You'd Prefer an Astronaut", ""),
		song("3", "Duster", "Inside Out (Live)", "Live at the Echo", ""),
	})
	e.link(t, srv)

	// The first ask starts the index; until it's built, nothing is owned.
	o, err := e.svc.For(t.Context(), e.user)
	if err != nil {
		t.Fatal(err)
	}
	if o.Has(library.Song{Artist: "Slowdive", Title: "Alison"}) {
		t.Fatal("owned before the index was built")
	}
	e.svc.Wait()
	if o, err = e.svc.For(t.Context(), e.user); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		song library.Song
		want bool
	}{
		{library.Song{Artist: "Slowdive", Title: "Alison"}, true},                // remaster note trimmed
		{library.Song{Artist: "slowdive", Title: "ALISON (feat. Nobody)"}, true}, // case, feat.
		{library.Song{Artist: "Someone Else", Title: "Cover", ISRC: "gbaaa9300001"}, true},
		{library.Song{Artist: "Hum", Title: "Stars"}, true}, // "The" ignored
		{library.Song{Artist: "Hum", Title: "Stars (Live)"}, false},
		{library.Song{Artist: "Duster", Title: "Inside Out"}, false}, // only the live take is owned
		{library.Song{Artist: "Slowdive", Title: "When the Sun Hits"}, false},
	} {
		if got := o.Has(tc.song); got != tc.want {
			t.Errorf("Has(%+v) = %v, want %v", tc.song, got, tc.want)
		}
	}
	if !o.HasRelease("Slowdive", "Souvlaki (Deluxe Edition)") || o.HasRelease("Slowdive", "Pygmalion") {
		t.Error("HasRelease is release-level")
	}
	if !o.KnowsArtist("hum") || o.KnowsArtist("Low") {
		t.Error("KnowsArtist")
	}
	if st := o.Status(); st.Songs != 3 || st.Links != 1 || st.BuiltAt.IsZero() {
		t.Errorf("status: %+v", st)
	}
}

func TestIndexSurvivesARestartAndSkipsUnchangedLibraries(t *testing.T) {
	e := newEnv(t)
	srv := newSubsonic(t, []string{song("1", "Slowdive", "Alison", "Souvlaki", "")})
	link := e.link(t, srv)
	if err := e.svc.Build(t.Context(), link, false); err != nil {
		t.Fatal(err)
	}
	if srv.searches != 1 {
		t.Fatalf("%d listings", srv.searches)
	}

	// A new Service reads the saved index, without listing the library.
	fresh := library.New(e.db, e.links)
	if ok, err := fresh.Has(t.Context(), e.user, library.Song{Artist: "Slowdive", Title: "Alison"}); err != nil || !ok {
		t.Fatalf("after a restart: %v %v", ok, err)
	}
	fresh.Wait()
	// Not scanned since: nothing to list again.
	if err := fresh.Build(t.Context(), link, false); err != nil {
		t.Fatal(err)
	}
	if srv.searches != 1 {
		t.Errorf("rebuilt an unchanged library: %d listings", srv.searches)
	}
	// Scanned since, with a new song.
	srv.mu.Lock()
	srv.songs = append(srv.songs, song("2", "Hum", "Stars", "Astronaut", ""))
	srv.lastScan = "2026-10-02T10:00:00Z"
	srv.mu.Unlock()
	if err := fresh.Build(t.Context(), link, false); err != nil {
		t.Fatal(err)
	}
	if ok, _ := fresh.Has(t.Context(), e.user, library.Song{Artist: "Hum", Title: "Stars"}); !ok || srv.searches != 2 {
		t.Errorf("after a scan: has %v, %d listings", ok, srv.searches)
	}
	// Refreshing rebuilds even when nothing says it changed.
	if err := fresh.Refresh(t.Context(), e.user); err != nil {
		t.Fatal(err)
	}
	fresh.Wait()
	if srv.searches != 3 {
		t.Errorf("refresh: %d listings", srv.searches)
	}
}

func TestBigLibrary(t *testing.T) {
	e := newEnv(t)
	const n = 60_000
	songs := make([]string, n)
	for i := range songs {
		songs[i] = song(strconv.Itoa(i), fmt.Sprintf("Artist %d", i/12), fmt.Sprintf("Song %d", i), fmt.Sprintf("Album %d", i/12), fmt.Sprintf("XX%010d", i))
	}
	srv := newSubsonic(t, songs)
	e.link(t, srv)
	start := time.Now()
	if _, err := e.svc.For(t.Context(), e.user); err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	t.Logf("indexed %d songs in %v, %d pages", n, time.Since(start), srv.searches)
	if srv.searches != n/500+1 {
		t.Errorf("%d pages, want %d", srv.searches, n/500+1)
	}
	o, err := e.svc.For(t.Context(), e.user)
	if err != nil {
		t.Fatal(err)
	}
	const lookups = 10_000
	start = time.Now()
	found := 0
	for i := range lookups {
		j := i * 7 % n
		if o.Has(library.Song{Artist: fmt.Sprintf("Artist %d", j/12), Title: fmt.Sprintf("Song %d", j)}) {
			found++
		}
		o.HasRelease("Nobody", "Nothing")
	}
	per := time.Since(start) / lookups
	if found != lookups {
		t.Errorf("found %d of %d", found, lookups)
	}
	if per > time.Millisecond {
		t.Errorf("a lookup takes %v, want under a millisecond", per)
	}
}
