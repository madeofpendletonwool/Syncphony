// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"

	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// ListRooms returns every room.
func (s *Server) ListRooms(ctx context.Context, _ ListRoomsRequestObject) (ListRoomsResponseObject, error) {
	rs, err := s.Rooms.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(ListRooms200JSONResponse, len(rs))
	for i, r := range rs {
		out[i] = toRoom(r)
	}
	return out, nil
}

// CreateRoom makes a room owned by the caller.
func (s *Server) CreateRoom(ctx context.Context, req CreateRoomRequestObject) (CreateRoomResponseObject, error) {
	var st rooms.Settings
	if req.Body.Controls != nil {
		st.Controls = string(*req.Body.Controls)
	}
	var mode string
	if req.Body.FairnessMode != nil {
		mode = string(*req.Body.FairnessMode)
	}
	r, err := s.Rooms.Create(ctx, sessionFrom(ctx).User.ID, req.Body.Name, mode, st)
	if err != nil {
		return nil, err
	}
	return CreateRoom201JSONResponse(toRoom(r)), nil
}

// GetRoom returns a room.
func (s *Server) GetRoom(ctx context.Context, req GetRoomRequestObject) (GetRoomResponseObject, error) {
	r, err := s.Rooms.Get(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	return GetRoom200JSONResponse(toRoom(r)), nil
}

// UpdateRoom changes a room the caller owns.
func (s *Server) UpdateRoom(ctx context.Context, req UpdateRoomRequestObject) (UpdateRoomResponseObject, error) {
	u := rooms.Update{Name: req.Body.Name}
	if req.Body.FairnessMode != nil {
		u.FairnessMode = ptr(string(*req.Body.FairnessMode))
	}
	if req.Body.Controls != nil {
		u.Controls = ptr(string(*req.Body.Controls))
	}
	r, err := s.Rooms.Update(ctx, sessionFrom(ctx).User.ID, req.RoomId, u)
	if err != nil {
		return nil, err
	}
	return UpdateRoom200JSONResponse(toRoom(r)), nil
}

func toRoom(r store.Room) Room {
	return Room{
		Id: r.ID, Name: r.Name, OwnerId: r.OwnerID, FairnessMode: FairnessMode(r.FairnessMode),
		Controls: RoomControls(rooms.ParseSettings(r.Settings).Controls), CreatedAt: r.CreatedAt,
	}
}
