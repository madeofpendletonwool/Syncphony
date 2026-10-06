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
	st := rooms.Settings{Permissions: fromPermissionsChange(req.Body.Permissions), SkipVotePercent: req.Body.SkipVotePercent}
	if f := req.Body.Fairness; f != nil {
		st.Fairness = fromFairness(*f)
	}
	if m := req.Body.Matching; m != nil {
		st.Matching = fromMatching(*m)
	}
	if a := req.Body.Autopilot; a != nil {
		st.Autopilot = fromAutopilot(*a)
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
	u := rooms.Update{
		Name: req.Body.Name, Permissions: fromPermissionsChange(req.Body.Permissions), SkipVotePercent: req.Body.SkipVotePercent,
	}
	if req.Body.FairnessMode != nil {
		u.FairnessMode = ptr(string(*req.Body.FairnessMode))
	}
	if f := req.Body.Fairness; f != nil {
		u.Fairness = ptr(fromFairness(*f))
	}
	if m := req.Body.Matching; m != nil {
		u.Matching = ptr(fromMatching(*m))
	}
	if a := req.Body.Autopilot; a != nil {
		u.Autopilot = ptr(fromAutopilot(*a))
	}
	r, err := s.Rooms.Update(ctx, sessionFrom(ctx).User.ID, req.RoomId, u)
	if err != nil {
		return nil, err
	}
	return UpdateRoom200JSONResponse(toRoom(r)), nil
}

func toRoom(r store.Room) Room {
	st := rooms.ParseSettings(r.Settings)
	p := st.Permissions
	out := Room{
		Id: r.ID, Name: r.Name, OwnerId: r.OwnerID, FairnessMode: FairnessMode(r.FairnessMode),
		Permissions: RoomPermissions{
			PlayPause: PermissionLevel(p.PlayPause), Seek: PermissionLevel(p.Seek),
			Skip: SkipPermission(p.Skip), Speaker: PermissionLevel(p.Speaker),
		},
		SkipVotePercent: *st.SkipVotePercent, CreatedAt: r.CreatedAt,
		Fairness: RoomFairness{
			MaxInARow: st.Fairness.MaxInARow, Cooldown: st.Fairness.Cooldown,
			Weights: st.Fairness.Weights, RepeatWindowMinutes: st.Fairness.RepeatWindowMinutes,
		},
	}
	out.Matching = RoomMatching{Fallback: st.Matching.FallbackOn(), Borrow: st.Matching.Borrow}
	out.Autopilot = RoomAutopilot{On: st.Autopilot.On, Adventure: RoomAutopilotAdventure(st.Autopilot.Adventure)}
	if out.Fairness.Weights == nil {
		out.Fairness.Weights = map[string]int{}
	}
	return out
}

func fromMatching(m RoomMatching) rooms.Matching {
	return rooms.Matching{Fallback: &m.Fallback, Borrow: m.Borrow}
}

func fromAutopilot(a RoomAutopilot) rooms.Autopilot {
	return rooms.Autopilot{On: a.On, Adventure: string(a.Adventure)}
}

func fromFairness(f RoomFairness) rooms.Fairness {
	return rooms.Fairness{MaxInARow: f.MaxInARow, Cooldown: f.Cooldown, Weights: f.Weights, RepeatWindowMinutes: f.RepeatWindowMinutes}
}

// fromPermissionsChange reads the permissions a request sets; the rest
// are empty.
func fromPermissionsChange(c *RoomPermissionsChange) rooms.Permissions {
	var p rooms.Permissions
	if c == nil {
		return p
	}
	str := func(v *PermissionLevel) string {
		if v == nil {
			return ""
		}
		return string(*v)
	}
	p.PlayPause, p.Seek, p.Speaker = str(c.PlayPause), str(c.Seek), str(c.Speaker)
	if c.Skip != nil {
		p.Skip = string(*c.Skip)
	}
	return p
}
