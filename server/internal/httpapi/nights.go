// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"

	"github.com/madeofpendletonwool/syncphony/server/internal/nights"
)

// Song of the night (MAD-721): hearts, and nights that crown one.

func toHearts(h nights.Hearts) Hearts {
	out := Hearts{RoomId: h.RoomID, ItemId: h.ItemID, UserIds: h.UserIDs}
	if out.UserIds == nil {
		out.UserIds = []string{}
	}
	return out
}

func toNight(n nights.Night) Night {
	out := Night{
		Id: n.ID, RoomId: n.RoomID, StartedAt: n.StartedAt, EndedAt: n.EndedAt,
		EndedBy: NightEndedBy(n.EndedBy), Plays: int(n.Plays),
	}
	if n.Item != nil {
		out.SongOfTheNight = &SongOfTheNight{Item: toQueueItem(*n.Item), Hearts: int(n.Hearts)}
	}
	return out
}

// GetHearts returns who hearted a song.
func (s *Server) GetHearts(ctx context.Context, req GetHeartsRequestObject) (GetHeartsResponseObject, error) {
	h, err := s.Nights.Hearts(ctx, req.RoomId, req.ItemId)
	if err != nil {
		return nil, err
	}
	return GetHearts200JSONResponse(toHearts(h)), nil
}

// HeartSong hearts a song.
func (s *Server) HeartSong(ctx context.Context, req HeartSongRequestObject) (HeartSongResponseObject, error) {
	h, err := s.Nights.Heart(ctx, req.RoomId, sessionFrom(ctx).User.ID, req.ItemId, true)
	if err != nil {
		return nil, err
	}
	return HeartSong200JSONResponse(toHearts(h)), nil
}

// UnheartSong takes a heart back.
func (s *Server) UnheartSong(ctx context.Context, req UnheartSongRequestObject) (UnheartSongResponseObject, error) {
	h, err := s.Nights.Heart(ctx, req.RoomId, sessionFrom(ctx).User.ID, req.ItemId, false)
	if err != nil {
		return nil, err
	}
	return UnheartSong200JSONResponse(toHearts(h)), nil
}

// ListNights returns the room's past nights.
func (s *Server) ListNights(ctx context.Context, req ListNightsRequestObject) (ListNightsResponseObject, error) {
	limit := 20
	if req.Params.Limit != nil {
		limit = min(max(*req.Params.Limit, 1), 100)
	}
	ns, err := s.Nights.List(ctx, req.RoomId, limit)
	if err != nil {
		return nil, err
	}
	out := make(ListNights200JSONResponse, len(ns))
	for i, n := range ns {
		out[i] = toNight(n)
	}
	return out, nil
}

// EndNight ends the room's night and crowns its song.
func (s *Server) EndNight(ctx context.Context, req EndNightRequestObject) (EndNightResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	n, err := s.Nights.End(ctx, req.RoomId, u)
	if err != nil {
		return nil, err
	}
	return EndNight201JSONResponse(toNight(n)), nil
}
