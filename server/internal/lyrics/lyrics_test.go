// SPDX-License-Identifier: AGPL-3.0-only

package lyrics_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/lyrics"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// testdata holds responses shaped like the real service's.
var testdata = os.DirFS("testdata")

// lrclib is a stand-in LRCLIB answering every lookup with one fixture.
type lrclib struct {
	*httptest.Server
	mu       sync.Mutex
	status   int
	fixture  string
	requests []*http.Request
	// hold, if set, is waited on before answering.
	hold chan struct{}
}

func newLRCLIB(t *testing.T, status int, fixture string) *lrclib {
	l := &lrclib{status: status, fixture: fixture}
	l.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		l.requests = append(l.requests, r)
		status, fixture, hold := l.status, l.fixture, l.hold
		l.mu.Unlock()
		if hold != nil {
			<-hold
		}
		if r.URL.Path != "/api/get" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if fixture != "" {
			b, err := fs.ReadFile(testdata, fixture)
			if err != nil {
				t.Error(err)
			}
			_, _ = w.Write(b)
		}
	}))
	t.Cleanup(l.Close)
	return l
}

func (l *lrclib) answer(status int, fixture string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status, l.fixture = status, fixture
}

func (l *lrclib) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.requests)
}

func (l *lrclib) last() *http.Request {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.requests[len(l.requests)-1]
}

// session is a provider session with lyrics for some tracks.
type session struct {
	provider.Session
	lyrics map[string]provider.Lyrics
	err    error
	tracks map[string]provider.Track
	calls  int
}

func (s *session) Lyrics(_ context.Context, id string) (provider.Lyrics, error) {
	s.calls++
	if s.err != nil {
		return provider.Lyrics{}, s.err
	}
	l, ok := s.lyrics[id]
	if !ok {
		return provider.Lyrics{}, provider.ErrNotFound
	}
	return l, nil
}

func (s *session) Track(_ context.Context, id string) (provider.Track, error) {
	t, ok := s.tracks[id]
	if !ok {
		return provider.Track{}, provider.ErrNotFound
	}
	return t, nil
}

var rolloff = provider.Track{
	Ref:      provider.TrackRef{Provider: "navidrome", LinkID: "l1", ID: "s1"},
	Title:    "Rolloff",
	Artists:  []provider.ArtistCredit{{Name: "The Square Roots"}, {Name: "A Guest"}},
	Album:    provider.AlbumCredit{Title: "Low Pass"},
	Duration: 19600 * time.Millisecond,
}

type env struct {
	svc    *lyrics.Service
	lrclib *lrclib
	now    time.Time
}

func newEnv(t *testing.T, status int, fixture string) *env {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{lrclib: newLRCLIB(t, status, fixture), now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	e.svc = lyrics.New(db, lyrics.Options{
		LRCLIB:  &lyrics.LRCLIB{BaseURL: e.lrclib.URL + "/", UserAgent: "Syncphony/test"},
		MissTTL: time.Hour,
		Now:     func() time.Time { return e.now },
	})
	return e
}

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestProviderSyncedLyrics(t *testing.T) {
	e := newEnv(t, http.StatusOK, "lrclib-synced.json")
	sess := &session{lyrics: map[string]provider.Lyrics{"s1": {Plain: "Hi", Synced: []provider.LyricLine{{At: ms(1), Text: "Hi"}}}}}
	r, err := e.svc.Get(t.Context(), sess, rolloff)
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != "navidrome" || len(r.Synced) != 1 || r.Plain != "Hi" {
		t.Errorf("result = %+v", r)
	}
	if e.lrclib.count() != 0 {
		t.Error("asked LRCLIB though the provider had synced lyrics")
	}
}

func TestLRCLIBFallback(t *testing.T) {
	e := newEnv(t, http.StatusOK, "lrclib-synced.json")
	sess := &session{}
	r, err := e.svc.Get(t.Context(), sess, rolloff)
	if err != nil {
		t.Fatal(err)
	}
	want := []provider.LyricLine{
		{At: ms(100), Text: "Count it in"},
		{At: ms(4250), Text: "Low pass, high hopes"},
		{At: ms(9000), Text: ""},
		{At: ms(12500), Text: "Roll it off"},
		{At: ms(16000), Text: ""},
	}
	if r.Source != lyrics.SourceLRCLIB || r.Instrumental || !slices.Equal(r.Synced, want) ||
		r.Plain != "Count it in\nLow pass, high hopes\n\nRoll it off" {
		t.Errorf("result = %+v", r)
	}
	req := e.lrclib.last()
	want2 := url.Values{"track_name": {"Rolloff"}, "artist_name": {"The Square Roots"}, "album_name": {"Low Pass"}, "duration": {"20"}}
	if fmt.Sprint(req.URL.Query()) != fmt.Sprint(want2) {
		t.Errorf("query = %v", req.URL.Query())
	}
	if req.Header.Get("User-Agent") != "Syncphony/test" {
		t.Errorf("User-Agent = %q", req.Header.Get("User-Agent"))
	}

	// Cached: neither is asked again.
	again, err := e.svc.Get(t.Context(), sess, rolloff)
	if err != nil || fmt.Sprint(again) != fmt.Sprint(r) {
		t.Errorf("cached: %+v, %v", again, err)
	}
	if e.lrclib.count() != 1 || sess.calls != 1 {
		t.Errorf("not cached: %d LRCLIB requests, %d provider calls", e.lrclib.count(), sess.calls)
	}
}

func TestProviderPlainLyrics(t *testing.T) {
	sess := func() *session {
		return &session{lyrics: map[string]provider.Lyrics{"s1": {Plain: "From tags"}}}
	}
	// LRCLIB's synced lyrics beat the provider's plain ones...
	e := newEnv(t, http.StatusOK, "lrclib-synced.json")
	if r, err := e.svc.Get(t.Context(), sess(), rolloff); err != nil || r.Source != lyrics.SourceLRCLIB || len(r.Synced) == 0 {
		t.Errorf("LRCLIB has synced: %+v, %v", r, err)
	}
	// ...but not LRCLIB's plain ones, or nothing.
	for _, tc := range []struct {
		status  int
		fixture string
	}{{http.StatusOK, "lrclib-plain.json"}, {http.StatusNotFound, "lrclib-404.json"}, {http.StatusInternalServerError, ""}} {
		e := newEnv(t, tc.status, tc.fixture)
		r, err := e.svc.Get(t.Context(), sess(), rolloff)
		if err != nil || r.Source != "navidrome" || r.Plain != "From tags" || r.Synced != nil {
			t.Errorf("LRCLIB %d %s: %+v, %v", tc.status, tc.fixture, r, err)
		}
	}
}

func TestMissesAreCached(t *testing.T) {
	e := newEnv(t, http.StatusNotFound, "lrclib-404.json")
	sess := &session{}
	for range 2 {
		if _, err := e.svc.Get(t.Context(), sess, rolloff); !errors.Is(err, provider.ErrNotFound) {
			t.Fatalf("got %v, want ErrNotFound", err)
		}
	}
	if e.lrclib.count() != 1 || sess.calls != 1 {
		t.Errorf("miss not cached: %d LRCLIB requests, %d provider calls", e.lrclib.count(), sess.calls)
	}

	// Once the miss expires, they're asked again.
	e.now = e.now.Add(time.Hour)
	e.lrclib.answer(http.StatusOK, "lrclib-synced.json")
	if r, err := e.svc.Get(t.Context(), sess, rolloff); err != nil || r.Source != lyrics.SourceLRCLIB {
		t.Errorf("after expiry: %+v, %v", r, err)
	}
}

// When a service is down, a miss says nothing and isn't kept.
func TestOutagesAreNotCached(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sessErr error
		status  int
	}{
		{"LRCLIB down", nil, http.StatusBadGateway},
		{"LRCLIB rate limited", nil, http.StatusTooManyRequests},
		{"provider down", provider.ErrUnavailable, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.status, "")
			sess := &session{err: tc.sessErr}
			for range 2 {
				if _, err := e.svc.Get(t.Context(), sess, rolloff); !errors.Is(err, provider.ErrNotFound) {
					t.Fatalf("got %v, want ErrNotFound", err)
				}
			}
			if e.lrclib.count() != 2 {
				t.Errorf("%d LRCLIB requests, want 2", e.lrclib.count())
			}
		})
	}
}

func TestInstrumental(t *testing.T) {
	e := newEnv(t, http.StatusOK, "lrclib-instrumental.json")
	r, err := e.svc.Get(t.Context(), nil, rolloff)
	if err != nil || !r.Instrumental || r.Source != lyrics.SourceLRCLIB || r.Plain != "" || r.Synced != nil {
		t.Errorf("result = %+v, %v", r, err)
	}
}

// With the link gone there's no session, but the queue's metadata is
// enough for LRCLIB.
func TestNoSession(t *testing.T) {
	e := newEnv(t, http.StatusOK, "lrclib-synced.json")
	if r, err := e.svc.Get(t.Context(), nil, rolloff); err != nil || r.Source != lyrics.SourceLRCLIB {
		t.Errorf("result = %+v, %v", r, err)
	}
}

// Given only a ref, the track is looked up for LRCLIB.
func TestLooksUpTrack(t *testing.T) {
	e := newEnv(t, http.StatusOK, "lrclib-synced.json")
	sess := &session{tracks: map[string]provider.Track{"s1": rolloff}}
	if r, err := e.svc.Get(t.Context(), sess, provider.Track{Ref: rolloff.Ref}); err != nil || r.Source != lyrics.SourceLRCLIB {
		t.Errorf("result = %+v, %v", r, err)
	}
	if q := e.lrclib.last().URL.Query(); q.Get("track_name") != "Rolloff" {
		t.Errorf("query = %v", q)
	}
	// An unknown track is a miss, without asking LRCLIB.
	missing := provider.Track{Ref: provider.TrackRef{Provider: "navidrome", LinkID: "l1", ID: "nope"}}
	if _, err := e.svc.Get(t.Context(), sess, missing); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("missing: got %v, want ErrNotFound", err)
	}
	if e.lrclib.count() != 1 {
		t.Errorf("%d LRCLIB requests, want 1", e.lrclib.count())
	}
}

// A room full of phones asking at once costs one lookup.
func TestConcurrentGets(t *testing.T) {
	e := newEnv(t, http.StatusOK, "lrclib-synced.json")
	hold := make(chan struct{})
	e.lrclib.mu.Lock()
	e.lrclib.hold = hold
	e.lrclib.mu.Unlock()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if r, err := e.svc.Get(t.Context(), nil, rolloff); err != nil || r.Source != lyrics.SourceLRCLIB {
				t.Errorf("result = %+v, %v", r, err)
			}
		})
	}
	for e.lrclib.count() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(hold)
	wg.Wait()
	if n := e.lrclib.count(); n != 1 {
		t.Errorf("%d LRCLIB requests, want 1", n)
	}
}
