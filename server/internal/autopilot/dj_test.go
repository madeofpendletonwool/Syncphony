// SPDX-License-Identifier: AGPL-3.0-only

package autopilot_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
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
		a.Top = append(a.Top, musicgraph.Song{SongRef: musicgraph.SongRef{Title: t, Artist: a.Ref}, Score: 1 - 0.3*float64(i)})
	}
	return a
}

func like(name string, score float64) musicgraph.Similar {
	return musicgraph.Similar{Artist: musicgraph.ArtistRef{Name: name}, Score: score, Sources: []string{"test"}}
}

// The Test Patterns are most like Ghost Band, whom no service in the room
// has, then Null Island; Null Island is like Sine Language, two steps out.
var library = knowledge{
	"The Test Patterns": known("The Test Patterns", []musicgraph.Similar{like("Ghost Band", 0.95), like("Null Island", 0.9)},
		"Reference Tone", "Left Channel", "SMPTE"),
	"Ghost Band":    known("Ghost Band", nil, "Ghost Song"),
	"Null Island":   known("Null Island", []musicgraph.Similar{like("Sine Language", 0.8)}, "Greenwich", "Latitude", "Weather Buoy"),
	"Sine Language": known("Sine Language", nil, "Concert Pitch", "Tuning Fork"),
}

func (e *env) withGraph(k knowledge) {
	e.pilot.Graph = musicgraph.New(e.db, musicgraph.Options{Sources: []musicgraph.Source{k}})
}

func TestDJPicksFromTheGraph(t *testing.T) {
	e := newEnv(t)
	e.withGraph(library)
	seed := e.add(e.alice, "t01") // Reference Tone, The Test Patterns
	e.play(seed.ID, store.EndFinished)

	got := e.fill()
	if got == nil {
		t.Fatal("autopilot added nothing")
	}
	// Ghost Band is nearer, but no service in the room has them. The Test
	// Patterns just played, so the DJ moves on to Null Island's biggest hit.
	if got.TrackID != "t10" || got.AddedBy != e.alice.ID {
		t.Errorf("added %s for %s, want Greenwich (t10) for alice", got.TrackID, got.AddedBy)
	}
	info, _ := queue.ParseAutopilot(*got)
	r := info.Reason
	if r == nil || r.Kind != "similar-artist" || r.Via != "The Test Patterns" || r.Popularity != 1 || r.Score <= 0 {
		t.Fatalf("reason = %+v", r)
	}
	// It planned what might follow, and the room's energy curve is on.
	if r.Flow == nil || len(r.Flow.Ahead) == 0 || r.Flow.Target == nil {
		t.Errorf("flow = %+v: want a plan ahead, and the curve's target", r.Flow)
	}
	if info.SeedItemID != seed.ID || info.SeedTitle != "Reference Tone" {
		t.Errorf("seed = %+v, want alice's Reference Tone", info)
	}
}

// A room whose services only search (Spotify, nugs.net) gets autopilot
// from the graph; before, it got nothing.
func TestDJWithServicesThatOnlySearch(t *testing.T) {
	e := newEnvWith(t, fake.Options{NoRecommendations: true})
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	if got := e.fill(); got != nil {
		t.Fatalf("without the graph, added %s from a service that can't recommend", got.TrackID)
	}
	e.withGraph(library)
	e.play(e.add(e.bob, "t02").ID, store.EndFinished)
	if got := e.fill(); got == nil || got.TrackID != "t10" {
		t.Fatalf("with the graph: added %+v, want Greenwich (t10)", got)
	}
}

func TestDJExplores(t *testing.T) {
	e := newEnv(t)
	e.withGraph(library)
	e.settings(rooms.Autopilot{On: true, Explore: ptr(90)})
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	got := e.fill()
	if got == nil || got.TrackID != "t13" {
		t.Fatalf("added %+v, want Concert Pitch (t13), two steps out", got)
	}
	if info, _ := queue.ParseAutopilot(*got); info.Reason == nil || info.Reason.Kind != "two-steps" || info.Reason.Novelty != 1 {
		t.Errorf("reason = %+v", info.Reason)
	}
}

// When the graph knows nothing near the room's taste, the services' own
// recommendations take over, as before.
func TestDJFallsBack(t *testing.T) {
	e := newEnv(t)
	e.withGraph(knowledge{})
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	got := e.fill()
	if got == nil || got.TrackID != "t02" {
		t.Fatalf("added %+v, want the service's pick, t02", got)
	}
	if info, _ := queue.ParseAutopilot(*got); info.Reason != nil {
		t.Errorf("a service's pick has the DJ's reason: %+v", info.Reason)
	}
}

func ptr[T any](v T) *T { return &v }
