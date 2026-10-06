// SPDX-License-Identifier: AGPL-3.0-only

package spotify

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Library reads an account's playlists over the streaming protocol, as the
// Spotify apps do. The Web API only lists playlists to a user signed into
// the app, and users don't sign into ours (see the package comment).
//
// Implementations must be safe for concurrent use, and map failures to the
// provider package's errors as Audio's do.
type Library interface {
	// Playlists lists the playlists in login's library, in the user's order,
	// with folders flattened away.
	Playlists(ctx context.Context, login Login) ([]LibraryPlaylist, error)
	// PlaylistTracks lists up to n of a playlist's items from offset from.
	// Items that aren't tracks (episodes, local files) and tracks Spotify
	// has no metadata for are left out, so a page can be short.
	PlaylistTracks(ctx context.Context, login Login, playlistID string, from, n int) (LibraryPage, error)
}

// LibraryPlaylist is a playlist in an account's library.
type LibraryPlaylist struct {
	ID, Name string
	// Owner is the owner's username ("spotify" for Spotify's own mixes).
	Owner      string
	TrackCount int
	Images     []Image
}

// LibraryPage is a page of a playlist's tracks.
type LibraryPage struct {
	Tracks []LibraryTrack
	// Total is the playlist's length, in items, counting the left out ones.
	Total int
}

// LibraryTrack is a track's metadata from the streaming protocol.
type LibraryTrack struct {
	ID, Name   string
	Artists    []provider.ArtistCredit
	AlbumID    string
	AlbumName  string
	AlbumCover []Image
	Duration   time.Duration
	Explicit   bool
}

// playlistTracksPage is how many items a page of a playlist's tracks
// covers: one metadata request's worth.
const playlistTracksPage = 100

// playlistSession is a session that lists playlists, which Open returns
// when the provider has a Library.
type playlistSession struct{ *session }

var _ provider.PlaylistLister = playlistSession{}

// Playlists implements provider.PlaylistLister. The whole library comes in
// one page, from one or two requests.
func (s playlistSession) Playlists(ctx context.Context, cursor string) (provider.Page[provider.Playlist], error) {
	if cursor != "" {
		return provider.Page[provider.Playlist]{}, fmt.Errorf("spotify: bad playlists cursor %q", cursor)
	}
	lists, err := s.p.library.Playlists(ctx, s.login())
	if err != nil {
		return provider.Page[provider.Playlist]{}, err
	}
	page := provider.Page[provider.Playlist]{Items: make([]provider.Playlist, 0, len(lists))}
	for _, l := range lists {
		page.Items = append(page.Items, provider.Playlist{
			ID:         l.ID,
			Name:       l.Name,
			Owner:      l.Owner,
			TrackCount: l.TrackCount,
			Artwork:    s.artworkRef(l.Images),
		})
	}
	return page, nil
}

// PlaylistTracks implements provider.PlaylistLister. The cursor is the
// offset of the next page.
func (s playlistSession) PlaylistTracks(ctx context.Context, id, cursor string) (provider.Page[provider.Track], error) {
	if err := checkID("playlist", id); err != nil {
		return provider.Page[provider.Track]{}, err
	}
	offset := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 0 {
			return provider.Page[provider.Track]{}, fmt.Errorf("spotify: bad playlist cursor %q", cursor)
		}
		offset = n
	}
	lp, err := s.p.library.PlaylistTracks(ctx, s.login(), id, offset, playlistTracksPage)
	if err != nil {
		return provider.Page[provider.Track]{}, err
	}
	page := provider.Page[provider.Track]{Items: make([]provider.Track, 0, len(lp.Tracks))}
	for _, t := range lp.Tracks {
		page.Items = append(page.Items, provider.Track{
			Ref:      provider.TrackRef{Provider: ID, LinkID: s.link.ID, ID: t.ID},
			Title:    t.Name,
			Artists:  t.Artists,
			Album:    provider.AlbumCredit{ID: t.AlbumID, Title: t.AlbumName},
			Duration: t.Duration,
			Explicit: t.Explicit,
			Artwork:  s.artworkRef(t.AlbumCover),
		})
	}
	if next := offset + playlistTracksPage; next < lp.Total {
		page.Next = strconv.Itoa(next)
	}
	return page, nil
}
