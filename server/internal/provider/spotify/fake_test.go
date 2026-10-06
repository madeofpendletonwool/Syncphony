// SPDX-License-Identifier: AGPL-3.0-only

package spotify_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify"
)

const (
	clientID     = "client-1"
	clientSecret = "secret-1"
)

// sid pads a readable name into a well-formed 22-character Spotify ID.
func sid(name string) string { return name + strings.Repeat("0", 22-len(name)) }

var (
	trackSine   = sid("trackSine")
	trackSine2  = sid("trackSine2")
	trackSquare = sid("trackSquare")
	albumSine   = sid("albumSine")
	artistSines = sid("artistSines")
)

// fakeSpotify is the accounts service (app tokens and device pairing),
// Web API and image CDN in one TLS server, with a three-track library.
type fakeSpotify struct {
	*httptest.Server
	t *testing.T

	mu sync.Mutex
	// access are the app tokens that currently work.
	access map[string]bool
	issued int
	// fail, if set, answers every Web API request.
	fail http.HandlerFunc
	// devices are device codes of pairings, and whether they're approved.
	devices map[string]bool
	// pairTokens are access tokens from approved pairings.
	pairTokens map[string]bool
	// pairAs is the account pairings log in as. Default alice.
	pairAs string
	// slowDown makes the next poll of a pairing answer slow_down.
	slowDown bool

	paths   []string
	queries []url.Values
	forms   []url.Values
	auths   []string // Authorization headers of token requests
}

func newFake(t *testing.T) *fakeSpotify {
	f := &fakeSpotify{t: t, access: map[string]bool{}, devices: map[string]bool{}, pairTokens: map[string]bool{}, pairAs: "alice"}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// options returns provider options pointing at the fake.
func (f *fakeSpotify) options(audio spotify.Audio) spotify.Options {
	return spotify.Options{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Audio:        audio,
		Client:       f.Client(),
		AccountsURL:  f.URL,
		APIURL:       f.URL + "/v1",
		ImageURL:     f.URL + "/image",
	}
}

// set changes the fake's settings while it's serving.
func (f *fakeSpotify) set(change func(f *fakeSpotify)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// tokenRequests returns the forms and Authorization headers of the token
// requests so far.
func (f *fakeSpotify) tokenRequests() ([]url.Values, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.forms), slices.Clone(f.auths)
}

func (f *fakeSpotify) image(name string) string { return f.URL + "/image/" + name }

// revokeAccess makes the app tokens issued so far stop working, as if
// Spotify revoked them early.
func (f *fakeSpotify) revokeAccess() {
	f.mu.Lock()
	defer f.mu.Unlock()
	clear(f.access)
}

func (f *fakeSpotify) validAccess(tok string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.access[tok]
}

func (f *fakeSpotify) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.paths {
		if p == path {
			n++
		}
	}
	return n
}

func (f *fakeSpotify) last(path string) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.paths) - 1; i >= 0; i-- {
		if f.paths[i] == path {
			return f.queries[i]
		}
	}
	f.t.Fatalf("no request to %s", path)
	return nil
}

func (f *fakeSpotify) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.queries = append(f.queries, r.URL.Query())
	fail := f.fail
	f.mu.Unlock()

	switch {
	case r.URL.Path == "/oauth2/device/authorize":
		f.deviceAuthorize(w, r)
	case r.URL.Path == "/api/token" && r.FormValue("grant_type") == deviceGrant:
		f.deviceToken(w, r)
	case r.URL.Path == "/api/token":
		f.token(w, r)
	case strings.HasPrefix(r.URL.Path, "/image/"):
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg:" + strings.TrimPrefix(r.URL.Path, "/image/"))) //nolint:gosec // test server
	case strings.HasPrefix(r.URL.Path, "/v1/"):
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !f.validAccess(tok) {
			apiError(w, http.StatusUnauthorized, "The access token expired")
			return
		}
		if fail != nil {
			fail(w, r)
			return
		}
		f.api(w, r, strings.TrimPrefix(r.URL.Path, "/v1"))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeSpotify) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, "invalid_request")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forms = append(f.forms, r.PostForm)
	f.auths = append(f.auths, r.Header.Get("Authorization"))
	if id, secret, ok := r.BasicAuth(); !ok || id != clientID || secret != clientSecret {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_client"}`)
		return
	}
	if r.PostForm.Get("grant_type") != "client_credentials" {
		oauthError(w, "unsupported_grant_type")
		return
	}
	at := f.next("at")
	f.access[at] = true
	writeJSON(w, map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": 3600})
}

const deviceGrant = "urn:ietf:params:oauth:grant-type:device_code"

func (f *fakeSpotify) deviceAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("client_id") != pairClientID || r.FormValue("scope") != "streaming" {
		oauthError(w, "invalid_client")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	dc := f.next("dc")
	f.devices[dc] = false
	code := "CODE" + strings.TrimPrefix(dc, "dc-")
	writeJSON(w, obj{
		"device_code": dc, "user_code": code, "verification_uri": f.URL + "/pair",
		"verification_uri_complete": f.URL + "/pair?code=" + code, "expires_in": 600, "interval": 2,
	})
}

func (f *fakeSpotify) deviceToken(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	approved, ok := f.devices[r.FormValue("device_code")]
	switch {
	case r.FormValue("client_id") != pairClientID:
		oauthError(w, "invalid_client")
	case !ok:
		oauthError(w, "expired_token")
	case f.slowDown:
		f.slowDown = false
		oauthError(w, "slow_down")
	case !approved:
		oauthError(w, "authorization_pending")
	default:
		delete(f.devices, r.FormValue("device_code"))
		pt := f.next("pt")
		f.pairTokens[pt] = true
		writeJSON(w, obj{"access_token": pt, "token_type": "Bearer", "expires_in": 3600})
	}
}

// approve approves every pairing so far, as the user would at spotify.com/pair.
func (f *fakeSpotify) approve() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for dc := range f.devices {
		f.devices[dc] = true
	}
}

// next issues a token. The caller holds mu.
func (f *fakeSpotify) next(prefix string) string {
	f.issued++
	return prefix + "-" + strconv.Itoa(f.issued)
}

func oauthError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, `{"error":%q,"error_description":"nope"}`, code)
}

func apiError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"status":%d,"message":%q}}`, status, msg)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// --- The library -----------------------------------------------------------------

type obj = map[string]any

func (f *fakeSpotify) images(name string) []obj {
	return []obj{
		{"url": f.image(name + "64"), "width": 64, "height": 64},
		{"url": f.image(name + "640"), "width": 640, "height": 640},
		{"url": f.image(name + "300"), "width": 300, "height": 300},
	}
}

func artistCredit() []obj { return []obj{{"id": artistSines, "name": "The Sines"}} }

func (f *fakeSpotify) simpleAlbum() obj {
	return obj{
		"id": albumSine, "name": "Sine Language", "artists": artistCredit(),
		"release_date": "2024-03-01", "total_tracks": 3, "images": f.images("albumSine"),
	}
}

func simpleTrack(id, name string, n int) obj {
	return obj{
		"id": id, "type": "track", "name": name, "duration_ms": 180000 + n*1000,
		"explicit": n == 2, "is_local": false, "artists": artistCredit(),
	}
}

func (f *fakeSpotify) fullTrack(id string) obj {
	var t obj
	switch id {
	case trackSine:
		t = simpleTrack(trackSine, "Sine Wave", 1)
	case trackSine2:
		t = simpleTrack(trackSine2, "Sine of the Times", 2)
	case trackSquare:
		t = simpleTrack(trackSquare, "Square Dance", 3)
	default:
		return nil
	}
	t["album"] = f.simpleAlbum()
	return t
}

var library = []string{trackSine, trackSine2, trackSquare}

func (f *fakeSpotify) api(w http.ResponseWriter, r *http.Request, path string) {
	q := r.URL.Query()
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case path == "/search":
		f.search(w, q)
	case len(parts) == 2 && parts[0] == "tracks":
		t := f.fullTrack(parts[1])
		if t == nil {
			apiError(w, http.StatusNotFound, "Resource not found")
			return
		}
		writeJSON(w, t)
	case len(parts) == 2 && parts[0] == "albums" && parts[1] == albumSine:
		// The embedded page holds two of the three tracks, so the rest
		// must be fetched.
		a := f.simpleAlbum()
		a["tracks"] = obj{
			"items": []obj{simpleTrack(trackSine, "Sine Wave", 1), simpleTrack(trackSine2, "Sine of the Times", 2)},
			"next":  f.URL + "/v1/albums/" + albumSine + "/tracks?offset=2&limit=2", "total": 3,
		}
		writeJSON(w, a)
	case len(parts) == 3 && parts[0] == "albums" && parts[1] == albumSine && parts[2] == "tracks":
		if q.Get("offset") != "2" {
			apiError(w, http.StatusBadRequest, "unexpected offset")
			return
		}
		writeJSON(w, obj{"items": []obj{simpleTrack(trackSquare, "Square Dance", 3)}, "next": nil, "total": 3})
	case len(parts) == 2 && parts[0] == "artists" && parts[1] == artistSines:
		writeJSON(w, obj{"id": artistSines, "name": "The Sines", "images": f.images("artistSines")})
	case len(parts) == 3 && parts[0] == "artists" && parts[1] == artistSines && parts[2] == "albums":
		writeJSON(w, obj{"items": []obj{f.simpleAlbum()}, "next": nil, "total": 1})
	default:
		apiError(w, http.StatusNotFound, "Service not found")
	}
}

func (f *fakeSpotify) search(w http.ResponseWriter, q url.Values) {
	text := strings.ToLower(q.Get("q"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit > 10 {
		apiError(w, http.StatusBadRequest, "Invalid limit")
		return
	}
	page := func(all []obj) obj {
		next := any(nil)
		if offset+limit < len(all) {
			next = "more"
		}
		end := min(offset+limit, len(all))
		items := []obj{}
		if offset < end {
			items = all[offset:end]
		}
		return obj{"items": items, "next": next, "total": len(all)}
	}
	out := obj{}
	for _, typ := range strings.Split(q.Get("type"), ",") {
		var all []obj
		switch typ {
		case "track":
			for _, id := range library {
				if t := f.fullTrack(id); strings.Contains(strings.ToLower(t["name"].(string)), text) {
					all = append(all, t)
				}
			}
			out["tracks"] = page(all)
		case "album":
			if strings.Contains("sine language", text) {
				all = append(all, f.simpleAlbum())
			}
			out["albums"] = page(all)
		case "artist":
			if strings.Contains("the sines", text) {
				all = append(all, obj{"id": artistSines, "name": "The Sines", "images": f.images("artistSines")})
			}
			out["artists"] = page(all)
		}
	}
	writeJSON(w, out)
}

// --- Audio -------------------------------------------------------------------------

// fakeAudio serves the same bytes for every track in the library, except
// trackSquare, whose key Spotify refuses. Pairing tokens come from the fake
// accounts service, and log in as its pairAs account.
type fakeAudio struct {
	f    *fakeSpotify
	open atomic.Int64 // files open
	last atomic.Value // Login of the last Open
}

const pairClientID = "streaming-client"

var audioBytes = bytes.Repeat([]byte("OggS0123456789"), 100)

func (a *fakeAudio) ClientID() string { return pairClientID }

func (a *fakeAudio) Pair(_ context.Context, token string) (string, []byte, error) {
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	if !a.f.pairTokens[token] {
		return "", nil, fmt.Errorf("fake audio: token refused: %w", provider.ErrInvalidCredentials)
	}
	return a.f.pairAs, []byte("stored-" + a.f.pairAs), nil
}

func (a *fakeAudio) login(login spotify.Login, trackID string) error {
	if login.Username != "alice" || string(login.Stored) != "stored-alice" {
		return fmt.Errorf("fake audio: login refused: %w", provider.ErrAuthExpired)
	}
	if a.f.fullTrack(trackID) == nil {
		return fmt.Errorf("fake audio: %s: %w", trackID, provider.ErrNotFound)
	}
	if trackID == trackSquare {
		return fmt.Errorf("fake audio: %s: %w", trackID, provider.ErrNotPlayable)
	}
	return nil
}

func (a *fakeAudio) Check(_ context.Context, login spotify.Login, trackID string) error {
	return a.login(login, trackID)
}

func (a *fakeAudio) Open(_ context.Context, login spotify.Login, trackID string, _ spotify.AudioOpts) (spotify.AudioFile, error) {
	a.last.Store(login)
	if err := a.login(login, trackID); err != nil {
		return nil, err
	}
	a.open.Add(1)
	return &fakeFile{a: a}, nil
}

type fakeFile struct {
	a      *fakeAudio
	closed atomic.Bool
}

func (*fakeFile) ContentType() string { return "audio/ogg" }
func (*fakeFile) Size() int64         { return int64(len(audioBytes)) }

func (*fakeFile) ReadRange(_ context.Context, off, n int64) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(audioBytes[off : off+n])), nil
}

func (f *fakeFile) Close() error {
	if f.closed.CompareAndSwap(false, true) {
		f.a.open.Add(-1)
	}
	return nil
}
