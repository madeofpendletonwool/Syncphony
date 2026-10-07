// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/go-webauthn/webauthn/protocol"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Account management: admins change roles, disable and remove accounts,
// and anyone may delete their own. Every change is in the audit log.
//
// A removed account's row stays, anonymized: the rooms' history, recaps
// and stats point at it, and still add up. Everything else of theirs goes:
// sessions, password, passkeys, linked services with their sealed
// credentials, picture, reset links, and invites nobody used. The caller
// takes their songs out of the queues.

// RemovedName is what a removed account is called from then on.
const RemovedName = "Former member"

// Removed is what removing an account changed beyond the account itself.
type Removed struct {
	// Rooms are the rooms the account owned, now someone else's.
	Rooms []store.Room
}

// managed returns the account by may manage: an admin, and a member who
// isn't a guest and wasn't removed.
func (s *Service) managed(ctx context.Context, q *store.Queries, by store.User, userID string) (store.User, error) {
	if by.Role != store.RoleAdmin {
		return store.User{}, ErrForbidden
	}
	u, err := q.GetUser(ctx, userID)
	if store.IsNotFound(err) || (err == nil && u.RemovedAt.Valid) {
		return u, ErrNotFound
	} else if err != nil {
		return u, err
	}
	if _, err := q.GetGuest(ctx, u.ID); err == nil {
		return u, invalid("user", "guests are removed from their room instead")
	} else if !store.IsNotFound(err) {
		return u, err
	}
	return u, nil
}

// active reports whether u can sign in.
func active(u store.User) bool { return !u.DisabledAt.Valid && !u.RemovedAt.Valid }

// keepAnAdmin fails if u is the last admin who can sign in, and the change
// would take that away.
func keepAnAdmin(ctx context.Context, q *store.Queries, u store.User) error {
	if u.Role != store.RoleAdmin || !active(u) {
		return nil
	}
	n, err := q.CountActiveAdmins(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}

func (s *Service) audit(ctx context.Context, q *store.Queries, by *store.User, target store.User, action, detail string) error {
	p := store.AddUserAuditParams{
		ID: store.NewID(), ActorName: "the server's operator", TargetID: sql.NullString{String: target.ID, Valid: true},
		TargetName: target.DisplayName, Action: action, Detail: detail, CreatedAt: s.now(),
	}
	if by != nil {
		p.ActorID, p.ActorName = sql.NullString{String: by.ID, Valid: true}, by.DisplayName
	}
	return q.AddUserAudit(ctx, p)
}

// SetRole makes someone an admin or a member (admins only). There's always
// at least one admin who can sign in.
func (s *Service) SetRole(ctx context.Context, by store.User, userID, role string) (store.User, error) {
	if role != store.RoleAdmin && role != store.RoleMember {
		return store.User{}, invalid("role", "must be admin or member")
	}
	var out store.User
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		u, err := s.managed(ctx, q, by, userID)
		if err != nil {
			return err
		}
		out = u
		if u.Role == role {
			return nil
		}
		if role == store.RoleMember {
			if err := keepAnAdmin(ctx, q, u); err != nil {
				return err
			}
		}
		if err := q.SetUserRole(ctx, store.SetUserRoleParams{Role: role, ID: u.ID}); err != nil {
			return err
		}
		out.Role = role
		return s.audit(ctx, q, &by, u, store.AuditRoleChanged, role)
	})
	return out, err
}

// SetDisabled disables someone's account, or enables it again (admins
// only). A disabled account is signed out everywhere and can't sign in,
// but keeps everything else.
func (s *Service) SetDisabled(ctx context.Context, by store.User, userID string, disabled bool) (store.User, error) {
	var out store.User
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		u, err := s.managed(ctx, q, by, userID)
		if err != nil {
			return err
		}
		out = u
		if u.DisabledAt.Valid == disabled {
			return nil
		}
		if u.ID == by.ID {
			return invalid("user", "you can't disable your own account")
		}
		action, at := store.AuditEnabled, sql.NullTime{}
		if disabled {
			if err := keepAnAdmin(ctx, q, u); err != nil {
				return err
			}
			action, at = store.AuditDisabled, sql.NullTime{Time: s.now(), Valid: true}
			if err := q.DeleteUserSessions(ctx, u.ID); err != nil {
				return err
			}
		}
		if err := q.SetUserDisabled(ctx, store.SetUserDisabledParams{DisabledAt: at, ID: u.ID}); err != nil {
			return err
		}
		out.DisabledAt = at
		return s.audit(ctx, q, &by, u, action, "")
	})
	return out, err
}

// RemoveUser removes someone's account (admins only, and not their own:
// see DeleteSelf). The rooms they owned become by's.
func (s *Service) RemoveUser(ctx context.Context, by store.User, userID string) (Removed, error) {
	var out Removed
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		u, err := s.managed(ctx, q, by, userID)
		if err != nil {
			return err
		}
		if u.ID == by.ID {
			return invalid("user", "delete your own account from your Me page")
		}
		if err := keepAnAdmin(ctx, q, u); err != nil {
			return err
		}
		out.Rooms, err = s.remove(ctx, q, u, by.ID)
		if err != nil {
			return err
		}
		return s.audit(ctx, q, &by, u, store.AuditRemoved, "")
	})
	return out, err
}

// Reauth is how someone proves it's them before deleting their account:
// their password, or a passkey ceremony from BeginReauth.
type Reauth struct {
	Password   *string
	CeremonyID string
	// Credential is the browser's PublicKeyCredential.toJSON(), as JSON.
	Credential []byte
	IP         string
}

// BeginReauth starts a passkey check that it's really u, before something
// that can't be undone.
func (s *Service) BeginReauth(ctx context.Context, u store.User) (*Ceremony, error) {
	wu, err := s.loadWAUser(ctx, s.db.Queries, u)
	if err != nil {
		return nil, err
	}
	if len(wu.creds) == 0 {
		return nil, invalid("passkey", "you don't have a passkey; use your password")
	}
	assertion, session, err := s.webauthn.BeginLogin(wu)
	if err != nil {
		return nil, err
	}
	return s.startCeremony(&ceremony{kind: ceremonyReauth, session: *session, userID: u.ID}, assertion)
}

// reauthenticate checks r is u's password or passkey.
func (s *Service) reauthenticate(ctx context.Context, u store.User, r Reauth) error {
	if err := s.checkLimits(r.IP, u.Username); err != nil {
		return err
	}
	if r.Password != nil {
		pw, err := s.db.GetPassword(ctx, u.ID)
		if store.IsNotFound(err) {
			return invalid("password", "you don't have a password; use a passkey")
		} else if err != nil {
			return err
		}
		ok, _, err := s.cost.verifyPassword(pw.Hash, *r.Password)
		if err != nil {
			return err
		}
		if !ok {
			s.recordFailure(r.IP, u.Username)
			return ErrWrongPassword
		}
		return nil
	}
	if r.CeremonyID == "" {
		return invalid("password", "confirm it's you with your password or a passkey")
	}
	cer, err := s.cers.take(r.CeremonyID, ceremonyReauth)
	if err != nil {
		return err
	}
	if cer.userID != u.ID {
		return ErrCeremonyExpired
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(r.Credential)
	if err != nil {
		s.byIP.fail(r.IP)
		return fmt.Errorf("%w: %w", ErrPasskeyFailed, err)
	}
	wu, err := s.loadWAUser(ctx, s.db.Queries, u)
	if err != nil {
		return err
	}
	if _, err := s.webauthn.ValidateLogin(wu, cer.session, parsed); err != nil {
		s.recordFailure(r.IP, u.Username)
		return ErrInvalidCredentials
	}
	return nil
}

// DeleteSelf deletes the signed-in user's own account, once they've proved
// it's them. Their rooms go to the longest-standing admin. The last admin
// who can sign in can't leave.
func (s *Service) DeleteSelf(ctx context.Context, sess *Session, r Reauth) (Removed, error) {
	u := sess.User
	if sess.Guest != nil {
		return Removed{}, ErrForbidden
	}
	if err := s.reauthenticate(ctx, u, r); err != nil {
		return Removed{}, err
	}
	var out Removed
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		if err := keepAnAdmin(ctx, q, u); err != nil {
			return err
		}
		var heir string
		switch a, err := q.OldestActiveAdmin(ctx, u.ID); {
		case err == nil:
			heir = a.ID
		case !store.IsNotFound(err):
			return err
		}
		var err error
		if out.Rooms, err = s.remove(ctx, q, u, heir); err != nil {
			return err
		}
		return s.audit(ctx, q, &u, u, store.AuditDeletedSelf, "")
	})
	return out, err
}

// remove deletes everything of u's but the anonymized row, and gives their
// rooms to heir (if any).
func (s *Service) remove(ctx context.Context, q *store.Queries, u store.User, heir string) ([]store.Room, error) {
	for _, del := range []func(context.Context, string) error{
		q.DeleteUserSessions, q.DeletePassword, q.DeleteUserPasskeys, q.DeleteUserServiceLinks, q.DeleteAvatar,
	} {
		if err := del(ctx, u.ID); err != nil {
			return nil, err
		}
	}
	if _, err := q.DeleteUserResetLinks(ctx, u.ID); err != nil {
		return nil, err
	}
	if err := q.DeleteUnusedInvitesBy(ctx, sql.NullString{String: u.ID, Valid: true}); err != nil {
		return nil, err
	}
	var rooms []store.Room
	if heir != "" {
		var err error
		if rooms, err = q.TransferRooms(ctx, store.TransferRoomsParams{ToID: heir, FromID: u.ID}); err != nil {
			return nil, err
		}
	}
	// The username is freed for someone else; this one can't be signed up
	// with (it's longer than any username may be).
	return rooms, q.AnonymizeUser(ctx, store.AnonymizeUserParams{
		Username: "removed-" + u.ID, DisplayName: RemovedName, Now: sql.NullTime{Time: s.now(), Valid: true}, ID: u.ID,
	})
}

// Audit lists the newest account changes, newest first (admins only).
func (s *Service) Audit(ctx context.Context, by store.User, limit int) ([]store.UserAudit, error) {
	if by.Role != store.RoleAdmin {
		return nil, ErrForbidden
	}
	return s.db.ListUserAudit(ctx, int64(limit))
}

// User returns an account, or ErrNotFound.
func (s *Service) User(ctx context.Context, id string) (store.User, error) {
	u, err := s.db.GetUser(ctx, id)
	if store.IsNotFound(err) {
		return u, ErrNotFound
	}
	return u, err
}

// MayOwnRooms fails unless u is a member who can sign in: not a guest, and
// not disabled or removed.
func (s *Service) MayOwnRooms(ctx context.Context, u store.User) error {
	if !active(u) {
		return invalid("userId", "%s can't sign in, so can't look after a room", u.DisplayName)
	}
	if _, guest, err := s.GuestOf(ctx, u.ID); err != nil {
		return err
	} else if guest {
		return invalid("userId", "guests can't own rooms")
	}
	return nil
}

// MayJoinRooms fails unless u is a member who can sign in, and so can be
// let into a room: guests are in their own room by their pass.
func (s *Service) MayJoinRooms(ctx context.Context, u store.User) error {
	if !active(u) {
		return invalid("userId", "%s can't sign in", u.DisplayName)
	}
	if _, guest, err := s.GuestOf(ctx, u.ID); err != nil {
		return err
	} else if guest {
		return invalid("userId", "guests join one room, with its guest pass")
	}
	return nil
}
