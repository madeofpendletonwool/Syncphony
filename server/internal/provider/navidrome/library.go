// SPDX-License-Identifier: AGPL-3.0-only

package navidrome

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// StarredID is the playlist ID starred songs are listed by, the way
// Spotify lists Liked Songs. Navidrome's playlist IDs are UUIDs (other
// Subsonic servers' are numbers), so it can't name a real playlist.
const StarredID = "starred"

var (
	_ provider.PlaylistLister = (*session)(nil)
	_ provider.Collection     = (*session)(nil)
)

// maxAlbumList is the most albums getAlbumList2 returns at once.
const maxAlbumList = 500

// Playlists implements provider.PlaylistLister: the account's starred
// songs first, if it has any, then its playlists, all in one page.
// getPlaylists includes other users' public playlists.
func (s *session) Playlists(ctx context.Context, cursor string) (provider.Page[provider.Playlist], error) {
	if cursor != "" {
		return provider.Page[provider.Playlist]{}, fmt.Errorf("navidrome: bad playlists cursor %q", cursor)
	}
	starred, err := s.starred(ctx)
	if err != nil {
		return provider.Page[provider.Playlist]{}, err
	}
	r, err := s.api.call(ctx, "getPlaylists", nil)
	if err != nil {
		return provider.Page[provider.Playlist]{}, err
	}
	var page provider.Page[provider.Playlist]
	if n := len(starred.Song); n > 0 {
		page.Items = append(page.Items, provider.Playlist{
			ID: StarredID, Name: "Starred songs", Owner: s.api.creds.Username, TrackCount: n,
			Artwork: provider.ArtworkRef(starred.Song[0].CoverArt),
		})
	}
	if r.Playlists != nil {
		for _, pl := range r.Playlists.Playlist {
			page.Items = append(page.Items, provider.Playlist{
				ID: pl.ID, Name: pl.Name, Owner: pl.Owner, TrackCount: pl.SongCount,
				Artwork: provider.ArtworkRef(pl.CoverArt),
			})
		}
	}
	return page, nil
}

// PlaylistTracks implements provider.PlaylistLister. Subsonic returns a
// whole playlist at once, so there's only ever one page.
func (s *session) PlaylistTracks(ctx context.Context, id, cursor string) (provider.Page[provider.Track], error) {
	if cursor != "" {
		return provider.Page[provider.Track]{}, fmt.Errorf("navidrome: bad playlist cursor %q", cursor)
	}
	var songs []song
	if id == StarredID {
		starred, err := s.starred(ctx)
		if err != nil {
			return provider.Page[provider.Track]{}, err
		}
		songs = starred.Song
	} else {
		r, err := s.api.call(ctx, "getPlaylist", url.Values{"id": {id}})
		if err != nil {
			return provider.Page[provider.Track]{}, err
		}
		if r.Playlist == nil {
			return provider.Page[provider.Track]{}, fmt.Errorf("navidrome getPlaylist %q: %w", id, provider.ErrNotFound)
		}
		songs = r.Playlist.Entry
	}
	page := provider.Page[provider.Track]{Items: make([]provider.Track, 0, len(songs))}
	for _, so := range songs {
		page.Items = append(page.Items, s.track(so))
	}
	return page, nil
}

// Saved implements provider.Collection with the account's starred albums
// and artists.
func (s *session) Saved(ctx context.Context) (provider.Saved, error) {
	starred, err := s.starred(ctx)
	if err != nil {
		return provider.Saved{}, err
	}
	var out provider.Saved
	for _, al := range starred.Album {
		out.Albums = append(out.Albums, toAlbum(al))
	}
	for _, ar := range starred.Artist {
		out.Artists = append(out.Artists, toArtist(ar))
	}
	return out, nil
}

// AlbumList implements provider.Collection with getAlbumList2, whose list
// types are named as ours are.
func (s *session) AlbumList(ctx context.Context, kind provider.AlbumListKind, limit int) ([]provider.Album, error) {
	switch kind {
	case provider.AlbumsNewest, provider.AlbumsFrequent, provider.AlbumsRecent:
	default:
		return nil, fmt.Errorf("navidrome: album list %q: %w", kind, provider.ErrUnsupported)
	}
	size := min(max(limit, 1), maxAlbumList)
	r, err := s.api.call(ctx, "getAlbumList2", url.Values{"type": {string(kind)}, "size": {strconv.Itoa(size)}})
	if err != nil {
		return nil, err
	}
	if r.AlbumList2 == nil {
		return nil, nil
	}
	list := r.AlbumList2.Album[:min(len(r.AlbumList2.Album), size)]
	out := make([]provider.Album, len(list))
	for i, al := range list {
		out[i] = toAlbum(al)
	}
	return out, nil
}

// starred returns what the account has starred.
func (s *session) starred(ctx context.Context) (starred, error) {
	r, err := s.api.call(ctx, "getStarred2", nil)
	if err != nil || r.Starred2 == nil {
		return starred{}, err
	}
	return *r.Starred2, nil
}
