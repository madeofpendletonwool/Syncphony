// SPDX-License-Identifier: AGPL-3.0-only

package nugs

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/providertest"
)

func TestConformance(t *testing.T) {
	f := newFake(t)
	p := f.provider(nil)
	bad := provider.LinkInput{Fields: map[string]string{"email": fakeEmail, "password": "wrong"}}
	providertest.Run(t, providertest.Harness{
		Provider: p,
		Link:     func(t *testing.T) provider.Link { return link(t, p, nil) },
		Query:    "red rocks",
		BadInput: &bad,
		Expire:   func(*testing.T, provider.Link) { f.revoke() },
	})
}

// TestConformanceLive runs the suite against nugs.net. It's skipped unless
// SYNCPHONY_TEST_NUGS_EMAIL and _PASSWORD are set, for an account with a
// subscription. SYNCPHONY_TEST_NUGS_QUERY overrides the search, which must
// find a song and a venue.
func TestConformanceLive(t *testing.T) {
	email, password := os.Getenv("SYNCPHONY_TEST_NUGS_EMAIL"), os.Getenv("SYNCPHONY_TEST_NUGS_PASSWORD")
	if email == "" || password == "" {
		t.Skip("SYNCPHONY_TEST_NUGS_EMAIL and SYNCPHONY_TEST_NUGS_PASSWORD not set")
	}
	p := New(Options{})
	fields := map[string]string{"email": email, "password": password}
	creds, account, err := p.Linker().Complete(t.Context(), provider.LinkInput{Fields: fields})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var mu sync.Mutex
	l := provider.Link{ID: "live", Account: account, Credentials: creds, Sink: func(_ context.Context, c provider.Credentials) error {
		mu.Lock()
		defer mu.Unlock()
		creds = c
		return nil
	}}
	providertest.Run(t, providertest.Harness{
		Provider: p,
		Link: func(*testing.T) provider.Link {
			mu.Lock()
			defer mu.Unlock()
			l.Credentials = creds
			return l
		},
		Query:    cmp.Or(os.Getenv("SYNCPHONY_TEST_NUGS_QUERY"), "red rocks"),
		BadInput: &provider.LinkInput{Fields: map[string]string{"email": email, "password": password + "-wrong"}},
	})
}

func TestLinkNeedsSubscription(t *testing.T) {
	f := newFake(t)
	f.setLapsed(true)
	_, _, err := f.provider(nil).Linker().Complete(t.Context(), provider.LinkInput{Fields: map[string]string{"email": fakeEmail, "password": fakePassword}})
	if !errors.Is(err, provider.ErrInvalidCredentials) || !strings.Contains(err.Error(), "subscription") {
		t.Fatalf("Complete without a subscription: %v", err)
	}
}

func TestLinkKeepsNoPassword(t *testing.T) {
	f := newFake(t)
	l := link(t, f.provider(nil), nil)
	if bytes.Contains(l.Credentials, []byte(fakePassword)) {
		t.Fatalf("credentials hold the password: %s", l.Credentials)
	}
	if l.Account.ID != fakeUserID || l.Account.Name != fakeEmail {
		t.Errorf("account = %+v", l.Account)
	}
}

// TestRefreshRotation checks rotated refresh tokens are saved, and that
// sessions of one link share them: a refresh token works only once.
func TestRefreshRotation(t *testing.T) {
	f := newFake(t)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	p := f.provider(func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now })
	var saved []creds
	var savedMu sync.Mutex
	sink := func(_ context.Context, b provider.Credentials) error {
		var c creds
		if err := json.Unmarshal(b, &c); err != nil {
			t.Errorf("saved credentials: %v", err)
		}
		savedMu.Lock()
		saved = append(saved, c)
		savedMu.Unlock()
		return nil
	}
	l := link(t, p, sink)

	// Sessions opened from the same, soon stale, credentials refresh at
	// once; only one may use the refresh token.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			sess, err := p.Open(t.Context(), l)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := sess.Search(t.Context(), provider.SearchQuery{Text: "sand"}); err != nil {
				t.Errorf("Search: %v", err)
			}
		})
	}
	wg.Wait()
	if f.refreshCount() != 1 {
		t.Errorf("%d refreshes, want 1", f.refreshCount())
	}
	if len(saved) != 1 || saved[0].RefreshToken == "" || saved[0].UserID != fakeUserID {
		t.Fatalf("saved %+v, want the rotated token once", saved)
	}

	// After the access token expires, the next call refreshes with the
	// rotated token, even from a session opened with the original.
	clockMu.Lock()
	now = now.Add(2 * time.Hour)
	clockMu.Unlock()
	sess := open(t, p, l)
	if _, err := sess.Track(t.Context(), "1002.5004"); err != nil {
		t.Fatalf("Track after expiry: %v", err)
	}
	if f.refreshCount() != 2 || len(saved) != 2 || saved[1].RefreshToken == saved[0].RefreshToken {
		t.Errorf("refreshes %d, saved %+v", f.refreshCount(), saved)
	}

	// Linking again starts over with the new credentials.
	l2 := link(t, p, sink)
	if _, err := open(t, p, l2).Track(t.Context(), "1002.5004"); err != nil {
		t.Fatalf("Track after relinking: %v", err)
	}
}

func TestSearchRanking(t *testing.T) {
	f := newFake(t)
	sess := open(t, f.provider(nil), link(t, f.provider(nil), nil))
	page, err := sess.Search(t.Context(), provider.SearchQuery{Text: "  Tweezer ", Kinds: []provider.EntityKind{provider.KindTrack}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tr := range page.Tracks {
		got = append(got, tr.Ref.ID+" "+tr.Title+" | "+tr.Album.Title)
	}
	want := []string{
		// Exact matches, newest first, then undated ones.
		"1001.5001 Tweezer | 2024-12-31 · Madison Square Garden, New York, NY",
		"1002.5003 Tweezer | 2023-07-04 · Red Rocks Amphitheatre, Morrison, CO",
		"1003.5005 Tweezer | Sharin' In The Groove",
		"1001.5002 Tweezer Reprise | 2024-12-31 · Madison Square Garden, New York, NY",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("tracks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	tr := page.Tracks[0]
	if tr.Duration != 15*time.Minute || len(tr.Artists) != 1 || tr.Artists[0] != (provider.ArtistCredit{ID: "62", Name: "Phish"}) ||
		tr.Album.ID != "1001" || tr.Artwork != "/images/shows/msg.jpg" {
		t.Errorf("first track = %+v", tr)
	}

	page, err = sess.Search(t.Context(), provider.SearchQuery{Text: "phish", Kinds: []provider.EntityKind{provider.KindArtist}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Artists) != 1 || page.Artists[0] != (provider.Artist{ID: "62", Name: "Phish"}) {
		t.Errorf("artists = %+v", page.Artists)
	}

	// Paging.
	first, err := sess.Search(t.Context(), provider.SearchQuery{Text: "tweezer", Kinds: []provider.EntityKind{provider.KindTrack}, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	second, err := sess.Search(t.Context(), provider.SearchQuery{Text: "tweezer", Kinds: []provider.EntityKind{provider.KindTrack}, Limit: 3, Cursor: first.Next})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tracks) != 3 || first.Next != "3" || len(second.Tracks) != 1 || second.Next != "" {
		t.Errorf("pages: %d tracks (next %q), then %d (next %q)", len(first.Tracks), first.Next, len(second.Tracks), second.Next)
	}

	if _, err := sess.Search(t.Context(), provider.SearchQuery{Text: "x", Kinds: []provider.EntityKind{provider.KindPlaylist}}); !errors.Is(err, provider.ErrUnsupported) {
		t.Errorf("playlist search: %v", err)
	}
}

func TestArtist(t *testing.T) {
	f := newFake(t)
	p := f.provider(nil)
	sess := open(t, p, link(t, p, nil))
	ar, albums, err := sess.Artist(t.Context(), "62")
	if err != nil {
		t.Fatal(err)
	}
	if ar.Name != "Phish" || len(albums) != 3 || albums[1].Title != "2023-07-04 · Red Rocks Amphitheatre, Morrison, CO" ||
		albums[1].Year != 2023 || albums[1].TrackCount != 3 {
		t.Errorf("Artist = %+v, %+v", ar, albums)
	}
	if _, _, err := sess.Artist(t.Context(), "999"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("missing artist: %v", err)
	}
}

func TestStreamPicksFormat(t *testing.T) {
	f := newFake(t)
	p := f.provider(nil)
	sess := open(t, p, link(t, p, nil))
	for _, tc := range []struct {
		name string
		opts provider.StreamOpts
		want string
	}{
		{"anything goes", provider.StreamOpts{}, "b.flac16"},
		{"browser with FLAC", provider.StreamOpts{Accept: []string{"audio/flac", "audio/mp4", "audio/mpeg"}}, "b.flac16"},
		// AAC over ALAC: both are audio/mp4, and AAC is what plays.
		{"no FLAC", provider.StreamOpts{Accept: []string{"audio/mp4", "audio/mpeg"}}, "c.aac150"},
		{"bitrate cap", provider.StreamOpts{MaxBitrate: 320}, "c.aac150"},
		// Nothing fits: the best, for the transcoder.
		{"MP3 only", provider.StreamOpts{Accept: []string{"audio/mpeg"}}, "b.flac16"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := sess.Stream(t.Context(), "1002.5004", tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Body.Close()
			b, _ := io.ReadAll(a.Body)
			if !bytes.Equal(b, fakeAudio(tc.want, "5004")) {
				t.Errorf("played %.20q..., want %s", b, tc.want)
			}
		})
	}
}

func TestStreamHLS(t *testing.T) {
	f := newFake(t)
	p := f.provider(nil)
	sess := open(t, p, link(t, p, nil))
	a, err := sess.Stream(t.Context(), "1004.5006", provider.StreamOpts{Accept: []string{"audio/mpeg"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Body.Close()
	b, err := io.ReadAll(a.Body)
	if err != nil {
		t.Fatal(err)
	}
	if want := bytes.Join(f.hlsPlain, nil); !bytes.Equal(b, want) {
		t.Errorf("HLS stream = %q, want the high variant decrypted", b)
	}
	if a.ContentType != "video/mp2t" || a.Seekable || a.Length != -1 {
		t.Errorf("stream = %+v", a)
	}
}

func TestNotPlayable(t *testing.T) {
	f := newFake(t)
	p := f.provider(nil)
	sess := open(t, p, link(t, p, nil))
	if err := sess.CheckPlayable(t.Context(), "1003.5005"); !errors.Is(err, provider.ErrNotPlayable) {
		t.Errorf("CheckPlayable on a show that's only sold: %v", err)
	}
	if err := sess.CheckPlayable(t.Context(), "1001.9999"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("CheckPlayable on a missing track: %v", err)
	}

	p = f.provider(nil) // a fresh provider: no cached subscription
	sess = open(t, p, linkThenLapse(t, f, p))
	if err := sess.CheckPlayable(t.Context(), "1001.5001"); !errors.Is(err, provider.ErrNotPlayable) {
		t.Errorf("CheckPlayable after the subscription lapsed: %v", err)
	}
	if _, err := sess.Stream(t.Context(), "1001.5001", provider.StreamOpts{}); !errors.Is(err, provider.ErrNotPlayable) {
		t.Errorf("Stream after the subscription lapsed: %v", err)
	}
}

// linkThenLapse links, then lets the subscription lapse: Complete refuses
// accounts without one.
func linkThenLapse(t *testing.T, f *fakeNugs, p *Provider) provider.Link {
	t.Helper()
	l := link(t, p, nil)
	f.setLapsed(true)
	return l
}

func TestArtworkStaysOnImageHost(t *testing.T) {
	f := newFake(t)
	p := f.provider(nil)
	sess := open(t, p, link(t, p, nil))
	for _, ref := range []provider.ArtworkRef{"//evil.example/x.jpg", "https://evil.example/x.jpg", "/images/../connect/userinfo", "images/x.jpg"} {
		if _, _, err := sess.Artwork(t.Context(), ref, 0); !errors.Is(err, provider.ErrNotFound) {
			t.Errorf("Artwork(%q): %v, want ErrNotFound", ref, err)
		}
	}
}

func TestOpenRejectsBadCredentials(t *testing.T) {
	p := New(Options{})
	for _, c := range []string{"", "{}", `{"refreshToken":"x"}`, "not json"} {
		if _, err := p.Open(t.Context(), provider.Link{ID: "l", Credentials: provider.Credentials(c)}); !errors.Is(err, provider.ErrAuthExpired) {
			t.Errorf("Open(%q): %v, want ErrAuthExpired", c, err)
		}
	}
}

// testdata holds payloads captured from nugs.net, trimmed.
var testdata = os.DirFS("testdata")

func readTestdata(t *testing.T, name string, v any) {
	t.Helper()
	b, err := fs.ReadFile(testdata, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestRealSearch(t *testing.T) {
	var env struct {
		Response searchResponse `json:"Response"`
	}
	readTestdata(t, "search-dark-star.json", &env)
	r := rank(env.Response, "dark star")
	if len(r.artists) != 1 || r.artists[0].Name != "Dark Star Orchestra" || r.artists[0].ID == "" {
		t.Errorf("artists = %+v", r.artists)
	}
	// "Dark Star" itself first, though nugs.net lists it second.
	if len(r.tracks) == 0 || r.tracks[0] != (hit{show: "39571", track: "664838"}) {
		t.Errorf("tracks = %+v", r.tracks)
	}
	if len(r.albums) == 0 {
		t.Fatal("no albums from venue matches")
	}
	for _, al := range r.albums {
		if al.ID == "" || al.Title == "" || !strings.Contains(al.Title, " · ") || al.Year == 0 || al.Artwork == "" {
			t.Errorf("album %+v", al)
		}
	}
}

func TestRealShow(t *testing.T) {
	var env struct {
		Response *show `json:"Response"`
	}
	readTestdata(t, "show-30340.json", &env)
	sh := env.Response
	al := toAlbum(sh)
	want := provider.Album{
		ID: "30340", Title: "2018-02-02 · Aspen District Theater, Aspen, CO",
		Artists: []provider.ArtistCredit{{ID: "1034", Name: "Holly Bowling"}},
		Year:    2018, TrackCount: 17, Artwork: "/images/shows/hollybo180202_01.jpg",
	}
	if al.ID != want.ID || al.Title != want.Title || al.Artists[0] != want.Artists[0] || al.Year != want.Year ||
		al.TrackCount != want.TrackCount || al.Artwork != want.Artwork {
		t.Errorf("album = %+v\nwant %+v", al, want)
	}
	s := &session{link: provider.Link{ID: "l"}}
	tr, ok := findTrack(sh, "541822")
	if !ok {
		t.Fatal("track 541822 not found")
	}
	got := s.track(sh, tr)
	if got.Ref.ID != "30340.541822" || got.Title != "The Other One" || got.Duration != 731*time.Second {
		t.Errorf("track = %+v", got)
	}
	if !sh.streamable() {
		t.Error("show should be streamable")
	}
}

func TestRealArtistShows(t *testing.T) {
	var env struct {
		Response containersAll `json:"Response"`
	}
	readTestdata(t, "containers-artist-1034.json", &env)
	if len(env.Response.Containers) == 0 {
		t.Fatal("no shows")
	}
	al := toAlbum(&env.Response.Containers[0])
	if al.Title != "2026-04-28 · Sweetwater Music Hall, Mill Valley, CA" || al.TrackCount != 15 || al.Year != 2026 {
		t.Errorf("album = %+v", al)
	}
}

func TestParsePlaylistAttrs(t *testing.T) {
	a := attrs(`METHOD=AES-128,URI="https://k.example/key?a=1,b=2",IV=0x000102030405060708090a0b0c0d0e0f`)
	if a["METHOD"] != "AES-128" || a["URI"] != "https://k.example/key?a=1,b=2" || a["IV"] != "0x000102030405060708090a0b0c0d0e0f" {
		t.Errorf("attrs = %v", a)
	}
}

func TestUnwrapJSONP(t *testing.T) {
	for in, want := range map[string]string{
		`{"a":1}`:             `{"a":1}`,
		` callback({"a":1});`: `{"a":1}`,
		`cb({"a":"(x)"})`:     `{"a":"(x)"}`,
	} {
		if got := string(unwrapJSONP([]byte(in))); got != want {
			t.Errorf("unwrapJSONP(%q) = %q, want %q", in, got, want)
		}
	}
}
