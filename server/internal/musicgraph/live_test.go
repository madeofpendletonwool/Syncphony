// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// TestLive asks the real services about Radiohead and Creep, to check the
// clients still read what they send. It runs only with
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
	slog.SetLogLoggerLevel(slog.LevelDebug)
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
	svc := musicgraph.New(db, musicgraph.Options{Sources: sources, Finder: mb})

	a, err := svc.Artist(t.Context(), musicgraph.ArtistRef{Name: "Radiohead"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("sources %v, MBID %s", a.Sources, a.Ref.MBID)
	if a.Ref.MBID != radioheadMBID {
		t.Errorf("MBID = %q", a.Ref.MBID)
	}
	if len(a.Sources) < len(sources) {
		t.Errorf("only %v answered", a.Sources)
	}
	for i, s := range a.Similar[:min(len(a.Similar), 8)] {
		t.Logf("similar %d: %s %.3f %v", i, s.Artist.Name, s.Score, s.Sources)
	}
	for i, s := range a.Top[:min(len(a.Top), 8)] {
		t.Logf("top %d: %s %.3f %v", i, s.Title, s.Score, s.Sources)
	}
	for _, tg := range a.Tags[:min(len(a.Tags), 8)] {
		t.Logf("tag: %s %.2f", tg.Name, tg.Weight)
	}
	if len(a.Similar) < 10 || len(a.Top) < 10 || len(a.Tags) == 0 {
		t.Errorf("%d similar, %d top, %d tags: want more", len(a.Similar), len(a.Top), len(a.Tags))
	}

	tr, err := svc.Track(t.Context(), musicgraph.SongRef{Title: "Creep", Artist: a.Ref, ISRC: "GBAYE9200070"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Creep: %+v", tr)
	if tr.Rank == 0 {
		t.Error("no rank for Creep")
	}
	tr, err = svc.Track(t.Context(), musicgraph.SongRef{Title: "Karma Police", Artist: a.Ref, MBID: "9e2ad5bc-c6f9-40d2-a36f-3122ee2072a3"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Karma Police: %+v", tr)
	if tr.Year != 1997 {
		t.Errorf("Karma Police came out in %d, want MusicBrainz's 1997", tr.Year)
	}
}
