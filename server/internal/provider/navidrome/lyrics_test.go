// SPDX-License-Identifier: AGPL-3.0-only

package navidrome_test

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/navidrome"
)

// testdata holds responses shaped like the real service's.
var testdata = os.DirFS("testdata")

// serveFixtures answers Subsonic methods with files from testdata, and
// everything else as usual.
func serveFixtures(t *testing.T, srv *server, files map[string]string) {
	t.Helper()
	bodies := map[string][]byte{}
	for method, file := range files {
		b, err := fs.ReadFile(testdata, file)
		if err != nil {
			t.Fatal(err)
		}
		bodies["/rest/"+method] = b
	}
	srv.fail = func(w http.ResponseWriter, r *http.Request) {
		if b, ok := bodies[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
			return
		}
		srv.route(w, r)
	}
}

func TestLyricsSynced(t *testing.T) {
	srv := newServer(t)
	serveFixtures(t, srv, map[string]string{"getLyricsBySongId": "getLyricsBySongId.json"})
	l, err := open(t, navidrome.New(navidrome.Options{}), srv.URL).(provider.Lyricist).Lyrics(t.Context(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	// The synced entry wins over the plain one listed first, and the
	// offset (+250ms: sooner) applies, clamped at zero.
	want := []provider.LyricLine{
		{At: 0, Text: "Count it in"},
		{At: 4 * time.Second, Text: "Low pass, high hopes"},
		{At: 8750 * time.Millisecond, Text: ""},
		{At: 12250 * time.Millisecond, Text: "Roll it off"},
	}
	if !slices.Equal(l.Synced, want) {
		t.Errorf("synced = %+v", l.Synced)
	}
	if l.Plain != "Count it in\nLow pass, high hopes\n\nRoll it off" {
		t.Errorf("plain = %q", l.Plain)
	}
	if q := srv.last("/rest/getLyricsBySongId"); q.Get("id") != "s1" {
		t.Errorf("params = %v", q)
	}
}

func TestLyricsNone(t *testing.T) {
	srv := newServer(t)
	serveFixtures(t, srv, map[string]string{"getLyricsBySongId": "getLyricsBySongId-empty.json"})
	_, err := open(t, navidrome.New(navidrome.Options{}), srv.URL).(provider.Lyricist).Lyrics(t.Context(), "s1")
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if srv.count("/rest/getLyrics") != 0 {
		t.Error("fell back to getLyrics when the server said there are none")
	}
}

// Servers without OpenSubsonic don't know getLyricsBySongId; getLyrics has
// plain lyrics, found by artist and title.
func TestLyricsLegacyFallback(t *testing.T) {
	srv := newServer(t)
	serveFixtures(t, srv, map[string]string{"getLyrics": "getLyrics.json"})
	l, err := open(t, navidrome.New(navidrome.Options{}), srv.URL).(provider.Lyricist).Lyrics(t.Context(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if l.Synced != nil || l.Plain != "Plain first line\nPlain second line" {
		t.Errorf("lyrics = %+v", l)
	}
	q := srv.last("/rest/getLyrics")
	if q.Get("artist") != "The Square Roots" || q.Get("title") != "Rolloff" {
		t.Errorf("params = %v", q)
	}

	if _, err := open(t, navidrome.New(navidrome.Options{}), srv.URL).(provider.Lyricist).Lyrics(t.Context(), "missing"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("missing song: got %v, want ErrNotFound", err)
	}
}

func TestLyricsLegacyEmpty(t *testing.T) {
	srv := newServer(t)
	srv.fail = func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/rest/") {
		case "getLyrics":
			ok(w, `"lyrics":{}`)
		default:
			srv.route(w, r)
		}
	}
	_, err := open(t, navidrome.New(navidrome.Options{}), srv.URL).(provider.Lyricist).Lyrics(t.Context(), "s1")
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
