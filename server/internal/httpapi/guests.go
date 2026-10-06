// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"reflect"

	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Guests (MAD-720): people at the hangout who scan the room's guest pass
// and add songs without an account.

// ErrGuestsOff is starting a guest pass in a room that doesn't allow guests.
var ErrGuestsOff = errors.New("this room doesn't let guests join; its owner can turn that on in room settings")

// guestOps are the operations a guest may call, in their own room only.
// Everything else (other rooms, services, settings, invites, the speaker)
// is members'. Searching and browsing work on the server's shared services
// only, since a guest has none of their own.
var guestOps = map[string]bool{
	"GetMe":         true,
	"ListUsers":     true,
	"GetUserAvatar": true,
	"ListRooms":     true,
	"GetRoom":       true,
	// Shared services: what a guest searches and adds from.
	"ListProviders":     true,
	"ListLinks":         true,
	"Search":            true,
	"GetAlbum":          true,
	"GetArtist":         true,
	"ListPlaylists":     true,
	"GetPlaylistTracks": true,
	"GetCollection":     true,
	"GetRandomTracks":   true,
	"GetLinkArtwork":    true,
	"GetTrackLyrics":    true,
	// Suggestions come from the links the guest can add from.
	"GetSuggestions": true,
	// The room.
	"GetQueue":               true,
	"AddToQueue":             true,
	"MoveQueueItem":          true,
	"RemoveQueueItem":        true,
	"GetQueueItemArtwork":    true,
	"GetQueueItemPalette":    true,
	"GetQueueItemLyrics":     true,
	"GetQueueItemLinerNotes": true,
	"GetQueueItemSimilar":    true,
	"GetPlayback":            true,
	// Only skipping their own song, and voting if the room lets them
	// (the playback engine checks).
	"ControlPlayback": true,
	"SendReaction":    true,
	"GetHearts":       true,
	"HeartSong":       true,
	"UnheartSong":     true,
	"GetHistory":      true,
	"GetRoomStats":    true,
	"ListSessions":    true,
	"ListNights":      true,
}

// requestRoom is the room a request is about, if it names one.
func requestRoom(req any) (string, bool) {
	v := reflect.ValueOf(req)
	if v.Kind() != reflect.Struct {
		return "", false
	}
	f := v.FieldByName("RoomId")
	if !f.IsValid() || f.Kind() != reflect.String {
		return "", false
	}
	return f.String(), true
}

// guestAllowed lets a guest through to operations in guestOps about
// their own room.
func guestAllowed(g *store.Guest, operationID string, req any) error {
	if !guestOps[operationID] {
		return auth.ErrForbidden
	}
	if room, ok := requestRoom(req); ok && room != g.RoomID {
		return auth.ErrForbidden
	}
	return nil
}

// guestsChanged is the guests.updated event's data.
type guestsChanged struct {
	RoomID string `json:"roomId"`
}

func (s *Server) publishGuests(roomID string) {
	s.Bus.Publish(realtime.RoomTopic(roomID), realtime.Event{Type: realtime.GuestsUpdated, Data: guestsChanged{RoomID: roomID}})
}

// member returns the signed-in user, refusing guests.
func member(ctx context.Context) (store.User, error) {
	sess := sessionFrom(ctx)
	if sess.Guest != nil {
		return store.User{}, auth.ErrForbidden
	}
	return sess.User, nil
}

// mayHost reports whether u may manage a room's guests: its owner or an admin.
func mayHost(u store.User, room store.Room) bool {
	return u.Role == store.RoleAdmin || room.OwnerID == u.ID
}

func (s *Server) toGuestPass(ctx context.Context, p store.GuestPass) (GuestPass, error) {
	u, err := s.Auth.GuestPassURL(ctx, p)
	if err != nil {
		return GuestPass{}, err
	}
	out := GuestPass{Id: p.ID, RoomId: p.RoomID, Url: u, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt}
	if p.CreatedBy.Valid {
		out.CreatedBy = &p.CreatedBy.String
	}
	return out, nil
}

// GetGuestPass returns the room's current pass, for members and its displays.
func (s *Server) GetGuestPass(ctx context.Context, req GetGuestPassRequestObject) (GetGuestPassResponseObject, error) {
	room, err := s.Rooms.Get(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	if !rooms.ParseSettings(room.Settings).Guests.Allowed {
		return nil, auth.ErrNotFound
	}
	p, err := s.Auth.CurrentGuestPass(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	out, err := s.toGuestPass(ctx, p)
	return GetGuestPass200JSONResponse(out), err
}

// CreateGuestPass starts a new pass for the room.
func (s *Server) CreateGuestPass(ctx context.Context, req CreateGuestPassRequestObject) (CreateGuestPassResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	room, err := s.Rooms.Get(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	if !rooms.ParseSettings(room.Settings).Guests.Allowed {
		return nil, ErrGuestsOff
	}
	p, err := s.Auth.CreateGuestPass(ctx, u, room.ID, req.Body.ExpiresAt)
	if err != nil {
		return nil, err
	}
	s.publishGuests(room.ID)
	out, err := s.toGuestPass(ctx, p)
	return CreateGuestPass201JSONResponse(out), err
}

// RevokeGuestPass stops the room's pass letting anyone else in.
func (s *Server) RevokeGuestPass(ctx context.Context, req RevokeGuestPassRequestObject) (RevokeGuestPassResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	room, err := s.Rooms.Get(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	p, err := s.Auth.CurrentGuestPass(ctx, room.ID)
	if errors.Is(err, auth.ErrNotFound) {
		return RevokeGuestPass204Response{}, nil
	} else if err != nil {
		return nil, err
	}
	if !mayHost(u, room) && p.CreatedBy.String != u.ID {
		return nil, auth.ErrForbidden
	}
	if err := s.Auth.RevokeGuestPasses(ctx, room.ID); err != nil {
		return nil, err
	}
	s.publishGuests(room.ID)
	return RevokeGuestPass204Response{}, nil
}

// ListGuests lists the room's guests.
func (s *Server) ListGuests(ctx context.Context, req ListGuestsRequestObject) (ListGuestsResponseObject, error) {
	if _, err := member(ctx); err != nil {
		return nil, err
	}
	if _, err := s.Rooms.Get(ctx, req.RoomId); err != nil {
		return nil, err
	}
	rows, err := s.Auth.RoomGuests(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	out := make(ListGuests200JSONResponse, len(rows))
	for i, r := range rows {
		n, err := s.Queue.GuestSongs(ctx, req.RoomId, r.User.ID)
		if err != nil {
			return nil, err
		}
		out[i] = Guest{User: toUser(r.User, &r.Guest), JoinedAt: r.Guest.CreatedAt, ExpiresAt: r.Guest.ExpiresAt, Songs: n}
	}
	return out, nil
}

// KickGuest removes a guest from the room.
func (s *Server) KickGuest(ctx context.Context, req KickGuestRequestObject) (KickGuestResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	room, err := s.Rooms.Get(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	if !mayHost(u, room) {
		return nil, auth.ErrForbidden
	}
	if err := s.endGuest(ctx, room.ID, req.UserId); err != nil {
		return nil, err
	}
	return KickGuest204Response{}, nil
}

// endGuest signs a guest out for good and takes their waiting songs out
// of the queue.
func (s *Server) endGuest(ctx context.Context, roomID, userID string) error {
	if err := s.Auth.EndGuest(ctx, roomID, userID); err != nil {
		return err
	}
	s.publishGuests(roomID)
	return s.Queue.RemoveLane(ctx, roomID, userID)
}

// EndExpiredGuests ends every guest whose time is up. The server runs it
// every minute or so.
func (s *Server) EndExpiredGuests(ctx context.Context) error {
	gs, err := s.Auth.ExpiredGuests(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, g := range gs {
		if err := s.endGuest(ctx, g.RoomID, g.UserID); err != nil && !errors.Is(err, rooms.ErrNotFound) {
			errs = append(errs, err)
		} else {
			slog.Debug("guest expired", "room", g.RoomID, "user", g.UserID)
		}
	}
	return errors.Join(errs...)
}

// guestPass checks a pass from its link, and that its room still allows guests.
func (s *Server) guestPass(ctx context.Context, token string) (store.GuestPass, store.Room, error) {
	p, err := s.Auth.CheckGuestPass(ctx, requestFrom(ctx).ip, token)
	if err != nil {
		return p, store.Room{}, err
	}
	room, err := s.Rooms.Get(ctx, p.RoomID)
	if errors.Is(err, rooms.ErrNotFound) || (err == nil && !rooms.ParseSettings(room.Settings).Guests.Allowed) {
		return p, room, auth.ErrGuestPassInvalid
	}
	return p, room, err
}

// GetGuestInvite says which room a guest pass joins.
func (s *Server) GetGuestInvite(ctx context.Context, req GetGuestInviteRequestObject) (GetGuestInviteResponseObject, error) {
	p, room, err := s.guestPass(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	return GetGuestInvite200JSONResponse{RoomId: room.ID, RoomName: room.Name, ExpiresAt: p.ExpiresAt}, nil
}

// JoinAsGuest makes a guest and signs them in.
func (s *Server) JoinAsGuest(ctx context.Context, req JoinAsGuestRequestObject) (JoinAsGuestResponseObject, error) {
	p, _, err := s.guestPass(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	sess, err := s.Auth.JoinAsGuest(ctx, p, req.Body.DisplayName, requestFrom(ctx).userAgent)
	if err != nil {
		return nil, err
	}
	s.publishGuests(p.RoomID)
	resp, err := s.signedIn(ctx, sess)
	return JoinAsGuest201JSONResponse{resp}, err
}
