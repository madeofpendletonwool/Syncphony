// SPDX-License-Identifier: AGPL-3.0-only

package navidrome_test

import (
	"bytes"
	"crypto/md5" //nolint:gosec // checking Subsonic token auth
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/navidrome"
)

const (
	username = "alice"
	password = "correct horse"
)

// server is a tiny Subsonic server with one FLAC song, "s1".
type server struct {
	*httptest.Server
	t *testing.T

	mu       sync.Mutex
	requests []url.Values // query of every request, by arrival
	paths    []string
	// fail, if set, handles every request after auth.
	fail http.HandlerFunc
}

var audio = bytes.Repeat([]byte("0123456789"), 100)

func newServer(t *testing.T) *server {
	s := &server{t: t}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *server) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	s.requests = append(s.requests, q)
	s.paths = append(s.paths, r.URL.Path)
	fail := s.fail
	s.mu.Unlock()

	sum := md5.Sum([]byte(password + q.Get("s"))) //nolint:gosec // see import
	if q.Get("u") != username || q.Get("t") != hex.EncodeToString(sum[:]) {
		subsonicError(w, 40, "Wrong username or password")
		return
	}
	if fail != nil {
		fail(w, r)
		return
	}
	s.route(w, r)
}

// route answers the methods the server knows.
func (s *server) route(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch strings.TrimPrefix(r.URL.Path, "/rest/") {
	case "ping":
		ok(w, "")
	case "getSong":
		if q.Get("id") != "s1" {
			subsonicError(w, 70, "Song not found")
			return
		}
		ok(w, `"song":{"id":"s1","title":"Rolloff","album":"Low Pass","albumId":"al1","artist":"The Square Roots","artistId":"ar1","duration":20,"bitRate":900,"contentType":"audio/flac","coverArt":"al-al1","isrc":["USAAA2600001"],"explicitStatus":"explicit","musicBrainzId":"8f3471b5-7e6a-48da-86a9-c1c07a0f47ae"}`)
	case "stream":
		if q.Get("id") != "s1" {
			subsonicError(w, 70, "data not found")
			return
		}
		if f := q.Get("format"); f != "raw" {
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write(audio[:300])
			return
		}
		w.Header().Set("Content-Type", "audio/flac")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(audio))
	case "getCoverArt":
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png:" + q.Get("id") + ":" + q.Get("size"))) //nolint:gosec // test server

	default:
		http.NotFound(w, r)
	}
}

// ok writes a successful response. fields is the JSON object's members, if any.
func ok(w http.ResponseWriter, fields string) {
	w.Header().Set("Content-Type", "application/json")
	if fields != "" {
		fields = "," + fields
	}
	fmt.Fprintf(w, `{"subsonic-response":{"status":"ok","version":"1.16.1"%s}}`, fields)
}

func subsonicError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"subsonic-response":{"status":"failed","version":"1.16.1","error":{"code":%d,"message":%q}}}`, code, msg)
}

func (s *server) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.paths {
		if p == path {
			n++
		}
	}
	return n
}

func (s *server) last(path string) url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.paths) - 1; i >= 0; i-- {
		if s.paths[i] == path {
			return s.requests[i]
		}
	}
	s.t.Fatalf("no request to %s", path)
	return nil
}

func fields(base, pw string) map[string]string {
	return map[string]string{"url": base, "username": username, "password": pw}
}

func link(t *testing.T, p *navidrome.Provider, base string) provider.Link {
	t.Helper()
	creds, account, err := p.Linker().Complete(t.Context(), provider.LinkInput{Fields: fields(base, password)})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return provider.Link{ID: "link-1", Account: account, Credentials: creds}
}

func open(t *testing.T, p *navidrome.Provider, base string) provider.Session {
	t.Helper()
	sess, err := p.Open(t.Context(), link(t, p, base))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

func TestLinkUsesTokenAuth(t *testing.T) {
	srv := newServer(t)
	p := navidrome.New(navidrome.Options{})
	l := link(t, p, srv.URL)
	host := strings.TrimPrefix(srv.URL, "http://")
	if l.Account.ID != username+"@"+host || l.Account.Name != username+" on "+host {
		t.Errorf("account = %+v", l.Account)
	}
	q := srv.last("/rest/ping")
	if q.Has("p") || strings.Contains(q.Encode(), url.QueryEscape(password)) {
		t.Errorf("password sent in the clear: %v", q)
	}
	if q.Get("s") == "" || q.Get("v") == "" || q.Get("c") != "syncphony" || q.Get("f") != "json" {
		t.Errorf("ping params = %v", q)
	}

	// Salts are fresh per request.
	if _, err := open(t, p, srv.URL).Track(t.Context(), "s1"); err != nil {
		t.Fatal(err)
	}
	if srv.last("/rest/getSong").Get("s") == q.Get("s") {
		t.Error("salt reused")
	}
}

func TestLinkNormalizesURL(t *testing.T) {
	srv := newServer(t)
	p := navidrome.New(navidrome.Options{})
	want := link(t, p, srv.URL).Account.ID
	for _, in := range []string{srv.URL + "/", srv.URL + "/app/#/album/x", srv.URL + "/rest", strings.ToUpper(srv.URL[:4]) + srv.URL[4:]} {
		creds, account, err := p.Linker().Complete(t.Context(), provider.LinkInput{Fields: fields(in, password)})
		if err != nil {
			t.Errorf("Complete(%q): %v", in, err)
			continue
		}
		if account.ID != want {
			t.Errorf("Complete(%q): account %q, want %q", in, account.ID, want)
		}
		if bytes.Contains(creds, []byte("/app")) {
			t.Errorf("Complete(%q): stored %s", in, creds)
		}
	}
}

func TestLinkErrors(t *testing.T) {
	srv := newServer(t)
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>my blog</html>")
	}))
	defer html.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()

	p := navidrome.New(navidrome.Options{})
	for _, tc := range []struct {
		name string
		in   map[string]string
		want error
	}{
		{"wrong password", fields(srv.URL, "nope"), provider.ErrInvalidCredentials},
		{"bad URL", fields("ftp://example.com", password), provider.ErrInvalidCredentials},
		{"not Subsonic", fields(html.URL, password), provider.ErrUnavailable},
		{"unreachable", fields(dead.URL, password), provider.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := p.Linker().Complete(t.Context(), provider.LinkInput{Fields: tc.in})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			// Errors get logged, so they must not carry the token.
			if msg := err.Error(); strings.Contains(msg, "t=") || strings.Contains(msg, "s=") || strings.Contains(msg, password) {
				t.Errorf("error leaks auth: %s", msg)
			}
		})
	}
}

func TestSessionErrors(t *testing.T) {
	srv := newServer(t)
	p := navidrome.New(navidrome.Options{})
	sess := open(t, p, srv.URL)
	for _, tc := range []struct {
		name  string
		fail  http.HandlerFunc
		check func(error) bool
	}{
		{
			"password changed", func(w http.ResponseWriter, _ *http.Request) { subsonicError(w, 40, "Wrong username or password") },
			func(err error) bool { return errors.Is(err, provider.ErrAuthExpired) },
		},
		{
			"HTTP 401", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
			func(err error) bool { return errors.Is(err, provider.ErrAuthExpired) },
		},
		{
			"not found", func(w http.ResponseWriter, _ *http.Request) { subsonicError(w, 70, "Song not found") },
			func(err error) bool { return errors.Is(err, provider.ErrNotFound) },
		},
		{"rate limited", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
		}, func(err error) bool { d, ok := provider.RetryAfter(err); return ok && d == 7*time.Second }},
		{
			"down", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
			func(err error) bool { return errors.Is(err, provider.ErrUnavailable) },
		},
		{
			"not permitted", func(w http.ResponseWriter, _ *http.Request) { subsonicError(w, 50, "not authorized") },
			func(err error) bool {
				return err != nil && !errors.Is(err, provider.ErrAuthExpired) && !errors.Is(err, provider.ErrUnavailable)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv.mu.Lock()
			srv.fail = tc.fail
			srv.mu.Unlock()
			defer func() {
				srv.mu.Lock()
				srv.fail = nil
				srv.mu.Unlock()
			}()
			if _, err := sess.Track(t.Context(), "s1"); !tc.check(err) {
				t.Errorf("Track: unexpected error %v", err)
			}
		})
	}
}

func TestRefusesCrossHostRedirect(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Store(true) }))
	defer other.Close()
	// Same port, different host name: 127.0.0.1 vs localhost.
	otherURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherURL+r.URL.RequestURI(), http.StatusFound) //nolint:gosec // the point of the test
	}))
	defer redirect.Close()

	p := navidrome.New(navidrome.Options{})
	_, _, err := p.Linker().Complete(t.Context(), provider.LinkInput{Fields: fields(redirect.URL, password)})
	if !errors.Is(err, provider.ErrUnavailable) {
		t.Errorf("got %v, want ErrUnavailable", err)
	}
	if leaked.Load() {
		t.Error("followed a redirect to another host")
	}
}

func TestTrackMetadata(t *testing.T) {
	srv := newServer(t)
	sess := open(t, navidrome.New(navidrome.Options{}), srv.URL)
	tr, err := sess.Track(t.Context(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	want := provider.Track{
		Ref:      provider.TrackRef{Provider: "navidrome", LinkID: "link-1", ID: "s1"},
		Title:    "Rolloff",
		Artists:  []provider.ArtistCredit{{ID: "ar1", Name: "The Square Roots"}},
		Album:    provider.AlbumCredit{ID: "al1", Title: "Low Pass"},
		Duration: 20 * time.Second,
		ISRC:     "USAAA2600001",
		MBID:     "8f3471b5-7e6a-48da-86a9-c1c07a0f47ae",
		Explicit: true,
		Artwork:  "al-al1",
	}
	if fmt.Sprint(tr) != fmt.Sprint(want) {
		t.Errorf("Track =\n %+v\nwant\n %+v", tr, want)
	}
}

func TestStreamFormat(t *testing.T) {
	srv := newServer(t)
	sess := open(t, navidrome.New(navidrome.Options{}), srv.URL)
	st := sess.(provider.Streamer)
	for _, tc := range []struct {
		name        string
		opts        provider.StreamOpts
		format      string
		maxBitRate  string
		contentType string
	}{
		{"anything goes", provider.StreamOpts{}, "raw", "", "audio/flac"},
		{"original accepted", provider.StreamOpts{Accept: []string{"audio/flac", "audio/mpeg"}}, "raw", "", "audio/flac"},
		{"wildcard", provider.StreamOpts{Accept: []string{"audio/*"}}, "raw", "", "audio/flac"},
		{"needs transcoding", provider.StreamOpts{Accept: []string{"audio/mpeg"}}, "mp3", "192", "audio/mpeg"},
		{"over the bitrate cap", provider.StreamOpts{MaxBitrate: 128}, "mp3", "128", "audio/mpeg"},
		{"opus only", provider.StreamOpts{Accept: []string{"audio/ogg"}}, "opus", "192", "audio/mpeg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := st.Stream(t.Context(), "s1", tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			a.Body.Close()
			q := srv.last("/rest/stream")
			if q.Get("format") != tc.format || q.Get("maxBitRate") != tc.maxBitRate {
				t.Errorf("format=%q maxBitRate=%q, want %q %q", q.Get("format"), q.Get("maxBitRate"), tc.format, tc.maxBitRate)
			}
			if a.ContentType != tc.contentType {
				t.Errorf("content type %q, want %q", a.ContentType, tc.contentType)
			}
		})
	}
}

func TestStreamRangeIgnored(t *testing.T) {
	// A live transcode ignores Range and sends the whole stream.
	srv := newServer(t)
	sess := open(t, navidrome.New(navidrome.Options{}), srv.URL)
	a, err := sess.(provider.Streamer).Stream(t.Context(), "s1", provider.StreamOpts{
		Accept: []string{"audio/mpeg"},
		Range:  &provider.ByteRange{Start: 100, End: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Body.Close()
	if a.Offset != 0 || a.Seekable {
		t.Errorf("Offset %d, Seekable %v; want 0 and false", a.Offset, a.Seekable)
	}
}

func TestArtworkCache(t *testing.T) {
	srv := newServer(t)
	p := navidrome.New(navidrome.Options{})
	sess := open(t, p, srv.URL)
	read := func(sess provider.Session, size int) string {
		t.Helper()
		body, ct, err := sess.Artwork(t.Context(), "al-al1", size)
		if err != nil {
			t.Fatal(err)
		}
		defer body.Close()
		if ct != "image/png" {
			t.Errorf("content type %q", ct)
		}
		b, _ := io.ReadAll(body)
		return string(b)
	}
	if got := read(sess, 0); got != "png:al-al1:600" {
		t.Errorf("default size: got %q", got)
	}
	read(sess, 0)
	read(open(t, p, srv.URL), 0) // another session for the same account
	if n := srv.count("/rest/getCoverArt"); n != 1 {
		t.Errorf("%d fetches, want 1", n)
	}
	read(sess, 300)
	if n := srv.count("/rest/getCoverArt"); n != 2 {
		t.Errorf("%d fetches after a new size, want 2", n)
	}

	uncached := navidrome.New(navidrome.Options{ArtworkCacheBytes: -1})
	s2 := open(t, uncached, srv.URL)
	read(s2, 0)
	read(s2, 0)
	if n := srv.count("/rest/getCoverArt"); n != 4 {
		t.Errorf("%d fetches with the cache off, want 4", n)
	}
}
