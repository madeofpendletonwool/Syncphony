// SPDX-License-Identifier: AGPL-3.0-only

// Package realtime is the server's event bus: publishers announce changes
// (a room's queue, now playing, presence, a user's link status) on topics,
// and WebSocket connections subscribed to those topics push them to
// clients.
//
// Bus is an interface so a multi-instance deployment could put Redis or
// NATS behind it. Local, the in-process implementation, is all a single
// server needs.
package realtime

import "sync"

// Event types pushed to clients.
const (
	// Hello is the first event on a connection: who's present.
	Hello = "hello"
	// QueueUpdated carries a full queue snapshot and its version.
	QueueUpdated = "queue.updated"
	// NowPlayingUpdated carries the room's playback state.
	NowPlayingUpdated = "nowplaying.updated"
	// PlaybackNotice carries something members should hear about, such as
	// a song skipped because its service failed.
	PlaybackNotice = "playback.notice"
	// MemberJoined and MemberLeft carry a user whose first connection to
	// the room opened, or whose last one closed.
	MemberJoined = "member.joined"
	MemberLeft   = "member.left"
	// RoomUpdated carries a room whose name or settings changed.
	RoomUpdated = "room.updated"
	// RoomDeleted carries rooms.Deleted: the room is gone. It's the room's
	// last event.
	RoomDeleted = "room.deleted"
	// LinkStatus carries one of the user's service links whose status
	// changed. Sent on the user's topic, so it reaches every room they're in.
	LinkStatus = "link.status"
	// ReactionSent carries an emoji someone sent to the room's big screen.
	ReactionSent = "reaction.sent"
	// HeartsUpdated carries who hearted a song, after someone hearted it
	// or took it back.
	HeartsUpdated = "hearts.updated"
	// NightEnded carries a night that ended, and its song of the night.
	NightEnded = "night.ended"
	// GuestsUpdated says a room's guests or guest pass changed. It
	// carries the room's ID.
	GuestsUpdated = "guests.updated"
	// MembersUpdated carries rooms.MembersChanged: someone joined a room
	// that isn't open, asked to, or left or was removed.
	MembersUpdated = "members.updated"
	// GameRound carries a game round whenever it changes: announced, open
	// for answers (and as answers come in), revealed, done.
	GameRound = "game.round"
	// GameScores carries the night's game scores, after each reveal.
	GameScores = "game.scores"
	// GameQueue carries a queue game (connect the artists, a theme round,
	// a bracket) whenever it changes.
	GameQueue = "game.queue"
)

// Event is something that happened. Data holds domain values (store rows,
// snapshots); the WebSocket layer turns them into API types.
type Event struct {
	Type string
	// Version orders queue snapshots. 0 for other events.
	Version int64
	Data    any
}

// RoomTopic is the topic for events in a room.
func RoomTopic(roomID string) string { return "room:" + roomID }

// UserTopic is the topic for events for one user, wherever they are.
func UserTopic(userID string) string { return "user:" + userID }

// Bus delivers events to subscribers of a topic.
type Bus interface {
	// Publish delivers e to the topic's current subscribers. It never
	// blocks on slow subscribers.
	Publish(topic string, e Event)
	// Subscribe returns a subscription to the given topics.
	Subscribe(topics ...string) *Subscription
}

// subscriptionBuffer is how many events a subscriber may fall behind by
// before it's cut off and has to resync.
const subscriptionBuffer = 64

// Subscription receives events until it's closed.
type Subscription struct {
	// C delivers events. It's closed when the subscription ends: by Close,
	// or because the subscriber fell too far behind (see Lagged).
	C <-chan Event

	c      chan Event
	topics []string
	bus    *Local
	lagged bool // guarded by bus.mu
	closed bool // guarded by bus.mu
}

// Lagged reports whether the subscription was cut off for falling behind.
// The subscriber missed events and should resync from a snapshot.
func (s *Subscription) Lagged() bool {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	return s.lagged
}

// Close ends the subscription.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	s.bus.remove(s)
}

// Local is an in-process Bus.
type Local struct {
	mu   sync.Mutex
	subs map[string]map[*Subscription]struct{}
}

var _ Bus = (*Local)(nil)

// NewLocal returns an empty in-process bus.
func NewLocal() *Local { return &Local{subs: map[string]map[*Subscription]struct{}{}} }

// Subscribe implements Bus.
func (b *Local) Subscribe(topics ...string) *Subscription {
	c := make(chan Event, subscriptionBuffer)
	s := &Subscription{C: c, c: c, topics: topics, bus: b}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range topics {
		if b.subs[t] == nil {
			b.subs[t] = map[*Subscription]struct{}{}
		}
		b.subs[t][s] = struct{}{}
	}
	return s
}

// Publish implements Bus.
func (b *Local) Publish(topic string, e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs[topic] {
		select {
		case s.c <- e:
		default:
			s.lagged = true
			b.remove(s)
		}
	}
}

// remove unsubscribes s and closes its channel. The caller holds mu.
func (b *Local) remove(s *Subscription) {
	if s.closed {
		return
	}
	s.closed = true
	for _, t := range s.topics {
		delete(b.subs[t], s)
		if len(b.subs[t]) == 0 {
			delete(b.subs, t)
		}
	}
	close(s.c)
}

// Presence tracks who's connected to each room. A user can have several
// connections (phone and laptop); they're present while any is open.
type Presence struct {
	mu    sync.Mutex
	rooms map[string]map[string]int // room -> user -> open connections
}

// NewPresence returns an empty Presence.
func NewPresence() *Presence { return &Presence{rooms: map[string]map[string]int{}} }

// Join records a connection and reports whether it's the user's first in
// the room.
func (p *Presence) Join(roomID, userID string) (first bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rooms[roomID] == nil {
		p.rooms[roomID] = map[string]int{}
	}
	p.rooms[roomID][userID]++
	return p.rooms[roomID][userID] == 1
}

// Leave records a closed connection and reports whether it was the user's
// last in the room.
func (p *Presence) Leave(roomID, userID string) (last bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	users := p.rooms[roomID]
	if users[userID] == 0 {
		return false
	}
	users[userID]--
	if users[userID] > 0 {
		return false
	}
	delete(users, userID)
	if len(users) == 0 {
		delete(p.rooms, roomID)
	}
	return true
}

// Members returns the IDs of users present in a room.
func (p *Presence) Members(roomID string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]string, 0, len(p.rooms[roomID]))
	for id := range p.rooms[roomID] {
		ids = append(ids, id)
	}
	return ids
}
