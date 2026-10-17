// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/playback"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// The room's devices: its paired screens, and every signed-in browser
// with the room open. From any of them, someone may hand the speaker to
// a screen or to another of their own computers, or stop the speaker,
// without walking over to it. The device acts on it itself, over its
// room socket: it claims the room as it would from its own button, so
// it's the one that starts the audio. Phones aren't sent the speaker:
// their browsers only play after a tap on them. Everything here lives in
// memory; a browser is listed while its socket is open.

// Device errors.
var (
	ErrDeviceOffline = errors.New("that device isn't connected right now")
	ErrDeviceIsPhone = errors.New("a phone can only become the speaker from the phone itself: its browser needs a tap there")
	ErrNotYourDevice = errors.New("you can only make your own devices the speaker")
)

// deviceNameMax caps the name a browser gives itself.
const deviceNameMax = 40

// deviceRegistry is the browsers that have each room open right now.
type deviceRegistry struct {
	mu sync.Mutex
	m  map[string]map[deviceKey]*deviceLive
}

// deviceKey is one browser of one user: a browser's tabs share its
// device ID, and two people could, in theory, share a browser.
type deviceKey struct{ user, device string }

type deviceLive struct {
	conns    int
	kind     RoomDeviceKind
	name     string
	userName string
}

// connect records a connection and reports whether it's the browser's
// first in the room (it just came). The latest connection says what the
// browser is, so opening the big screen on a computer makes it a screen.
func (d *deviceRegistry) connect(roomID string, u store.User, device string, kind RoomDeviceKind, name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = map[string]map[deviceKey]*deviceLive{}
	}
	room := d.m[roomID]
	if room == nil {
		room = map[deviceKey]*deviceLive{}
		d.m[roomID] = room
	}
	k := deviceKey{u.ID, device}
	l := room[k]
	if l == nil {
		l = &deviceLive{}
		room[k] = l
	}
	l.conns++
	l.kind, l.name, l.userName = kind, name, u.DisplayName
	return l.conns == 1
}

// disconnect records a closed connection and reports whether it was the
// browser's last in the room (it just went).
func (d *deviceRegistry) disconnect(roomID, userID, device string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	room := d.m[roomID]
	k := deviceKey{userID, device}
	l := room[k]
	if l == nil {
		return false
	}
	l.conns--
	if l.conns > 0 {
		return false
	}
	delete(room, k)
	if len(room) == 0 {
		delete(d.m, roomID)
	}
	return true
}

// roomDevice is one browser with a room open.
type roomDevice struct {
	userID, device string
	deviceLive
}

// list returns a room's browsers, by whose they are, then by name.
func (d *deviceRegistry) list(roomID string) []roomDevice {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]roomDevice, 0, len(d.m[roomID]))
	for k, l := range d.m[roomID] {
		out = append(out, roomDevice{userID: k.user, device: k.device, deviceLive: *l})
	}
	slices.SortFunc(out, func(a, b roomDevice) int {
		return cmp.Or(cmp.Compare(a.userName, b.userName), cmp.Compare(a.name, b.name), cmp.Compare(a.device, b.device))
	})
	return out
}

// find returns userID's browser with a room open, and whether anyone
// else has a browser of that ID there.
func (d *deviceRegistry) find(roomID, userID, device string) (l deviceLive, ok, others bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, v := range d.m[roomID] {
		if k.device != device {
			continue
		}
		if k.user == userID {
			l, ok = *v, true
		} else {
			others = true
		}
	}
	return l, ok, others
}

// socketDevice is what a signed-in browser's socket says it is: a big
// screen (display=1) is a screen; otherwise its own word, phone if it
// says nothing, so a browser that might need a tap is never sent the
// speaker.
func socketDevice(display bool, kind, name string) (RoomDeviceKind, string) {
	name = strings.TrimSpace(name)
	if len(name) > deviceNameMax {
		name = name[:deviceNameMax]
	}
	if display {
		return RoomDeviceKindScreen, cmp.Or(name, "Big screen")
	}
	k := RoomDeviceKind(kind)
	if k != RoomDeviceKindComputer {
		k = RoomDeviceKindPhone
	}
	return k, cmp.Or(name, "Browser")
}

// devicesChanged tells the room a browser came or went.
func (s *Server) devicesChanged(roomID string) {
	s.Bus.Publish(realtime.RoomTopic(roomID), realtime.Event{Type: realtime.DevicesUpdated, Data: DevicesChanged{RoomId: roomID}})
}

// sendDeviceCommand sends a command for one device on topic: its
// display's, or its user's (where only the browser with that device ID
// acts on it).
func (s *Server) sendDeviceCommand(topic, roomID, deviceID string, action DeviceCommandAction) {
	s.Bus.Publish(topic, realtime.Event{Type: realtime.DeviceCommand, Data: DeviceCommand{Action: action, DeviceId: deviceID, RoomId: roomID}})
}

// maySpeak reports whether userID may be the room's speaker.
func maySpeak(room store.Room, userID string) bool {
	return rooms.Allowed(rooms.ParseSettings(room.Settings).Permissions.Speaker, room.OwnerID, userID)
}

// canManageDisplay reports whether u may change one of room's displays:
// the room's owner, whoever paired it, or an admin.
func canManageDisplay(u store.User, room store.Room, d store.Display) bool {
	return u.Role == store.RoleAdmin || room.OwnerID == u.ID || d.PairedBy.String == u.ID
}

// displaySpeakable reports whether sess may make display d the speaker,
// leaving aside whether it's connected: it plays as whoever paired it,
// so they need the say-so too.
func displaySpeakable(sess *auth.Session, room store.Room, d store.Display) bool {
	return sess.Guest == nil && canManageDisplay(sess.User, room, d) && d.Audio && d.PairedBy.Valid &&
		maySpeak(room, sess.User.ID) && maySpeak(room, d.PairedBy.String)
}

// roomDisplay returns the room's paired display with this ID, if it's one.
func (s *Server) roomDisplay(ctx context.Context, roomID, id string) (store.Display, bool, error) {
	d, err := s.Auth.Display(ctx, id)
	if errors.Is(err, auth.ErrNotFound) {
		return store.Display{}, false, nil
	} else if err != nil {
		return store.Display{}, false, err
	}
	return d, d.RoomID == roomID, nil
}

// ListRoomDevices lists the room's screens and the browsers that have it open.
func (s *Server) ListRoomDevices(ctx context.Context, req ListRoomDevicesRequestObject) (ListRoomDevicesResponseObject, error) {
	sess := sessionFrom(ctx)
	u := sess.User
	room, err := s.Rooms.Get(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	ds, err := s.Auth.Displays(ctx, room.ID)
	if err != nil {
		return nil, err
	}
	devices := s.devices.list(room.ID)
	out := make(ListRoomDevices200JSONResponse, 0, len(ds)+len(devices))
	for _, d := range ds {
		online, _, _ := s.boxes.live(d.ID)
		out = append(out, RoomDevice{
			Id: d.ID, Kind: RoomDeviceKindScreen, Name: d.Name, Online: online, DisplayId: ptr(d.ID),
			CanSpeak: online && displaySpeakable(sess, room, d),
			CanStop:  canManageDisplay(u, room, d),
		})
	}
	mine := sess.Guest == nil && maySpeak(room, u.ID)
	for _, d := range devices {
		own := d.userID == u.ID
		out = append(out, RoomDevice{
			Id: d.device, Kind: d.kind, Name: d.name, Online: true, UserId: ptr(d.userID), UserName: ptr(d.userName),
			CanSpeak: own && mine && d.kind != RoomDeviceKindPhone,
			CanStop:  own || room.OwnerID == u.ID || u.Role == store.RoleAdmin,
		})
	}
	return out, nil
}

// MakeDeviceSpeaker tells a screen, or one of the caller's computers, to
// become the room's speaker.
func (s *Server) MakeDeviceSpeaker(ctx context.Context, req MakeDeviceSpeakerRequestObject) (MakeDeviceSpeakerResponseObject, error) {
	sess := sessionFrom(ctx)
	u := sess.User
	room, err := s.Rooms.Get(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	d, isDisplay, err := s.roomDisplay(ctx, room.ID, req.DeviceId)
	if err != nil {
		return nil, err
	}
	if isDisplay {
		switch {
		case !canManageDisplay(u, room, d):
			return nil, auth.ErrForbidden
		case !d.Audio:
			return nil, ErrDisplayNoAudio
		case !d.PairedBy.Valid:
			return nil, ErrDisplayOrphan
		case !displaySpeakable(sess, room, d):
			return nil, playback.ErrForbidden
		}
		if online, _, _ := s.boxes.live(d.ID); !online {
			return nil, ErrDisplayOffline
		}
		s.sendDeviceCommand(realtime.DisplayTopic(d.ID), room.ID, d.ID, Speak)
		return MakeDeviceSpeaker202Response{}, nil
	}
	if sess.Guest != nil || !maySpeak(room, u.ID) {
		return nil, playback.ErrForbidden
	}
	dev, ok, others := s.devices.find(room.ID, u.ID, req.DeviceId)
	switch {
	case !ok && others:
		return nil, ErrNotYourDevice
	case !ok:
		return nil, ErrDeviceOffline
	case dev.kind == RoomDeviceKindPhone:
		return nil, ErrDeviceIsPhone
	}
	s.sendDeviceCommand(realtime.UserTopic(u.ID), room.ID, req.DeviceId, Speak)
	return MakeDeviceSpeaker202Response{}, nil
}

// StopDeviceSpeaker tells the room's speaker to stop, and lets go of it.
// The device hears first, so it doesn't take the room straight back.
func (s *Server) StopDeviceSpeaker(ctx context.Context, req StopDeviceSpeakerRequestObject) (StopDeviceSpeakerResponseObject, error) {
	u := sessionFrom(ctx).User
	room, err := s.Rooms.Get(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	np, err := s.Playback.NowPlaying(ctx, room.ID)
	if err != nil {
		return nil, err
	}
	p := np.Player
	if p == nil || p.DeviceID != req.DeviceId {
		return nil, playback.ErrNotPlayer
	}
	d, isDisplay, err := s.roomDisplay(ctx, room.ID, req.DeviceId)
	if err != nil {
		return nil, err
	}
	if isDisplay {
		if !canManageDisplay(u, room, d) {
			return nil, auth.ErrForbidden
		}
		s.sendDeviceCommand(realtime.DisplayTopic(d.ID), room.ID, d.ID, Stop)
	} else {
		if p.UserID != u.ID && room.OwnerID != u.ID && u.Role != store.RoleAdmin {
			return nil, playback.ErrForbidden
		}
		s.sendDeviceCommand(realtime.UserTopic(p.UserID), room.ID, p.DeviceID, Stop)
	}
	if err := s.Playback.Drop(ctx, room.ID, req.DeviceId); err != nil {
		return nil, err
	}
	if np, err = s.Playback.NowPlaying(ctx, room.ID); err != nil {
		return nil, err
	}
	return StopDeviceSpeaker200JSONResponse(s.nowPlayingFor(np, false)), nil
}
