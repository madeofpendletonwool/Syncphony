// SPDX-License-Identifier: AGPL-3.0-only

// Package providertest is the conformance suite every provider runs. It
// checks the parts of the provider contract that the type system can't:
// that capabilities match the interfaces a session implements, that search
// results resolve, that streams honor ranges, and that failures map to the
// provider package's errors.
//
// A provider's tests call Run with a Harness:
//
//	func TestConformance(t *testing.T) {
//		providertest.Run(t, providertest.Harness{
//			Provider: p,
//			Link:     func(t *testing.T) provider.Link { ... },
//			Query:    "a query that finds tracks",
//		})
//	}
package providertest

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Harness tells the suite how to exercise a provider.
type Harness struct {
	Provider provider.Provider
	// Link returns a working link to a test account. Required.
	Link func(t *testing.T) provider.Link
	// Query is search text that finds at least one track, and at least one
	// album if the provider can search albums. Required.
	Query string
	// MissingID is a well-formed ID that doesn't exist on the service.
	// Defaults to "syncphony-conformance-missing".
	MissingID string
	// BadInput is link input that Complete must reject with
	// ErrInvalidCredentials. Nil skips that check.
	BadInput *provider.LinkInput
	// Expire makes link's credentials stop working, e.g. by revoking its
	// token. Nil skips the ErrAuthExpired check.
	Expire func(t *testing.T, link provider.Link)
}

// Run runs the conformance suite as subtests of t.
func Run(t *testing.T, h Harness) {
	t.Helper()
	if h.Provider == nil || h.Link == nil || h.Query == "" {
		t.Fatal("providertest: Harness needs Provider, Link and Query")
	}
	if h.MissingID == "" {
		h.MissingID = "syncphony-conformance-missing"
	}
	s := &suite{Harness: h, info: h.Provider.Info()}
	t.Run("Info", s.testInfo)
	t.Run("Linker", s.testLinker)
	t.Run("Capabilities", s.testCapabilities)
	t.Run("Search", s.testSearch)
	t.Run("NotFound", s.testNotFound)
	t.Run("Artwork", s.testArtwork)
	t.Run("Concurrency", s.testConcurrency)
	switch s.info.Capabilities.Playback {
	case provider.PlaybackStream:
		t.Run("Stream", s.testStream)
	case provider.PlaybackRemote:
		t.Run("Remote", s.testRemote)
	}
	if s.info.Capabilities.Playlists {
		t.Run("Playlists", s.testPlaylists)
	}
	if s.info.Capabilities.Lyrics {
		t.Run("Lyrics", s.testLyrics)
	}
	t.Run("AuthExpired", s.testAuthExpired)
}

type suite struct {
	Harness
	info provider.Info
}

func (s *suite) open(t *testing.T) (provider.Session, provider.Link) {
	t.Helper()
	link := s.Link(t)
	sess, err := s.Provider.Open(t.Context(), link)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := sess.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return sess, link
}

// someTrack returns a track found by Query.
func (s *suite) someTrack(t *testing.T, sess provider.Session) provider.Track {
	t.Helper()
	page, err := sess.Search(t.Context(), provider.SearchQuery{Text: s.Query, Kinds: []provider.EntityKind{provider.KindTrack}})
	if err != nil {
		t.Fatalf("Search(%q): %v", s.Query, err)
	}
	if len(page.Tracks) == 0 {
		t.Fatalf("Search(%q) found no tracks", s.Query)
	}
	return page.Tracks[0]
}

func (s *suite) testInfo(t *testing.T) {
	if err := provider.Validate(s.Provider); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.NewRegistry(s.Provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func (s *suite) testLinker(t *testing.T) {
	l := s.Provider.Linker()
	switch l.Method() {
	case provider.LinkCredentials:
		if _, err := l.BeginOAuth(t.Context(), provider.OAuthRequest{State: "x", RedirectURL: "https://example.com/cb"}); !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("BeginOAuth on a credentials linker: got %v, want ErrUnsupported", err)
		}
	case provider.LinkOAuth2:
		const state = "providertest-state"
		start, err := l.BeginOAuth(t.Context(), provider.OAuthRequest{State: state, RedirectURL: "https://syncphony.example.com/api/links/callback"})
		if err != nil {
			t.Fatalf("BeginOAuth: %v", err)
		}
		u, err := url.Parse(start.AuthURL)
		if err != nil || u.Scheme != "https" {
			t.Fatalf("BeginOAuth URL %q: want an https URL (%v)", start.AuthURL, err)
		}
		if got := u.Query().Get("state"); got != state {
			t.Errorf("BeginOAuth URL state = %q, want %q", got, state)
		}
	}
	if s.BadInput != nil {
		if _, _, err := l.Complete(t.Context(), *s.BadInput); !errors.Is(err, provider.ErrInvalidCredentials) {
			t.Errorf("Complete(BadInput): got %v, want ErrInvalidCredentials", err)
		}
	}
	link := s.Link(t)
	if len(link.Credentials) == 0 {
		t.Error("Link returned empty credentials")
	}
	if link.Account.ID == "" {
		t.Error("Link returned an empty account ID")
	}
}

func (s *suite) testCapabilities(t *testing.T) {
	sess, _ := s.open(t)
	c := s.info.Capabilities
	check := func(name string, declared, implemented bool) {
		if declared != implemented {
			t.Errorf("%s: capability declared %v, session implements it %v", name, declared, implemented)
		}
	}
	_, streamer := sess.(provider.Streamer)
	_, remote := sess.(provider.Remote)
	_, playlists := sess.(provider.PlaylistLister)
	_, lyrics := sess.(provider.Lyricist)
	check("Streamer", c.Playback == provider.PlaybackStream, streamer)
	check("Remote", c.Playback == provider.PlaybackRemote, remote)
	check("PlaylistLister", c.Playlists, playlists)
	check("Lyricist", c.Lyrics, lyrics)
}

func (s *suite) testSearch(t *testing.T) {
	sess, link := s.open(t)
	ctx := t.Context()
	c := s.info.Capabilities

	page, err := sess.Search(ctx, provider.SearchQuery{Text: s.Query, Kinds: []provider.EntityKind{provider.KindTrack}, Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Tracks) == 0 {
		t.Fatalf("Search(%q) found no tracks", s.Query)
	}
	if len(page.Tracks) > 5 {
		t.Errorf("Search with Limit 5 returned %d tracks", len(page.Tracks))
	}
	if len(page.Albums)+len(page.Artists)+len(page.Playlists) > 0 {
		t.Error("Search for tracks only returned other kinds")
	}
	for _, tr := range page.Tracks {
		s.checkTrack(t, tr, link)
		got, err := sess.Track(ctx, tr.Ref.ID)
		if err != nil {
			t.Errorf("Track(%q) from search: %v", tr.Ref.ID, err)
			continue
		}
		if got.Ref != tr.Ref || got.Title != tr.Title {
			t.Errorf("Track(%q) = %+v, search said %+v", tr.Ref.ID, got.Ref, tr.Ref)
		}
	}

	if c.CanSearch(provider.KindAlbum) {
		page, err := sess.Search(ctx, provider.SearchQuery{Text: s.Query, Kinds: []provider.EntityKind{provider.KindAlbum}, Limit: 5})
		if err != nil {
			t.Fatalf("Search albums: %v", err)
		}
		if len(page.Albums) == 0 {
			t.Fatalf("Search(%q) found no albums", s.Query)
		}
		a := page.Albums[0]
		got, tracks, err := sess.Album(ctx, a.ID)
		if err != nil {
			t.Fatalf("Album(%q) from search: %v", a.ID, err)
		}
		if got.ID != a.ID || got.Title == "" {
			t.Errorf("Album(%q) = %+v", a.ID, got)
		}
		if len(tracks) == 0 {
			t.Errorf("Album(%q) has no tracks", a.ID)
		}
		for _, tr := range tracks {
			s.checkTrack(t, tr, link)
		}
	}

	for _, k := range []provider.EntityKind{provider.KindAlbum, provider.KindArtist, provider.KindPlaylist} {
		if c.CanSearch(k) {
			continue
		}
		if _, err := sess.Search(ctx, provider.SearchQuery{Text: s.Query, Kinds: []provider.EntityKind{k}}); !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("Search for undeclared kind %s: got %v, want ErrUnsupported", k, err)
		}
	}

	// Paging: the second page must not repeat the first.
	first, err := sess.Search(ctx, provider.SearchQuery{Text: s.Query, Kinds: []provider.EntityKind{provider.KindTrack}, Limit: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if first.Next != "" {
		second, err := sess.Search(ctx, provider.SearchQuery{Text: s.Query, Kinds: []provider.EntityKind{provider.KindTrack}, Limit: 1, Cursor: first.Next})
		if err != nil {
			t.Fatalf("Search page 2: %v", err)
		}
		if len(second.Tracks) > 0 && second.Tracks[0].Ref == first.Tracks[0].Ref {
			t.Error("Search page 2 repeats page 1")
		}
	}
}

func (s *suite) checkTrack(t *testing.T, tr provider.Track, link provider.Link) {
	t.Helper()
	if tr.Ref.Provider != s.info.ID || tr.Ref.LinkID != link.ID || tr.Ref.ID == "" {
		t.Errorf("track %q: ref %+v, want provider %q, link %q and an ID", tr.Title, tr.Ref, s.info.ID, link.ID)
	}
	if tr.Title == "" {
		t.Errorf("track %+v has no title", tr.Ref)
	}
	if len(tr.Artists) == 0 {
		t.Errorf("track %q has no artists", tr.Title)
	}
	if tr.Duration <= 0 {
		t.Errorf("track %q has duration %v", tr.Title, tr.Duration)
	}
}

func (s *suite) testNotFound(t *testing.T) {
	sess, _ := s.open(t)
	ctx := t.Context()
	if _, err := sess.Track(ctx, s.MissingID); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("Track(missing): got %v, want ErrNotFound", err)
	}
	if _, _, err := sess.Album(ctx, s.MissingID); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("Album(missing): got %v, want ErrNotFound", err)
	}
	if st, ok := sess.(provider.Streamer); ok {
		a, err := st.Stream(ctx, s.MissingID, provider.StreamOpts{})
		if err == nil {
			a.Body.Close()
		}
		if !errors.Is(err, provider.ErrNotFound) {
			t.Errorf("Stream(missing): got %v, want ErrNotFound", err)
		}
	}
	if r, ok := sess.(provider.Remote); ok {
		if err := r.Play(ctx, s.MissingID, 0); !errors.Is(err, provider.ErrNotFound) {
			t.Errorf("Play(missing): got %v, want ErrNotFound", err)
		}
	}
}

func (s *suite) testArtwork(t *testing.T) {
	sess, _ := s.open(t)
	tr := s.someTrack(t, sess)
	if !s.info.Capabilities.Artwork {
		body, _, err := sess.Artwork(t.Context(), tr.Artwork, 300)
		if err == nil {
			body.Close()
		}
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("Artwork without the capability: got %v, want ErrUnsupported", err)
		}
		return
	}
	if tr.Artwork == "" {
		t.Skipf("track %q has no artwork", tr.Title)
	}
	body, ct, err := sess.Artwork(t.Context(), tr.Artwork, 300)
	if err != nil {
		t.Fatalf("Artwork(%q): %v", tr.Artwork, err)
	}
	defer body.Close()
	if mt, _, _ := mime.ParseMediaType(ct); !strings.HasPrefix(mt, "image/") {
		t.Errorf("Artwork content type %q, want image/*", ct)
	}
	if b, err := io.ReadAll(body); err != nil || len(b) == 0 {
		t.Errorf("Artwork body: %d bytes, %v", len(b), err)
	}
}

// testConcurrency runs calls from several goroutines, for the race detector.
func (s *suite) testConcurrency(t *testing.T) {
	sess, _ := s.open(t)
	tr := s.someTrack(t, sess)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 8 {
		wg.Go(func() {
			if _, err := sess.Search(t.Context(), provider.SearchQuery{Text: s.Query}); err != nil {
				errs <- err
			}
			if _, err := sess.Track(t.Context(), tr.Ref.ID); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func (s *suite) testStream(t *testing.T) {
	sess, _ := s.open(t)
	st := sess.(provider.Streamer)
	ctx := t.Context()
	tr := s.someTrack(t, sess)

	a, err := st.Stream(ctx, tr.Ref.ID, provider.StreamOpts{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	full, err := io.ReadAll(a.Body)
	a.Body.Close()
	if err != nil {
		t.Fatalf("reading stream: %v", err)
	}
	if mt, _, _ := mime.ParseMediaType(a.ContentType); !strings.HasPrefix(mt, "audio/") {
		t.Errorf("content type %q, want audio/*", a.ContentType)
	}
	if a.Offset != 0 {
		t.Errorf("full stream Offset = %d, want 0", a.Offset)
	}
	if a.Length >= 0 && a.Length != int64(len(full)) {
		t.Errorf("Length = %d, but read %d bytes", a.Length, len(full))
	}
	if a.Size >= 0 && a.Size != int64(len(full)) {
		t.Errorf("Size = %d, but the full stream is %d bytes", a.Size, len(full))
	}
	if a.ContentRange() != "" {
		t.Errorf("full stream ContentRange = %q, want empty", a.ContentRange())
	}
	if len(full) < 200 {
		t.Fatalf("stream is only %d bytes", len(full))
	}
	if !a.Seekable {
		return
	}

	ranges := []provider.ByteRange{
		{Start: 100, End: 199},
		{Start: int64(len(full)) / 2, End: -1},
		{Start: int64(len(full)) - 10, End: int64(len(full)) + 1000}, // End past EOF is clamped
	}
	for _, r := range ranges {
		a, err := st.Stream(ctx, tr.Ref.ID, provider.StreamOpts{Range: &r})
		if err != nil {
			t.Errorf("Stream range %+v: %v", r, err)
			continue
		}
		got, err := io.ReadAll(a.Body)
		a.Body.Close()
		if err != nil {
			t.Errorf("reading range %+v: %v", r, err)
			continue
		}
		end := min(r.End, int64(len(full))-1)
		if r.End < 0 {
			end = int64(len(full)) - 1
		}
		if a.Offset != r.Start {
			t.Errorf("range %+v: Offset = %d", r, a.Offset)
		}
		if a.Length >= 0 && a.Length != int64(len(got)) {
			t.Errorf("range %+v: Length = %d, but read %d bytes", r, a.Length, len(got))
		}
		if !bytes.Equal(got, full[r.Start:end+1]) {
			t.Errorf("range %+v: got %d bytes that don't match the file", r, len(got))
		}
	}

	r := provider.ByteRange{Start: int64(len(full)) + 10, End: -1}
	a, err = st.Stream(ctx, tr.Ref.ID, provider.StreamOpts{Range: &r})
	if err == nil {
		a.Body.Close()
	}
	if !errors.Is(err, provider.ErrRange) {
		t.Errorf("range past EOF: got %v, want ErrRange", err)
	}
}

func (s *suite) testRemote(t *testing.T) {
	sess, _ := s.open(t)
	r := sess.(provider.Remote)
	ctx := t.Context()
	tr := s.someTrack(t, sess)
	const slack = 5 * time.Second

	state := func() provider.RemoteState {
		t.Helper()
		st, err := r.State(ctx)
		if err != nil {
			t.Fatalf("State: %v", err)
		}
		return st
	}
	if err := r.Play(ctx, tr.Ref.ID, 2*time.Second); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if st := state(); st.TrackID != tr.Ref.ID || !st.Playing || st.Position < 2*time.Second || st.Position > 2*time.Second+slack {
		t.Errorf("after Play at 2s: %+v", st)
	}
	if err := r.Pause(ctx); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if st := state(); st.Playing {
		t.Errorf("after Pause: %+v", st)
	}
	seek := min(tr.Duration/2, 10*time.Second)
	if err := r.Seek(ctx, seek); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if st := state(); st.Position < seek || st.Position > seek+slack {
		t.Errorf("after Seek to %v: %+v", seek, st)
	}
	if err := r.Resume(ctx); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if st := state(); !st.Playing || st.TrackID != tr.Ref.ID {
		t.Errorf("after Resume: %+v", st)
	}
	if err := r.Pause(ctx); err != nil {
		t.Fatalf("Pause: %v", err)
	}
}

func (s *suite) testPlaylists(t *testing.T) {
	sess, link := s.open(t)
	pl := sess.(provider.PlaylistLister)
	ctx := t.Context()
	page, err := pl.Playlists(ctx, "")
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(page.Items) == 0 {
		t.Skip("test account has no playlists")
	}
	p := page.Items[0]
	if p.ID == "" || p.Name == "" {
		t.Errorf("playlist %+v needs an ID and name", p)
	}
	seen := map[provider.TrackRef]bool{}
	cursor := ""
	for range 100 {
		tp, err := pl.PlaylistTracks(ctx, p.ID, cursor)
		if err != nil {
			t.Fatalf("PlaylistTracks(%q, %q): %v", p.ID, cursor, err)
		}
		for _, tr := range tp.Items {
			s.checkTrack(t, tr, link)
			seen[tr.Ref] = true
		}
		if tp.Next == "" {
			break
		}
		if tp.Next == cursor {
			t.Fatalf("PlaylistTracks cursor %q doesn't advance", cursor)
		}
		cursor = tp.Next
	}
	if p.TrackCount > 0 && len(seen) == 0 {
		t.Errorf("playlist %q says %d tracks but lists none", p.Name, p.TrackCount)
	}
	if _, err := pl.PlaylistTracks(ctx, s.MissingID, ""); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("PlaylistTracks(missing): got %v, want ErrNotFound", err)
	}
}

func (s *suite) testLyrics(t *testing.T) {
	sess, _ := s.open(t)
	l := sess.(provider.Lyricist)
	tr := s.someTrack(t, sess)
	if _, err := l.Lyrics(t.Context(), tr.Ref.ID); err != nil && !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("Lyrics: %v", err)
	}
	if _, err := l.Lyrics(t.Context(), s.MissingID); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("Lyrics(missing): got %v, want ErrNotFound", err)
	}
}

func (s *suite) testAuthExpired(t *testing.T) {
	if s.Expire == nil {
		t.Skip("Harness.Expire not set")
	}
	link := s.Link(t)
	s.Expire(t, link)
	ctx := t.Context()
	sess, err := s.Provider.Open(ctx, link)
	if err != nil {
		if !errors.Is(err, provider.ErrAuthExpired) {
			t.Errorf("Open with expired credentials: got %v, want ErrAuthExpired", err)
		}
		return
	}
	defer sess.Close()
	if _, err := sess.Search(ctx, provider.SearchQuery{Text: s.Query}); !errors.Is(err, provider.ErrAuthExpired) {
		t.Errorf("Search with expired credentials: got %v, want ErrAuthExpired", err)
	}
}
