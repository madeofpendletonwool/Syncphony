// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// auditLimit is how many account changes the audit log shows.
const auditLimit = 100

// UpdateUser changes someone's role, or disables or enables them (admins).
func (s *Server) UpdateUser(ctx context.Context, req UpdateUserRequestObject) (UpdateUserResponseObject, error) {
	by := sessionFrom(ctx).User
	var (
		u   store.User
		err error
	)
	if req.Body.Role == nil && req.Body.Disabled == nil {
		return nil, &auth.InvalidInputError{Field: "body", Message: "set role or disabled"}
	}
	if r := req.Body.Role; r != nil {
		if u, err = s.Auth.SetRole(ctx, by, req.Id, string(*r)); err != nil {
			return nil, err
		}
	}
	if d := req.Body.Disabled; d != nil {
		if u, err = s.Auth.SetDisabled(ctx, by, req.Id, *d); err != nil {
			return nil, err
		}
	}
	out, err := s.apiUser(ctx, u)
	return UpdateUser200JSONResponse(out), err
}

// RemoveUser removes someone's account (admins).
func (s *Server) RemoveUser(ctx context.Context, req RemoveUserRequestObject) (RemoveUserResponseObject, error) {
	removed, err := s.Auth.RemoveUser(ctx, sessionFrom(ctx).User, req.Id)
	if err != nil {
		return nil, err
	}
	return RemoveUser204Response{}, s.afterRemoval(ctx, req.Id, removed)
}

// BeginReauth starts a passkey check before deleting your account.
func (s *Server) BeginReauth(ctx context.Context, _ BeginReauthRequestObject) (BeginReauthResponseObject, error) {
	c, err := s.Auth.BeginReauth(ctx, sessionFrom(ctx).User)
	if err != nil {
		return nil, err
	}
	return BeginReauth200JSONResponse{CeremonyJSONResponse{CeremonyId: c.ID, Options: c.Options}}, nil
}

// DeleteMe deletes your own account.
func (s *Server) DeleteMe(ctx context.Context, req DeleteMeRequestObject) (DeleteMeResponseObject, error) {
	sess := sessionFrom(ctx)
	r := auth.Reauth{Password: req.Body.Password, IP: requestFrom(ctx).ip}
	if req.Body.CeremonyId != nil {
		r.CeremonyID = *req.Body.CeremonyId
	}
	if c := req.Body.Credential; c != nil {
		cred, err := json.Marshal(*c)
		if err != nil {
			return nil, err
		}
		r.Credential = cred
	}
	removed, err := s.Auth.DeleteSelf(ctx, sess, r)
	if err != nil {
		return nil, err
	}
	if err := s.afterRemoval(ctx, sess.User.ID, removed); err != nil {
		return nil, err
	}
	return DeleteMe204Response{Headers: DeleteMe204ResponseHeaders{SetCookie: ptr(s.clearCookie().String())}}, nil
}

// afterRemoval drops a removed account's waiting songs from every room, and
// tells the rooms they owned about their new owner.
func (s *Server) afterRemoval(ctx context.Context, userID string, removed auth.Removed) error {
	for _, r := range removed.Rooms {
		s.Rooms.Updated(r)
	}
	rs, err := s.Rooms.List(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range rs {
		if err := s.Queue.RemoveLane(ctx, r.ID, userID); err != nil && !errors.Is(err, rooms.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ListUserAudit lists who changed whose account (admins).
func (s *Server) ListUserAudit(ctx context.Context, _ ListUserAuditRequestObject) (ListUserAuditResponseObject, error) {
	rows, err := s.Auth.Audit(ctx, sessionFrom(ctx).User, auditLimit)
	if err != nil {
		return nil, err
	}
	out := make(ListUserAudit200JSONResponse, len(rows))
	for i, a := range rows {
		e := UserAuditEntry{
			Id: a.ID, ActorName: a.ActorName, TargetName: a.TargetName, Action: UserAuditEntryAction(a.Action), CreatedAt: a.CreatedAt,
		}
		if a.ActorID.Valid {
			e.ActorId = &a.ActorID.String
		}
		if a.TargetID.Valid {
			e.TargetId = &a.TargetID.String
		}
		if a.Action == store.AuditRoleChanged {
			e.Role = ptr(Role(a.Detail))
		}
		out[i] = e
	}
	return out, nil
}

// --- Rooms ----------------------------------------------------------------------

func actor(ctx context.Context) rooms.Actor {
	u := sessionFrom(ctx).User
	return rooms.Actor{UserID: u.ID, Admin: u.Role == store.RoleAdmin}
}

// DeleteRoom deletes a room (its owner or an admin).
func (s *Server) DeleteRoom(ctx context.Context, req DeleteRoomRequestObject) (DeleteRoomResponseObject, error) {
	if err := s.Rooms.Delete(ctx, actor(ctx), req.RoomId); err != nil {
		return nil, err
	}
	return DeleteRoom204Response{}, nil
}

// TransferRoom gives a room to someone else (its owner or an admin).
func (s *Server) TransferRoom(ctx context.Context, req TransferRoomRequestObject) (TransferRoomResponseObject, error) {
	u, err := s.Auth.User(ctx, req.Body.UserId)
	if errors.Is(err, auth.ErrNotFound) {
		return nil, &auth.InvalidInputError{Field: "userId", Message: "there's nobody by that ID"}
	} else if err != nil {
		return nil, err
	}
	if err := s.Auth.MayOwnRooms(ctx, u); err != nil {
		return nil, err
	}
	r, err := s.Rooms.Transfer(ctx, actor(ctx), req.RoomId, u.ID)
	if err != nil {
		return nil, err
	}
	return TransferRoom200JSONResponse(toRoom(r)), nil
}
