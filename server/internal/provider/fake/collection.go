// SPDX-License-Identifier: AGPL-3.0-only

package fake

import (
	"context"
	"fmt"
	"slices"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

var _ provider.Collection = (*session)(nil)

// Saved implements provider.Collection: the first album of every artist,
// and the first artist.
func (s *session) Saved(ctx context.Context) (provider.Saved, error) {
	if err := s.begin(ctx); err != nil {
		return provider.Saved{}, err
	}
	var out provider.Saved
	for _, ar := range library.artists {
		out.Albums = append(out.Albums, toAlbum(ar.albums[0]))
	}
	out.Artists = append(out.Artists, toArtist(library.artists[0]))
	return out, nil
}

// AlbumList implements provider.Collection. Newest is the library
// backwards, most played in order, and recently played every other album.
func (s *session) AlbumList(ctx context.Context, kind provider.AlbumListKind, limit int) ([]provider.Album, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	var albums []*album
	switch kind {
	case provider.AlbumsNewest:
		albums = slices.Clone(library.albums)
		slices.Reverse(albums)
	case provider.AlbumsFrequent:
		albums = library.albums
	case provider.AlbumsRecent:
		for i, al := range library.albums {
			if i%2 == 1 {
				albums = append(albums, al)
			}
		}
	default:
		return nil, fmt.Errorf("fake: album list %q: %w", kind, provider.ErrUnsupported)
	}
	out := make([]provider.Album, 0, min(len(albums), max(limit, 0)))
	for _, al := range albums[:min(len(albums), max(limit, 0))] {
		out = append(out, toAlbum(al))
	}
	return out, nil
}
