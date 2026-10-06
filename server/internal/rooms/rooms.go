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
	// OnUpdate, if set, is called after a room's settings change.
	OnUpdate func(store.Room)
}

// New returns a Service.
func New(db *store.Store, bus realtime.Bus) *Service {
	return &Service{db: db, bus: bus, Now: store.Now}
}

// Permission levels: who may do something in a room. The owner always may.
const (
	Everyone = "everyone"
	Owner    = "owner"
	// Vote is a skip level only: members vote, and the song is skipped
	// once enough of them have.
	Vote = "vote"
)

// Permissions say who may control playback in a room. Whoever queued a
// song may always skip it.
type Permissions struct {
	// PlayPause is who may play and pause: Everyone or Owner.
	PlayPause string `json:"playPause"`
	// Seek is who may seek: Everyone or Owner.
	Seek string `json:"seek"`
	// Skip is who may skip: Everyone, Vote or Owner.
	Skip string `json:"skip"`
	// Speaker is who may become the room's speaker: Everyone or Owner.
	Speaker string `json:"speaker"`
}

// DefaultSkipVotePercent makes a skip vote need a majority.
const DefaultSkipVotePercent = 50

// Settings are a room's options, stored as JSON in rooms.settings.
type Settings struct {
	// Controls is the single setting rooms had before Permissions (who may
	// do everything, Everyone or Owner). It's only read, as the default
	// for each permission, and never written.
	Controls    string      `json:"controls,omitempty"`
	Permissions Permissions `json:"permissions"`
	// SkipVotePercent: a skip vote passes once more than this percent of
	// the room has voted (see VotesNeeded). 0 to 99.
	SkipVotePercent *int `json:"skipVotePercent,omitempty"`
}

// ParseSettings reads a room's settings, filling in defaults. Unknown or
// bad values fall back to the defaults rather than failing.
func ParseSettings(raw string) Settings {
	var st Settings
	_ = json.Unmarshal([]byte(raw), &st)
	fallback := Everyone
	if st.Controls == Owner {
		fallback = Owner
	}
	st.Controls = ""
	p := &st.Permissions
	for _, level := range []*string{&p.PlayPause, &p.Seek, &p.Speaker} {
		if *level != Everyone && *level != Owner {
			*level = fallback
		}
	}
	if p.Skip != Everyone && p.Skip != Owner && p.Skip != Vote {
		p.Skip = fallback
	}
	if st.SkipVotePercent == nil || *st.SkipVotePercent < 0 || *st.SkipVotePercent > 99 {
		st.SkipVotePercent = ptr(DefaultSkipVotePercent)
	}
	return st
}

// Allowed reports whether userID may act at level in a room owned by
// ownerID. Only the owner may act directly at the Vote level.
func Allowed(level, ownerID, userID string) bool {
	return userID == ownerID || level == Everyone
}

// VotesNeeded is how many votes skip a song when voters members could
// vote: more than percent of them, and at least one.
func VotesNeeded(voters, percent int) int {
	return min(max(voters*percent/100+1, 1), max(voters, 1))
}

// List returns every room, oldest first.
func (s *Service) List(ctx context.Context) ([]store.Room, error) { return s.db.ListRooms(ctx) }

// Create makes a room owned by ownerID. mode "" means round robin, and
// empty settings are defaults.
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

// Update is a change to a room. Nil fields, and empty permissions, are
// left alone.
type Update struct {
	Name, FairnessMode *string
	Permissions        Permissions
	SkipVotePercent    *int
}

// Update changes a room. Only its owner may. Everyone in the room hears
// about it, and a new fairness mode reorders the queue at once.
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
	for _, f := range []struct {
		to   *string
		from string
	}{
		{&st.Permissions.PlayPause, u.Permissions.PlayPause},
		{&st.Permissions.Seek, u.Permissions.Seek},
		{&st.Permissions.Skip, u.Permissions.Skip},
		{&st.Permissions.Speaker, u.Permissions.Speaker},
	} {
		if f.from != "" {
			*f.to = f.from
		}
	}
	if u.SkipVotePercent != nil {
		st.SkipVotePercent = u.SkipVotePercent
	}
	name, raw, err := validate(name, mode, st)
	if err != nil {
		return r, err
	}
	updated, err := s.db.UpdateRoom(ctx, store.UpdateRoomParams{Name: name, FairnessMode: mode, Settings: raw, ID: id})
	if err != nil {
		return updated, err
	}
	s.bus.Publish(realtime.RoomTopic(id), realtime.Event{Type: realtime.RoomUpdated, Data: updated})
	if s.OnUpdate != nil {
		s.OnUpdate(updated)
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
	p := &st.Permissions
	for _, level := range []*string{&p.PlayPause, &p.Seek, &p.Skip, &p.Speaker} {
		if *level == "" {
			*level = Everyone
		}
	}
	for _, level := range []string{p.PlayPause, p.Seek, p.Speaker} {
		if level != Everyone && level != Owner {
			return "", "", &InvalidInputError{"play/pause, seek and speaker permissions are everyone or owner"}
		}
	}
	if p.Skip != Everyone && p.Skip != Owner && p.Skip != Vote {
		return "", "", &InvalidInputError{"the skip permission is everyone, vote or owner"}
	}
	if st.SkipVotePercent == nil {
		st.SkipVotePercent = ptr(DefaultSkipVotePercent)
	}
	if *st.SkipVotePercent < 0 || *st.SkipVotePercent > 99 {
		return "", "", &InvalidInputError{"the skip vote percent is 0 to 99"}
	}
	st.Controls = ""
	raw, err := json.Marshal(st)
	return name, string(raw), err
}

func ptr[T any](v T) *T { return &v }

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
	// SkipVotes is the vote to skip Item, while the room votes on skips.
	SkipVotes *SkipVotes
}

// SkipVotes is a vote to skip the playing song.
type SkipVotes struct {
	// Voters are the IDs of users who voted to skip, in the order they did.
	Voters []string
	// Needed is how many votes skip the song, given who's in the room now.
	Needed int
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

// Played is a song the room played, and how it ended.
type Played struct {
	Item      store.QueueItem
	StartedAt time.Time
	EndedAt   time.Time
	EndReason string
}

// History returns up to limit songs the room finished playing, newest
// first. The song playing now isn't one until it ends.
func (s *Service) History(ctx context.Context, id string, limit int) ([]Played, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	// One extra, for the play still in progress.
	rows, err := s.db.ListHistory(ctx, store.ListHistoryParams{RoomID: id, Limit: int64(limit) + 1})
	if err != nil {
		return nil, err
	}
	out := make([]Played, 0, len(rows))
	for _, r := range rows {
		if !r.PlayHistory.EndedAt.Valid || len(out) == limit {
			continue
		}
		out = append(out, Played{
			Item: r.QueueItem, StartedAt: r.PlayHistory.StartedAt,
			EndedAt: r.PlayHistory.EndedAt.Time, EndReason: r.PlayHistory.EndReason.String,
		})
	}
	return out, nil
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
