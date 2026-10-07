// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"cmp"
	"context"
	"slices"

	"github.com/madeofpendletonwool/syncphony/server/internal/admin"
	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// requireAdmin fails unless the signed-in user is an admin.
func requireAdmin(ctx context.Context) error {
	if sessionFrom(ctx).User.Role != store.RoleAdmin {
		return auth.ErrForbidden
	}
	return nil
}

func toServerSettings(st admin.Settings) ServerSettings {
	return ServerSettings{InstanceName: st.InstanceName, InviteExpiryHours: int(st.InviteExpiry().Hours())}
}

// GetServerSettings returns the server's own settings.
func (s *Server) GetServerSettings(ctx context.Context, _ GetServerSettingsRequestObject) (GetServerSettingsResponseObject, error) {
	st, err := s.Admin.Settings(ctx)
	return GetServerSettings200JSONResponse(toServerSettings(st)), err
}

// UpdateServerSettings changes the server's settings (admins).
func (s *Server) UpdateServerSettings(ctx context.Context, req UpdateServerSettingsRequestObject) (UpdateServerSettingsResponseObject, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	st, err := s.Admin.Update(ctx, admin.Update{InstanceName: req.Body.InstanceName, InviteExpiryHours: req.Body.InviteExpiryHours})
	if err != nil {
		return nil, err
	}
	return UpdateServerSettings200JSONResponse(toServerSettings(st)), nil
}

func toBackup(b admin.Backup) Backup {
	return Backup{Name: b.Name, CreatedAt: b.CreatedAt, Bytes: b.Bytes}
}

// GetServerInfo reports the server's version, uptime, database and
// backups (admins).
func (s *Server) GetServerInfo(ctx context.Context, _ GetServerInfoRequestObject) (GetServerInfoResponseObject, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	size, err := s.Admin.DatabaseSize(ctx)
	if err != nil {
		return nil, err
	}
	bs, err := s.Admin.Backups()
	if err != nil {
		return nil, err
	}
	out := ServerInfo{Version: s.Version, StartedAt: s.StartedAt, DatabaseBytes: size, Backups: make([]Backup, len(bs))}
	for i, b := range bs {
		out.Backups[i] = toBackup(b)
	}
	return GetServerInfo200JSONResponse(out), nil
}

// CreateBackup backs up the database now (admins).
func (s *Server) CreateBackup(ctx context.Context, _ CreateBackupRequestObject) (CreateBackupResponseObject, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	b, err := s.Admin.Backup(ctx)
	if err != nil {
		return nil, err
	}
	return CreateBackup201JSONResponse(toBackup(b)), nil
}

// ListAllLinks lists every linked service and its health, failing ones
// first (admins).
func (s *Server) ListAllLinks(ctx context.Context, _ ListAllLinksRequestObject) (ListAllLinksResponseObject, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	rows, err := s.Links.All(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(rows, func(a, b store.ServiceLink) int {
		return cmp.Or(cmp.Compare(linkRank(a.Status), linkRank(b.Status)), cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.AccountLabel, b.AccountLabel))
	})
	out := make(ListAllLinks200JSONResponse, len(rows))
	for i, l := range rows {
		out[i] = toServiceLink(l)
	}
	return out, nil
}

// linkRank puts failing links before working ones.
func linkRank(status string) int {
	if status == store.LinkOK {
		return 1
	}
	return 0
}

// ListActiveRooms lists every room, who's in it, and its speaker, busiest
// first (admins).
func (s *Server) ListActiveRooms(ctx context.Context, _ ListActiveRoomsRequestObject) (ListActiveRoomsResponseObject, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	rs, err := s.Rooms.List(ctx)
	if err != nil {
		return nil, err
	}
	me := sessionFrom(ctx).User.ID
	out := make(ListActiveRooms200JSONResponse, len(rs))
	for i, r := range rs {
		a := RoomActivity{
			RoomId: r.ID, RoomName: r.Name, OwnerId: r.OwnerID, Visibility: RoomVisibility(r.Visibility),
			Members: s.Presence.Members(r.ID), State: PlaybackStateIdle,
		}
		if a.CanEnter, err = s.Rooms.CanEnter(ctx, me, r); err != nil {
			return nil, err
		}
		if a.Members == nil {
			a.Members = []string{}
		}
		np, err := s.Playback.NowPlaying(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		pn := toNowPlaying(np)
		a.State, a.Player = pn.State, pn.Player
		// What a room is playing is its own business, until the admin joins.
		if pn.Item != nil && pn.Item.Track.Title != "" && a.CanEnter {
			a.Title = &pn.Item.Track.Title
		}
		out[i] = a
	}
	slices.SortStableFunc(out, func(a, b RoomActivity) int {
		return cmp.Or(cmp.Compare(len(b.Members), len(a.Members)), cmp.Compare(stateRank(a.State), stateRank(b.State)))
	})
	return out, nil
}

// stateRank puts playing rooms first.
func stateRank(st PlaybackState) int {
	switch st {
	case PlaybackStatePlaying, PlaybackStateLoading:
		return 0
	case PlaybackStatePaused:
		return 1
	}
	return 2
}

// ListAllSessions lists everyone's signed-in devices (admins).
func (s *Server) ListAllSessions(ctx context.Context, _ ListAllSessionsRequestObject) (ListAllSessionsResponseObject, error) {
	sess := sessionFrom(ctx)
	rows, err := s.Auth.AllSessions(ctx, sess.User)
	if err != nil {
		return nil, err
	}
	current := sess.ID()
	out := make(ListAllSessions200JSONResponse, len(rows))
	for i, r := range rows {
		id := auth.SessionID(r.TokenHash)
		out[i] = UserSession{
			Id: id, UserId: r.UserID, UserAgent: r.UserAgent, CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, Current: id == current,
		}
	}
	return out, nil
}

// RevokeSession signs out anyone's device (admins).
func (s *Server) RevokeSession(ctx context.Context, req RevokeSessionRequestObject) (RevokeSessionResponseObject, error) {
	if err := s.Auth.RevokeAnySession(ctx, sessionFrom(ctx).User, req.Id); err != nil {
		return nil, err
	}
	return RevokeSession204Response{}, nil
}
