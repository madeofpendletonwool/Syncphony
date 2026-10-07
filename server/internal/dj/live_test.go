// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// TestLive walks the real music graph for a room where Alice played
// Radiohead and Bob played Portishead tonight, after a week of the Beatles
// and, three weeks ago, a week of Arctic Monkeys. It logs the shortlist at
// three explore levels, then a throwback fill's, to see that the picks
// make sense. It runs only with SYNCPHONY_TEST_MUSICGRAPH_LIVE=1; Last.fm
// joins with SYNCPHONY_LASTFM_KEY.
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
	in := Input{History: h, Present: []string{"alice", "bob"}, Now: now}
	in.Tags, in.Related = CachedTags(t.Context(), g, in), Related(t.Context(), g)
	p := NewProfile(in)

	// Past nights: a week of Arctic Monkeys, then a week of the Beatles,
	// folded as Memory folds them, with tags from the real graph.
	tags := func(a string) []musicgraph.Tag {
		art, err := g.Artist(t.Context(), musicgraph.ArtistRef{Name: a})
		if err != nil {
			return nil
		}
		return art.Tags
	}
	var rows []store.DjAffinity
	for n := range 14 {
		artist := "Arctic Monkeys"
		if n >= 7 {
			artist = "The Beatles"
		}
		ended := now.Add(-time.Duration(14-n) * 24 * time.Hour)
		var night []store.ListHistoryRow
		for i := range 8 {
			night = append(night, played(item([]string{"alice", "bob"}[i%2], artist, fmt.Sprint(artist, n, i)), ended.Add(-time.Duration(i)*4*time.Minute), store.EndFinished))
		}
		rows = fold("room", rows, Input{History: night, Now: ended}, int64(n+1), ended, tags)
	}
	lt := readLongTerm(store.DjRoom{Nights: 14}, rows)
	t.Logf("past nights: artists %v, tags %v", lt.Top(5), lt.TopTags(8))

	e := &Engine{Graph: g, Rand: func(int) int { return 0 }}
	show := func(label string, scored []candidate) {
		t.Logf("%s:", label)
		for i, c := range scored[:min(len(scored), 12)] {
			t.Logf("  %2d %-26s %-32s %-14s via %-14s sim %.2f pop %.2f nov %.1f prior %.2f = %.3f",
				i+1, c.song.Artist.Name, c.song.Title, c.kind, c.via, c.similarity, c.popularity, c.novelty, c.prior, c.score)
		}
		if len(scored) == 0 {
			t.Error("nothing to play")
		}
	}
	for _, explore := range []float64{0, 0.5, 1} {
		cands := e.walk(t.Context(), p, lt, explore, nil)
		show(fmt.Sprintf("explore %.1f, %d candidates", explore, len(cands)), diverse(score(cands, p, lt, explore, false), shortlist))
	}
	back := Throwbacks(p, lt)
	show(fmt.Sprintf("a throwback fill, bringing back %v", back), diverse(score(e.walk(t.Context(), p, lt, 0.25, back), p, lt, 0.25, false), shortlist))
}
