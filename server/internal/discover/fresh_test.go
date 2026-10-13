// SPDX-License-Identifier: AGPL-3.0-only

package discover_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/discover"
	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/releases"
)

// releaseBook stands in for the release lookup: artists' releases, some
// cached, the rest only fetched.
type releaseBook struct {
	mu       sync.Mutex
	releases map[string][]releases.Release
	cached   map[string]bool
	fetched  []string
	warmed   []string
}

func (b *releaseBook) On() bool { return true }

func (b *releaseBook) Cached(_ context.Context, a musicgraph.ArtistRef) ([]releases.Release, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := match.Simplify(a.Name)
	return b.releases[k], b.cached[k], nil
}

func (b *releaseBook) Get(_ context.Context, a musicgraph.ArtistRef) ([]releases.Release, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := match.Simplify(a.Name)
	b.fetched = append(b.fetched, a.Name)
	b.cached[k] = true
	return b.releases[k], nil
}

func (b *releaseBook) Warm(as ...musicgraph.ArtistRef) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, a := range as {
		b.warmed = append(b.warmed, a.Name)
	}
}

func (b *releaseBook) Tracks(_ context.Context, artist, title string, _ int64) (musicgraph.DeezerAlbum, []musicgraph.DeezerTrack, error) {
	if title != "Pygmalion" {
		return musicgraph.DeezerAlbum{}, nil, provider.ErrNotFound
	}
	return musicgraph.DeezerAlbum{ID: 77, Title: "Pygmalion", Cover: "pyg"}, []musicgraph.DeezerTrack{
		{ID: 771, Title: "Rutti", Artist: artist, ISRC: "GB0000000001"},
		{ID: 772, Title: "Crazy for You", Artist: artist},
	}, nil
}

// release adds an artist's release, daysAgo days before e's now.
func (b *releaseBook) add(artist, title, kind string, daysAgo int, cached bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := match.Simplify(artist)
	b.releases[k] = append(b.releases[k], releases.Release{
		Key: library.ReleaseKey(artist, title), Title: title, Artist: artist, Kind: kind,
		Date: freshNow.AddDate(0, 0, -daysAgo).Format(time.DateOnly), Sources: []string{"deezer"},
	})
	b.cached[k] = b.cached[k] || cached
}

var freshNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func freshEnv(t *testing.T) (*env, *releaseBook) {
	t.Helper()
	e := newEnv(t)
	e.svc.Now = func() time.Time { return freshNow }
	b := &releaseBook{releases: map[string][]releases.Release{}, cached: map[string]bool{}}
	e.svc.Releases = b
	return e, b
}

func freshTitles(f discover.Fresh) []string {
	var out []string
	for _, it := range f.Items {
		out = append(out, it.Artist+": "+it.Title)
	}
	return out
}

func TestFreshFeed(t *testing.T) {
	e, b := freshEnv(t)
	b.add("Slowdive", "Everything Is Alive", "album", 20, true)
	b.add("Slowdive", "Kisses", "single", 40, true)
	b.add("Slowdive", "Live at the Roundhouse", "live", 5, true)
	b.add("Slowdive", "Souvlaki", "album", 400, true) // too old
	b.add("Hum", "Inlet", "album", 50, true)
	b.add("Swirlies", "Cats of the Wild", "ep", 10, true)
	b.add("Ride", "Interplay", "album", 30, true)
	b.add("Ride", "Peace Sign", "single", 3, true)
	b.add("Lush", "Lovelife", "album", 15, true)     // owned
	b.add("Failure", "Wild Type", "single", 8, true) // blocked
	b.add("Codeine", "Barely Real", "ep", 59, true)
	b.add("Codeine", "White Birch", "album", 61, true) // past the window
	// Everyone else is known to have put nothing out.
	for k := range e.graph.artists {
		b.cached[k] = true
	}
	credit := []provider.ArtistCredit{{Name: "Lush"}}
	e.svc.Library = owned{library.Holding(provider.Track{Title: "Ladykillers", Artists: credit, Album: provider.AlbumCredit{Title: "Lovelife"}})}
	if _, err := e.svc.Block(t.Context(), e.alice, "Failure"); err != nil {
		t.Fatal(err)
	}
	set := e.set(t, "Slowdive", "Hum", "Duster")
	f, err := e.svc.Fresh(t.Context(), e.alice, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The seeds' first, albums before singles; then the neighbors', the
	// one near all three seeds first; live albums last.
	want := []string{
		"Slowdive: Everything Is Alive",
		"Hum: Inlet",
		"Slowdive: Kisses",
		"Swirlies: Cats of the Wild",
		"Ride: Interplay",
		"Codeine: Barely Real",
		"Ride: Peace Sign",
		"Slowdive: Live at the Roundhouse",
	}
	if got := freshTitles(f); !slices.Equal(got, want) {
		t.Errorf("fresh:\n got %q\nwant %q", got, want)
	}
	if f.Days != 60 || f.Partial || f.NoReleases {
		t.Errorf("days %d, partial %v, no releases %v", f.Days, f.Partial, f.NoReleases)
	}
	for _, it := range f.Items {
		switch it.Artist {
		case "Slowdive":
			if !it.Seed || !slices.Equal(it.Because, []string{"Slowdive"}) {
				t.Errorf("a seed's release: %+v", it)
			}
		case "Swirlies":
			if it.Seed || len(it.Because) != 2 {
				t.Errorf("a neighbor's release: %+v", it)
			}
		}
	}

	// A wider window takes in more.
	days := 90
	if _, err := e.svc.Update(t.Context(), e.alice, set.ID, discover.Changes{FreshDays: &days}); err != nil {
		t.Fatal(err)
	}
	f, err = e.svc.Fresh(t.Context(), e.alice, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Days != 90 || !slices.Contains(freshTitles(f), "Codeine: White Birch") {
		t.Errorf("90 days: %q", freshTitles(f))
	}
	bad := 45
	var invalid *discover.InvalidInputError
	if _, err := e.svc.Update(t.Context(), e.alice, set.ID, discover.Changes{FreshDays: &bad}); !errors.As(err, &invalid) {
		t.Errorf("45 days: %v", err)
	}
}

func TestFreshFeedFetchesSeedsAndWarmsTheRest(t *testing.T) {
	e, b := freshEnv(t)
	b.add("Slowdive", "Everything Is Alive", "album", 20, false)
	b.add("Ride", "Interplay", "album", 30, false)
	set := e.set(t, "Slowdive")
	f, err := e.svc.Fresh(t.Context(), e.alice, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := freshTitles(f); !slices.Equal(got, []string{"Slowdive: Everything Is Alive"}) {
		t.Errorf("fresh: %q", got)
	}
	if !f.Partial || !slices.Contains(b.warmed, "Ride") || slices.Contains(b.fetched, "Ride") {
		t.Errorf("partial %v; fetched %v, warmed %v", f.Partial, b.fetched, b.warmed)
	}
}

func TestFreshFeedIsYours(t *testing.T) {
	e, _ := freshEnv(t)
	set := e.set(t, "Slowdive")
	if _, err := e.svc.Fresh(t.Context(), e.bob, set.ID); !errors.Is(err, discover.ErrNotFound) {
		t.Errorf("someone else's set: %v", err)
	}
	e.svc.Releases = nil
	f, err := e.svc.Fresh(t.Context(), e.alice, set.ID)
	if err != nil || !f.NoReleases || len(f.Items) != 0 {
		t.Errorf("no source of releases: %+v, %v", f, err)
	}
}

func TestReleaseTracks(t *testing.T) {
	e, _ := freshEnv(t)
	items, err := e.svc.ReleaseTracks(t.Context(), "Slowdive", "Pygmalion", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].DeezerID != 771 || items[0].Album != "Pygmalion" || items[0].Cover != "pyg" || items[0].ISRC != "GB0000000001" {
		t.Errorf("tracks: %+v", items)
	}
	if items, err := e.svc.ReleaseTracks(t.Context(), "Slowdive", "Not on Deezer", 0); err != nil || len(items) != 0 {
		t.Errorf("not on Deezer: %+v, %v", items, err)
	}
	var invalid *discover.InvalidInputError
	if _, err := e.svc.ReleaseTracks(t.Context(), " ", "Pygmalion", 0); !errors.As(err, &invalid) {
		t.Errorf("no artist: %v", err)
	}
}

func TestWarmReleases(t *testing.T) {
	e, b := freshEnv(t)
	b.add("Hum", "Inlet", "album", 50, true)
	e.set(t, "Slowdive", "Hum")
	if _, err := e.svc.Create(t.Context(), e.bob, "Bob's", []discover.Seed{{Name: "slowdive"}, {Name: "Duster"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WarmReleases(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Every set's seeds once, those not cached, then their neighbors.
	if len(b.warmed) < 3 || !slices.Equal(b.warmed[:2], []string{"Duster", "Slowdive"}) {
		t.Fatalf("warmed %v", b.warmed)
	}
	for _, name := range []string{"Ride", "Swirlies", "Failure", "Codeine"} {
		if !slices.Contains(b.warmed, name) {
			t.Errorf("didn't warm %s: %v", name, b.warmed)
		}
	}
	if slices.Contains(b.warmed, "Hum") {
		t.Error("warmed a cached artist")
	}
}
