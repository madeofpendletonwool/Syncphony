// SPDX-License-Identifier: AGPL-3.0-only

package artistpage_test

import (
	"context"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/artistpage"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
)

// graph knows a little about the fake's artists.
type graph struct {
	artists map[string]musicgraph.Artist
	genres  map[string]musicgraph.Genre
}

func (g graph) Artist(_ context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, error) {
	if out, ok := g.artists[a.Name]; ok {
		return out, nil
	}
	return musicgraph.Artist{}, provider.ErrNotFound
}

func (g graph) CachedArtist(ctx context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, bool, error) {
	out, err := g.Artist(ctx, a)
	return out, true, err
}

func (g graph) Genre(_ context.Context, tag string) (musicgraph.Genre, error) {
	if out, ok := g.genres[tag]; ok {
		return out, nil
	}
	return musicgraph.Genre{}, provider.ErrNotFound
}

func (graph) WarmArtist(...musicgraph.ArtistRef) {}

func song(title, artist string) musicgraph.Song {
	return musicgraph.Song{SongRef: musicgraph.SongRef{Title: title, Artist: musicgraph.ArtistRef{Name: artist}}}
}

var known = graph{
	artists: map[string]musicgraph.Artist{
		"Null Island": {
			// One the fake doesn't have, and one twice (a remaster).
			Top: []musicgraph.Song{
				song("Equator", "Null Island"), song("Not In The Library", "Null Island"),
				song("Latitude", "Null Island"), song("Equator - 2020 Remaster", "Null Island"),
			},
			Similar: []musicgraph.Similar{
				{Artist: musicgraph.ArtistRef{Name: "Sine Language"}, Score: 0.9},
				{Artist: musicgraph.ArtistRef{Name: "Nobody Here"}, Score: 0.5},
			},
		},
	},
	genres: map[string]musicgraph.Genre{
		"test tones": {Name: "test tones", Artists: []musicgraph.ArtistRef{{Name: "Nobody Here"}, {Name: "The Test Patterns"}}},
	},
}

func open(t *testing.T, opts fake.Options) (provider.Session, provider.Capabilities) {
	t.Helper()
	p := fake.New(opts)
	creds, account, err := p.Linker().Complete(t.Context(), provider.LinkInput{Fields: map[string]string{"username": fake.Username, "password": fake.Password}})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := p.Open(t.Context(), provider.Link{ID: "l1", Account: account, Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess, p.Info().Capabilities
}

// artist finds one of the fake's artists by name.
func artist(t *testing.T, sess provider.Session, name string) (provider.Artist, []provider.Album) {
	t.Helper()
	page, err := sess.Search(t.Context(), provider.SearchQuery{Text: name, Kinds: []provider.EntityKind{provider.KindArtist}})
	if err != nil || len(page.Artists) == 0 {
		t.Fatalf("no %s: %v", name, err)
	}
	a, albums, err := sess.Artist(t.Context(), page.Artists[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return a, albums
}

func titles(ts []provider.Track) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Title)
	}
	return out
}

func TestTopFromGraph(t *testing.T) {
	sess, caps := open(t, fake.Options{})
	a, albums := artist(t, sess, "Null Island")
	top, appears := artistpage.Page{Sess: sess, Caps: caps, Graph: known}.Top(t.Context(), a, albums)
	// In the graph's order, found on the service, each song once.
	if want := []string{"Equator", "Latitude"}; !slices.Equal(titles(top), want) {
		t.Errorf("top = %v, want %v", titles(top), want)
	}
	if len(appears) != 0 {
		t.Errorf("appears on %v", titles(appears))
	}
}

func TestTopFromService(t *testing.T) {
	sess, caps := open(t, fake.Options{})
	a, albums := artist(t, sess, "Sine Language")
	// The graph doesn't know them: the service's top tracks.
	top, _ := artistpage.Page{Sess: sess, Caps: caps, Graph: known}.Top(t.Context(), a, albums)
	if want := []string{"Concert Pitch", "Fundamental"}; !slices.Equal(titles(top), want) {
		t.Errorf("top = %v, want %v", titles(top), want)
	}
	// A service that can't say: what its search finds first.
	sess, caps = open(t, fake.Options{NoRecommendations: true})
	a, albums = artist(t, sess, "Sine Language")
	top, _ = artistpage.Page{Sess: sess, Caps: caps}.Top(t.Context(), a, albums)
	if len(top) != 6 || top[0].Title != "Concert Pitch" {
		t.Errorf("top = %v", titles(top))
	}
}

func TestAppearsOn(t *testing.T) {
	sess, caps := open(t, fake.Options{})
	a, albums := artist(t, sess, "Null Island")
	// Their songs on an album that isn't theirs, but not their top songs.
	_, appears := artistpage.Page{Sess: sess, Caps: caps, Graph: known}.Top(t.Context(), a, albums[:1])
	if want := []string{"Greenwich", "Gulf of Guinea"}; !slices.Equal(titles(appears), want) {
		t.Errorf("appears on %v, want %v", titles(appears), want)
	}
}

func TestSimilar(t *testing.T) {
	sess, caps := open(t, fake.Options{})
	a, _ := artist(t, sess, "Null Island")
	rel, songs := artistpage.Page{Sess: sess, Caps: caps, Graph: known}.Similar(t.Context(), a)
	if len(rel) != 2 || rel[0].Name != "Sine Language" || rel[0].Artist == nil || rel[0].Score != 0.9 || rel[1].Artist != nil {
		t.Fatalf("related: %+v", rel)
	}
	if want := []string{"Concert Pitch", "Tuning Fork"}; !slices.Equal(titles(songs), want) {
		t.Errorf("songs = %v, want %v", titles(songs), want)
	}

	// Without a graph, the service's own recommendations.
	rel, songs = artistpage.Page{Sess: sess, Caps: caps}.Similar(t.Context(), a)
	if len(rel) != 2 || rel[0].Name != "Sine Language" || rel[0].Artist == nil || len(songs) != 4 {
		t.Errorf("from the service: %+v, %v", rel, titles(songs))
	}
	// Neither: nothing.
	sess, caps = open(t, fake.Options{NoRecommendations: true})
	if rel, songs := (artistpage.Page{Sess: sess, Caps: caps}).Similar(t.Context(), a); len(rel) != 0 || len(songs) != 0 {
		t.Errorf("nothing to go on: %+v, %v", rel, songs)
	}
}

func TestGenre(t *testing.T) {
	sess, caps := open(t, fake.Options{})
	p := artistpage.Page{Sess: sess, Caps: caps, Graph: known}
	rel, songs, err := p.Genre(t.Context(), "test tones")
	if err != nil {
		t.Fatal(err)
	}
	// The ones on the service first.
	if len(rel) != 2 || rel[0].Name != "The Test Patterns" || rel[0].Artist == nil || rel[1].Artist != nil {
		t.Errorf("artists: %+v", rel)
	}
	if len(songs) != 2 {
		t.Errorf("songs = %v", titles(songs))
	}
	if _, _, err := p.Genre(t.Context(), "polka"); err == nil {
		t.Error("unknown genre found")
	}
}

func TestShuffle(t *testing.T) {
	sess, caps := open(t, fake.Options{})
	_, albums := artist(t, sess, "Null Island")
	p := artistpage.Page{Sess: sess, Caps: caps}
	ts, err := p.Shuffle(t.Context(), albums, 4, seeded())
	if err != nil || len(ts) != 4 {
		t.Fatalf("Shuffle = %v, %v", titles(ts), err)
	}
	// Spread across both albums.
	on := map[string]int{}
	for _, tr := range ts {
		on[tr.Album.Title]++
	}
	if on["Zero Zero"] != 2 || on["Prime Meridian"] != 2 {
		t.Errorf("albums: %v", on)
	}
	if ts, _ := p.Shuffle(t.Context(), albums, 100, seeded()); len(ts) != 6 {
		t.Errorf("more than they have: %d", len(ts))
	}
}

func TestElsewhere(t *testing.T) {
	sess, caps := open(t, fake.Options{})
	if a := artistpage.Elsewhere(t.Context(), sess, caps, "null island"); a == nil || a.Name != "Null Island" {
		t.Errorf("Elsewhere = %+v", a)
	}
	if a := artistpage.Elsewhere(t.Context(), sess, caps, "Null"); a != nil {
		t.Errorf("partial name found %+v", a)
	}
}

// seeded is the same "random" order every run.
func seeded() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) } //nolint:gosec // a test's fixed order
