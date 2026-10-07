// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

func refNames(as []musicgraph.ArtistRef) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Name)
	}
	return out
}

func TestGenreFromLastFM(t *testing.T) {
	e := newEnv(t)
	e.http.routes["/lastfm/?tag.getTopArtists&trip hop"] = `{"topartists":{"artist":[
		{"name":"Massive Attack","mbid":"ma"},{"name":"Portishead","mbid":""},{"name":"massive attack","mbid":""}]}}`
	g, err := e.svc.Genre(t.Context(), "trip hop")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Massive Attack", "Portishead"}; !slices.Equal(refNames(g.Artists), want) {
		t.Errorf("artists = %v, want %v", refNames(g.Artists), want)
	}
	if !slices.Equal(g.Sources, []string{"lastfm"}) || g.Artists[0].MBID != "ma" {
		t.Errorf("genre: %+v", g)
	}
	// Cached, under any spelling.
	n := e.http.count()
	if _, err := e.svc.Genre(t.Context(), "Trip-Hop"); err != nil || e.http.count() != n {
		t.Errorf("not cached: %v, %d requests", err, e.http.count()-n)
	}
}

func TestGenreFromCachedArtists(t *testing.T) {
	e := newEnv(t)
	// Radiohead's tags are learned when asked about them.
	if _, err := e.svc.Artist(t.Context(), radiohead); err != nil {
		t.Fatal(err)
	}
	g, err := e.svc.Genre(t.Context(), "Art Rock")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(refNames(g.Artists), []string{"Radiohead"}) || !slices.Equal(g.Sources, []string{"cache"}) {
		t.Errorf("genre: %+v", g)
	}
	// Kept briefly: more artists with the tag may be learned.
	e.now = e.now.Add(2 * time.Hour)
	e.http.routes["/lastfm/?tag.getTopArtists&Art Rock"] = `{"topartists":{"artist":[{"name":"Pink Floyd"}]}}`
	if g, _ := e.svc.Genre(t.Context(), "Art Rock"); !slices.Equal(refNames(g.Artists), []string{"Pink Floyd"}) {
		t.Errorf("after expiry: %v", refNames(g.Artists))
	}
}

func TestUnknownGenre(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Genre(t.Context(), "nothing at all"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("unknown genre: %v", err)
	}
	if _, err := e.svc.Genre(t.Context(), "  "); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("no name: %v", err)
	}
}
