// SPDX-License-Identifier: AGPL-3.0-only

package discover_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/discover"
	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// graph is a music graph of made-up artists, each with five top songs.
type graph struct {
	mu      sync.Mutex
	artists map[string]musicgraph.Artist
	fetched []string
	warmed  []string
}

func (g *graph) find(a musicgraph.ArtistRef) (musicgraph.Artist, bool) {
	out, ok := g.artists[match.Simplify(a.Name)]
	return out, ok
}

func (g *graph) CachedArtist(_ context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	out, ok := g.find(a)
	if !ok {
		return musicgraph.Artist{}, false, nil
	}
	return out, true, nil
}

func (g *graph) Artist(_ context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fetched = append(g.fetched, a.Name)
	return musicgraph.Artist{}, provider.ErrNotFound
}

func (g *graph) WarmArtist(as ...musicgraph.ArtistRef) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, a := range as {
		g.warmed = append(g.warmed, a.Name)
	}
}

// artist adds an artist like others (name: score).
func (g *graph) artist(name string, fans int64, similar ...any) {
	a := musicgraph.Artist{Ref: musicgraph.ArtistRef{Name: name}, Fans: fans}
	for i := 0; i+1 < len(similar); i += 2 {
		a.Similar = append(a.Similar, musicgraph.Similar{Artist: musicgraph.ArtistRef{Name: similar[i].(string)}, Score: similar[i+1].(float64)})
	}
	for i := range 5 {
		a.Top = append(a.Top, musicgraph.Song{
			SongRef: musicgraph.SongRef{Title: fmt.Sprintf("%s song %d", name, i+1), Artist: a.Ref, Album: name + " LP", DeezerID: int64(len(name)*100 + i)},
			Score:   1 - float64(i)*0.2,
		})
	}
	g.artists[match.Simplify(name)] = a
}

// owned stands in for the library.
type owned struct{ o *library.Owned }

func (l owned) For(context.Context, string) (*library.Owned, error) { return l.o, nil }

type env struct {
	db    *store.Store
	svc   *discover.Service
	graph *graph
	alice string
	bob   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, graph: &graph{artists: map[string]musicgraph.Artist{}}}
	for _, name := range []string{"alice", "bob"} {
		u, err := db.CreateUser(t.Context(), store.CreateUserParams{
			ID: store.NewID(), Username: name, DisplayName: name, Color: "#000", Role: store.RoleMember, CreatedAt: store.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if name == "alice" {
			e.alice = u.ID
		} else {
			e.bob = u.ID
		}
	}
	// Slowdive, Hum and Duster: shoegaze and space rock. Swirlies is near
	// all three; Ride only near Slowdive, if nearer it.
	g := e.graph
	g.artist("Slowdive", 900_000, "Ride", 0.9, "Swirlies", 0.6, "Lush", 0.5, "Chapterhouse", 0.45, "Hum", 0.4, "Pale Saints", 0.4)
	g.artist("Hum", 300_000, "Failure", 0.8, "Swirlies", 0.6, "Shiner", 0.5, "Quicksand", 0.4, "Duster", 0.3)
	g.artist("Duster", 200_000, "Codeine", 0.7, "Swirlies", 0.5, "Bedhead", 0.5, "Low", 0.4, "Failure", 0.2)
	for _, a := range []string{"Ride", "Swirlies", "Lush", "Chapterhouse", "Pale Saints", "Failure", "Shiner", "Quicksand", "Codeine", "Bedhead", "Low"} {
		g.artist(a, 100_000)
	}
	e.svc = discover.New(db)
	e.svc.Graph = g
	return e
}

func (e *env) set(t *testing.T, seeds ...string) discover.Set {
	t.Helper()
	var ss []discover.Seed
	for _, s := range seeds {
		ss = append(ss, discover.Seed{Name: s})
	}
	set, err := e.svc.Create(t.Context(), e.alice, "Moody", ss)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func artists(items []discover.Item) []string {
	var out []string
	for _, it := range items {
		if !slices.Contains(out, it.Artist) {
			out = append(out, it.Artist)
		}
	}
	return out
}

func TestOverlapRanksFirst(t *testing.T) {
	e := newEnv(t)
	set := e.set(t, "Slowdive", "Hum", "Duster")
	f, err := e.svc.Feed(t.Context(), e.alice, set.ID, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != discover.PageSize || f.Next == "" {
		t.Fatalf("page of %d, next %q", len(f.Items), f.Next)
	}
	// Swirlies is near all three seeds: their hit leads, though Ride is
	// nearer Slowdive than Swirlies is to anyone.
	first := f.Items[0]
	if first.Artist != "Swirlies" || first.Title != "Swirlies song 1" {
		t.Errorf("first: %+v", first)
	}
	if len(first.Because) != 2 || !slices.Contains([]string{"Slowdive", "Hum"}, first.Because[0]) {
		t.Errorf("because: %v", first.Because)
	}
	// Seeds take turns, and no artist has more than two songs a page.
	count := map[string]int{}
	for _, it := range f.Items {
		count[it.Artist]++
		if count[it.Artist] > 2 {
			t.Errorf("%s has %d songs on the first page", it.Artist, count[it.Artist])
		}
	}
	got := artists(f.Items)
	for _, want := range []string{"Ride", "Failure", "Codeine"} {
		if !slices.Contains(got, want) {
			t.Errorf("no %s on the first page: %v", want, got)
		}
	}
	// The whole feed: 20+ songs, seeds' own deep cuts among them.
	all := e.all(t, set.ID)
	if len(all) < 20 {
		t.Fatalf("%d songs in all", len(all))
	}
	if !slices.ContainsFunc(all, func(it discover.Item) bool { return it.Seed && it.Artist == "Hum" }) {
		t.Error("no songs of the seeds' own")
	}
	keys := map[string]bool{}
	for _, it := range all {
		if keys[it.Key] {
			t.Errorf("%s twice", it.Title)
		}
		keys[it.Key] = true
	}
}

// all reads every page of a feed.
func (e *env) all(t *testing.T, setID string) []discover.Item {
	t.Helper()
	var out []discover.Item
	cursor := ""
	for range 100 {
		f, err := e.svc.Feed(t.Context(), e.alice, setID, cursor, false)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, f.Items...)
		if cursor = f.Next; cursor == "" {
			return out
		}
	}
	t.Fatal("the feed never ends")
	return nil
}

func TestOwnedSongsAreLeftOut(t *testing.T) {
	e := newEnv(t)
	credit := func(name string) []provider.ArtistCredit { return []provider.ArtistCredit{{Name: name}} }
	e.svc.Library = owned{library.Holding(
		provider.Track{Title: "Swirlies Song 1 - 2009 Remaster", Artists: credit("Swirlies"), Album: provider.AlbumCredit{Title: "Odds"}},
		provider.Track{Title: "Anything", Artists: credit("Ride"), Album: provider.AlbumCredit{Title: "Ride LP"}},
	)}
	set := e.set(t, "Slowdive", "Hum", "Duster")
	all := e.all(t, set.ID)
	for _, it := range all {
		if it.Title == "Swirlies song 1" {
			t.Error("a song alice has")
		}
		if it.Artist == "Ride" {
			t.Errorf("a song off a release alice has: %s", it.Title)
		}
	}
	// Having one song of an artist's doesn't hide the rest.
	if !slices.ContainsFunc(all, func(it discover.Item) bool { return it.Title == "Swirlies song 2" }) {
		t.Error("Swirlies' other songs were left out too")
	}
}

func TestThumbsDownIsForGood(t *testing.T) {
	e := newEnv(t)
	set := e.set(t, "Slowdive", "Hum")
	other := e.set(t, "Duster")
	f, _ := e.svc.Feed(t.Context(), e.alice, set.ID, "", false)
	down := f.Items[0]
	if _, err := e.svc.Thumb(t.Context(), e.alice, set.ID, discover.ThumbDown, discover.Song{Title: down.Title, Artist: down.Artist}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{set.ID, other.ID} {
		for _, it := range e.all(t, id) {
			if it.Key == down.Key {
				t.Errorf("%s still in a feed after a thumbs-down", it.Title)
			}
		}
	}
	// Not for bob: thumbs are personal. (Bob can't read alice's set.)
	if _, err := e.svc.Feed(t.Context(), e.bob, set.ID, "", false); !errors.Is(err, discover.ErrNotFound) {
		t.Errorf("bob read alice's feed: %v", err)
	}
	// Blocking an artist leaves out every song of theirs.
	if _, err := e.svc.Block(t.Context(), e.alice, "Failure"); err != nil {
		t.Fatal(err)
	}
	for _, it := range e.all(t, set.ID) {
		if it.Artist == "Failure" {
			t.Fatalf("a blocked artist's song: %s", it.Title)
		}
	}
	if err := e.svc.Unblock(t.Context(), e.alice, "failure"); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(e.all(t, set.ID), func(it discover.Item) bool { return it.Artist == "Failure" }) {
		t.Error("unblocking didn't bring them back")
	}
}

func TestExploreLeansObscure(t *testing.T) {
	e := newEnv(t)
	// Two artists just as near Low: one famous, one not.
	e.graph.artist("Low", 500_000, "Big Band", 0.8, "Small Band", 0.8)
	e.graph.artist("Big Band", 9_000_000)
	e.graph.artist("Small Band", 300)
	set := e.set(t, "Low")
	firstOf := func(explore int) string {
		if _, err := e.svc.Update(t.Context(), e.alice, set.ID, discover.Changes{Explore: &explore}); err != nil {
			t.Fatal(err)
		}
		f, err := e.svc.Feed(t.Context(), e.alice, set.ID, "", false)
		if err != nil {
			t.Fatal(err)
		}
		return f.Items[0].Artist
	}
	if got := firstOf(0); got != "Big Band" {
		t.Errorf("explore 0 leads with %s", got)
	}
	if got := firstOf(100); got != "Small Band" {
		t.Errorf("explore 100 leads with %s", got)
	}
}

func TestFeedIsKeptAndRebuilt(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return now }
	set := e.set(t, "Hum")
	first, _ := e.svc.Feed(t.Context(), e.alice, set.ID, "", false)
	// The graph learns more; the kept feed doesn't, until it's rebuilt.
	e.graph.artist("Hum", 300_000, "Failure", 0.8, "Swirlies", 0.6, "Duster", 0.3, "Hum Newcomer", 0.95)
	e.graph.artist("Hum Newcomer", 1000)
	has := func(f discover.Feed) bool { return slices.Contains(artists(f.Items), "Hum Newcomer") }
	if f, _ := e.svc.Feed(t.Context(), e.alice, set.ID, "", false); has(f) || len(f.Items) != len(first.Items) {
		t.Error("rebuilt before it went stale")
	}
	if f, _ := e.svc.Feed(t.Context(), e.alice, set.ID, "", true); !has(f) {
		t.Error("refresh didn't rebuild")
	}
	e.graph.artist("Hum", 300_000, "Failure", 0.8)
	now = now.Add(11 * time.Minute)
	if f, _ := e.svc.Feed(t.Context(), e.alice, set.ID, "", false); has(f) {
		t.Error("not rebuilt after going stale")
	}
}

func TestArtistsMissingFromTheGraphAreFetchedOrWarmed(t *testing.T) {
	e := newEnv(t)
	set := e.set(t, "Hum", "Unknown Seed")
	if _, err := e.svc.Feed(t.Context(), e.alice, set.ID, "", false); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(e.graph.fetched, "Unknown Seed") {
		t.Errorf("fetched %v", e.graph.fetched)
	}
}

func TestSets(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	set, err := e.svc.Create(ctx, e.alice, "  90s   alt ", []discover.Seed{{Name: "Hum"}, {Name: " hum "}})
	if err != nil {
		t.Fatal(err)
	}
	if set.Name != "90s alt" || len(set.Artists) != 1 || set.Explore != discover.DefaultExplore || set.Mode != discover.ModeBoth {
		t.Fatalf("created: %+v", set)
	}
	if _, err := e.svc.Create(ctx, e.alice, " ", nil); !isInvalid(err) {
		t.Errorf("no name: %v", err)
	}
	set, err = e.svc.AddArtist(ctx, e.alice, set.ID, discover.Seed{Name: "Failure", MBID: "ABC-123"})
	if err != nil || len(set.Artists) != 2 || set.Artists[1].ArtistKey != "abc-123" {
		t.Fatalf("added: %+v %v", set.Artists, err)
	}
	if _, err := e.svc.AddArtist(ctx, e.bob, set.ID, discover.Seed{Name: "Low"}); !errors.Is(err, discover.ErrNotFound) {
		t.Errorf("bob added to alice's set: %v", err)
	}
	if set, err = e.svc.RemoveArtist(ctx, e.alice, set.ID, "hum"); err != nil || len(set.Artists) != 1 {
		t.Fatalf("removed: %+v %v", set.Artists, err)
	}
	mode, explore, name := "fresh", 101, "Renamed"
	if _, err := e.svc.Update(ctx, e.alice, set.ID, discover.Changes{Explore: &explore}); !isInvalid(err) {
		t.Errorf("explore 101: %v", err)
	}
	explore = 80
	if set, err = e.svc.Update(ctx, e.alice, set.ID, discover.Changes{Mode: &mode, Explore: &explore, Name: &name}); err != nil || set.Mode != "fresh" || set.Explore != 80 || set.Name != "Renamed" {
		t.Fatalf("updated: %+v %v", set, err)
	}
	if err := e.svc.Delete(ctx, e.bob, set.ID); !errors.Is(err, discover.ErrNotFound) {
		t.Errorf("bob deleted alice's set: %v", err)
	}
	if err := e.svc.Delete(ctx, e.alice, set.ID); err != nil {
		t.Fatal(err)
	}
	if sets, _ := e.svc.Sets(ctx, e.alice); len(sets) != 0 {
		t.Errorf("sets after deleting: %+v", sets)
	}
	if _, err := e.svc.Thumb(ctx, e.alice, "", "sideways", discover.Song{Title: "x", Artist: "y"}); !isInvalid(err) {
		t.Errorf("a sideways thumb: %v", err)
	}
}

func TestSearchArtistsOffersWhatWasTyped(t *testing.T) {
	e := newEnv(t)
	got, err := e.svc.SearchArtists(t.Context(), "  my   friend's band ")
	if err != nil || len(got) != 1 || got[0].Name != "my friend's band" {
		t.Fatalf("%+v %v", got, err)
	}
	e.svc.Deezer = deezer{}
	if got, _ := e.svc.SearchArtists(t.Context(), "hum"); len(got) != 2 || got[0].Name != "Hum" || got[0].Fans == 0 {
		t.Errorf("from Deezer: %+v", got)
	}
}

func TestPreviewsAreFoundOnce(t *testing.T) {
	e := newEnv(t)
	// Last.fm-only songs: no Deezer IDs.
	a := e.graph.artists["hum"]
	for i := range a.Top {
		a.Top[i].DeezerID = 0
	}
	e.graph.artists["hum"] = a
	d := &countingDeezer{}
	e.svc.Deezer = d
	set := e.set(t, "Hum")
	f, err := e.svc.Feed(t.Context(), e.alice, set.ID, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range f.Items {
		if it.Artist != "Hum" {
			continue
		}
		if it.Title == "Hum song 2" {
			if it.DeezerID != 0 {
				t.Errorf("a song Deezer hasn't: %+v", it)
			}
		} else if it.DeezerID != 77 || it.Cover != "c0ffee" {
			t.Errorf("not found on Deezer: %+v", it)
		}
	}
	n := d.n()
	if _, err := e.svc.Feed(t.Context(), e.alice, set.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if d.n() != n {
		t.Errorf("searched Deezer again: %d, then %d", n, d.n())
	}
}

type deezer struct{}

func (deezer) FindTrack(context.Context, musicgraph.SongRef) (musicgraph.DeezerTrack, error) {
	return musicgraph.DeezerTrack{}, provider.ErrNotFound
}

func (deezer) SearchArtists(context.Context, string, int) ([]musicgraph.DeezerArtist, error) {
	return []musicgraph.DeezerArtist{{ID: 1, Name: "Hum", Fans: 300_000}, {ID: 2, Name: "Hum (Ambient)"}}, nil
}

type countingDeezer struct {
	deezer
	mu    sync.Mutex
	calls int
}

func (d *countingDeezer) n() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func (d *countingDeezer) FindTrack(_ context.Context, s musicgraph.SongRef) (musicgraph.DeezerTrack, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	if strings.HasSuffix(s.Title, "song 2") {
		return musicgraph.DeezerTrack{}, provider.ErrNotFound
	}
	return musicgraph.DeezerTrack{ID: 77, Title: s.Title, Artist: s.Artist.Name, Cover: "c0ffee", HasPreview: true}, nil
}

func isInvalid(err error) bool {
	var inv *discover.InvalidInputError
	return errors.As(err, &inv)
}
