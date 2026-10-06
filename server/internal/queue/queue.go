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

	"github.com/madeofpendletonwool/syncphony/server/internal/links"
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

// RepeatError is a song refused by the room's repeat guard: it's already
// waiting or playing, or played too recently.
type RepeatError struct {
	Title string
	// Minutes is the room's repeat window.
	Minutes int
}

func (e *RepeatError) Error() string {
	return fmt.Sprintf("%q is already in the queue or played in the last %s. This room doesn't repeat songs that soon.", e.Title, window(e.Minutes))
}

func window(minutes int) string {
	switch {
	case minutes == 60:
		return "hour"
	case minutes%60 == 0:
		return fmt.Sprintf("%d hours", minutes/60)
	default:
		return fmt.Sprintf("%d minutes", minutes)
	}
}

// GuestLimitError is a guest adding more songs than the room lets them.
type GuestLimitError struct {
	// Limit is the room's limit, and Left how many more they may add.
	Limit, Left int
}

func (e *GuestLimitError) Error() string {
	if e.Left == 0 {
		return fmt.Sprintf("Guests can add %d songs here, and you've added yours. Thanks for the picks!", e.Limit)
	}
	return fmt.Sprintf("Guests can add %d songs here; you have %d left.", e.Limit, e.Left)
}

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
	// GetUsable returns a link userID may use, or links.ErrNotFound.
	GetUsable(ctx context.Context, userID, linkID string) (store.ServiceLink, error)
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
	// OnAdd, if set, is called with the songs added, once they're in. Like
	// OnChange it must not block. MusicBrainz enrichment starts here.
	OnAdd func(tracks []provider.Track)

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

// TrackRef names a song to queue: a track ID on a link the user can use,
// or, with FromItemID, the song of an item the room already had.
type TrackRef struct {
	LinkID  string
	TrackID string
	// FromItemID queues the song of one of the room's items again, from
	// the same link. That needs a link the user can use, unless the room
	// lets people borrow (rooms.Matching.Borrow).
	FromItemID string
}

// ErrCantBorrow is queueing again a song from someone else's service in a
// room that doesn't allow it.
var ErrCantBorrow = errors.New("that song is on someone else's service, and this room doesn't let people borrow songs")

// Add appends songs to the end of userID's lane, in the order given. It
// looks each one up on its service first, so the queue keeps a snapshot of
// its metadata.
//
// A single song is also checked with its service, if the service can say
// ahead of time that it won't play it (provider.PlayChecker); a refused
// song is a *NotPlayableError. Larger adds (an album) aren't checked: one
// check per song would trip Spotify's throttling, and a song that won't
// play is skipped with a notice when its turn comes.
//
// If the room has a repeat window, songs that are already waiting or
// playing, or started within the window, are left out. If that leaves
// nothing to add, it's a *RepeatError.
func (s *Service) Add(ctx context.Context, roomID, userID string, refs []TrackRef) (rooms.QueueSnapshot, error) {
	if len(refs) == 0 {
		return rooms.QueueSnapshot{}, &InvalidInputError{"add at least one song"}
	}
	if len(refs) > MaxAdd {
		return rooms.QueueSnapshot{}, &InvalidInputError{fmt.Sprintf("add at most %d songs at a time", MaxAdd)}
	}
	room, err := s.rooms.Get(ctx, roomID)
	if err != nil {
		return rooms.QueueSnapshot{}, err
	}
	// Look the tracks up before taking the room's lock: services can be slow.
	tracks, err := s.lookup(ctx, room, userID, refs)
	if err != nil {
		return rooms.QueueSnapshot{}, err
	}
	var added []provider.Track
	snap, err := s.Change(ctx, roomID, func(q *store.Queries, room store.Room) error {
		now := s.Now()
		tracks, err := s.withoutRepeats(ctx, q, room, tracks, now)
		if err != nil {
			return err
		}
		if err := guestLimit(ctx, q, room, userID, len(tracks)); err != nil {
			return err
		}
		added = tracks
		pos, err := q.NextLanePosition(ctx, store.NextLanePositionParams{RoomID: roomID, AddedBy: userID})
		if err != nil {
			return err
		}
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
	if err == nil && s.OnAdd != nil {
		s.OnAdd(added)
	}
	return snap, err
}

// guestLimit refuses n more songs from a guest who'd go over the room's
// limit. Members have no limit.
func guestLimit(ctx context.Context, q *store.Queries, room store.Room, userID string, n int) error {
	if _, err := q.GetGuest(ctx, userID); store.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	limit := rooms.ParseSettings(room.Settings).Guests.SongLimit()
	if limit == 0 {
		return nil
	}
	added, err := q.CountGuestSongs(ctx, store.CountGuestSongsParams{RoomID: room.ID, AddedBy: userID})
	if err != nil {
		return err
	}
	if left := max(limit-int(added), 0); n > left {
		return &GuestLimitError{Limit: limit, Left: left}
	}
	return nil
}

// AutopilotInfo says why autopilot chose a song. It's stored with the item.
type AutopilotInfo struct {
	// SeedItemID is the item whose song it's like, if any. A song autopilot
	// picked at random, to keep the music going, has none.
	SeedItemID string `json:"seedItemId,omitempty"`
	SeedTitle  string `json:"seedTitle,omitempty"`
	SeedArtist string `json:"seedArtist,omitempty"`
}

// ParseAutopilot reads an autopilot item's info. ok is false for a
// member's song.
func ParseAutopilot(it store.QueueItem) (info AutopilotInfo, ok bool) {
	if !it.IsAutopilot() {
		return info, false
	}
	_ = json.Unmarshal([]byte(it.Autopilot.String), &info)
	return info, true
}

// ErrNotDry is autopilot adding a song while members' songs, or another
// autopilot song, are already waiting.
var ErrNotDry = errors.New("the queue has songs waiting")

// AddAutopilot queues a song autopilot chose, for forUser (whose taste
// seeded it). It goes in nobody's lane and plays after every member's song.
// It's ErrNotDry if anything is already waiting, and a *RepeatError if the
// room's repeat guard refuses it.
func (s *Service) AddAutopilot(ctx context.Context, roomID, forUser string, t provider.Track, info AutopilotInfo) (rooms.QueueSnapshot, error) {
	meta, err := json.Marshal(t)
	if err != nil {
		return rooms.QueueSnapshot{}, err
	}
	why, err := json.Marshal(info)
	if err != nil {
		return rooms.QueueSnapshot{}, err
	}
	snap, err := s.Change(ctx, roomID, func(q *store.Queries, room store.Room) error {
		items, err := q.ListUpcoming(ctx, roomID)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(items, func(it store.QueueItem) bool { return it.State == store.ItemQueued }) {
			return ErrNotDry
		}
		now := s.Now()
		if _, err := s.withoutRepeats(ctx, q, room, []provider.Track{t}, now); err != nil {
			return err
		}
		_, err = q.AddQueueItem(ctx, store.AddQueueItemParams{
			ID: store.NewID(), RoomID: roomID, AddedBy: forUser,
			Provider: t.Ref.Provider, LinkID: sql.NullString{String: t.Ref.LinkID, Valid: true}, TrackID: t.Ref.ID,
			Metadata: string(meta), Autopilot: sql.NullString{String: string(why), Valid: true}, Now: now,
		})
		return err
	})
	if err == nil && s.OnAdd != nil {
		s.OnAdd([]provider.Track{t})
	}
	return snap, err
}

// withoutRepeats drops the songs the room's repeat guard refuses, including
// a song given twice. It's a *RepeatError if none are left.
func (s *Service) withoutRepeats(ctx context.Context, q *store.Queries, room store.Room, tracks []provider.Track, now time.Time) ([]provider.Track, error) {
	minutes := rooms.ParseSettings(room.Settings).Fairness.RepeatWindowMinutes
	if minutes == 0 {
		return tracks, nil
	}
	since := now.Add(-time.Duration(minutes) * time.Minute)
	seen := map[string]bool{}
	out := make([]provider.Track, 0, len(tracks))
	for _, t := range tracks {
		key := t.Ref.Provider + "\x00" + t.Ref.ID
		if seen[key] || (t.ISRC != "" && seen["isrc\x00"+t.ISRC]) {
			continue
		}
		seen[key] = true
		if t.ISRC != "" {
			seen["isrc\x00"+t.ISRC] = true
		}
		n, err := q.RecentDuplicates(ctx, store.RecentDuplicatesParams{
			RoomID: room.ID, Provider: t.Ref.Provider, TrackID: t.Ref.ID, Isrc: t.ISRC, Since: since,
		})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, &RepeatError{Title: tracks[0].Title, Minutes: minutes}
	}
	return out, nil
}

// lookup fetches each track's metadata, opening each link once. Songs
// queued again from an item reuse its snapshot.
func (s *Service) lookup(ctx context.Context, room store.Room, userID string, refs []TrackRef) ([]provider.Track, error) {
	sessions := map[string]provider.Session{}
	defer func() {
		for _, sess := range sessions {
			sess.Close()
		}
	}()
	out := make([]provider.Track, len(refs))
	for i, r := range refs {
		if r.FromItemID != "" {
			t, err := s.again(ctx, room, userID, r.FromItemID)
			if err != nil {
				return nil, err
			}
			out[i] = t
			continue
		}
		if r.LinkID == "" || r.TrackID == "" {
			return nil, &InvalidInputError{"each song needs a linkId and trackId, or a fromItemId"}
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

// again returns the song of one of the room's items, to queue it again.
func (s *Service) again(ctx context.Context, room store.Room, userID, itemID string) (provider.Track, error) {
	it, err := s.Item(ctx, room.ID, itemID)
	if err != nil {
		return provider.Track{}, err
	}
	var t provider.Track
	if err := json.Unmarshal([]byte(it.Metadata), &t); err != nil {
		return provider.Track{}, err
	}
	if !it.LinkID.Valid {
		return provider.Track{}, &InvalidInputError{"that song's service was unlinked"}
	}
	if !rooms.ParseSettings(room.Settings).Matching.Borrow {
		if _, err := s.tracks.GetUsable(ctx, userID, it.LinkID.String); err != nil {
			if errors.Is(err, links.ErrNotFound) {
				return provider.Track{}, ErrCantBorrow
			}
			return provider.Track{}, err
		}
	}
	t.Ref = provider.TrackRef{Provider: it.Provider, LinkID: it.LinkID.String, ID: it.TrackID}
	return t, nil
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
		if it.AddedBy != userID || it.IsAutopilot() {
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
// songs, and autopilot's; the room's owner can remove anyone's. The
// playing song isn't removed here: skipping it is the playback engine's job.
func (s *Service) Remove(ctx context.Context, roomID, userID, itemID string) (rooms.QueueSnapshot, error) {
	return s.Change(ctx, roomID, func(q *store.Queries, room store.Room) error {
		it, err := queuedItem(ctx, q, roomID, itemID)
		if err != nil {
			return err
		}
		if it.AddedBy != userID && room.OwnerID != userID && !it.IsAutopilot() {
			return ErrForbidden
		}
		return q.SetQueueItemState(ctx, store.SetQueueItemStateParams{State: store.ItemRemoved, UpdatedAt: s.Now(), ID: itemID})
	})
}

// GuestSongs is how many songs a guest has added to a room, toward its limit.
func (s *Service) GuestSongs(ctx context.Context, roomID, userID string) (int, error) {
	n, err := s.db.CountGuestSongs(ctx, store.CountGuestSongsParams{RoomID: roomID, AddedBy: userID})
	return int(n), err
}

// RemoveLane takes all of userID's waiting songs out of a room's queue,
// as when a guest leaves. Their playing song plays on.
func (s *Service) RemoveLane(ctx context.Context, roomID, userID string) error {
	_, err := s.Change(ctx, roomID, func(q *store.Queries, _ store.Room) error {
		lane, err := q.ListLane(ctx, store.ListLaneParams{RoomID: roomID, AddedBy: userID})
		if err != nil {
			return err
		}
		now := s.Now()
		for _, it := range lane {
			if err := q.SetQueueItemState(ctx, store.SetQueueItemStateParams{State: store.ItemRemoved, UpdatedAt: now, ID: it.ID}); err != nil {
				return err
			}
		}
		return nil
	})
	return err
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
