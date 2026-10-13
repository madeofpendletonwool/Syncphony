// SPDX-License-Identifier: AGPL-3.0-only

package releases_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/releases"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// sources stand in for Deezer, ListenBrainz and MusicBrainz.
type sources struct {
	mu     sync.Mutex
	albums map[string][]musicgraph.DeezerAlbum
	fresh  []musicgraph.FreshRelease
	groups map[string][]musicbrainz.ReleaseGroup
	tracks map[int64][]musicgraph.DeezerTrack
	// fail fails a source: "deezer", "listenbrainz" or "musicbrainz".
	fail  map[string]bool
	asked []string
}

func (s *sources) note(what string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, what)
	for src, f := range s.fail {
		if f && len(what) >= len(src) && what[:len(src)] == src {
			return fmt.Errorf("%s is down: %w", src, provider.ErrUnavailable)
		}
	}
	return nil
}

func (s *sources) ArtistAlbums(_ context.Context, name string) ([]musicgraph.DeezerAlbum, error) {
	if err := s.note("deezer albums " + name); err != nil {
		return nil, err
	}
	al, ok := s.albums[match.Simplify(name)]
	if !ok {
		return nil, provider.ErrNotFound
	}
	return al, nil
}

func (s *sources) FindAlbum(_ context.Context, artist, title string) (musicgraph.DeezerAlbum, error) {
	if err := s.note("deezer find " + title); err != nil {
		return musicgraph.DeezerAlbum{}, err
	}
	for _, al := range s.albums[match.Simplify(artist)] {
		if al.Title == title {
			return al, nil
		}
	}
	return musicgraph.DeezerAlbum{}, provider.ErrNotFound
}

func (s *sources) AlbumTracks(_ context.Context, id int64) ([]musicgraph.DeezerTrack, error) {
	if err := s.note(fmt.Sprint("deezer tracks ", id)); err != nil {
		return nil, err
	}
	return s.tracks[id], nil
}

func (s *sources) FreshReleases(context.Context, int) ([]musicgraph.FreshRelease, error) {
	if err := s.note("listenbrainz fresh"); err != nil {
		return nil, err
	}
	return s.fresh, nil
}

func (s *sources) RecentReleaseGroups(_ context.Context, mbid, _, _ string) ([]musicbrainz.ReleaseGroup, error) {
	if err := s.note("musicbrainz " + mbid); err != nil {
		return nil, err
	}
	return s.groups[mbid], nil
}

func (s *sources) count(what string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, a := range s.asked {
		if a == what {
			n++
		}
	}
	return n
}

const wednesdayID = "dd000000-0000-4000-8000-000000000001"

var wednesday = musicgraph.ArtistRef{Name: "Wednesday", MBID: wednesdayID}

type env struct {
	src *sources
	svc *releases.Service
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	e.src = &sources{
		fail: map[string]bool{},
		albums: map[string][]musicgraph.DeezerAlbum{
			"wednesday": {
				{ID: 1, Title: "Bleeds (Deluxe Edition)", Date: "2026-09-19", Type: "album", Cover: "c1"},
				{ID: 2, Title: "Elderberry Wine", Date: "2026-07-22", Type: "single", Cover: "c2"},
				{ID: 3, Title: "Rat Saw God", Date: "2023-04-07", Type: "album", Cover: "c3"},
				{ID: 6, Title: "Rat Saw God (Deluxe)", Date: "2026-09-30", Type: "album"}, // a reissue
				{ID: 4, Title: "Live at the Orange Peel", Date: "2026-08-30", Type: "album"},
				{ID: 5, Title: "Hits So Far", Date: "2026-10-01", Type: "compile"},
			},
		},
		fresh: []musicgraph.FreshRelease{
			{Title: "Bleeds", Artist: "Wednesday", ArtistMBIDs: []string{wednesdayID}, MBID: "rg-bleeds", CoverRelease: "r-bleeds", Date: "2026-09-19", PrimaryType: "Album"},
			{Title: "Split", Artist: "Wednesday & MJ Lenderman", ArtistMBIDs: []string{wednesdayID, "mj"}, MBID: "rg-split", Date: "2026-09-01", PrimaryType: "EP"},
			{Title: "Someone Else's", Artist: "Waxahatchee", ArtistMBIDs: []string{"wax"}, Date: "2026-09-01", PrimaryType: "Album"},
		},
		groups: map[string][]musicbrainz.ReleaseGroup{
			wednesdayID: {
				{MBID: "rg-bleeds", Title: "Bleeds", PrimaryType: "Album", FirstReleased: "2026-09"},
				{MBID: "rg-year", Title: "A Year Alone", PrimaryType: "Album", FirstReleased: "2026"},
			},
		},
		tracks: map[int64][]musicgraph.DeezerTrack{1: {{ID: 11, Title: "Reality TV Argument Bleeds", HasPreview: true}}},
	}
	e.svc = releases.New(db, releases.Options{
		Deezer: e.src, ListenBrainz: e.src, MusicBrainz: e.src,
		Now: func() time.Time { return e.now },
	})
	return e
}

func titles(rs []releases.Release) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Title+"|"+r.Date+"|"+r.Kind)
	}
	return out
}

func TestReleasesMergeTheSources(t *testing.T) {
	e := newEnv(t)
	rs, err := e.svc.Get(t.Context(), wednesday)
	if err != nil {
		t.Fatal(err)
	}
	// Newest first; one Bleeds from three sources, at its precise date;
	// nothing from before the lookback, or dated only by year, or
	// reissued; the live album and the compilation told apart.
	want := []string{
		"Hits So Far|2026-10-01|compilation",
		"Bleeds|2026-09-19|album",
		"Split|2026-09-01|ep",
		"Live at the Orange Peel|2026-08-30|live",
		"Elderberry Wine|2026-07-22|single",
	}
	if got := titles(rs); !slices.Equal(got, want) {
		t.Fatalf("releases:\n got %q\nwant %q", got, want)
	}
	b := rs[1]
	if b.MBID != "rg-bleeds" || b.CoverRelease != "r-bleeds" || b.DeezerID != 1 || b.Cover != "c1" || b.Artist != "Wednesday" {
		t.Errorf("Bleeds: %+v", b)
	}
	if !slices.Equal(b.Sources, []string{"listenbrainz", "musicbrainz", "deezer"}) {
		t.Errorf("Bleeds' sources: %v", b.Sources)
	}
	if rs[2].Artist != "Wednesday & MJ Lenderman" {
		t.Errorf("the split is credited to %q", rs[2].Artist)
	}
}

func TestReleasesAreCached(t *testing.T) {
	e := newEnv(t)
	if _, ok, _ := e.svc.Cached(t.Context(), wednesday); ok {
		t.Fatal("cached before asking")
	}
	if _, err := e.svc.Get(t.Context(), wednesday); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(t.Context(), musicgraph.ArtistRef{Name: "Waxahatchee"}); err != nil {
		t.Fatal(err)
	}
	rs, ok, err := e.svc.Cached(t.Context(), musicgraph.ArtistRef{Name: "wednesday"})
	if err != nil || !ok || len(rs) != 5 {
		t.Fatalf("cached: %d, %v, %v", len(rs), ok, err)
	}
	if n := e.src.count("deezer albums Wednesday"); n != 1 {
		t.Errorf("asked Deezer %d times", n)
	}
	// ListenBrainz's list is read once for every artist.
	if n := e.src.count("listenbrainz fresh"); n != 1 {
		t.Errorf("read ListenBrainz's list %d times", n)
	}
	// A day later, they're asked again.
	e.now = e.now.Add(25 * time.Hour)
	if _, ok, _ := e.svc.Cached(t.Context(), wednesday); ok {
		t.Error("still cached a day later")
	}
	if _, err := e.svc.Get(t.Context(), wednesday); err != nil {
		t.Fatal(err)
	}
	if n := e.src.count("listenbrainz fresh"); n != 2 {
		t.Errorf("read ListenBrainz's list %d times", n)
	}
}

func TestReleasesWhenSourcesFail(t *testing.T) {
	e := newEnv(t)
	e.src.fail["musicbrainz"] = true
	rs, err := e.svc.Get(t.Context(), wednesday)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 5 {
		t.Errorf("the others' releases: %q", titles(rs))
	}
	// An answer some source failed to give is kept an hour.
	e.now = e.now.Add(2 * time.Hour)
	if _, ok, _ := e.svc.Cached(t.Context(), wednesday); ok {
		t.Error("a partial answer kept over an hour")
	}
	// One source down and the others with nothing new is nothing new.
	if rs, err := e.svc.Get(t.Context(), musicgraph.ArtistRef{Name: "Duster", MBID: "duster"}); err != nil || len(rs) != 0 {
		t.Errorf("nothing new: %v, %v", rs, err)
	}
	// With every source down, nothing is kept. (ListenBrainz's list is
	// read again every few hours; till then, it answers.)
	e.src.fail["deezer"], e.src.fail["listenbrainz"] = true, true
	e.now = e.now.Add(7 * time.Hour)
	if _, err := e.svc.Get(t.Context(), musicgraph.ArtistRef{Name: "Hum", MBID: "hum"}); !errors.Is(err, provider.ErrUnavailable) {
		t.Errorf("every source down: %v", err)
	}
	if _, ok, _ := e.svc.Cached(t.Context(), musicgraph.ArtistRef{Name: "Hum"}); ok {
		t.Error("kept a failure")
	}
}

func TestReleaseTracks(t *testing.T) {
	e := newEnv(t)
	al, tracks, err := e.svc.Tracks(t.Context(), "Wednesday", "Bleeds", 1)
	if err != nil {
		t.Fatal(err)
	}
	if al.ID != 1 || len(tracks) != 1 || tracks[0].ID != 11 {
		t.Errorf("by Deezer ID: %+v, %+v", al, tracks)
	}
	// Without an ID, it's found by title; and remembered.
	for range 2 {
		al, _, err = e.svc.Tracks(t.Context(), "Wednesday", "Elderberry Wine", 0)
		if err != nil || al.ID != 2 || al.Cover != "c2" {
			t.Errorf("by title: %+v, %v", al, err)
		}
	}
	if n := e.src.count("deezer find Elderberry Wine"); n != 1 {
		t.Errorf("looked for it %d times", n)
	}
	if _, _, err := e.svc.Tracks(t.Context(), "Wednesday", "Twin Plagues", 0); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("not on Deezer: %v", err)
	}
}

func TestWarmingReleases(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	e.svc.Warm(wednesday, wednesday, musicgraph.ArtistRef{Name: "Waxahatchee"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, a, _ := e.svc.Cached(t.Context(), wednesday)
		_, b, _ := e.svc.Cached(t.Context(), musicgraph.ArtistRef{Name: "Waxahatchee"})
		if a && b {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not warmed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if n := e.src.count("deezer albums Wednesday"); n != 1 {
		t.Errorf("warmed Wednesday %d times", n)
	}
}
