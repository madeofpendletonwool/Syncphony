// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/games"
	"github.com/madeofpendletonwool/syncphony/server/internal/nights"
	"github.com/madeofpendletonwool/syncphony/server/internal/palette"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
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
	// closeNoAccess tells the client it can't open the room any more.
	closeNoAccess websocket.StatusCode = 4003
	// closeRoomGone tells the client the room was deleted.
	closeRoomGone websocket.StatusCode = 4004
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
		rc := &roomConn{s: s, token: token, display: r.URL.Query().Get("display") == "1", device: r.URL.Query().Get("device")}
		if sess, err := s.Auth.Authenticate(ctx, token); err == nil {
			// A guest may watch their own room only.
			if sess.Guest != nil && sess.Guest.RoomID != r.PathValue("id") {
				writeError(w, r, auth.ErrForbidden)
				return
			}
			rc.user, rc.guest = sess.User, sess.Guest != nil
		} else if c, derr := r.Cookie(DisplayCookie); derr == nil && errors.Is(err, auth.ErrUnauthenticated) {
			// A paired display, which may watch its own room only.
			d, derr := s.Auth.AuthenticateDisplay(ctx, c.Value)
			if derr != nil {
				writeError(w, r, derr)
				return
			}
			if d.Display.RoomID != r.PathValue("id") {
				writeError(w, r, auth.ErrForbidden)
				return
			}
			rc.token, rc.paired, rc.display = c.Value, true, true
		} else {
			writeError(w, r, err)
			return
		}
		room, err := s.Rooms.Get(ctx, r.PathValue("id"))
		if err == nil && !rc.paired && !rc.guest {
			// A room you can't open isn't there.
			if ok, aerr := s.Rooms.CanEnter(ctx, rc.user.ID, room); aerr != nil {
				err = aerr
			} else if !ok {
				err = rooms.ErrNotFound
			}
		}
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
		rc.c, rc.room, rc.sent = c, room, since
		rc.serve(ctx)
	})
}

// roomConn is one client's connection to a room.
type roomConn struct {
	s     *Server
	c     *websocket.Conn
	user  store.User
	token string
	// display: the connection is a big screen, which isn't in the room
	// (no presence). paired: it's a paired display, with no user at all;
	// token is then the display's. guest: the user is a guest, in the
	// room by their pass.
	display, paired, guest bool
	room                   store.Room
	// sent is the last queue version the client has.
	sent int64
	// device is the client's device ID, if it said; player is the room's
	// speaker, as of the last playback state sent. The speaker sees what
	// a game round hides: its lock screen shows the song anyway.
	device string
	player *rooms.Player
}

// speaker reports whether the connection is the room's speaker.
func (rc *roomConn) speaker() bool {
	p := rc.player
	return p != nil && rc.device != "" && p.DeviceID == rc.device && (rc.paired || p.UserID == rc.user.ID)
}

func (rc *roomConn) serve(ctx context.Context) {
	s, roomID, userID := rc.s, rc.room.ID, rc.user.ID
	// Server push only: reading just handles pings and close frames, and
	// ctx ends when the client goes away.
	ctx = rc.c.CloseRead(ctx)

	// Subscribe before reading snapshots, so nothing falls in between.
	topics := []string{realtime.RoomTopic(roomID)}
	if !rc.paired {
		topics = append(topics, realtime.UserTopic(userID))
	}
	sub := s.Bus.Subscribe(topics...)
	defer sub.Close()

	// A big screen watches the room without being in it: it doesn't
	// count toward who's here, or skip votes.
	if !rc.display {
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
	}

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
			if e.Type == realtime.RoomDeleted {
				rc.c.Close(closeRoomGone, "room deleted")
				return
			}
			if m, ok := e.Data.(rooms.MembersChanged); ok && m.Change == rooms.Removed && m.UserID == userID && !rc.paired {
				rc.c.Close(closeNoAccess, "removed from the room")
				return
			}
		case <-ping.C:
			if err := rc.check(ctx); err != nil {
				rc.c.Close(closeSessionEnded, "session ended")
				return
			}
			if ok, err := rc.canEnter(ctx); err == nil && !ok {
				rc.c.Close(closeNoAccess, "the room isn't open to you")
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

// check reports whether the connection's session, or display, is still good.
func (rc *roomConn) check(ctx context.Context) error {
	if rc.paired {
		return rc.s.Auth.CheckDisplay(ctx, rc.token)
	}
	return rc.s.Auth.Check(ctx, rc.token)
}

// canEnter reports whether the connection's user may still open the room,
// should it have stopped being open to them. Displays and guests are in
// the room by their pairing or pass.
func (rc *roomConn) canEnter(ctx context.Context) (bool, error) {
	if rc.paired || rc.guest {
		return true, nil
	}
	r, err := rc.s.Rooms.Get(ctx, rc.room.ID)
	if err != nil {
		return false, err
	}
	return rc.s.Rooms.CanEnter(ctx, rc.user.ID, r)
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
	hello := RoomHello{RoomId: rc.room.ID, RoomName: rc.room.Name, You: rc.user.ID, Members: []User{}, ServerTime: time.Now().UTC()}
	users = slices.DeleteFunc(users, func(u store.User) bool { return !present[u.ID] })
	members, err := s.apiUsers(ctx, users)
	if err != nil {
		return err
	}
	hello.Members = append(hello.Members, members...)
	if err := rc.write(ctx, realtime.Hello, 0, hello); err != nil {
		return err
	}
	np, err := s.Playback.NowPlaying(ctx, rc.room.ID)
	if err != nil {
		return err
	}
	rc.player = np.Player
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
	if err := rc.send(ctx, realtime.Event{Type: realtime.NowPlayingUpdated, Data: np}); err != nil {
		return err
	}
	return rc.helloGames(ctx)
}

// helloGames sends the round that's up, and tonight's scores.
func (rc *roomConn) helloGames(ctx context.Context) error {
	g := rc.s.Games
	if g == nil {
		return nil
	}
	if rd, ok := g.Current(rc.room.ID); ok {
		if err := rc.send(ctx, realtime.Event{Type: realtime.GameRound, Data: rd}); err != nil {
			return err
		}
	}
	sc, err := g.Scores(ctx, rc.room.ID)
	if err != nil || sc.Mode == rooms.ScoresOff || len(sc.Players) == 0 {
		return err
	}
	return rc.send(ctx, realtime.Event{Type: realtime.GameScores, Data: sc})
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
		data = rc.s.queueFor(d, rc.speaker())
	case rooms.NowPlaying:
		rc.player = d.Player
		data = rc.s.nowPlayingFor(d, rc.speaker())
	case games.Round:
		data = toGameRound(d)
	case games.Scores:
		data = toGameScores(d, rc.user.ID)
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
		u, err := rc.s.apiUser(ctx, d)
		if err != nil {
			return err
		}
		data = u
	case store.ServiceLink:
		data = toServiceLink(d)
	case store.Room:
		data = toRoom(d)
	case rooms.Deleted:
		data = map[string]string{"roomId": d.RoomID}
	case Reaction:
		data = d
	case nights.Hearts:
		data = toHearts(d)
	case nights.Night:
		data = toNight(d)
	case guestsChanged:
		data = d
	case rooms.MembersChanged:
		data = RoomMembersChanged{RoomId: d.RoomID, UserId: d.UserID, Change: RoomMembersChangedChange(d.Change)}
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
	if info, ok := queue.ParseAutopilot(it); ok {
		out.Autopilot = &AutopilotPick{
			SeedItemId: nonEmpty(info.SeedItemID), SeedTitle: nonEmpty(info.SeedTitle), SeedArtist: nonEmpty(info.SeedArtist),
			Reason: nonEmpty(info.Summary), Source: nonEmpty(info.Source),
		}
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
