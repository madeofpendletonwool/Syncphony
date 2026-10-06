// SPDX-License-Identifier: AGPL-3.0-only

package musicbrainz_test

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// testdata holds responses shaped like the real services'.
var testdata = os.DirFS("testdata")

const (
	recordingID = "8f3471b5-7e6a-48da-86a9-c1c07a0f47ae"
	lowPass     = "aa000000-0000-4000-8000-000000000002"
	lowPassRG   = "bb000000-0000-4000-8000-000000000002"
	artistID    = "0b3a1c55-2f0c-4a35-9a3e-6a1f33a0e001"
)

// mb stands in for MusicBrainz and the Cover Art Archive.
type mb struct {
	*httptest.Server
	t *testing.T

	mu       sync.Mutex
	requests []*http.Request
	// routes maps a path (with the query, for searches) to a fixture, or
	// to "" for a 404. Unlisted paths are 404s too.
	routes map[string]string
	// status, if set, is the answer to everything.
	status int
	// covers maps a Cover Art Archive path to a PNG's width.
	covers map[string]int
}

func newMB(t *testing.T) *mb {
	m := &mb{t: t, routes: map[string]string{}, covers: map[string]int{}}
	m.Server = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.Close)
	return m
}

func (m *mb) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.requests = append(m.requests, r)
	status, file, width := m.status, m.routes[r.URL.Path], m.covers[r.URL.Path]
	if q := r.URL.Query().Get("query"); q != "" {
		file = m.routes[r.URL.Path+"?"+q]
	}
	m.mu.Unlock()
	switch {
	case status != 0:
		w.WriteHeader(status)
	case strings.HasPrefix(r.URL.Path, "/img/"):
		// The archive.org end of a redirect.
		var b bytes.Buffer
		w2, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/img/"))
		if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w2, 1))); err != nil {
			m.t.Error(err)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(b.Bytes())
	case width > 0:
		http.Redirect(w, r, "/img/"+strconv.Itoa(width), http.StatusTemporaryRedirect)
	case file != "":
		b, err := fs.ReadFile(testdata, file)
		if err != nil {
			m.t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		b, _ := fs.ReadFile(testdata, "not-found.json")
		_, _ = w.Write(b)
	}
}

func (m *mb) route(path, fixture string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes[path] = fixture
}

func (m *mb) cover(path string, width int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.covers[path] = width
}

func (m *mb) fail(status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = status
}

func (m *mb) paths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, r := range m.requests {
		out = append(out, r.URL.Path)
	}
	return out
}

func (m *mb) last() *http.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests[len(m.requests)-1]
}

type env struct {
	svc *musicbrainz.Service
	mb  *mb
	now time.Time
}

func newEnv(t *testing.T, interval time.Duration) *env {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{mb: newMB(t), now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	e.svc = musicbrainz.New(db, musicbrainz.Options{
		UserAgent: "Syncphony/test ( https://example.com )",
		BaseURL:   e.mb.URL, CoverArtURL: e.mb.URL,
		Interval: interval, MissTTL: time.Hour,
		Now: func() time.Time { return e.now },
	})
	return e
}

// rolloff as a Navidrome track: with an MBID and ISRC from its tags.
var rolloff = provider.Track{
	Ref:      provider.TrackRef{Provider: "navidrome", LinkID: "l1", ID: "s1"},
	Title:    "Rolloff",
	Artists:  []provider.ArtistCredit{{Name: "The Square Roots"}},
	Album:    provider.AlbumCredit{Title: "Low Pass"},
	Duration: 20 * time.Second,
	ISRC:     "USAAA2600001",
	MBID:     recordingID,
}

var want = musicbrainz.IDs{Recording: recordingID, Release: lowPass, ReleaseGroup: lowPassRG, Artist: artistID}

func TestResolveByMBID(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.route("/ws/2/recording/"+recordingID, "recording.json")
	ids, err := e.svc.Resolve(t.Context(), rolloff)
	w := want
	w.Method = musicbrainz.MethodMBID
	if err != nil || ids != w {
		t.Fatalf("Resolve = %+v, %v", ids, err)
	}
	r := e.mb.last()
	if r.URL.Query().Get("fmt") != "json" || r.URL.Query().Get("inc") != "artists+releases+release-groups" {
		t.Errorf("query = %v", r.URL.Query())
	}
	if r.Header.Get("User-Agent") != "Syncphony/test ( https://example.com )" {
		t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
	}
	// Cached.
	if ids, err := e.svc.Resolve(t.Context(), rolloff); err != nil || ids != w || len(e.mb.paths()) != 1 {
		t.Errorf("again: %+v, %v after %d requests", ids, err, len(e.mb.paths()))
	}
	if ids, ok, err := e.svc.Lookup(t.Context(), rolloff.Ref); !ok || err != nil || ids != w {
		t.Errorf("Lookup = %+v, %v, %v", ids, ok, err)
	}
}

// The release is the one named like the album, official, earliest;
// without an album name, an official non-compilation album.
func TestReleasePick(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.route("/ws/2/recording/"+recordingID, "recording.json")
	tr := rolloff
	tr.Album.Title = ""
	ids, err := e.svc.Resolve(t.Context(), tr)
	if err != nil || ids.Release != lowPass {
		t.Fatalf("Resolve = %+v, %v", ids, err)
	}
}

func TestResolveByISRC(t *testing.T) {
	e := newEnv(t, -1)
	// The tagged MBID is stale (merged away); the ISRC is on a live
	// recording too, which isn't this one.
	e.mb.route("/ws/2/isrc/USAAA2600001", "isrc.json")
	ids, err := e.svc.Resolve(t.Context(), rolloff)
	w := want
	w.Method = musicbrainz.MethodISRC
	if err != nil || ids != w {
		t.Fatalf("Resolve = %+v, %v", ids, err)
	}
	if got := e.mb.paths(); len(got) != 2 || got[0] != "/ws/2/recording/"+recordingID {
		t.Errorf("requests: %v", got)
	}
}

func TestResolveBySearch(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.route(`/ws/2/recording?recording:"Rolloff" AND artist:"The Square Roots"`, "search.json")
	tr := rolloff
	tr.MBID, tr.ISRC = "", ""
	ids, err := e.svc.Resolve(t.Context(), tr)
	w := want
	w.Method = musicbrainz.MethodSearch
	// The top hit is 5 minutes long, so not this; the third scores too low.
	if err != nil || ids != w {
		t.Fatalf("Resolve = %+v, %v", ids, err)
	}
	if q := e.mb.last().URL.Query(); q.Get("limit") != "10" {
		t.Errorf("query = %v", q)
	}
}

func TestSearchQuoting(t *testing.T) {
	e := newEnv(t, -1)
	tr := provider.Track{Ref: provider.TrackRef{Provider: "x", ID: "1"}, Title: `Say "Hi" \o/`, Artists: []provider.ArtistCredit{{Name: "AC/DC"}}}
	if _, err := e.svc.Resolve(t.Context(), tr); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	q, _ := url.QueryUnescape(e.mb.last().URL.Query().Get("query"))
	if q != `recording:"Say \"Hi\" \\o/" AND artist:"AC/DC"` {
		t.Errorf("query = %s", q)
	}
}

func TestMissesAreCached(t *testing.T) {
	e := newEnv(t, -1)
	for range 2 {
		if _, err := e.svc.Resolve(t.Context(), rolloff); !errors.Is(err, provider.ErrNotFound) {
			t.Fatalf("got %v, want ErrNotFound", err)
		}
	}
	// MBID, ISRC and search, once.
	if n := len(e.mb.paths()); n != 3 {
		t.Errorf("%d requests, want 3", n)
	}
	if _, ok, err := e.svc.Lookup(t.Context(), rolloff.Ref); !ok || !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("Lookup: %v, %v", ok, err)
	}
	e.now = e.now.Add(time.Hour)
	e.mb.route("/ws/2/recording/"+recordingID, "recording.json")
	if _, err := e.svc.Resolve(t.Context(), rolloff); err != nil {
		t.Errorf("after the miss expired: %v", err)
	}
}

func TestOutagesAreNotCached(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError} {
		e := newEnv(t, -1)
		e.mb.fail(status)
		if _, err := e.svc.Resolve(t.Context(), rolloff); err == nil || errors.Is(err, provider.ErrNotFound) {
			t.Fatalf("HTTP %d: got %v", status, err)
		}
		if _, ok, _ := e.svc.Lookup(t.Context(), rolloff.Ref); ok {
			t.Errorf("HTTP %d: cached", status)
		}
	}
}

// MusicBrainz allows a request a second.
func TestRateLimit(t *testing.T) {
	e := newEnv(t, 100*time.Millisecond)
	start := time.Now()
	if _, err := e.svc.Resolve(t.Context(), rolloff); !errors.Is(err, provider.ErrNotFound) {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 200*time.Millisecond {
		t.Errorf("3 requests in %v", d)
	}
}

func TestCoverArt(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.route("/ws/2/recording/"+recordingID, "recording.json")
	ctx := t.Context()
	go e.svc.Run(ctx)

	// Not resolved yet: none, but it's queued.
	if _, err := e.svc.CoverArt(ctx, rolloff, 400); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("unresolved: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok, _ := e.svc.Lookup(ctx, rolloff.Ref); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never resolved")
		}
		time.Sleep(5 * time.Millisecond)
	}

	e.mb.cover("/release/"+lowPass+"/front-500", 500)
	img, err := e.svc.CoverArt(ctx, rolloff, 400)
	if err != nil || img.ContentType != "image/png" {
		t.Fatalf("CoverArt = %v, %v", img.ContentType, err)
	}
	if cfg, err := png.DecodeConfig(bytes.NewReader(img.Data)); err != nil || cfg.Width != 500 {
		t.Errorf("image: %+v, %v", cfg, err)
	}
	// Cached.
	n := len(e.mb.paths())
	if _, err := e.svc.CoverArt(ctx, rolloff, 300); err != nil || len(e.mb.paths()) != n {
		t.Errorf("not cached: %v", err)
	}

	// Big screens get the biggest thumbnail; when the release has none,
	// its release group's.
	e.mb.cover("/release-group/"+lowPassRG+"/front-1200", 1200)
	if img, err := e.svc.CoverArt(ctx, rolloff, 2048); err != nil || len(img.Data) == 0 {
		t.Fatalf("big: %v", err)
	}
	got := e.mb.paths()
	if got[len(got)-3] != "/release/"+lowPass+"/front-1200" || got[len(got)-2] != "/release-group/"+lowPassRG+"/front-1200" {
		t.Errorf("requests: %v", got[n:])
	}

	// Neither has a small one: none, remembered.
	n = len(e.mb.paths())
	if _, err := e.svc.CoverArt(ctx, rolloff, 100); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("no cover: %v", err)
	}
	if _, err := e.svc.CoverArt(ctx, rolloff, 100); !errors.Is(err, provider.ErrNotFound) || len(e.mb.paths()) != n+2 {
		t.Errorf("miss not cached: %v, %v", err, e.mb.paths()[n:])
	}
}

func TestEnqueueSkipsDuplicates(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.route("/ws/2/recording/"+recordingID, "recording.json")
	e.svc.Enqueue(rolloff, rolloff, rolloff)
	go e.svc.Run(t.Context())
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok, _ := e.svc.Lookup(t.Context(), rolloff.Ref); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never resolved")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(e.mb.paths()); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}
