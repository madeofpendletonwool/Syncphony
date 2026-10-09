// SPDX-License-Identifier: AGPL-3.0-only

package recap

import (
	"context"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Graph is Facts from musicgraph's cache. Songs are warmed into it as
// they're queued, so by the end of the night most are there; ones that
// aren't are left out of the mix rather than waited for.
type Graph struct{ G *musicgraph.Service }

// Tags implements Facts.
func (g Graph) Tags(ctx context.Context, artist string) []Tag {
	a, ok, err := g.G.CachedArtist(ctx, musicgraph.ArtistRef{Name: artist})
	if err != nil || !ok {
		return nil
	}
	out := make([]Tag, len(a.Tags))
	for i, t := range a.Tags {
		out[i] = Tag{Name: t.Name, Weight: t.Weight}
	}
	return out
}

// Year implements Facts.
func (g Graph) Year(ctx context.Context, t provider.Track) int {
	if len(t.Artists) == 0 {
		return 0
	}
	ref := musicgraph.SongRef{Title: t.Title, Artist: musicgraph.ArtistRef{Name: t.Artists[0].Name}, MBID: t.MBID, ISRC: t.ISRC}
	tr, ok, err := g.G.CachedTrack(ctx, ref)
	if err != nil || !ok {
		return 0
	}
	return tr.Year
}
