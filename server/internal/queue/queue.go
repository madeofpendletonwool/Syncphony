// SPDX-License-Identifier: AGPL-3.0-only

// Package queue changes room queues: adding songs to your lane, reordering
// it, and removing songs. Changes to a room are serialized, and each one
// bumps the room's queue version and pushes the new snapshot (with the
// fair play order) to everyone in the room.
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Errors returned by Service, besides rooms.ErrNotFound and the errors of
// opening links and looking up tracks.
var (
	ErrNotFound  = errors.New("queue item not found")
	ErrForbidden = errors.New("that isn't yours to change")
	ErrNotQueued = errors.New("that song isn't waiting in the queue any more")
)

// NotPlayableError is a song its service said it won't play, refused when
// it was added. It is provider.ErrNotPlayable.
type NotPlayableError struct {
	Service, Title string
}

func (e *NotPlayableError) Error() string {
	return fmt.Sprintf("%s won't let Syncphony play %q. Try another version, or add it from another service.", e.Service, e.Title)
}

func (e *NotPlayableError) Unwrap() error { return provider.ErrNotPlayable }

// InvalidInputError is a bad request, such as adding no songs.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

// MaxAdd is the most songs one Add may queue, e.g. a whole album.
const MaxAdd = 100

// lanePositionStep is the gap between lane positions, matching
// store.NextLanePosition.
const lanePositionStep = 1024

// Tracks opens a user's own links, to look up what they queue. It's
// links.Service in production.
type Tracks interface {
	OpenFor(ctx context.Context, userID, linkID string) (provider.Session, error)
	Provider(id string) (provider.Provider, error)
}

// Service changes room queues.
type Service struct {
	db     *store.Store
	rooms  *rooms.Service
	tracks Tracks
	// Now is the clock. Default store.Now.
	Now func() time.Time
	// OnChange, if set, is called with the room's ID after every committed
	// change. The playback engine uses it to start playing when songs
	// arrive. It runs on the caller's goroutine and must not block.
	OnChange func(roomID string)

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New returns a Service.
func New(db *store.Store, rs *rooms.Service, tracks Tracks) *Service {
	return &Service{db: db, rooms: rs, tracks: tracks, Now: store.Now, locks: map[string]*sync.Mutex{}}
}

func (s *Service) lock(roomID string) func() {
	s.mu.Lock()
	l, ok := s.locks[roomID]
	if !ok {
		l = &sync.Mutex{}
		s.locks[roomID] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Change runs fn in a transaction while holding the room's lock, then
// bumps the queue version and broadcasts the new snapshot. Every change to
// a room's queue goes through here, including the playback engine's, so
// changes never interleave and each gets its own version.
func (s *Service) Change(ctx context.Context, roomID string, fn func(q *store.Queries, room store.Room) error) (rooms.QueueSnapshot, error) {
	defer s.lock(roomID)()
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		room, err := q.GetRoom(ctx, roomID)
		if store.IsNotFound(err) {
			return rooms.ErrNotFound
		} else if err != nil {
			return err
		}
		return fn(q, room)
	})
	if err != nil {
		return rooms.QueueSnapshot{}, err
	}
	snap, err := s.rooms.QueueChanged(ctx, roomID)
	if err == nil && s.OnChange != nil {
		s.OnChange(roomID)
	}
	return snap, err
}

// TrackRef names a song to queue: a track ID on one of the user's links.
type TrackRef struct {
	LinkID  string
	TrackID string
}

// Add appends songs to the end of userID's lane, in the order given. It
// looks each one up on its service first, so the queue keeps a snapshot of
// its metadata.
//
// A single song is also checked with its service, if the service can say
// ahead of time that it won't play it (provider.PlayChecker); a refused
// song is a *NotPlayableError. Larger adds (an album) aren't checked: one
// check per song would trip Spotify's throttling, and a song that won't
// play is skipped with a notice when its turn comes.
func (s *Service) Add(ctx context.Context, roomID, userID string, refs []TrackRef) (rooms.QueueSnapshot, error) {
	if len(refs) == 0 {
		return rooms.QueueSnapshot{}, &InvalidInputError{"add at least one song"}
	}
	if len(refs) > MaxAdd {
		return rooms.QueueSnapshot{}, &InvalidInputError{fmt.Sprintf("add at most %d songs at a time", MaxAdd)}
	}
	if _, err := s.rooms.Get(ctx, roomID); err != nil {
		return rooms.QueueSnapshot{}, err
	}
	// Look the tracks up before taking the room's lock: services can be slow.
	tracks, err := s.lookup(ctx, userID, refs)
	if err != nil {
		return rooms.QueueSnapshot{}, err
	}
	return s.Change(ctx, roomID, func(q *store.Queries, _ store.Room) error {
		pos, err := q.NextLanePosition(ctx, store.NextLanePositionParams{RoomID: roomID, AddedBy: userID})
		if err != nil {
			return err
		}
		now := s.Now()
		for _, t := range tracks {
			meta, err := json.Marshal(t)
			if err != nil {
				return err
			}
			if _, err := q.AddQueueItem(ctx, store.AddQueueItemParams{
				ID: store.NewID(), RoomID: roomID, AddedBy: userID,
				Provider: t.Ref.Provider, LinkID: sql.NullString{String: t.Ref.LinkID, Valid: true}, TrackID: t.Ref.ID,
				Metadata: string(meta), LanePosition: pos, Now: now,
			}); err != nil {
				return err
			}
			pos += lanePositionStep
		}
		return nil
	})
}

// lookup fetches each track's metadata, opening each link once.
func (s *Service) lookup(ctx context.Context, userID string, refs []TrackRef) ([]provider.Track, error) {
	sessions := map[string]provider.Session{}
	defer func() {
		for _, sess := range sessions {
			sess.Close()
		}
	}()
	out := make([]provider.Track, len(refs))
	for i, r := range refs {
		if r.LinkID == "" || r.TrackID == "" {
			return nil, &InvalidInputError{"each song needs a linkId and trackId"}
		}
		sess, ok := sessions[r.LinkID]
		if !ok {
			var err error
			if sess, err = s.tracks.OpenFor(ctx, userID, r.LinkID); err != nil {
				return nil, err
			}
			sessions[r.LinkID] = sess
		}
		t, err := sess.Track(ctx, r.TrackID)
		if err != nil {
			return nil, err
		}
		if pc, ok := sess.(provider.PlayChecker); ok && len(refs) == 1 {
			err := pc.CheckPlayable(ctx, r.TrackID)
			switch {
			case errors.Is(err, provider.ErrNotPlayable):
				return nil, &NotPlayableError{Service: s.serviceName(t.Ref.Provider), Title: t.Title}
			case err != nil:
				// The service couldn't tell. Queue it; if it won't play,
				// playback skips it.
				slog.Debug("checking a song is playable", "link", r.LinkID, "track", r.TrackID, "err", err)
			}
		}
		out[i] = t
	}
	return out, nil
}

// serviceName is a provider's display name, for messages.
func (s *Service) serviceName(providerID string) string {
	if p, err := s.tracks.Provider(providerID); err == nil {
		return p.Info().Name
	}
	return providerID
}

// Item returns one of a room's queue items, in any state.
func (s *Service) Item(ctx context.Context, roomID, itemID string) (store.QueueItem, error) {
	it, err := s.db.GetQueueItem(ctx, itemID)
	if store.IsNotFound(err) || (err == nil && it.RoomID != roomID) {
		return it, ErrNotFound
	}
	return it, err
}

// Move puts one of userID's queued songs at position (0 is the front) in
// their lane. Positions past the end mean the end.
func (s *Service) Move(ctx context.Context, roomID, userID, itemID string, position int) (rooms.QueueSnapshot, error) {
	return s.Change(ctx, roomID, func(q *store.Queries, _ store.Room) error {
		it, err := queuedItem(ctx, q, roomID, itemID)
		if err != nil {
			return err
		}
		if it.AddedBy != userID {
			return ErrForbidden
		}
		lane, err := q.ListLane(ctx, store.ListLaneParams{RoomID: roomID, AddedBy: userID})
		if err != nil {
			return err
		}
		lane = slices.DeleteFunc(lane, func(l store.QueueItem) bool { return l.ID == itemID })
		position = min(max(position, 0), len(lane))
		lane = slices.Insert(lane, position, it)
		// Renumber the lane. Lanes are short, and this keeps the gaps even.
		now := s.Now()
		for i, l := range lane {
			want := int64(i+1) * lanePositionStep
			if l.LanePosition == want {
				continue
			}
			if err := q.MoveQueueItem(ctx, store.MoveQueueItemParams{LanePosition: want, UpdatedAt: now, ID: l.ID}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Remove takes a queued song out of the queue. Anyone can remove their own
// songs; the room's owner can remove anyone's. The playing song isn't
// removed here: skipping it is the playback engine's job.
func (s *Service) Remove(ctx context.Context, roomID, userID, itemID string) (rooms.QueueSnapshot, error) {
	return s.Change(ctx, roomID, func(q *store.Queries, room store.Room) error {
		it, err := queuedItem(ctx, q, roomID, itemID)
		if err != nil {
			return err
		}
		if it.AddedBy != userID && room.OwnerID != userID {
			return ErrForbidden
		}
		return q.SetQueueItemState(ctx, store.SetQueueItemStateParams{State: store.ItemRemoved, UpdatedAt: s.Now(), ID: itemID})
	})
}

// queuedItem returns an item of the room that's still waiting to play.
func queuedItem(ctx context.Context, q *store.Queries, roomID, itemID string) (store.QueueItem, error) {
	it, err := q.GetQueueItem(ctx, itemID)
	if store.IsNotFound(err) || (err == nil && it.RoomID != roomID) {
		return it, ErrNotFound
	} else if err != nil {
		return it, err
	}
	if it.State != store.ItemQueued {
		return it, ErrNotQueued
	}
	return it, nil
}
