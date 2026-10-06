// SPDX-License-Identifier: AGPL-3.0-only

package spotify_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/providertest"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify"
)

func newProvider(t *testing.T, f *fakeSpotify, edit ...func(*spotify.Options)) (*spotify.Provider, *fakeAudio) {
	t.Helper()
	audio := &fakeAudio{f: f}
	opts := f.options(audio)
	for _, e := range edit {
		e(&opts)
	}
	p, err := spotify.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p, audio
}

// pair runs a device pairing that f approves, and returns its result.
func pair(t *testing.T, p *spotify.Provider, f *fakeSpotify) string {
	t.Helper()
	dp := p.Linker().(provider.DevicePairer)
	start, err := dp.BeginPairing(t.Context())
	if err != nil {
		t.Fatalf("BeginPairing: %v", err)
	}
	f.approve()
	paired, err := dp.PollPairing(t.Context(), start.Secret)
	if err != nil {
		t.Fatalf("PollPairing: %v", err)
	}
	return paired
}

func link(t *testing.T, p *spotify.Provider, f *fakeSpotify) provider.Link {
	t.Helper()
	creds, account, err := p.Linker().Complete(t.Context(), provider.LinkInput{Paired: pair(t, p, f)})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return provider.Link{ID: "link-1", Account: account, Credentials: creds}
}

func open(t *testing.T, p *spotify.Provider, l provider.Link) provider.Session {
	t.Helper()
	s, err := p.Open(t.Context(), l)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestConformance(t *testing.T) {
	f := newFake(t)
	p, _ := newProvider(t, f)
	providertest.Run(t, providertest.Harness{
		Provider: p,
		Link:     func(t *testing.T) provider.Link { return link(t, p, f) },
		Query:    "sine",
		BadInput: &provider.LinkInput{Paired: `{"username":"alice"}`},
		// No Expire: searching uses the app's token, so a user's link can't
		// expire for it. TestStreamLoginExpired covers the streaming login.
	})
}

func TestNew(t *testing.T) {
	if _, err := spotify.New(spotify.Options{ClientSecret: clientSecret, Audio: &fakeAudio{}}); err == nil {
		t.Error("New without a client ID succeeded")
	}
	if _, err := spotify.New(spotify.Options{ClientID: clientID, Audio: &fakeAudio{}}); err == nil {
		t.Error("New without a client secret succeeded")
	}
	if _, err := spotify.New(spotify.Options{ClientID: clientID, ClientSecret: clientSecret}); err == nil {
		t.Error("New without an audio backend succeeded")
	}
}

func TestLink(t *testing.T) {
	f := newFake(t)
	p, _ := newProvider(t, f)
	l := p.Linker()
	if l.Method() != provider.LinkDevice || l.Fields() != nil {
		t.Errorf("linker: method %s, fields %v", l.Method(), l.Fields())
	}
	if _, err := l.BeginOAuth(t.Context(), provider.OAuthRequest{}); !errors.Is(err, provider.ErrUnsupported) {
		t.Errorf("BeginOAuth: %v", err)
	}
	link := link(t, p, f)
	if link.Account != (provider.AccountInfo{ID: "alice", Name: "alice"}) {
		t.Errorf("account %+v", link.Account)
	}
	if strings.Contains(string(link.Credentials), clientSecret) {
		t.Error("credentials contain the client secret")
	}
	if _, _, err := l.Complete(t.Context(), provider.LinkInput{}); !errors.Is(err, provider.ErrInvalidCredentials) {
		t.Errorf("Complete without a pairing: %v", err)
	}
}

func TestPairing(t *testing.T) {
	f := newFake(t)
	p, _ := newProvider(t, f)
	dp := p.Linker().(provider.DevicePairer)
	start, err := dp.BeginPairing(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if start.VerifyURL != f.URL+"/pair?code="+start.UserCode || start.Interval != 2*time.Second || start.ExpiresIn != 10*time.Minute {
		t.Errorf("pairing %+v", start)
	}
	if _, err := dp.PollPairing(t.Context(), start.Secret); !errors.Is(err, provider.ErrPending) {
		t.Errorf("before approval: %v", err)
	}
	f.set(func(f *fakeSpotify) { f.slowDown = true })
	if _, err := dp.PollPairing(t.Context(), start.Secret); !errors.Is(err, provider.ErrRateLimited) {
		t.Errorf("slow_down: %v", err)
	}
	f.approve()
	got, err := dp.PollPairing(t.Context(), start.Secret)
	if err != nil {
		t.Fatal(err)
	}
	var pr struct {
		Username string
		Stored   []byte
	}
	if err := json.Unmarshal([]byte(got), &pr); err != nil || pr.Username != "alice" || string(pr.Stored) != "stored-alice" {
		t.Errorf("paired %s (%v)", got, err)
	}
	if _, err := dp.PollPairing(t.Context(), start.Secret); !errors.Is(err, provider.ErrInvalidCredentials) {
		t.Errorf("a used device code: %v", err)
	}
}

func TestAppToken(t *testing.T) {
	f := newFake(t)
	p, _ := newProvider(t, f)
	s := open(t, p, link(t, p, f))
	for range 3 {
		if _, err := s.Track(t.Context(), trackSine); err != nil {
			t.Fatal(err)
		}
	}
	forms, auths := f.tokenRequests()
	if len(forms) != 1 || forms[0].Get("grant_type") != "client_credentials" {
		t.Fatalf("token requests %v: want one client credentials grant, reused", forms)
	}
	if id, secret, ok := basicAuth(auths[0]); !ok || id != clientID || secret != clientSecret {
		t.Errorf("token request Authorization %q", auths[0])
	}

	// A token Spotify revoked early is replaced once.
	f.revokeAccess()
	if _, err := s.Track(t.Context(), trackSine); err != nil {
		t.Fatalf("Track after the token was revoked: %v", err)
	}
	if forms, _ := f.tokenRequests(); len(forms) != 2 {
		t.Errorf("%d token requests, want 2", len(forms))
	}

	// Wrong app credentials are the server's problem, not the user's.
	bad, _ := newProvider(t, f, func(o *spotify.Options) { o.ClientSecret = "wrong" })
	_, err := open(t, bad, link(t, bad, f)).Track(t.Context(), trackSine)
	if !errors.Is(err, provider.ErrUnavailable) || errors.Is(err, provider.ErrAuthExpired) || strings.Contains(err.Error(), "wrong") {
		t.Errorf("wrong client secret: %v (unavailable, not expired, and without the secret)", err)
	}
}

func basicAuth(h string) (id, secret string, ok bool) {
	r := http.Request{Header: http.Header{"Authorization": {h}}}
	return r.BasicAuth()
}

func TestSessionErrors(t *testing.T) {
	f := newFake(t)
	p, _ := newProvider(t, f)
	s := open(t, p, link(t, p, f))
	ctx := t.Context()

	failWith := func(h http.HandlerFunc) { f.set(func(f *fakeSpotify) { f.fail = h }) }
	failWith(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		apiError(w, http.StatusTooManyRequests, "API rate limit exceeded")
	})
	_, err := s.Track(ctx, trackSine)
	if d, ok := provider.RetryAfter(err); !errors.Is(err, provider.ErrRateLimited) || !ok || d != 7*time.Second {
		t.Errorf("429: %v (retry after %v)", err, d)
	}

	failWith(func(w http.ResponseWriter, _ *http.Request) { apiError(w, http.StatusBadGateway, "Bad gateway") })
	if _, err := s.Track(ctx, trackSine); !errors.Is(err, provider.ErrUnavailable) {
		t.Errorf("502: %v", err)
	}

	failWith(func(w http.ResponseWriter, _ *http.Request) { apiError(w, http.StatusForbidden, "Forbidden") })
	if _, err := s.Track(ctx, trackSine); err == nil || errors.Is(err, provider.ErrAuthExpired) {
		t.Errorf("403: %v, want an error that isn't ErrAuthExpired", err)
	}
	failWith(nil)

	before := f.count("/v1/tracks/bad")
	if _, err := s.Track(ctx, "bad"); !errors.Is(err, provider.ErrNotFound) || f.count("/v1/tracks/bad") != before {
		t.Errorf("malformed ID: %v, and it shouldn't reach Spotify", err)
	}
}

func TestMetadata(t *testing.T) {
	f := newFake(t)
	p, _ := newProvider(t, f)
	s := open(t, p, link(t, p, f))
	tr, err := s.Track(t.Context(), trackSine2)
	if err != nil {
		t.Fatal(err)
	}
	want := provider.Track{
		Ref:      provider.TrackRef{Provider: "spotify", LinkID: "link-1", ID: trackSine2},
		Title:    "Sine of the Times",
		Artists:  []provider.ArtistCredit{{ID: artistSines, Name: "The Sines"}},
		Album:    provider.AlbumCredit{ID: albumSine, Title: "Sine Language"},
		Duration: 182 * time.Second,
		Explicit: true,
		Artwork:  "640:albumSine640,300:albumSine300,64:albumSine64",
	}
	if tr.Ref != want.Ref || tr.Title != want.Title || tr.Album != want.Album || tr.Duration != want.Duration ||
		!tr.Explicit || tr.Artwork != want.Artwork || len(tr.Artists) != 1 || tr.Artists[0] != want.Artists[0] {
		t.Errorf("Track:\n got %+v\nwant %+v", tr, want)
	}

	al, tracks, err := s.Album(t.Context(), albumSine)
	if err != nil {
		t.Fatal(err)
	}
	if al.Year != 2024 || al.TrackCount != 3 || len(tracks) != 3 {
		t.Fatalf("Album: %+v with %d tracks", al, len(tracks))
	}
	for _, tr := range tracks {
		if tr.Album.ID != albumSine || tr.Artwork != want.Artwork {
			t.Errorf("album track %q: album %+v, artwork %q", tr.Title, tr.Album, tr.Artwork)
		}
	}
}

func TestSearchLimit(t *testing.T) {
	f := newFake(t)
	p, _ := newProvider(t, f)
	s := open(t, p, link(t, p, f))
	if _, err := s.Search(t.Context(), provider.SearchQuery{Text: "sine", Limit: 50}); err != nil {
		t.Fatal(err)
	}
	if got := f.last("/v1/search").Get("limit"); got != "10" {
		t.Errorf("limit %s, want Spotify's development-mode maximum of 10", got)
	}
	if got := f.last("/v1/search").Get("type"); got != "track,album,artist" {
		t.Errorf("type %s", got)
	}
	if _, err := s.Search(t.Context(), provider.SearchQuery{Text: "sine", Kinds: []provider.EntityKind{provider.KindPlaylist}}); !errors.Is(err, provider.ErrUnsupported) {
		t.Errorf("playlist search: %v", err)
	}
	page, err := s.Search(t.Context(), provider.SearchQuery{Text: "  "})
	if err != nil || len(page.Tracks) > 0 {
		t.Errorf("blank search: %+v, %v", page, err)
	}
}

func TestArtwork(t *testing.T) {
	f := newFake(t)
	p, _ := newProvider(t, f)
	s := open(t, p, link(t, p, f))
	tr, err := s.Track(t.Context(), trackSine)
	if err != nil {
		t.Fatal(err)
	}
	for size, want := range map[int]string{0: "albumSine640", 200: "albumSine300", 300: "albumSine300", 64: "albumSine64", 1000: "albumSine640"} {
		body, ct, err := s.Artwork(t.Context(), tr.Artwork, size)
		if err != nil {
			t.Fatalf("Artwork size %d: %v", size, err)
		}
		b, _ := io.ReadAll(body)
		body.Close()
		if ct != "image/jpeg" || string(b) != "jpeg:"+want {
			t.Errorf("Artwork size %d: %s %q, want %s", size, ct, b, want)
		}
	}

	// Refs come back from browsers: they mustn't make the server fetch
	// from anywhere but Spotify's image hosts.
	for _, ref := range []provider.ArtworkRef{
		"640:https://evil.example.com/x.jpg",
		"640:http://i.scdn.co/image/abc",
		"640:../../v1/me",
		"nonsense",
	} {
		if _, _, err := s.Artwork(t.Context(), ref, 0); !errors.Is(err, provider.ErrNotFound) {
			t.Errorf("Artwork(%q): %v, want ErrNotFound", ref, err)
		}
	}
}

func TestStream(t *testing.T) {
	f := newFake(t)
	p, audio := newProvider(t, f)
	s := open(t, p, link(t, p, f)).(provider.Streamer)
	a, err := s.Stream(t.Context(), trackSine, provider.StreamOpts{Range: &provider.ByteRange{Start: 10, End: 19}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(a.Body)
	if string(b) != string(audioBytes[10:20]) || a.ContentRange() != "bytes 10-19/1400" || a.ContentType != "audio/ogg" {
		t.Errorf("range stream: %q, %s, %s", b, a.ContentRange(), a.ContentType)
	}
	if audio.open.Load() != 1 {
		t.Error("file closed before the body")
	}
	a.Body.Close()
	if audio.open.Load() != 0 {
		t.Error("closing the body didn't close the file")
	}
	if login := audio.last.Load().(spotify.Login); login.Username != "alice" || string(login.Stored) != "stored-alice" {
		t.Errorf("audio login %+v", login)
	}

	if _, err := s.Stream(t.Context(), trackSine, provider.StreamOpts{Range: &provider.ByteRange{Start: 5000, End: -1}}); !errors.Is(err, provider.ErrRange) {
		t.Errorf("range past the end: %v", err)
	}
	if audio.open.Load() != 0 {
		t.Error("a refused range left the file open")
	}
}

func TestStreamLoginExpired(t *testing.T) {
	f := newFake(t)
	p, _ := newProvider(t, f)
	l := link(t, p, f)
	var c map[string]any
	_ = json.Unmarshal(l.Credentials, &c)
	c["stream_creds"] = []byte("revoked")
	l.Credentials, _ = json.Marshal(c)
	if _, err := open(t, p, l).(provider.Streamer).Stream(t.Context(), trackSine, provider.StreamOpts{}); !errors.Is(err, provider.ErrAuthExpired) {
		t.Errorf("stream with a revoked streaming login: %v", err)
	}

	delete(c, "stream_creds")
	l.Credentials, _ = json.Marshal(c)
	if _, err := p.Open(t.Context(), l); !errors.Is(err, provider.ErrAuthExpired) {
		t.Errorf("credentials without a streaming login: %v", err)
	}
}

func TestCheckPlayable(t *testing.T) {
	f := newFake(t)
	p, _ := newProvider(t, f)
	s := open(t, p, link(t, p, f)).(provider.PlayChecker)
	if err := s.CheckPlayable(t.Context(), trackSine); err != nil {
		t.Errorf("playable track: %v", err)
	}
	if err := s.CheckPlayable(t.Context(), trackSquare); !errors.Is(err, provider.ErrNotPlayable) {
		t.Errorf("refused track: %v", err)
	}
	if err := s.CheckPlayable(t.Context(), "bad"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("malformed ID: %v", err)
	}
}

func TestPlaylists(t *testing.T) {
	f := newFake(t)
	p, _ := newProvider(t, f)
	if !p.Info().Capabilities.Playlists {
		t.Error("no Playlists capability with a library")
	}
	pl := open(t, p, link(t, p, f)).(provider.PlaylistLister)
	ctx := t.Context()

	page, err := pl.Playlists(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []provider.Playlist{
		{ID: spotify.LikedSongsID, Name: "Liked Songs", Owner: "alice", TrackCount: 2},
		{ID: playlistSines, Name: "Sines", Owner: "alice", TrackCount: 3, Artwork: "300:playlistSines300"},
		{ID: playlistLong, Name: "Long", Owner: "spotify", TrackCount: 150},
	}
	if !slices.Equal(page.Items, want) || page.Next != "" {
		t.Errorf("Playlists: %+v", page)
	}
	if _, err := pl.Playlists(ctx, "1"); err == nil {
		t.Error("Playlists with a cursor succeeded")
	}

	tp, err := pl.PlaylistTracks(ctx, playlistSines, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tp.Items) != 2 || tp.Next != "" {
		t.Fatalf("Sines: %+v", tp)
	}
	first := tp.Items[0]
	if first.Ref != (provider.TrackRef{Provider: spotify.ID, LinkID: "link-1", ID: trackSine}) || first.Title != "Sine Wave" ||
		first.Album != (provider.AlbumCredit{ID: albumSine, Title: "Sine Language"}) || first.Duration != 181*time.Second ||
		first.Artwork != "640:albumSine640,64:albumSine64" || len(first.Artists) != 1 {
		t.Errorf("track %+v", first)
	}
	if !tp.Items[1].Explicit {
		t.Errorf("track %+v isn't explicit", tp.Items[1])
	}

	// Pages cover a fixed number of items, so a page can hold fewer tracks.
	tp, err = pl.PlaylistTracks(ctx, playlistLong, "")
	if err != nil || len(tp.Items) != 1 || tp.Items[0].Ref.ID != trackSquare || tp.Next != "100" {
		t.Fatalf("Long, page 1: %+v, %v", tp, err)
	}
	tp, err = pl.PlaylistTracks(ctx, playlistLong, tp.Next)
	if err != nil || len(tp.Items) != 1 || tp.Items[0].Ref.ID != trackSine || tp.Next != "" {
		t.Fatalf("Long, page 2: %+v, %v", tp, err)
	}

	// Liked Songs has an ID of its own, which isn't a Spotify ID.
	tp, err = pl.PlaylistTracks(ctx, spotify.LikedSongsID, "")
	if err != nil || len(tp.Items) != 2 || tp.Items[0].Ref.ID != trackSine2 || tp.Next != "" {
		t.Fatalf("Liked Songs: %+v, %v", tp, err)
	}

	for _, id := range []string{sid("playlistNone"), "not an ID"} {
		if _, err := pl.PlaylistTracks(ctx, id, ""); !errors.Is(err, provider.ErrNotFound) {
			t.Errorf("PlaylistTracks(%q): %v, want ErrNotFound", id, err)
		}
	}
	if _, err := pl.PlaylistTracks(ctx, playlistSines, "-1"); err == nil {
		t.Error("PlaylistTracks with a bad cursor succeeded")
	}
}

func TestNoLibrary(t *testing.T) {
	f := newFake(t)
	p, _ := newProvider(t, f, func(o *spotify.Options) { o.Library = nil })
	if p.Info().Capabilities.Playlists {
		t.Error("Playlists capability without a library")
	}
	if _, ok := open(t, p, link(t, p, f)).(provider.PlaylistLister); ok {
		t.Error("session lists playlists without a library")
	}
}
