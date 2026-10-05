// SPDX-License-Identifier: AGPL-3.0-only

// Package playback is the server-authoritative player for each room: what
// is playing, whether it's paused, where it is, and which device is the
// room's speaker. See docs/adr/0003-playback-engine.md.
//
// Each room is a small state machine:
//
//	idle ──► loading ──► playing ◄──► paused
//	  ▲                     │
//	  └──── (queue empty) ◄─┴─► ended ──► loading (next song)
//
// "ended" is momentary: the engine ends the song and starts the next one
// in the same step, so it is never published.
//
// Songs play through one of two drivers, chosen by the song's service:
//
//   - Stream: the player device loads the song from the stream endpoint
//     and reports back (started, progress, ended, error).
//   - Remote: the engine tells the service's own player what to do and
//     polls its state.
//
// A room only plays while a device has claimed it as the speaker.
// Failures skip the song with a notice instead of stalling the room.
package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/transcode"
)

// Errors returned by Engine, besides rooms.ErrNotFound.
var (
	ErrForbidden      = errors.New("you can't control playback in this room")
	ErrNoPlayer       = errors.New("nothing is playing this room; become the speaker first")
	ErrNotPlayer      = errors.New("this device isn't the room's speaker")
	ErrNothingPlaying = errors.New("nothing is playing")
	ErrNotStreamable  = errors.New("that song isn't playing or up next, or can't be streamed")
)

// InvalidInputError is a bad command or report.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

// Playback states.
const (
	StateIdle    = "idle"
	StateLoading = "loading"
	StatePlaying = "playing"
	StatePaused  = "paused"
)

// Sessions opens provider sessions for links. It's links.Service in
// production. Songs play through the link of whoever queued them.
type Sessions interface {
	Open(ctx context.Context, linkID string) (provider.Session, error)
}

// Config tunes the engine. Zero values take the defaults.
type Config struct {
	// LoadTimeout is how long a streamed song may take to start before
	// it's skipped. Default 20s.
	LoadTimeout time.Duration
	// PlayerTimeout is how long the speaker may go without reporting
	// before the room pauses. Mobile browsers throttle background pages,
	// so this is generous. Default 2m.
	PlayerTimeout time.Duration
	// EndGrace is how far past a streamed song's end the engine waits for
	// the player's "ended" before moving on anyway. Default 15s.
	EndGrace time.Duration
	// RemotePoll is how often a remote player's state is read. Default 2s.
	RemotePoll time.Duration
	// MaxErrors is how many songs in a row may fail before the room stops
	// trying. Default 3.
	MaxErrors int
	// Transcoder converts formats the player can't decode. Nil disables
	// transcoding.
	Transcoder transcode.Transcoder
	// Now is the clock. Default store.Now.
	Now func() time.Time
}

// Engine runs playback for every room.
type Engine struct {
	cfg      Config
	db       *store.Store
	rooms    *rooms.Service
	queue    *queue.Service
	sessions Sessions

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	closed bool
	byID   map[string]*room
}

// New returns an Engine and registers it for queue changes, so a room with
// a speaker starts playing when songs arrive. Call Run to start its timers
// and Close when done.
func New(db *store.Store, rs *rooms.Service, qs *queue.Service, sessions Sessions, cfg Config) *Engine {
	if cfg.LoadTimeout == 0 {
		cfg.LoadTimeout = 20 * time.Second
	}
	if cfg.PlayerTimeout == 0 {
		cfg.PlayerTimeout = 2 * time.Minute
	}
	if cfg.EndGrace == 0 {
		cfg.EndGrace = 15 * time.Second
	}
	if cfg.RemotePoll == 0 {
		cfg.RemotePoll = 2 * time.Second
	}
	if cfg.MaxErrors == 0 {
		cfg.MaxErrors = 3
	}
	if cfg.Now == nil {
		cfg.Now = store.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{cfg: cfg, db: db, rooms: rs, queue: qs, sessions: sessions, ctx: ctx, cancel: cancel, byID: map[string]*room{}}
	qs.OnChange = e.queueChanged
	return e
}

// Run drives the engine's timers (load and player timeouts, remote
// polling) until ctx is done.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Tick(ctx)
		}
	}
}

// Close stops background work and closes remote sessions.
func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	all := make([]*room, 0, len(e.byID))
	for _, r := range e.byID {
		all = append(all, r)
	}
	e.mu.Unlock()
	e.cancel()
	e.wg.Wait()
	for _, r := range all {
		r.mu.Lock()
		r.closeRemote()
		r.mu.Unlock()
	}
}

// room is one room's playback state. mu guards everything, and is held
// across provider calls so a room's changes never interleave.
type room struct {
	mu     sync.Mutex
	id     string
	loaded bool
	np     rooms.NowPlaying
	// errors counts songs in a row that failed to play.
	errors int
	// halted means the room stopped after too many failures; it waits for
	// someone to press play instead of starting songs as they arrive.
	halted bool
	// since is when the current state began (for the load timeout).
	since time.Time
	// The remote driver's open session, while a remote song is current.
	sess     provider.Session
	remote   provider.Remote
	lastPoll time.Time
}

func (r *room) closeRemote() {
	if r.sess != nil {
		r.sess.Close()
	}
	r.sess, r.remote = nil, nil
}

// lock returns the room's state, locked and loaded.
func (e *Engine) lock(ctx context.Context, id string) (*room, error) {
	e.mu.Lock()
	r, ok := e.byID[id]
	if !ok {
		r = &room{id: id}
		e.byID[id] = r
	}
	e.mu.Unlock()
	r.mu.Lock()
	if !r.loaded {
		if err := e.load(ctx, r); err != nil {
			r.mu.Unlock()
			return nil, err
		}
	}
	return r, nil
}

// load reads a room's state after startup. A song that was playing comes
// back paused at its start: nobody knows how far the old speaker got, and
// the speaker has to claim the room again anyway.
func (e *Engine) load(ctx context.Context, r *room) error {
	row, err := e.rooms.Get(ctx, r.id)
	if err != nil {
		return err
	}
	now := e.cfg.Now()
	r.np = rooms.NowPlaying{RoomID: r.id, State: StateIdle, At: now}
	if row.PlayerDeviceID.Valid {
		if err := e.db.SetRoomPlayer(ctx, store.SetRoomPlayerParams{ID: r.id}); err != nil {
			return err
		}
	}
	it, err := e.db.GetPlaying(ctx, r.id)
	if err != nil && !store.IsNotFound(err) {
		return err
	}
	if err == nil {
		r.np.Item, r.np.State = &it, StatePaused
		r.np.Driver = e.driverOf(ctx, it)
	}
	snap, err := e.rooms.QueueSnapshot(ctx, r.id)
	if err != nil {
		return err
	}
	r.np.Next = nextOf(snap)
	r.loaded = true
	return nil
}

// driverOf works out how an item plays, without starting it. Unknown (its
// link is gone, say) reads as stream; starting it will fail and skip it.
func (e *Engine) driverOf(ctx context.Context, it store.QueueItem) string {
	if !it.LinkID.Valid {
		return string(provider.PlaybackStream)
	}
	sess, err := e.sessions.Open(ctx, it.LinkID.String)
	if err != nil {
		return string(provider.PlaybackStream)
	}
	defer sess.Close()
	if _, ok := sess.(provider.Remote); ok {
		return string(provider.PlaybackRemote)
	}
	return string(provider.PlaybackStream)
}

func nextOf(snap rooms.QueueSnapshot) *store.QueueItem {
	if len(snap.UpNext) == 0 {
		return nil
	}
	i := slices.IndexFunc(snap.Items, func(it store.QueueItem) bool { return it.ID == snap.UpNext[0] })
	if i < 0 {
		return nil
	}
	return &snap.Items[i]
}

func duration(it *store.QueueItem) time.Duration {
	var t provider.Track
	if err := json.Unmarshal([]byte(it.Metadata), &t); err != nil {
		return 0
	}
	return t.Duration
}

// position is where playback is at now.
func (r *room) position(now time.Time) time.Duration {
	pos := r.np.Position
	if r.np.State == StatePlaying {
		pos += now.Sub(r.np.At)
	}
	if r.np.Item != nil {
		if d := duration(r.np.Item); d > 0 && pos > d {
			pos = d
		}
	}
	return max(pos, 0)
}

// view is the room's state as of now, safe to hand out.
func (r *room) view(now time.Time) rooms.NowPlaying {
	np := r.np
	np.Position, np.At = r.position(now), now
	if np.Player != nil {
		p := *np.Player
		np.Player = &p
	}
	return np
}

func (e *Engine) publish(r *room) { e.rooms.PublishNowPlaying(r.view(e.cfg.Now())) }

func (e *Engine) notice(r *room, itemID, msg string) {
	slog.Info("playback notice", "room", r.id, "item", itemID, "msg", msg)
	e.rooms.PublishNotice(rooms.Notice{RoomID: r.id, ItemID: itemID, Message: msg})
}

// NowPlaying returns a room's playback state.
func (e *Engine) NowPlaying(ctx context.Context, roomID string) (rooms.NowPlaying, error) {
	r, err := e.lock(ctx, roomID)
	if err != nil {
		return rooms.NowPlaying{}, err
	}
	defer r.mu.Unlock()
	return r.view(e.cfg.Now()), nil
}

// Claim makes a device the room's speaker, taking over from any other.
// If the room is idle and songs are waiting, the first one starts.
func (e *Engine) Claim(ctx context.Context, roomID, userID, deviceID, name string) (rooms.NowPlaying, error) {
	if deviceID == "" || len(deviceID) > 128 || len(name) > 64 {
		return rooms.NowPlaying{}, &InvalidInputError{"a device needs an ID of up to 128 characters and a name of up to 64"}
	}
	row, err := e.rooms.Get(ctx, roomID)
	if err != nil {
		return rooms.NowPlaying{}, err
	}
	r, err := e.lock(ctx, roomID)
	if err != nil {
		return rooms.NowPlaying{}, err
	}
	defer r.mu.Unlock()
	same := r.np.Player != nil && r.np.Player.DeviceID == deviceID && r.np.Player.UserID == userID
	if !same && !rooms.CanControl(row, userID) {
		return rooms.NowPlaying{}, ErrForbidden
	}
	now := e.cfg.Now()
	if err := e.db.SetRoomPlayer(ctx, store.SetRoomPlayerParams{PlayerDeviceID: nullString(deviceID), ID: roomID}); err != nil {
		return rooms.NowPlaying{}, err
	}
	r.np.Player = &rooms.Player{DeviceID: deviceID, UserID: userID, Name: name, LastSeen: now}
	if !same {
		// A new speaker has to load the song.
		r.np.Position, r.np.At = r.position(now), now
		r.np.Revision++
		if r.np.State == StatePlaying && r.np.Driver == string(provider.PlaybackStream) {
			r.np.State, r.since = StateLoading, now
		}
	}
	if r.np.State == StateIdle && !r.halted {
		if err := e.next(ctx, r, ""); err != nil {
			return rooms.NowPlaying{}, err
		}
	} else {
		e.publish(r)
	}
	return r.view(now), nil
}

// Release stops a device being the speaker. The current song pauses. The
// speaker's own user or the room's owner may release it.
func (e *Engine) Release(ctx context.Context, roomID, userID, deviceID string) (rooms.NowPlaying, error) {
	row, err := e.rooms.Get(ctx, roomID)
	if err != nil {
		return rooms.NowPlaying{}, err
	}
	r, err := e.lock(ctx, roomID)
	if err != nil {
		return rooms.NowPlaying{}, err
	}
	defer r.mu.Unlock()
	p := r.np.Player
	if p == nil || p.DeviceID != deviceID {
		return rooms.NowPlaying{}, ErrNotPlayer
	}
	if p.UserID != userID && row.OwnerID != userID {
		return rooms.NowPlaying{}, ErrForbidden
	}
	if err := e.db.SetRoomPlayer(ctx, store.SetRoomPlayerParams{ID: roomID}); err != nil {
		return rooms.NowPlaying{}, err
	}
	r.np.Player = nil
	if r.np.State == StatePlaying || r.np.State == StateLoading {
		if err := e.pause(ctx, r); err != nil {
			// The speaker is leaving either way; stop the room's clock.
			slog.Warn("playback: pausing the remote player on release", "room", roomID, "err", err)
			now := e.cfg.Now()
			r.closeRemote()
			r.np.Position, r.np.At, r.np.State = r.position(now), now, StatePaused
			r.np.Revision++
		}
	}
	e.publish(r)
	return r.view(e.cfg.Now()), nil
}

// Command actions.
const (
	ActionPlay  = "play"
	ActionPause = "pause"
	ActionSkip  = "skip"
	ActionSeek  = "seek"
)

// Command is a member's request to control playback.
type Command struct {
	Action string
	// Position is where to seek to.
	Position time.Duration
	// ItemID, if set, makes a skip apply only while that item is current,
	// so two people tapping skip at once skip one song, not two.
	ItemID string
}

// Command controls playback.
func (e *Engine) Command(ctx context.Context, roomID, userID string, c Command) (rooms.NowPlaying, error) {
	row, err := e.rooms.Get(ctx, roomID)
	if err != nil {
		return rooms.NowPlaying{}, err
	}
	r, err := e.lock(ctx, roomID)
	if err != nil {
		return rooms.NowPlaying{}, err
	}
	defer r.mu.Unlock()
	allowed := rooms.CanControl(row, userID) ||
		(c.Action == ActionSkip && r.np.Item != nil && r.np.Item.AddedBy == userID)
	if !allowed {
		return rooms.NowPlaying{}, ErrForbidden
	}
	now := e.cfg.Now()
	switch c.Action {
	case ActionPlay:
		err = e.play(ctx, r)
	case ActionPause:
		if r.np.Item == nil {
			return rooms.NowPlaying{}, ErrNothingPlaying
		}
		if r.np.State == StatePlaying || r.np.State == StateLoading {
			err = e.pause(ctx, r)
			e.publish(r)
		}
	case ActionSkip:
		if r.np.Item == nil {
			return rooms.NowPlaying{}, ErrNothingPlaying
		}
		if c.ItemID == "" || c.ItemID == r.np.Item.ID {
			err = e.next(ctx, r, store.EndSkipped)
		}
	case ActionSeek:
		if r.np.Item == nil {
			return rooms.NowPlaying{}, ErrNothingPlaying
		}
		to := max(c.Position, 0)
		if d := duration(r.np.Item); d > 0 {
			to = min(to, d)
		}
		if r.remote != nil {
			if err := r.remote.Seek(ctx, to); err != nil {
				return rooms.NowPlaying{}, err
			}
		}
		r.np.Position, r.np.At = to, now
		r.np.Revision++
		e.publish(r)
	default:
		return rooms.NowPlaying{}, &InvalidInputError{"action is play, pause, skip or seek"}
	}
	if err != nil {
		return rooms.NowPlaying{}, err
	}
	return r.view(e.cfg.Now()), nil
}

func (e *Engine) play(ctx context.Context, r *room) error {
	if r.np.Player == nil {
		return ErrNoPlayer
	}
	if r.np.Item == nil || r.halted {
		r.halted = false
		return e.next(ctx, r, store.EndSkipped)
	}
	if r.np.State != StatePaused {
		return nil
	}
	now := e.cfg.Now()
	if r.np.Driver == string(provider.PlaybackRemote) {
		var err error
		if r.remote != nil {
			err = r.remote.Resume(ctx)
		} else {
			// After a restart there's no session; start the song again where it was.
			err = e.startRemote(ctx, r, *r.np.Item, r.np.Position)
		}
		if err != nil {
			return e.failed(ctx, r, fmt.Sprintf("Couldn't resume %s", title(r.np.Item)), err)
		}
	}
	r.np.State, r.np.At, r.since = StatePlaying, now, now
	r.np.Revision++
	e.publish(r)
	return nil
}

// pause pauses the current song where it is. The caller publishes.
func (e *Engine) pause(ctx context.Context, r *room) error {
	now := e.cfg.Now()
	if r.remote != nil {
		if err := r.remote.Pause(ctx); err != nil {
			return err
		}
	}
	r.np.Position, r.np.At = r.position(now), now
	r.np.State = StatePaused
	r.np.Revision++
	return nil
}

// Report events, sent by the speaker about the song it's playing.
const (
	EventPlaying  = "playing"
	EventProgress = "progress"
	EventPaused   = "paused"
	EventEnded    = "ended"
	EventError    = "error"
)

// Report is what the speaker tells the server about a streamed song.
type Report struct {
	DeviceID string
	ItemID   string
	Event    string
	Position time.Duration
	// Error describes an EventError, for the notice.
	Error string
}

// Report applies a speaker's report. Reports about anything but the
// current streamed song are stale and ignored; the reply carries the
// current state so the speaker can catch up.
func (e *Engine) Report(ctx context.Context, roomID, userID string, rep Report) (rooms.NowPlaying, error) {
	r, err := e.lock(ctx, roomID)
	if err != nil {
		return rooms.NowPlaying{}, err
	}
	defer r.mu.Unlock()
	p := r.np.Player
	if p == nil || p.DeviceID != rep.DeviceID || p.UserID != userID {
		return rooms.NowPlaying{}, ErrNotPlayer
	}
	now := e.cfg.Now()
	p.LastSeen = now
	if r.np.Item == nil || r.np.Item.ID != rep.ItemID || r.np.Driver != string(provider.PlaybackStream) {
		return r.view(now), nil
	}
	switch rep.Event {
	case EventPlaying:
		if r.np.State == StateLoading || r.np.State == StatePlaying {
			changed := r.np.State == StateLoading
			r.np.State, r.np.Position, r.np.At = StatePlaying, rep.Position, now
			r.errors = 0
			if changed {
				e.publish(r)
			}
		}
	case EventProgress:
		if r.np.State == StatePlaying {
			r.np.Position, r.np.At = rep.Position, now
		}
	case EventPaused:
		if r.np.State == StatePlaying {
			r.np.State, r.np.Position, r.np.At = StatePaused, rep.Position, now
			e.publish(r)
		}
	case EventEnded:
		err = e.next(ctx, r, store.EndFinished)
	case EventError:
		cause := rep.Error
		if cause == "" {
			cause = "the speaker reported an error"
		}
		err = e.failed(ctx, r, fmt.Sprintf("Couldn't play %s", title(r.np.Item)), errors.New(cause))
	default:
		return rooms.NowPlaying{}, &InvalidInputError{"event is playing, progress, paused, ended or error"}
	}
	if err != nil {
		return rooms.NowPlaying{}, err
	}
	return r.view(e.cfg.Now()), nil
}

// next ends the current song for reason and starts the next one, skipping
// songs that fail to start. With no speaker, or after too many failures,
// the room goes idle instead. The caller holds r.mu.
func (e *Engine) next(ctx context.Context, r *room, reason string) error {
	for {
		start := r.np.Player != nil && !r.halted
		item, snap, err := e.rotate(ctx, r, reason, start)
		if err != nil {
			return err
		}
		now := e.cfg.Now()
		r.np.Item, r.np.Position, r.np.At, r.since = item, 0, now, now
		r.np.Next = nextOf(snap)
		r.np.Revision++
		if item == nil {
			r.np.State, r.np.Driver = StateIdle, ""
			e.publish(r)
			return nil
		}
		cause := e.begin(ctx, r, *item)
		if cause == nil {
			e.publish(r)
			return nil
		}
		e.countFailure(r, item, fmt.Sprintf("Skipped %s: it couldn't start", title(item)), cause)
		reason = store.EndError
	}
}

// failed skips the current song after it failed to play.
func (e *Engine) failed(ctx context.Context, r *room, msg string, cause error) error {
	e.countFailure(r, r.np.Item, msg, cause)
	return e.next(ctx, r, store.EndError)
}

func (e *Engine) countFailure(r *room, item *store.QueueItem, msg string, cause error) {
	slog.Warn("playback failed", "room", r.id, "item", item.ID, "err", cause)
	e.notice(r, item.ID, msg)
	r.errors++
	if r.errors >= e.cfg.MaxErrors {
		r.halted, r.errors = true, 0
		e.notice(r, "", fmt.Sprintf("Stopped after %d songs in a row couldn't play. Press play to try again.", e.cfg.MaxErrors))
	}
}

// rotate ends the playing song with reason and, if start, makes the next
// song in fair order the playing one. It returns that song (nil if none)
// and the queue as it now stands.
func (e *Engine) rotate(ctx context.Context, r *room, reason string, start bool) (*store.QueueItem, rooms.QueueSnapshot, error) {
	r.closeRemote()
	var next *store.QueueItem
	snap, err := e.queue.Change(ctx, r.id, func(q *store.Queries, _ store.Room) error {
		now := e.cfg.Now()
		if cur, err := q.GetPlaying(ctx, r.id); err == nil {
			state := store.ItemPlayed
			if reason != store.EndFinished {
				state = store.ItemSkipped
			}
			if err := q.SetQueueItemState(ctx, store.SetQueueItemStateParams{State: state, UpdatedAt: now, ID: cur.ID}); err != nil {
				return err
			}
			if err := q.EndOpenPlays(ctx, store.EndOpenPlaysParams{EndedAt: nullTime(now), EndReason: nullString(reason), RoomID: r.id}); err != nil {
				return err
			}
		} else if !store.IsNotFound(err) {
			return err
		}
		if !start {
			return nil
		}
		snap, err := rooms.SnapshotTx(ctx, q, r.id)
		if err != nil {
			return err
		}
		it := nextOf(snap)
		if it == nil {
			return nil
		}
		if err := q.SetQueueItemState(ctx, store.SetQueueItemStateParams{State: store.ItemPlaying, UpdatedAt: now, ID: it.ID}); err != nil {
			return err
		}
		if _, err := q.StartPlay(ctx, store.StartPlayParams{ID: store.NewID(), RoomID: r.id, QueueItemID: it.ID, StartedAt: now}); err != nil {
			return err
		}
		it.State = store.ItemPlaying
		next = it
		return nil
	})
	return next, snap, err
}

// begin starts an item that rotate made current. Streamed songs wait for
// the speaker to load them; remote songs start on the service's player.
func (e *Engine) begin(ctx context.Context, r *room, it store.QueueItem) error {
	if !it.LinkID.Valid {
		return errors.New("its service was unlinked")
	}
	sess, err := e.sessions.Open(ctx, it.LinkID.String)
	if err != nil {
		return err
	}
	if _, ok := sess.(provider.Remote); ok {
		sess.Close()
		r.np.Driver = string(provider.PlaybackRemote)
		if err := e.startRemote(ctx, r, it, 0); err != nil {
			return err
		}
		r.np.State = StatePlaying
		r.errors = 0
		return nil
	}
	_, streams := sess.(provider.Streamer)
	sess.Close()
	if !streams {
		return errors.New("its service can't play songs")
	}
	r.np.Driver = string(provider.PlaybackStream)
	r.np.State = StateLoading
	return nil
}

// startRemote opens a session for a remote song and plays it from at. The
// session stays open while the song is current: it's the remote's handle.
func (e *Engine) startRemote(ctx context.Context, r *room, it store.QueueItem, at time.Duration) error {
	sess, err := e.sessions.Open(ctx, it.LinkID.String)
	if err != nil {
		return err
	}
	rem, ok := sess.(provider.Remote)
	if !ok {
		sess.Close()
		return errors.New("its service can't play remotely")
	}
	if err := rem.Play(ctx, it.TrackID, at); err != nil {
		sess.Close()
		return err
	}
	r.closeRemote()
	r.sess, r.remote, r.lastPoll = sess, rem, e.cfg.Now()
	return nil
}

// queueChanged runs after any queue change. It may be called while the
// engine holds a room's lock (the engine changes the queue too), so the
// work happens on another goroutine.
func (e *Engine) queueChanged(roomID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	r, ok := e.byID[roomID]
	if !ok {
		return // never loaded, so nobody has claimed it as speaker
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		r.mu.Lock()
		defer r.mu.Unlock()
		if err := e.refresh(e.ctx, r); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("playback: reacting to queue change", "room", roomID, "err", err)
		}
	}()
}

// refresh starts an idle room when songs arrive, and keeps the preloaded
// next song current as the order changes.
func (e *Engine) refresh(ctx context.Context, r *room) error {
	if !r.loaded {
		return nil
	}
	if r.np.State == StateIdle && r.np.Player != nil && !r.halted {
		return e.next(ctx, r, "")
	}
	snap, err := e.rooms.QueueSnapshot(ctx, r.id)
	if err != nil {
		return err
	}
	next := nextOf(snap)
	if idOf(next) != idOf(r.np.Next) {
		r.np.Next = next
		e.publish(r)
	}
	return nil
}

func idOf(it *store.QueueItem) string {
	if it == nil {
		return ""
	}
	return it.ID
}

// Tick runs the engine's timers once: load timeouts, a silent speaker,
// streamed songs whose end was never reported, and remote polling. Run
// calls it every second.
func (e *Engine) Tick(ctx context.Context) {
	e.mu.Lock()
	all := make([]*room, 0, len(e.byID))
	for _, r := range e.byID {
		all = append(all, r)
	}
	e.mu.Unlock()
	for _, r := range all {
		r.mu.Lock()
		if err := e.tick(ctx, r); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("playback: tick", "room", r.id, "err", err)
		}
		r.mu.Unlock()
	}
}

func (e *Engine) tick(ctx context.Context, r *room) error {
	if !r.loaded || r.np.Item == nil {
		return nil
	}
	now := e.cfg.Now()
	switch r.np.Driver {
	case string(provider.PlaybackStream):
		switch r.np.State {
		case StateLoading:
			if now.Sub(r.since) >= e.cfg.LoadTimeout {
				return e.failed(ctx, r, fmt.Sprintf("Skipped %s: it took too long to start", title(r.np.Item)), errors.New("load timeout"))
			}
		case StatePlaying:
			if p := r.np.Player; p != nil && now.Sub(p.LastSeen) >= e.cfg.PlayerTimeout {
				if err := e.pause(ctx, r); err != nil {
					return err
				}
				e.notice(r, r.np.Item.ID, fmt.Sprintf("Paused: lost touch with %s", playerName(p)))
				e.publish(r)
				return nil
			}
			if d := duration(r.np.Item); d > 0 && r.np.Position+now.Sub(r.np.At) >= d+e.cfg.EndGrace {
				return e.next(ctx, r, store.EndFinished)
			}
		}
	case string(provider.PlaybackRemote):
		if r.np.State == StatePlaying && r.remote != nil && now.Sub(r.lastPoll) >= e.cfg.RemotePoll {
			return e.poll(ctx, r, now)
		}
	}
	return nil
}

// poll reads a remote player's state and follows it: the song ended, or
// someone paused or resumed it on the service's own app.
func (e *Engine) poll(ctx context.Context, r *room, now time.Time) error {
	r.lastPoll = now
	st, err := r.remote.State(ctx)
	if err != nil {
		return e.failed(ctx, r, fmt.Sprintf("Skipped %s: lost touch with its player", title(r.np.Item)), err)
	}
	d := duration(r.np.Item)
	// Some services report the old track for a moment after a change.
	settled := now.Sub(r.since) > 5*time.Second
	switch {
	case st.TrackID != r.np.Item.TrackID && settled:
		return e.next(ctx, r, store.EndFinished)
	case st.TrackID != r.np.Item.TrackID:
	case !st.Playing && d > 0 && st.Position >= d-2*time.Second:
		return e.next(ctx, r, store.EndFinished)
	case !st.Playing:
		r.np.State, r.np.Position, r.np.At = StatePaused, st.Position, now
		r.np.Revision++
		e.publish(r)
	default:
		r.np.Position, r.np.At = st.Position+now.Sub(st.At), now
	}
	return nil
}

// Stream opens a song's audio for the speaker. Only the playing song and
// queued songs (for preloading) can be streamed. The session closes with
// the stream's body.
func (e *Engine) Stream(ctx context.Context, roomID, itemID string, opts provider.StreamOpts) (*provider.AudioStream, error) {
	it, err := e.db.GetQueueItem(ctx, itemID)
	if store.IsNotFound(err) || (err == nil && it.RoomID != roomID) {
		return nil, queue.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if (it.State != store.ItemPlaying && it.State != store.ItemQueued) || !it.LinkID.Valid {
		return nil, ErrNotStreamable
	}
	sess, err := e.sessions.Open(ctx, it.LinkID.String)
	if err != nil {
		return nil, err
	}
	s, ok := sess.(provider.Streamer)
	if !ok {
		sess.Close()
		return nil, ErrNotStreamable
	}
	a, err := transcode.Stream(ctx, s, it.TrackID, opts, e.cfg.Transcoder)
	if err != nil {
		sess.Close()
		return nil, err
	}
	a.Body = &closeBoth{ReadCloser: a.Body, also: sess}
	return a, nil
}

// closeBoth closes a stream's body and then the session it came from.
type closeBoth struct {
	io.ReadCloser
	also io.Closer
}

func (c *closeBoth) Close() error {
	err := c.ReadCloser.Close()
	c.also.Close()
	return err
}

func title(it *store.QueueItem) string {
	var t provider.Track
	if json.Unmarshal([]byte(it.Metadata), &t) != nil || t.Title == "" {
		return "a song"
	}
	return "“" + t.Title + "”"
}

func playerName(p *rooms.Player) string {
	if p.Name == "" {
		return "the speaker"
	}
	return p.Name
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func nullTime(t time.Time) sql.NullTime { return sql.NullTime{Time: t, Valid: true} }
