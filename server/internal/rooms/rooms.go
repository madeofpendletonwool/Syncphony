// SPDX-License-Identifier: AGPL-3.0-only

// Package rooms manages rooms and publishes changes to their state. The
// queue and playback engines call QueueChanged and PublishNowPlaying after
// they change a room.
package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/fairness"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Errors returned by Service.
var (
	ErrNotFound  = errors.New("room not found")
	ErrForbidden = errors.New("only the room's owner can change it")
)

// InvalidInputError is a bad room field.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

// Service manages rooms and announces changes to them.
type Service struct {
	db  *store.Store
	bus realtime.Bus
	// Now is the clock. Default store.Now.
	Now func() time.Time
}

// New returns a Service.
func New(db *store.Store, bus realtime.Bus) *Service {
	return &Service{db: db, bus: bus, Now: store.Now}
}

// Who may control playback (play, pause, skip, seek, and becoming the
// speaker). The owner always may, and anyone may skip their own song.
const (
	ControlsEveryone = "everyone"
	ControlsOwner    = "owner"
)

// Settings are a room's options, stored as JSON in rooms.settings.
type Settings struct {
	Controls string `json:"controls,omitempty"`
}

// ParseSettings reads a room's settings, filling in defaults. Unknown or
// bad values fall back to the defaults rather than failing.
func ParseSettings(raw string) Settings {
	var st Settings
	_ = json.Unmarshal([]byte(raw), &st)
	if st.Controls != ControlsOwner {
		st.Controls = ControlsEveryone
	}
	return st
}

// CanControl reports whether userID may control playback in room.
func CanControl(room store.Room, userID string) bool {
	return room.OwnerID == userID || ParseSettings(room.Settings).Controls == ControlsEveryone
}

// List returns every room, oldest first.
func (s *Service) List(ctx context.Context) ([]store.Room, error) { return s.db.ListRooms(ctx) }

// Create makes a room owned by ownerID. mode "" means round robin.
func (s *Service) Create(ctx context.Context, ownerID, name, mode string, st Settings) (store.Room, error) {
	if mode == "" {
		mode = store.FairnessRoundRobin
	}
	name, raw, err := validate(name, mode, st)
	if err != nil {
		return store.Room{}, err
	}
	return s.db.CreateRoom(ctx, store.CreateRoomParams{
		ID: store.NewID(), Name: name, OwnerID: ownerID, FairnessMode: mode, Settings: raw, CreatedAt: s.Now(),
	})
}

// Update is a change to a room. Nil fields are left alone.
type Update struct {
	Name, FairnessMode, Controls *string
}

// Update changes a room. Only its owner may. A new fairness mode reorders
// the queue at once, so the new order is pushed to the room.
func (s *Service) Update(ctx context.Context, userID, id string, u Update) (store.Room, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return r, err
	}
	if r.OwnerID != userID {
		return r, ErrForbidden
	}
	name, mode, st := r.Name, r.FairnessMode, ParseSettings(r.Settings)
	if u.Name != nil {
		name = *u.Name
	}
	if u.FairnessMode != nil {
		mode = *u.FairnessMode
	}
	if u.Controls != nil {
		st.Controls = *u.Controls
	}
	name, raw, err := validate(name, mode, st)
	if err != nil {
		return r, err
	}
	updated, err := s.db.UpdateRoom(ctx, store.UpdateRoomParams{Name: name, FairnessMode: mode, Settings: raw, ID: id})
	if err != nil {
		return updated, err
	}
	if mode != r.FairnessMode {
		if _, err := s.QueueChanged(ctx, id); err != nil {
			return updated, err
		}
	}
	return updated, nil
}

func validate(name, mode string, st Settings) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return "", "", &InvalidInputError{"a room name is 1 to 64 characters"}
	}
	if mode != store.FairnessRoundRobin && mode != store.FairnessFIFO {
		return "", "", &InvalidInputError{"fairness mode is round_robin or fifo"}
	}
	if st.Controls == "" {
		st.Controls = ControlsEveryone
	}
	if st.Controls != ControlsEveryone && st.Controls != ControlsOwner {
		return "", "", &InvalidInputError{"controls is everyone or owner"}
	}
	raw, err := json.Marshal(st)
	return name, string(raw), err
}

// QueueSnapshot is a room's queue at a version: the playing item and the
// queued items, each user's lane in order, and the play order the room's
// fairness policy makes of them.
type QueueSnapshot struct {
	RoomID  string
	Version int64
	Items   []store.QueueItem
	// UpNext is the IDs of the queued items, in the order they will play.
	UpNext []string
}

// NowPlaying is a room's playback state, as the playback engine sees it.
// Item is nil when nothing is playing.
type NowPlaying struct {
	RoomID string
	State  string
	Item   *store.QueueItem
	// Driver is how Item plays: provider.PlaybackStream (through the
	// player device) or provider.PlaybackRemote (on the service's own
	// device). Empty when nothing is playing.
	Driver string
	// Position is the playback position at At. While playing it advances
	// with the clock.
	Position time.Duration
	At       time.Time
	// Revision increases whenever the player must act: a new item, play,
	// pause or seek. Players apply a state once per revision.
	Revision int64
	// Player is the device acting as the room's speaker, if any.
	Player *Player
	// Next is the item that plays after this one, for preloading.
	Next *store.QueueItem
}

// Player is the device a room plays through.
type Player struct {
	DeviceID string
	UserID   string
	Name     string
	// LastSeen is when the device last reported in.
	LastSeen time.Time
}

// Notice is something members should hear about, such as a song skipped
// because its service failed.
type Notice struct {
	RoomID string
	// ItemID is the item it's about, if any.
	ItemID  string
	Message string
}

// Get returns a room.
func (s *Service) Get(ctx context.Context, id string) (store.Room, error) {
	r, err := s.db.GetRoom(ctx, id)
	if store.IsNotFound(err) {
		return r, ErrNotFound
	}
	return r, err
}

// QueueSnapshot reads a room's queue and its version together.
func (s *Service) QueueSnapshot(ctx context.Context, id string) (QueueSnapshot, error) {
	var snap QueueSnapshot
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		var err error
		snap, err = SnapshotTx(ctx, q, id)
		return err
	})
	return snap, err
}

// SnapshotTx reads a room's queue within a transaction, for engines that
// need the fair order while changing the queue.
func SnapshotTx(ctx context.Context, q *store.Queries, id string) (QueueSnapshot, error) {
	r, err := q.GetRoom(ctx, id)
	if store.IsNotFound(err) {
		return QueueSnapshot{}, ErrNotFound
	} else if err != nil {
		return QueueSnapshot{}, err
	}
	items, err := q.ListUpcoming(ctx, id)
	if err != nil {
		return QueueSnapshot{}, err
	}
	history, err := q.LastPlayedByUser(ctx, id)
	if err != nil {
		return QueueSnapshot{}, err
	}
	order := fairness.ForMode(r.FairnessMode).Order(fairnessState(items, history))
	upNext := make([]string, len(order))
	for i, it := range order {
		upNext[i] = it.ID
	}
	return QueueSnapshot{RoomID: id, Version: r.QueueVersion, Items: items, UpNext: upNext}, nil
}

// fairnessState builds the fairness engine's input from the upcoming items
// (lanes in order, as ListUpcoming returns them) and each user's last play.
func fairnessState(items []store.QueueItem, history []store.LastPlayedByUserRow) fairness.State {
	s := fairness.State{Lanes: map[string][]fairness.Item{}, LastPlayed: map[string]time.Time{}}
	for _, it := range items {
		switch it.State {
		case store.ItemPlaying:
			s.Playing = it.AddedBy
		case store.ItemQueued:
			s.Lanes[it.AddedBy] = append(s.Lanes[it.AddedBy], fairness.Item{ID: it.ID, User: it.AddedBy, AddedAt: it.AddedAt})
		}
	}
	for _, h := range history {
		s.LastPlayed[h.UserID] = h.StartedAt
	}
	return s
}

// QueueChanged bumps the room's queue version and pushes the new snapshot
// to everyone in the room, and returns it. Call it after committing a queue
// change.
func (s *Service) QueueChanged(ctx context.Context, id string) (QueueSnapshot, error) {
	var snap QueueSnapshot
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		if _, err := q.BumpQueueVersion(ctx, id); store.IsNotFound(err) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		var err error
		snap, err = SnapshotTx(ctx, q, id)
		return err
	})
	if err != nil {
		return QueueSnapshot{}, err
	}
	s.bus.Publish(realtime.RoomTopic(id), realtime.Event{Type: realtime.QueueUpdated, Version: snap.Version, Data: snap})
	return snap, nil
}

// PublishNowPlaying pushes a room's playback state to everyone in it.
func (s *Service) PublishNowPlaying(np NowPlaying) {
	s.bus.Publish(realtime.RoomTopic(np.RoomID), realtime.Event{Type: realtime.NowPlayingUpdated, Data: np})
}

// PublishNotice pushes a notice to everyone in a room.
func (s *Service) PublishNotice(n Notice) {
	s.bus.Publish(realtime.RoomTopic(n.RoomID), realtime.Event{Type: realtime.PlaybackNotice, Data: n})
}
