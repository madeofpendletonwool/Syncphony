// SPDX-License-Identifier: AGPL-3.0-only

package rooms

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Who can see and join a room (ADR 0011). The owner always can. Admins
// aren't let in by being admins: they can change or delete any room, but
// to open one that isn't open they join it as an admin, and the room is
// told.
const (
	// Open rooms are everyone's on the server.
	Open = store.VisibilityOpen
	// Unlisted rooms are for their members, and anyone with one of their
	// invites. Any member can share one.
	Unlisted = store.VisibilityUnlisted
	// Private rooms are for the members their owner lets in, directly or
	// with an invite only the owner (or an admin) can make.
	Private = store.VisibilityPrivate
)

// Access errors.
var (
	// ErrInviteInvalid: a wrong, expired, used up or revoked invite.
	ErrInviteInvalid = errors.New("that room link doesn't work any more; ask for a new one")
	// ErrNotMember: removing someone who isn't in the room.
	ErrNotMember = errors.New("they're not a member of this room")
)

// Invite limits.
const (
	MaxInviteUses = 1000
	MaxInviteTTL  = 90 * 24 * time.Hour
)

// Membership changes, for MembersChanged.
const (
	Joined    = "joined"
	Requested = "requested"
	Removed   = "removed"
)

// MembersChanged is the members.updated event: someone joined the room,
// asked to, or left or was removed. Someone removed loses the room at once.
type MembersChanged struct {
	RoomID, UserID, Change string
}

func validateVisibility(v string) error {
	if v != Open && v != Unlisted && v != Private {
		return &InvalidInputError{"visibility is open, unlisted or private"}
	}
	return nil
}

// CanEnter reports whether userID may open the room: it's open, it's
// theirs, or they're a member.
func (s *Service) CanEnter(ctx context.Context, userID string, r store.Room) (bool, error) {
	if r.Visibility == Open || r.OwnerID == userID {
		return true, nil
	}
	m, err := s.db.GetRoomMember(ctx, store.GetRoomMemberParams{RoomID: r.ID, UserID: userID})
	if store.IsNotFound(err) {
		return false, nil
	}
	return err == nil && m.Status == store.MemberJoined, err
}

// Visible returns the rooms userID may open, oldest first.
func (s *Service) Visible(ctx context.Context, userID string) ([]store.Room, error) {
	rs, err := s.db.ListRooms(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := s.db.MemberRoomIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(rs, func(r store.Room) bool {
		return r.Visibility != Open && r.OwnerID != userID && !slices.Contains(ids, r.ID)
	}), nil
}

// MayInvite reports whether by may see and make the room's invites: anyone
// in an unlisted room, and only those who manage a private one. Open rooms
// need none.
func MayInvite(by Actor, r store.Room) bool {
	switch r.Visibility {
	case Unlisted:
		return true
	case Private:
		return by.manages(r)
	}
	return false
}

// Manages reports whether by may change the room and who's in it.
func (a Actor) Manages(r store.Room) bool { return a.manages(r) }

// Members returns the room's members and the people asking to join, in
// the order they joined or asked.
func (s *Service) Members(ctx context.Context, roomID string) ([]store.RoomMember, error) {
	return s.db.ListRoomMembers(ctx, roomID)
}

// AddMember lets userID into a room that isn't open, or approves their
// request to join. Only its owner or an admin may. The caller checks
// userID is a member of the server, not a guest.
func (s *Service) AddMember(ctx context.Context, by Actor, roomID, userID string) error {
	r, err := s.Get(ctx, roomID)
	if err != nil {
		return err
	}
	if !by.manages(r) {
		return ErrForbidden
	}
	if r.Visibility == Open {
		return &InvalidInputError{"everyone on the server can already join an open room"}
	}
	if userID == r.OwnerID {
		return nil
	}
	if err := s.db.AddRoomMember(ctx, store.AddRoomMemberParams{
		RoomID: roomID, UserID: userID, AddedBy: sql.NullString{String: by.UserID, Valid: true}, CreatedAt: s.Now(),
	}); err != nil {
		return err
	}
	s.membersChanged(roomID, userID, Joined)
	return nil
}

// JoinAsAdmin makes an admin a member of a room they can't otherwise open.
func (s *Service) JoinAsAdmin(ctx context.Context, by Actor, roomID string) (store.Room, error) {
	r, err := s.Get(ctx, roomID)
	if err != nil {
		return r, err
	}
	if !by.Admin {
		return r, ErrForbidden
	}
	if ok, err := s.CanEnter(ctx, by.UserID, r); err != nil || ok {
		return r, err
	}
	if err := s.db.AddRoomMember(ctx, store.AddRoomMemberParams{
		RoomID: roomID, UserID: by.UserID, AddedBy: sql.NullString{String: by.UserID, Valid: true}, CreatedAt: s.Now(),
	}); err != nil {
		return r, err
	}
	s.membersChanged(roomID, by.UserID, Joined)
	return r, nil
}

// RemoveMember takes someone out of a room, or turns down their request
// to join. The room's owner or an admin may remove anyone but the owner;
// anyone may leave, or take back their request. Their connections to the
// room close; the caller takes their songs out of the queue.
func (s *Service) RemoveMember(ctx context.Context, by Actor, roomID, userID string) error {
	r, err := s.Get(ctx, roomID)
	if err != nil {
		return err
	}
	if userID != by.UserID && !by.manages(r) {
		return ErrForbidden
	}
	if userID == r.OwnerID {
		return &InvalidInputError{"the owner can't leave their own room; hand it over first"}
	}
	n, err := s.db.RemoveRoomMember(ctx, store.RemoveRoomMemberParams{RoomID: roomID, UserID: userID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotMember
	}
	s.membersChanged(roomID, userID, Removed)
	return nil
}

func (s *Service) membersChanged(roomID, userID, change string) {
	s.bus.Publish(realtime.RoomTopic(roomID), realtime.Event{
		Type: realtime.MembersUpdated, Data: MembersChanged{RoomID: roomID, UserID: userID, Change: change},
	})
}

// changeVisibility sets an updated room's visibility within the update's
// transaction. See Update.
func (s *Service) changeVisibility(ctx context.Context, q *store.Queries, r *store.Room, visibility string, present []string) error {
	was := r.Visibility
	updated, err := q.SetRoomVisibility(ctx, store.SetRoomVisibilityParams{Visibility: visibility, ID: r.ID})
	if err != nil {
		return err
	}
	*r = updated
	// Invites made for the old audience (anyone could share an unlisted
	// room's) shouldn't let anyone into a private one.
	if err := q.DeleteRoomInvites(ctx, r.ID); err != nil {
		return err
	}
	if was != Open {
		return nil
	}
	items, err := q.ListUpcoming(ctx, r.ID)
	if err != nil {
		return err
	}
	keep := slices.Clone(present)
	for _, it := range items {
		if !it.IsAutopilot() {
			keep = append(keep, it.AddedBy)
		}
	}
	slices.Sort(keep)
	for _, id := range slices.Compact(keep) {
		if id == r.OwnerID {
			continue
		}
		// Guests are in by their pass, whatever the room's visibility.
		if _, err := q.GetGuest(ctx, id); err == nil {
			continue
		} else if !store.IsNotFound(err) {
			return err
		}
		if err := q.AddRoomMember(ctx, store.AddRoomMemberParams{RoomID: r.ID, UserID: id, CreatedAt: s.Now()}); err != nil {
			return err
		}
	}
	return nil
}

// Invites returns the room's invites that still work, newest first.
func (s *Service) Invites(ctx context.Context, roomID string) ([]store.RoomInvite, error) {
	return s.db.ListRoomInvites(ctx, store.ListRoomInvitesParams{RoomID: roomID, Now: nullTime(s.Now())})
}

// CreateInvite makes an invite to a room that isn't open (see MayInvite).
// expires nil means it works until revoked, and maxUses 0 lets in any
// number of people.
func (s *Service) CreateInvite(ctx context.Context, by Actor, roomID string, expires *time.Time, maxUses int) (store.RoomInvite, error) {
	r, err := s.Get(ctx, roomID)
	if err != nil {
		return store.RoomInvite{}, err
	}
	if r.Visibility == Open {
		return store.RoomInvite{}, &InvalidInputError{"everyone on the server can already join an open room"}
	}
	if !MayInvite(by, r) {
		return store.RoomInvite{}, ErrForbidden
	}
	now := s.Now()
	p := store.CreateRoomInviteParams{
		Code: strings.ToLower(rand.Text()), RoomID: roomID, CreatedBy: sql.NullString{String: by.UserID, Valid: true}, CreatedAt: now,
	}
	if expires != nil {
		if !expires.After(now) || expires.Sub(now) > MaxInviteTTL {
			return store.RoomInvite{}, &InvalidInputError{fmt.Sprintf("an invite lasts up to %d days, or until it's revoked", int(MaxInviteTTL.Hours()/24))}
		}
		p.ExpiresAt = nullTime(expires.UTC())
	}
	if maxUses < 0 || maxUses > MaxInviteUses {
		return store.RoomInvite{}, &InvalidInputError{fmt.Sprintf("an invite lets in 1 to %d people, or 0 for any number", MaxInviteUses)}
	}
	if maxUses > 0 {
		p.MaxUses = sql.NullInt64{Int64: int64(maxUses), Valid: true}
	}
	return s.db.CreateRoomInvite(ctx, p)
}

// RevokeInvite stops an invite working. Whoever made it may, and so may
// the room's owner or an admin.
func (s *Service) RevokeInvite(ctx context.Context, by Actor, roomID, code string) error {
	r, err := s.Get(ctx, roomID)
	if err != nil {
		return err
	}
	inv, err := s.db.GetRoomInvite(ctx, code)
	if store.IsNotFound(err) || (err == nil && inv.RoomID != roomID) {
		return ErrInviteInvalid
	} else if err != nil {
		return err
	}
	if !by.manages(r) && inv.CreatedBy.String != by.UserID {
		return ErrForbidden
	}
	_, err = s.db.DeleteRoomInvite(ctx, store.DeleteRoomInviteParams{RoomID: roomID, Code: code})
	return err
}

// Invite returns a working invite and its room.
func (s *Service) Invite(ctx context.Context, code string) (store.RoomInvite, store.Room, error) {
	inv, err := s.db.GetRoomInvite(ctx, code)
	if store.IsNotFound(err) || (err == nil && !inviteWorks(inv, s.Now())) {
		return inv, store.Room{}, ErrInviteInvalid
	} else if err != nil {
		return inv, store.Room{}, err
	}
	r, err := s.Get(ctx, inv.RoomID)
	if errors.Is(err, ErrNotFound) || (err == nil && r.Visibility == Open) {
		return inv, r, ErrInviteInvalid
	}
	return inv, r, err
}

func inviteWorks(inv store.RoomInvite, now time.Time) bool {
	return (!inv.ExpiresAt.Valid || inv.ExpiresAt.Time.After(now)) && (!inv.MaxUses.Valid || inv.Uses < inv.MaxUses.Int64)
}

// UseInvite lets userID into the invite's room, or, in a private room that
// approves joins, asks the owner to. It returns the room and Joined or
// Requested. Someone already in uses up nothing. The caller checks userID
// is a member of the server, not a guest.
func (s *Service) UseInvite(ctx context.Context, code, userID string) (store.Room, string, error) {
	_, r, err := s.Invite(ctx, code)
	if err != nil {
		return r, "", err
	}
	if ok, err := s.CanEnter(ctx, userID, r); err != nil || ok {
		return r, Joined, err
	}
	change := Joined
	if r.Visibility == Private && ParseSettings(r.Settings).ApproveJoins {
		change = Requested
	}
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		if change == Requested {
			n, err := q.RequestRoomMembership(ctx, store.RequestRoomMembershipParams{RoomID: r.ID, UserID: userID, CreatedAt: s.Now()})
			if err != nil || n == 0 {
				return err // they'd already asked
			}
		} else if err := q.AddRoomMember(ctx, store.AddRoomMemberParams{RoomID: r.ID, UserID: userID, CreatedAt: s.Now()}); err != nil {
			return err
		}
		if _, err := q.UseRoomInvite(ctx, store.UseRoomInviteParams{Code: code, Now: nullTime(s.Now())}); store.IsNotFound(err) {
			return ErrInviteInvalid // used up by someone else just now
		} else if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return r, "", err
	}
	s.membersChanged(r.ID, userID, change)
	return r, change, nil
}

// Membership returns userID's tie to the room: store.MemberJoined,
// store.MemberPending, or "" for none.
func (s *Service) Membership(ctx context.Context, roomID, userID string) (string, error) {
	m, err := s.db.GetRoomMember(ctx, store.GetRoomMemberParams{RoomID: roomID, UserID: userID})
	if store.IsNotFound(err) {
		return "", nil
	}
	return m.Status, err
}

// DeleteSpentInvites deletes invites that have expired or been used up.
func (s *Service) DeleteSpentInvites(ctx context.Context) error {
	return s.db.DeleteSpentRoomInvites(ctx, nullTime(s.Now()))
}

func nullTime(t time.Time) sql.NullTime { return sql.NullTime{Time: t, Valid: true} }
