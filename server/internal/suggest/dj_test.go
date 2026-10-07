// SPDX-License-Identifier: AGPL-3.0-only

package suggest_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/suggest"
)

// knowledge is a music knowledge source in memory, about the fake's
// artists and one the fake doesn't have.
type knowledge map[string]musicgraph.Artist

func (knowledge) Name() string { return "test" }

func (k knowledge) Artist(_ context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, error) {
	if out, ok := k[a.Name]; ok {
		return out, nil
	}
	return musicgraph.Artist{}, fmt.Errorf("%s: %w", a.Name, provider.ErrNotFound)
}

func (knowledge) Track(context.Context, musicgraph.SongRef) (musicgraph.Track, error) {
	return musicgraph.Track{}, provider.ErrNotFound
}

func known(name string, similar []musicgraph.Similar, top ...string) musicgraph.Artist {
	a := musicgraph.Artist{Ref: musicgraph.ArtistRef{Name: name}, Similar: similar}
	for i, t := range top {
		a.Top = append(a.Top, musicgraph.Song{SongRef: musicgraph.SongRef{Title: t, Artist: a.Ref}, Score: 1 - 0.2*float64(i)})
	}
	return a
}

func like(name string, score float64) musicgraph.Similar {
	return musicgraph.Similar{Artist: musicgraph.ArtistRef{Name: name}, Score: score, Sources: []string{"test"}}
}

// The Test Patterns are most like Ghost Band, whom no service has, then
// Null Island; Null Island is like Sine Language.
var library = knowledge{
	"The Test Patterns": known("The Test Patterns", []musicgraph.Similar{like("Ghost Band", 0.95), like("Null Island", 0.9)},
		"SMPTE", "Left Channel", "Reference Tone"),
	"Ghost Band":    known("Ghost Band", nil, "Ghost Song"),
	"Null Island":   known("Null Island", []musicgraph.Similar{like("Sine Language", 0.8)}, "Greenwich", "Latitude", "Weather Buoy"),
	"Sine Language": known("Sine Language", nil, "Concert Pitch", "Tuning Fork"),
}

func (e *env) withGraph(k knowledge) {
	e.sg.Graph = musicgraph.New(e.db, musicgraph.Options{Sources: []musicgraph.Source{k}})
}

// artists are the fake's artists of the suggestions' tracks, by ID.
func artistOf(id string) string {
	switch {
	case id <= "t06":
		return "The Test Patterns"
	case id <= "t12":
		return "Null Island"
	}
	return "Sine Language"
}

func TestDJYourVibe(t *testing.T) {
	e := newEnv(t, fake.Options{})
	e.withGraph(library)
	mine := e.add(e.alice, "t01") // Reference Tone, The Test Patterns
	e.play(mine.ID, store.EndFinished)
	e.play(e.add(e.bob, "t13").ID, store.EndFinished) // Concert Pitch, Sine Language

	got := e.suggest(e.alice, suggest.ScopeMine, suggest.OriginHistory, 10, false)
	// Alice's taste leads to The Test Patterns and on to Null Island
	// (Greenwich, t10), which the service's own recommendations don't.
	// Bob's Sine Language isn't her vibe.
	if !slices.Contains(ids(got), "t10") {
		t.Fatalf("suggested %v, want the DJ's Greenwich (t10)", ids(got))
	}
	for _, s := range got {
		if a := artistOf(s.Track.Ref.ID); a == "Sine Language" {
			t.Errorf("alice's vibe has bob's %s (%s)", s.Track.Ref.ID, a)
		}
		if s.Seed.ID != mine.ID {
			t.Errorf("%s is like %s, want alice's t01", s.Track.Ref.ID, s.Seed.TrackID)
		}
		if s.Track.Ref.LinkID != e.alice.link {
			t.Errorf("%s is from link %s, want alice's", s.Track.Ref.ID, s.Track.Ref.LinkID)
		}
		if s.Track.Ref.ID == "t01" || s.Track.Ref.ID == "t13" {
			t.Errorf("suggested %s, which just played", s.Track.Ref.ID)
		}
	}
}

func TestDJGroupVibe(t *testing.T) {
	e := newEnv(t, fake.Options{})
	e.withGraph(library)
	alices := e.add(e.alice, "t01")
	e.play(alices.ID, store.EndFinished)
	bobs := e.add(e.bob, "t16") // Fundamental, Sine Language's other album
	e.play(bobs.ID, store.EndFinished)

	got := e.suggest(e.alice, suggest.ScopeGroup, suggest.OriginHistory, 10, false)
	byArtist := map[string]bool{}
	for _, s := range got {
		a := artistOf(s.Track.Ref.ID)
		byArtist[a] = true
		// Each is credited to the member's song it leads from.
		want := alices.ID
		if a == "Sine Language" {
			want = bobs.ID
		}
		if s.Seed.ID != want {
			t.Errorf("%s (%s) is like %s", s.Track.Ref.ID, a, s.Seed.TrackID)
		}
	}
	if !byArtist["Sine Language"] || !byArtist["Null Island"] {
		t.Errorf("suggested %v, want both members' vibes", ids(got))
	}
}

func TestDJQueueVibe(t *testing.T) {
	e := newEnv(t, fake.Options{})
	e.withGraph(library)
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	waiting := e.add(e.bob, "t07") // Latitude, Null Island

	// The queued change of vibe is the vibe: Null Island and what's like
	// them, not The Test Patterns.
	got := e.suggest(e.alice, suggest.ScopeGroup, suggest.OriginQueue, 10, false)
	if len(got) == 0 {
		t.Fatal("suggested nothing")
	}
	for _, s := range got {
		if a := artistOf(s.Track.Ref.ID); a == "The Test Patterns" || s.Seed.ID != waiting.ID {
			t.Errorf("%s (%s) is like %s, want only like bob's waiting t07", s.Track.Ref.ID, a, s.Seed.TrackID)
		}
	}

	// Alice queued nothing: her own songs that played stand in.
	for _, s := range e.suggest(e.alice, suggest.ScopeMine, suggest.OriginQueue, 10, false) {
		if s.Seed.AddedBy != e.alice.ID {
			t.Errorf("alice's queue vibe has %s, like bob's %s", s.Track.Ref.ID, s.Seed.TrackID)
		}
	}
}

// A room the DJ turned an artist away from isn't suggested them.
func TestDJTurnsAway(t *testing.T) {
	e := newEnv(t, fake.Options{})
	e.withGraph(library)
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	e.play(e.add(e.bob, "t07").ID, store.EndSkipped)
	for _, s := range e.suggest(e.alice, suggest.ScopeGroup, suggest.OriginHistory, 10, false) {
		if a := artistOf(s.Track.Ref.ID); a == "Null Island" {
			t.Errorf("suggested %s by %s, whom the room just skipped", s.Track.Ref.ID, a)
		}
	}
}

// When the graph knows nothing near the vibe, the services' own
// recommendations stand in, as before.
func TestDJFallsBack(t *testing.T) {
	e := newEnv(t, fake.Options{})
	e.withGraph(knowledge{})
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	got := e.suggest(e.alice, suggest.ScopeMine, suggest.OriginHistory, 5, false)
	if want := []string{"t02", "t03", "t04", "t05", "t06"}; !slices.Equal(ids(got), want) {
		t.Fatalf("suggested %v, want the service's %v", ids(got), want)
	}
}

// A service that only searches gets the DJ's suggestions too.
func TestDJSearchOnlyService(t *testing.T) {
	e := newEnv(t, fake.Options{ID: "spotty", Name: "Spotty", NoRecommendations: true})
	e.withGraph(library)
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	if got := ids(e.suggest(e.alice, suggest.ScopeMine, suggest.OriginHistory, 10, false)); !slices.Contains(got, "t10") {
		t.Fatalf("suggested %v, want the DJ's Greenwich (t10)", got)
	}
}
