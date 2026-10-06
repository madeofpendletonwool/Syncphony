// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/madeofpendletonwool/syncphony/server/internal/palette"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// WebSocket timings and close codes. See RoomEvent in api/openapi.yaml.
const (
	defaultPingEvery = 30 * time.Second
	wsWriteTimeout   = 10 * time.Second
	// closeSessionEnded tells the client to sign in again.
	closeSessionEnded websocket.StatusCode = 4001
)

// RoomSocket serves GET /ws/rooms/{id}: a server-push stream of a room's
// events (RoomEvent in the spec).
func (s *Server) RoomSocket() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var token string
		if c, err := r.Cookie(SessionCookie); err == nil {
			token = c.Value
		}
		sess, err := s.Auth.Authenticate(ctx, token)
		if err != nil {
			writeError(w, r, err)
			return
		}
		room, err := s.Rooms.Get(ctx, r.PathValue("id"))
		if errors.Is(err, rooms.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not_found", "room not found")
			return
		} else if err != nil {
			writeError(w, r, err)
			return
		}
		since := int64(-1)
		if v := r.URL.Query().Get("since"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				since = n
			}
		}
		opts := &websocket.AcceptOptions{}
		if u, err := url.Parse(s.BaseURL); err == nil {
			opts.OriginPatterns = []string{u.Host}
		}
		c, err := websocket.Accept(w, r, opts) // rejects foreign origins with 403
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		(&roomConn{s: s, c: c, user: sess.User, token: token, room: room, sent: since}).serve(ctx)
	})
}

// roomConn is one client's connection to a room.
type roomConn struct {
	s     *Server
	c     *websocket.Conn
	user  store.User
	token string
	room  store.Room
	// sent is the last queue version the client has.
	sent int64
}

func (rc *roomConn) serve(ctx context.Context) {
	s, roomID, userID := rc.s, rc.room.ID, rc.user.ID
	// Server push only: reading just handles pings and close frames, and
	// ctx ends when the client goes away.
	ctx = rc.c.CloseRead(ctx)

	// Subscribe before reading snapshots, so nothing falls in between.
	sub := s.Bus.Subscribe(realtime.RoomTopic(roomID), realtime.UserTopic(userID))
	defer sub.Close()

	if s.Presence.Join(roomID, userID) {
		s.Bus.Publish(realtime.RoomTopic(roomID), realtime.Event{Type: realtime.MemberJoined, Data: rc.user})
		s.Playback.MembersChanged(roomID)
	}
	defer func() {
		if s.Presence.Leave(roomID, userID) {
			s.Bus.Publish(realtime.RoomTopic(roomID), realtime.Event{Type: realtime.MemberLeft, Data: rc.user})
			s.Playback.MembersChanged(roomID)
		}
	}()

	if err := rc.hello(ctx); err != nil {
		rc.fail(err)
		return
	}

	every := s.PingEvery
	if every <= 0 {
		every = defaultPingEvery
	}
	ping := time.NewTicker(every)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				if sub.Lagged() {
					rc.c.Close(websocket.StatusTryAgainLater, "fell behind; reconnect with since")
				}
				return
			}
			if err := rc.send(ctx, e); err != nil {
				rc.fail(err)
				return
			}
		case <-ping.C:
			if err := s.Auth.Check(ctx, rc.token); err != nil {
				rc.c.Close(closeSessionEnded, "session ended")
				return
			}
			pctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := rc.c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// hello sends who's here, then the queue (unless the client is current)
// and what's playing.
func (rc *roomConn) hello(ctx context.Context) error {
	s := rc.s
	users, err := s.Auth.Users(ctx)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, id := range s.Presence.Members(rc.room.ID) {
		present[id] = true
	}
	hello := RoomHello{RoomId: rc.room.ID, RoomName: rc.room.Name, You: rc.user.ID, Members: []User{}}
	for _, u := range users {
		if present[u.ID] {
			hello.Members = append(hello.Members, toUser(u))
		}
	}
	if err := rc.write(ctx, realtime.Hello, 0, hello); err != nil {
		return err
	}
	snap, err := s.Rooms.QueueSnapshot(ctx, rc.room.ID)
	if err != nil {
		return err
	}
	if rc.sent > snap.Version {
		rc.sent = -1 // a version from the future (e.g. a restored database) is stale too
	}
	if err := rc.send(ctx, realtime.Event{Type: realtime.QueueUpdated, Version: snap.Version, Data: snap}); err != nil {
		return err
	}
	np, err := s.Playback.NowPlaying(ctx, rc.room.ID)
	if err != nil {
		return err
	}
	return rc.send(ctx, realtime.Event{Type: realtime.NowPlayingUpdated, Data: np})
}

// send converts a bus event to its API form and writes it.
func (rc *roomConn) send(ctx context.Context, e realtime.Event) error {
	var data any
	switch d := e.Data.(type) {
	case rooms.QueueSnapshot:
		// Snapshots the client already has (from hello, or a resume) are
		// skipped; versions only grow.
		if d.Version <= rc.sent {
			return nil
		}
		rc.sent = d.Version
		data = toQueueSnapshot(d)
	case rooms.NowPlaying:
		data = toNowPlaying(d)
	case rooms.Notice:
		n := PlaybackNotice{RoomId: d.RoomID, Message: d.Message}
		if d.ItemID != "" {
			n.ItemId = &d.ItemID
		}
		data = n
	case store.User:
		if e.Type == realtime.MemberJoined && d.ID == rc.user.ID {
			return nil // the client knows it joined
		}
		data = toUser(d)
	case store.ServiceLink:
		data = toServiceLink(d)
	case store.Room:
		data = toRoom(d)
	default:
		slog.Error("realtime: no API form for event", "type", e.Type, "data", e.Data)
		return nil
	}
	return rc.write(ctx, e.Type, e.Version, data)
}

// envelope is RoomEvent with typed data.
type envelope struct {
	Type    string `json:"type"`
	Version int64  `json:"version,omitempty"`
	Data    any    `json:"data"`
}

func (rc *roomConn) write(ctx context.Context, typ string, version int64, data any) error {
	ctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return wsjson.Write(ctx, rc.c, envelope{Type: typ, Version: version, Data: data})
}

// fail closes the connection after an unexpected error.
func (rc *roomConn) fail(err error) {
	if websocket.CloseStatus(err) == -1 && !errors.Is(err, context.Canceled) {
		slog.Warn("realtime: closing connection", "room", rc.room.ID, "user", rc.user.ID, "err", err)
	}
	rc.c.Close(websocket.StatusInternalError, "server error")
}

func toQueueSnapshot(s rooms.QueueSnapshot) QueueSnapshot {
	out := QueueSnapshot{RoomId: s.RoomID, Version: s.Version, Items: make([]QueueItem, len(s.Items)), UpNext: s.UpNext}
	if out.UpNext == nil {
		out.UpNext = []string{}
	}
	for i, it := range s.Items {
		out.Items[i] = toQueueItem(it)
	}
	return out
}

func toQueueItem(it store.QueueItem) QueueItem {
	// metadata is the provider.Track snapshot taken when the item was queued.
	var t provider.Track
	if err := json.Unmarshal([]byte(it.Metadata), &t); err != nil {
		slog.Warn("queue item has unreadable metadata", "item", it.ID, "err", err)
	}
	track := QueuedTrack{
		Provider: it.Provider, TrackId: it.TrackID, Title: t.Title, Artists: []string{}, ArtistIds: &[]string{},
		DurationMs: t.Duration.Milliseconds(), Explicit: t.Explicit,
	}
	if it.LinkID.Valid {
		track.LinkId = &it.LinkID.String
	}
	for _, a := range t.Artists {
		track.Artists = append(track.Artists, a.Name)
		*track.ArtistIds = append(*track.ArtistIds, a.ID)
	}
	if t.Album.Title != "" {
		track.Album = &t.Album.Title
	}
	if t.Album.ID != "" {
		track.AlbumId = &t.Album.ID
	}
	if t.Artwork != "" {
		track.Artwork = ptr(string(t.Artwork))
	}
	out := QueueItem{
		Id: it.ID, AddedBy: it.AddedBy, State: QueueItemState(it.State), LanePosition: it.LanePosition, AddedAt: it.AddedAt, Track: track,
	}
	if it.ViaLinkID.Valid {
		out.Via = &PlaysVia{Provider: it.ViaProvider.String, LinkId: it.ViaLinkID.String, TrackId: it.ViaTrackID.String}
	}
	if it.Palette.Valid {
		var p palette.Palette
		if err := json.Unmarshal([]byte(it.Palette.String), &p); err == nil {
			out.Palette = ptr(toPalette(p))
		}
	}
	return out
}

// toPalette converts a palette saved on a queue item.
func toPalette(p palette.Palette) Palette {
	c := func(c palette.Color) OklchColor { return OklchColor{L: c.L, C: c.C, H: c.H} }
	out := Palette{Dominant: c(p.Dominant), Vibrant: c(p.Vibrant), Muted: c(p.Muted), Dark: c(p.Dark), Light: c(p.Light)}
	if p.Accent != nil {
		out.Accent = &Accent{H: p.Accent.H, C: p.Accent.C}
	}
	return out
}
