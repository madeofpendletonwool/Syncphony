// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"
	"math"

	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// ListRooms returns the rooms the caller can open.
func (s *Server) ListRooms(ctx context.Context, _ ListRoomsRequestObject) (ListRoomsResponseObject, error) {
	sess := sessionFrom(ctx)
	var rs []store.Room
	if g := sess.Guest; g != nil {
		// A guest sees their own room only, whoever else can see it.
		r, err := s.Rooms.Get(ctx, g.RoomID)
		if err != nil && !errors.Is(err, rooms.ErrNotFound) {
			return nil, err
		} else if err == nil {
			rs = append(rs, r)
		}
	} else {
		var err error
		if rs, err = s.Rooms.Visible(ctx, sess.User.ID); err != nil {
			return nil, err
		}
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
	if sc := req.Body.Screens; sc != nil {
		st.Screens = fromScreens(*sc)
	}
	if g := req.Body.Guests; g != nil {
		st.Guests = fromGuests(*g)
	}
	if g := req.Body.Games; g != nil {
		st.Games = fromRoomGames(*g)
	}
	var mode string
	if req.Body.FairnessMode != nil {
		mode = string(*req.Body.FairnessMode)
	}
	if req.Body.ApproveJoins != nil {
		st.ApproveJoins = *req.Body.ApproveJoins
	}
	var visibility string
	if req.Body.Visibility != nil {
		visibility = string(*req.Body.Visibility)
	}
	r, err := s.Rooms.CreateWith(ctx, sessionFrom(ctx).User.ID, req.Body.Name, mode, visibility, st)
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

// UpdateRoom changes a room the caller owns, or any room for an admin.
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
	if sc := req.Body.Screens; sc != nil {
		u.Screens = ptr(fromScreens(*sc))
	}
	if g := req.Body.Guests; g != nil {
		u.Guests = ptr(fromGuests(*g))
	}
	if g := req.Body.Games; g != nil {
		u.Games = ptr(fromRoomGames(*g))
	}
	u.ApproveJoins = req.Body.ApproveJoins
	if v := req.Body.Visibility; v != nil {
		u.Visibility = ptr(string(*v))
		// Closing the room keeps whoever's in it now (guests are in anyway).
		u.Present = s.Presence.Members(req.RoomId)
	}
	r, err := s.Rooms.Update(ctx, actor(ctx), req.RoomId, u)
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
		Visibility: RoomVisibility(r.Visibility), ApproveJoins: st.ApproveJoins,
		Permissions: RoomPermissions{
			PlayPause: PermissionLevel(p.PlayPause), Seek: PermissionLevel(p.Seek),
			Skip: SkipPermission(p.Skip), Speaker: PermissionLevel(p.Speaker),
			StartRounds: PermissionLevel(p.StartRounds),
		},
		SkipVotePercent: *st.SkipVotePercent, CreatedAt: r.CreatedAt,
		Fairness: RoomFairness{
			MaxInARow: st.Fairness.MaxInARow, Cooldown: st.Fairness.Cooldown,
			Weights: st.Fairness.Weights, RepeatWindowMinutes: st.Fairness.RepeatWindowMinutes,
		},
	}
	out.Matching = RoomMatching{Fallback: st.Matching.FallbackOn(), Borrow: st.Matching.Borrow}
	out.Autopilot = RoomAutopilot{On: st.Autopilot.On, Adventure: RoomAutopilotAdventure(st.Autopilot.Adventure), Explore: new(st.Autopilot.ExploreLevel()), EnergyCurve: new(st.Autopilot.EnergyCurveOn())}
	out.Guests = RoomGuests{Allowed: st.Guests.Allowed, MaxSongs: st.Guests.SongLimit(), CanVote: st.Guests.CanVote()}
	sc := st.Screens.Normal()
	out.Screens = RoomScreens{Look: RoomScreensLook(sc.Look), Scene: sc.Scene, Intensity: float32(*sc.Intensity)}
	out.Games = toRoomGames(st.Games)
	if out.Fairness.Weights == nil {
		out.Fairness.Weights = map[string]int{}
	}
	return out
}

func fromMatching(m RoomMatching) rooms.Matching {
	return rooms.Matching{Fallback: &m.Fallback, Borrow: m.Borrow}
}

func fromAutopilot(a RoomAutopilot) rooms.Autopilot {
	return rooms.Autopilot{On: a.On, Adventure: string(a.Adventure), Explore: a.Explore, EnergyCurve: a.EnergyCurve}
}

func fromScreens(s RoomScreens) rooms.Screens {
	return rooms.Screens{Look: string(s.Look), Scene: s.Scene, Intensity: ptr(math.Round(float64(s.Intensity)*100) / 100)}
}

func fromGuests(g RoomGuests) rooms.Guests {
	return rooms.Guests{Allowed: g.Allowed, MaxSongs: &g.MaxSongs, NoVote: !g.CanVote}
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
	p.PlayPause, p.Seek, p.Speaker, p.StartRounds = str(c.PlayPause), str(c.Seek), str(c.Speaker), str(c.StartRounds)
	if c.Skip != nil {
		p.Skip = string(*c.Skip)
	}
	return p
}
