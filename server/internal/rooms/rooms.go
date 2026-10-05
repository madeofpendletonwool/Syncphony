// SPDX-License-Identifier: AGPL-3.0-only

// Package rooms reads room state for realtime clients and publishes changes
// to it. The queue and playback engines call QueueChanged and
// NowPlayingChanged after they change a room.
package rooms

import (
	"context"
	"errors"

	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// ErrNotFound means the room doesn't exist.
var ErrNotFound = errors.New("room not found")

// Service reads and announces room state.
type Service struct {
	db  *store.Store
	bus realtime.Bus
}

// New returns a Service.
func New(db *store.Store, bus realtime.Bus) *Service { return &Service{db: db, bus: bus} }

// QueueSnapshot is a room's queue at a version: the playing item and the
// queued items, each user's lane in order. The fairness engine decides the
// play order across lanes.
type QueueSnapshot struct {
	RoomID  string
	Version int64
	Items   []store.QueueItem
}

// NowPlaying is what a room is playing. Item is nil when nothing is.
type NowPlaying struct {
	RoomID string
	Item   *store.QueueItem
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
		snap, err = queueSnapshot(ctx, q, id)
		return err
	})
	return snap, err
}

func queueSnapshot(ctx context.Context, q *store.Queries, id string) (QueueSnapshot, error) {
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
	return QueueSnapshot{RoomID: id, Version: r.QueueVersion, Items: items}, nil
}

// NowPlaying reads what a room is playing.
func (s *Service) NowPlaying(ctx context.Context, id string) (NowPlaying, error) {
	item, err := s.db.GetPlaying(ctx, id)
	if store.IsNotFound(err) {
		return NowPlaying{RoomID: id}, nil
	} else if err != nil {
		return NowPlaying{}, err
	}
	return NowPlaying{RoomID: id, Item: &item}, nil
}

// QueueChanged bumps the room's queue version and pushes the new snapshot
// to everyone in the room. Call it after committing a queue change.
func (s *Service) QueueChanged(ctx context.Context, id string) error {
	var snap QueueSnapshot
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		if _, err := q.BumpQueueVersion(ctx, id); store.IsNotFound(err) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		var err error
		snap, err = queueSnapshot(ctx, q, id)
		return err
	})
	if err != nil {
		return err
	}
	s.bus.Publish(realtime.RoomTopic(id), realtime.Event{Type: realtime.QueueUpdated, Version: snap.Version, Data: snap})
	return nil
}

// NowPlayingChanged pushes what's playing to everyone in the room.
func (s *Service) NowPlayingChanged(ctx context.Context, id string) error {
	np, err := s.NowPlaying(ctx, id)
	if err != nil {
		return err
	}
	s.bus.Publish(realtime.RoomTopic(id), realtime.Event{Type: realtime.NowPlayingUpdated, Data: np})
	return nil
}
