// SPDX-License-Identifier: AGPL-3.0-only

package autopilot

import (
	"encoding/json"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/dj"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func TestExplain(t *testing.T) {
	names := map[string]string{"sam": "Sam", "jo": "Jo"}
	name := func(id string) string { return names[id] }
	song := func(artist, title string) provider.Track {
		return provider.Track{Title: title, Artists: []provider.ArtistCredit{{Name: artist}}}
	}
	seed := func(user, artist, title string) store.QueueItem {
		meta, _ := json.Marshal(song(artist, title))
		return store.QueueItem{ID: "i1", AddedBy: user, Metadata: string(meta)}
	}
	cases := []struct {
		name         string
		pick         dj.Pick
		want, source string
	}{
		{
			"similar artist",
			dj.Pick{Track: song("Portishead", "Roads"), Seed: seed("sam", "Radiohead", "Creep"), Why: dj.Why{
				Kind: dj.KindSimilarArtist, Via: "Radiohead", Similarity: 0.8234, Sources: []string{"lastfm", "deezer"},
			}},
			"Because Sam's Radiohead → Portishead (similar 0.82)", "Last.fm, Deezer",
		},
		{
			"autopilot's own taste",
			dj.Pick{Track: song("Portishead", "Roads"), Why: dj.Why{Kind: dj.KindSimilarArtist, Via: "Radiohead", Similarity: 0.5}},
			"Because the room's Radiohead → Portishead (similar 0.50)", "",
		},
		{
			"top song",
			dj.Pick{Track: song("Radiohead", "Creep"), Why: dj.Why{Kind: dj.KindArtist, Via: "Radiohead"}},
			"A top song by Radiohead, an artist the room loves", "",
		},
		{
			"deep cut",
			dj.Pick{Track: song("Radiohead", "Nude"), Why: dj.Why{Kind: dj.KindArtist, DeepCut: true}},
			"A deep cut by Radiohead, an artist the room loves", "",
		},
		{
			"bridge",
			dj.Pick{Track: song("Portishead", "Roads"), Why: dj.Why{Kind: dj.KindSimilarArtist, Bridge: &dj.Bridge{
				Users: [2]string{"sam", "jo"}, Artists: [2]string{"Radiohead", "Massive Attack"},
			}}},
			"Bridging Sam's Radiohead and Jo's Massive Attack", "",
		},
		{
			"similar song",
			dj.Pick{Track: song("Muse", "Unintended"), Seed: seed("jo", "Radiohead", "Creep"), Why: dj.Why{Kind: dj.KindSimilarSong, Sources: []string{"lastfm"}}},
			"Like Jo's Creep", "Last.fm",
		},
		{
			"throwback",
			dj.Pick{Track: song("Blur", "Song 2"), Why: dj.Why{Kind: dj.KindThrowback, Via: "Blur"}},
			"A throwback to Blur, a favorite from past nights", "",
		},
	}
	for _, c := range cases {
		got, source := explain(c.pick, name)
		if got != c.want || source != c.source {
			t.Errorf("%s: explained %q (%q), want %q (%q)", c.name, got, source, c.want, c.source)
		}
	}
}
