// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"

	"github.com/madeofpendletonwool/syncphony/server/internal/playlists"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Syncphony's own playlists (MAD-737, ADR 0016).

// playlistActor is the signed-in member. Guests have no playlists.
func playlistActor(ctx context.Context) (playlists.Actor, error) {
	u, err := member(ctx)
	if err != nil {
		return playlists.Actor{}, err
	}
	return roomActor(u), nil
}

func toPlaylistSong(s store.PlaylistSong) PlaylistSong {
	// A playlist song renders like a queued one: the same snapshot.
	it := toQueueItem(store.QueueItem{Provider: s.Provider, LinkID: s.LinkID, TrackID: s.TrackID, Metadata: s.Metadata})
	out := PlaylistSong{Id: s.ID, Track: it.Track, AddedAt: s.AddedAt}
	if s.AddedBy.Valid {
		out.AddedBy = &s.AddedBy.String
	}
	return out
}

func toPlaylistSongs(ss []store.PlaylistSong) []PlaylistSong {
	out := make([]PlaylistSong, len(ss))
	for i, s := range ss {
		out[i] = toPlaylistSong(s)
	}
	return out
}

func toPlaylistNight(p store.Playlist) *PlaylistNight {
	if !p.NightRoomID.Valid || !p.NightFrom.Valid || !p.NightTo.Valid {
		return nil
	}
	return &PlaylistNight{RoomId: p.NightRoomID.String, From: p.NightFrom.Time, To: p.NightTo.Time}
}

func toSavedPlaylist(p playlists.Playlist) SavedPlaylist {
	out := SavedPlaylist{
		Id: p.ID, Name: p.Name, OwnerId: p.OwnerID, Night: toPlaylistNight(p.Playlist),
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Songs: toPlaylistSongs(p.Songs),
	}
	if p.RoomID.Valid {
		out.RoomId = &p.RoomID.String
	}
	return out
}

// ListSavedPlaylists lists the playlists the caller can see.
func (s *Server) ListSavedPlaylists(ctx context.Context, _ ListSavedPlaylistsRequestObject) (ListSavedPlaylistsResponseObject, error) {
	a, err := playlistActor(ctx)
	if err != nil {
		return nil, err
	}
	ps, err := s.Playlists.List(ctx, a)
	if err != nil {
		return nil, err
	}
	out := make(ListSavedPlaylists200JSONResponse, len(ps))
	for i, p := range ps {
		out[i] = SavedPlaylistSummary{
			Id: p.ID, Name: p.Name, OwnerId: p.OwnerID, Night: toPlaylistNight(p.Playlist),
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, SongCount: p.Songs, Covers: toPlaylistSongs(p.Covers),
		}
		if p.RoomID.Valid {
			out[i].RoomId = &p.RoomID.String
		}
	}
	return out, nil
}

// CreateSavedPlaylist makes an empty playlist.
func (s *Server) CreateSavedPlaylist(ctx context.Context, req CreateSavedPlaylistRequestObject) (CreateSavedPlaylistResponseObject, error) {
	a, err := playlistActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.Playlists.Create(ctx, a, req.Body.Name, deref(req.Body.RoomId))
	if err != nil {
		return nil, err
	}
	return CreateSavedPlaylist201JSONResponse(toSavedPlaylist(p)), nil
}

// SaveNightPlaylist saves the songs a room played as a playlist.
func (s *Server) SaveNightPlaylist(ctx context.Context, req SaveNightPlaylistRequestObject) (SaveNightPlaylistResponseObject, error) {
	a, err := playlistActor(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	p, err := s.Playlists.SaveNight(ctx, a, req.RoomId, b.From, b.To, playlists.NightOptions{
		Name: b.Name, KeepSkipped: b.KeepSkipped != nil && *b.KeepSkipped, KeepRepeats: b.KeepRepeats != nil && *b.KeepRepeats, Share: b.Share != nil && *b.Share,
	})
	if err != nil {
		return nil, err
	}
	return SaveNightPlaylist201JSONResponse(toSavedPlaylist(p)), nil
}

// GetSavedPlaylist returns a playlist and its songs.
func (s *Server) GetSavedPlaylist(ctx context.Context, req GetSavedPlaylistRequestObject) (GetSavedPlaylistResponseObject, error) {
	a, err := playlistActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.Playlists.Get(ctx, a, req.PlaylistId)
	if err != nil {
		return nil, err
	}
	return GetSavedPlaylist200JSONResponse(toSavedPlaylist(p)), nil
}

// UpdateSavedPlaylist renames or shares a playlist.
func (s *Server) UpdateSavedPlaylist(ctx context.Context, req UpdateSavedPlaylistRequestObject) (UpdateSavedPlaylistResponseObject, error) {
	a, err := playlistActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.Playlists.Update(ctx, a, req.PlaylistId, req.Body.Name, req.Body.RoomId)
	if err != nil {
		return nil, err
	}
	return UpdateSavedPlaylist200JSONResponse(toSavedPlaylist(p)), nil
}

// DeleteSavedPlaylist deletes a playlist.
func (s *Server) DeleteSavedPlaylist(ctx context.Context, req DeleteSavedPlaylistRequestObject) (DeleteSavedPlaylistResponseObject, error) {
	a, err := playlistActor(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Playlists.Delete(ctx, a, req.PlaylistId); err != nil {
		return nil, err
	}
	return DeleteSavedPlaylist204Response{}, nil
}

// AddSavedPlaylistSongs adds songs to the end of a playlist.
func (s *Server) AddSavedPlaylistSongs(ctx context.Context, req AddSavedPlaylistSongsRequestObject) (AddSavedPlaylistSongsResponseObject, error) {
	a, err := playlistActor(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]playlists.SongRef, len(req.Body.Items))
	for i, it := range req.Body.Items {
		refs[i] = playlists.SongRef{LinkID: deref(it.LinkId), TrackID: deref(it.TrackId), ItemID: deref(it.ItemId)}
	}
	p, err := s.Playlists.AddSongs(ctx, a, req.PlaylistId, refs)
	if err != nil {
		return nil, err
	}
	return AddSavedPlaylistSongs200JSONResponse(toSavedPlaylist(p)), nil
}

// RemoveSavedPlaylistSong takes a song out of a playlist.
func (s *Server) RemoveSavedPlaylistSong(ctx context.Context, req RemoveSavedPlaylistSongRequestObject) (RemoveSavedPlaylistSongResponseObject, error) {
	a, err := playlistActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.Playlists.RemoveSong(ctx, a, req.PlaylistId, req.SongId)
	if err != nil {
		return nil, err
	}
	return RemoveSavedPlaylistSong200JSONResponse(toSavedPlaylist(p)), nil
}

// MoveSavedPlaylistSong moves a song within a playlist.
func (s *Server) MoveSavedPlaylistSong(ctx context.Context, req MoveSavedPlaylistSongRequestObject) (MoveSavedPlaylistSongResponseObject, error) {
	a, err := playlistActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.Playlists.MoveSong(ctx, a, req.PlaylistId, req.SongId, req.Body.Position)
	if err != nil {
		return nil, err
	}
	return MoveSavedPlaylistSong200JSONResponse(toSavedPlaylist(p)), nil
}

// GetSavedPlaylistSongArtwork loads a playlist song's artwork through the
// link it came from, like a queued song's.
func (s *Server) GetSavedPlaylistSongArtwork(ctx context.Context, req GetSavedPlaylistSongArtworkRequestObject) (GetSavedPlaylistSongArtworkResponseObject, error) {
	a, err := playlistActor(ctx)
	if err != nil {
		return nil, err
	}
	song, err := s.Playlists.Song(ctx, a, req.PlaylistId, req.SongId)
	if err != nil {
		return nil, err
	}
	t, err := playlists.Track(song)
	if err != nil {
		return nil, provider.ErrNotFound
	}
	px := 0
	if req.Params.Size != nil {
		px = *req.Params.Size
	}
	img, err := s.Artwork.ForTrack(ctx, t, px)
	if err != nil {
		return nil, err
	}
	return imageResponse(img), nil
}

// playlistSnapshot is a playlist song to queue, for AddToQueue.
func (s *Server) playlistSnapshot(ctx context.Context, songID string) (*provider.Track, error) {
	a, err := playlistActor(ctx)
	if err != nil {
		return nil, err
	}
	song, err := s.Playlists.SongByID(ctx, a, songID)
	if err != nil {
		return nil, err
	}
	t, err := playlists.Track(song)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
