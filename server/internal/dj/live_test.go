// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// TestLive walks the real music graph for a room where Alice played
// Radiohead and Bob played Portishead, and logs the shortlist at three
// explore levels, to see that the picks make sense. It runs only with
// SYNCPHONY_TEST_MUSICGRAPH_LIVE=1; Last.fm joins with SYNCPHONY_LASTFM_KEY.
func TestLive(t *testing.T) {
	if os.Getenv("SYNCPHONY_TEST_MUSICGRAPH_LIVE") == "" {
		t.Skip("set SYNCPHONY_TEST_MUSICGRAPH_LIVE=1 to ask the real services")
	}
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ua := "Syncphony/test ( https://github.com/madeofpendletonwool/syncphony )"
	mb := musicbrainz.New(db, musicbrainz.Options{UserAgent: ua})
	sources := []musicgraph.Source{
		musicgraph.NewListenBrainz(musicgraph.ListenBrainzOptions{UserAgent: ua, Token: os.Getenv("SYNCPHONY_LISTENBRAINZ_TOKEN")}),
		musicgraph.NewDeezer(musicgraph.DeezerOptions{UserAgent: ua}),
		musicgraph.NewMusicBrainz(mb),
	}
	if key := os.Getenv("SYNCPHONY_LASTFM_KEY"); key != "" {
		sources = append(sources, musicgraph.NewLastFM(musicgraph.LastFMOptions{Key: key, UserAgent: ua}))
	}
	g := musicgraph.New(db, musicgraph.Options{Sources: sources, Finder: mb})

	now := time.Now()
	h := []store.ListHistoryRow{
		played(item("bob", "Portishead", "Glory Box"), now.Add(-4*time.Minute), store.EndFinished),
		played(item("alice", "Radiohead", "No Surprises"), now.Add(-8*time.Minute), store.EndFinished),
		played(item("bob", "Portishead", "Roads"), now.Add(-12*time.Minute), store.EndFinished),
		played(item("alice", "Radiohead", "Karma Police"), now.Add(-16*time.Minute), store.EndFinished),
	}
	p := NewProfile(Input{History: h, Present: []string{"alice", "bob"}, Now: now})
	e := &Engine{Graph: g, Rand: func(int) int { return 0 }}
	for _, explore := range []float64{0, 0.5, 1} {
		ctx := t.Context()
		cands := e.walk(ctx, p, explore)
		scored := diverse(score(cands, p, explore, false))
		t.Logf("explore %.1f: %d candidates", explore, len(cands))
		for i, c := range scored[:min(len(scored), 12)] {
			t.Logf("  %2d %-26s %-32s %-14s via %-10s sim %.2f pop %.2f nov %.1f = %.3f",
				i+1, c.song.Artist.Name, c.song.Title, c.kind, c.via, c.similarity, c.popularity, c.novelty, c.score)
		}
		if len(scored) == 0 {
			t.Error("nothing to play")
		}
	}
}
