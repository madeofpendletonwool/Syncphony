// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"

	"github.com/madeofpendletonwool/syncphony/server/internal/savedartists"
)

// Saved artists (MAD-839): artists someone wants to remember, in their
// Library beside playlists. Guests can't save: they have no Library.

// ListSavedArtists returns the caller's saved artists.
func (s *Server) ListSavedArtists(ctx context.Context, _ ListSavedArtistsRequestObject) (ListSavedArtistsResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	as, err := s.SavedArtists.List(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := make(ListSavedArtists200JSONResponse, len(as))
	for i, a := range as {
		out[i] = toSavedArtist(a)
	}
	return out, nil
}

// SaveArtist saves an artist, by name or from a room's song.
func (s *Server) SaveArtist(ctx context.Context, req SaveArtistRequestObject) (SaveArtistResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := savedartists.Input{Name: deref(b.Name), LinkID: deref(b.LinkId), ArtistID: deref(b.ArtistId), ItemID: deref(b.ItemId)}
	if b.ArtistIndex != nil {
		in.ArtistIndex = *b.ArtistIndex
	}
	a, created, err := s.SavedArtists.Save(ctx, u.ID, in)
	if err != nil {
		return nil, err
	}
	if created {
		return SaveArtist201JSONResponse(toSavedArtist(a)), nil
	}
	return SaveArtist200JSONResponse(toSavedArtist(a)), nil
}

// UnsaveArtist removes one of the caller's saved artists.
func (s *Server) UnsaveArtist(ctx context.Context, req UnsaveArtistRequestObject) (UnsaveArtistResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.SavedArtists.Delete(ctx, u.ID, req.Id); err != nil {
		return nil, err
	}
	return UnsaveArtist204Response{}, nil
}

func toSavedArtist(a savedartists.Artist) SavedArtist {
	out := SavedArtist{Id: a.ID, Name: a.Name, Key: a.Key, Refs: make([]ResolvedRef, len(a.Refs)), SavedAt: a.SavedAt}
	for i, r := range a.Refs {
		out.Refs[i] = ResolvedRef{LinkId: r.LinkID, Id: r.ArtistID}
	}
	if a.Picture.Valid {
		out.Picture = &a.Picture.String
	}
	if a.RoomID.Valid {
		out.RoomId = &a.RoomID.String
	}
	if a.QueuedBy.Valid {
		out.QueuedBy = &a.QueuedBy.String
	}
	return out
}
