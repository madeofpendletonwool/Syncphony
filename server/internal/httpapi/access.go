// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"

	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Room visibility (ADR 0011): who can see and join a room. Every operation
// about a room ({roomId} in its path) goes through enterRoom first.

// adminRoomOps are the operations an admin may call on a room they can't
// open: looking after it, without seeing what's in it.
var adminRoomOps = map[string]bool{
	"UpdateRoom":      true,
	"DeleteRoom":      true,
	"TransferRoom":    true,
	"JoinRoomAsAdmin": true,
}

// selfRoomOps check access themselves: someone who asked to join a room
// they can't open yet may take their request back.
var selfRoomOps = map[string]bool{
	"RemoveRoomMember": true,
}

// enterRoom lets a member of the server through to an operation about a
// room only if they can open it. A room they can't is not found, so its
// existence doesn't leak.
func (s *Server) enterRoom(ctx context.Context, u store.User, roomID, operationID string) error {
	r, err := s.Rooms.Get(ctx, roomID)
	if err != nil {
		return err
	}
	ok, err := s.Rooms.CanEnter(ctx, u.ID, r)
	switch {
	case err != nil:
		return err
	case ok, selfRoomOps[operationID], u.Role == store.RoleAdmin && adminRoomOps[operationID]:
		return nil
	}
	return rooms.ErrNotFound
}

func roomActor(u store.User) rooms.Actor {
	return rooms.Actor{UserID: u.ID, Admin: u.Role == store.RoleAdmin}
}

// ListRoomMembers lists a room's members, and for those who manage it,
// who's asked to join.
func (s *Server) ListRoomMembers(ctx context.Context, req ListRoomMembersRequestObject) (ListRoomMembersResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.Rooms.Get(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	ms, err := s.Rooms.Members(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	users, err := s.Auth.Users(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]store.User{}
	for _, u := range users {
		byID[u.ID] = u
	}
	manages := roomActor(u).Manages(r)
	out := ListRoomMembers200JSONResponse{}
	for _, m := range ms {
		usr, ok := byID[m.UserID]
		if !ok || (m.Status == store.MemberPending && !manages) {
			continue
		}
		au, err := s.apiUser(ctx, usr)
		if err != nil {
			return nil, err
		}
		rm := RoomMember{User: au, Status: RoomMemberStatus(m.Status), JoinedAt: m.CreatedAt}
		if m.AddedBy.Valid && m.AddedBy.String != m.UserID {
			rm.AddedBy = &m.AddedBy.String
		}
		out = append(out, rm)
	}
	return out, nil
}

// AddRoomMember lets someone into a room, or approves their request.
func (s *Server) AddRoomMember(ctx context.Context, req AddRoomMemberRequestObject) (AddRoomMemberResponseObject, error) {
	by, err := member(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.Auth.User(ctx, req.UserId)
	if errors.Is(err, auth.ErrNotFound) {
		return nil, &auth.InvalidInputError{Field: "userId", Message: "there's nobody by that ID"}
	} else if err != nil {
		return nil, err
	}
	if err := s.Auth.MayJoinRooms(ctx, u); err != nil {
		return nil, err
	}
	if err := s.Rooms.AddMember(ctx, roomActor(by), req.RoomId, u.ID); err != nil {
		return nil, err
	}
	return AddRoomMember204Response{}, nil
}

// RemoveRoomMember removes someone from a room, turns down their request,
// or lets them leave. Their waiting songs go with them.
func (s *Server) RemoveRoomMember(ctx context.Context, req RemoveRoomMemberRequestObject) (RemoveRoomMemberResponseObject, error) {
	by, err := member(ctx)
	if err != nil {
		return nil, err
	}
	// Taking back your own request is all someone outside may do; to
	// anyone else, the room isn't there.
	if req.UserId != by.ID {
		if err := s.enterRoom(ctx, by, req.RoomId, ""); err != nil {
			return nil, err
		}
	}
	if err := s.Rooms.RemoveMember(ctx, roomActor(by), req.RoomId, req.UserId); errors.Is(err, rooms.ErrNotMember) && req.UserId == by.ID {
		return nil, rooms.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if err := s.Queue.RemoveLane(ctx, req.RoomId, req.UserId); err != nil {
		return nil, err
	}
	return RemoveRoomMember204Response{}, nil
}

// JoinRoomAsAdmin lets an admin into a room they can't otherwise open, and
// tells everyone in it.
func (s *Server) JoinRoomAsAdmin(ctx context.Context, req JoinRoomAsAdminRequestObject) (JoinRoomAsAdminResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.Rooms.Get(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	already, err := s.Rooms.CanEnter(ctx, u.ID, r)
	if err != nil {
		return nil, err
	}
	if r, err = s.Rooms.JoinAsAdmin(ctx, roomActor(u), req.RoomId); err != nil {
		return nil, err
	}
	if !already {
		s.Rooms.PublishNotice(rooms.Notice{RoomID: r.ID, Message: u.DisplayName + " joined as a server admin"})
	}
	return JoinRoomAsAdmin200JSONResponse(toRoom(r)), nil
}

// ListRoomInvites lists a room's invite links, for those who may share them.
func (s *Server) ListRoomInvites(ctx context.Context, req ListRoomInvitesRequestObject) (ListRoomInvitesResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.Rooms.Get(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	out := ListRoomInvites200JSONResponse{}
	if !rooms.MayInvite(roomActor(u), r) {
		if r.Visibility == rooms.Open {
			return out, nil
		}
		return nil, rooms.ErrForbidden
	}
	invs, err := s.Rooms.Invites(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	for _, inv := range invs {
		out = append(out, s.toRoomInvite(inv))
	}
	return out, nil
}

// CreateRoomInvite makes an invite link to a room that isn't open.
func (s *Server) CreateRoomInvite(ctx context.Context, req CreateRoomInviteRequestObject) (CreateRoomInviteResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	maxUses := 0
	if req.Body.MaxUses != nil {
		maxUses = *req.Body.MaxUses
	}
	inv, err := s.Rooms.CreateInvite(ctx, roomActor(u), req.RoomId, req.Body.ExpiresAt, maxUses)
	if err != nil {
		return nil, err
	}
	return CreateRoomInvite201JSONResponse(s.toRoomInvite(inv)), nil
}

// RevokeRoomInvite stops an invite link working.
func (s *Server) RevokeRoomInvite(ctx context.Context, req RevokeRoomInviteRequestObject) (RevokeRoomInviteResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Rooms.RevokeInvite(ctx, roomActor(u), req.RoomId, req.Code); err != nil {
		return nil, err
	}
	return RevokeRoomInvite204Response{}, nil
}

// GetRoomInvite says which room an invite is for, and where you stand.
func (s *Server) GetRoomInvite(ctx context.Context, req GetRoomInviteRequestObject) (GetRoomInviteResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	_, r, err := s.Rooms.Invite(ctx, req.Code)
	if err != nil {
		return nil, err
	}
	p, err := s.invitePreview(ctx, u, r)
	return GetRoomInvite200JSONResponse(p), err
}

// UseRoomInvite joins a room with an invite, or asks to.
func (s *Server) UseRoomInvite(ctx context.Context, req UseRoomInviteRequestObject) (UseRoomInviteResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	r, change, err := s.Rooms.UseInvite(ctx, req.Code, u.ID)
	if err != nil {
		return nil, err
	}
	p, err := s.invitePreview(ctx, u, r)
	if err != nil {
		return nil, err
	}
	status := RoomInviteResultStatusMember
	if change == rooms.Requested {
		status = RoomInviteResultStatusPending
	}
	return UseRoomInvite200JSONResponse{Status: status, Room: p}, nil
}

func (s *Server) invitePreview(ctx context.Context, u store.User, r store.Room) (RoomInvitePreview, error) {
	p := RoomInvitePreview{
		RoomId: r.ID, RoomName: r.Name, OwnerId: r.OwnerID, Visibility: RoomVisibility(r.Visibility),
		Approval: r.Visibility == rooms.Private && rooms.ParseSettings(r.Settings).ApproveJoins,
		Status:   RoomInvitePreviewStatusNone,
	}
	ok, err := s.Rooms.CanEnter(ctx, u.ID, r)
	if err != nil {
		return p, err
	}
	if ok {
		p.Status = RoomInvitePreviewStatusMember
		return p, nil
	}
	m, err := s.Rooms.Membership(ctx, r.ID, u.ID)
	if m == store.MemberPending {
		p.Status = RoomInvitePreviewStatusPending
	}
	return p, err
}

func (s *Server) toRoomInvite(inv store.RoomInvite) RoomInvite {
	out := RoomInvite{
		Code: inv.Code, RoomId: inv.RoomID, Url: roomInviteURL(s.BaseURL, inv.Code), CreatedAt: inv.CreatedAt, Uses: int(inv.Uses),
	}
	if inv.CreatedBy.Valid {
		out.CreatedBy = &inv.CreatedBy.String
	}
	if inv.ExpiresAt.Valid {
		out.ExpiresAt = &inv.ExpiresAt.Time
	}
	if inv.MaxUses.Valid {
		out.MaxUses = ptr(int(inv.MaxUses.Int64))
	}
	return out
}

func roomInviteURL(base, code string) string { return base + "/room-invite/" + code }
