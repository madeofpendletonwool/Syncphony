// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// socket is a test client's room WebSocket.
type socket struct {
	t *testing.T
	c *websocket.Conn
}

type event struct {
	Type    string          `json:"type"`
	Version int64           `json:"version"`
	Data    json.RawMessage `json:"data"`
}

// dialRoom opens a room socket. On failure it returns the HTTP status.
func (c *client) dialRoom(roomID, query string, headers ...string) (*socket, int, error) {
	c.e.t.Helper()
	h := http.Header{}
	for _, ck := range c.http.Jar.Cookies(mustURL(c.e.t, c.e.srv.URL)) {
		h.Add("Cookie", ck.String())
	}
	for i := 0; i+1 < len(headers); i += 2 {
		h.Set(headers[i], headers[i+1])
	}
	u := "ws" + strings.TrimPrefix(c.e.srv.URL, "http") + "/ws/rooms/" + roomID
	if query != "" {
		u += "?" + query
	}
	conn, res, err := websocket.Dial(c.e.t.Context(), u, &websocket.DialOptions{HTTPHeader: h})
	status := 0
	if res != nil {
		status = res.StatusCode
		if res.Body != nil {
			res.Body.Close()
		}
	}
	if err != nil {
		return nil, status, err
	}
	c.e.t.Cleanup(func() { _ = conn.CloseNow() })
	return &socket{t: c.e.t, c: conn}, status, nil
}

func (c *client) mustDial(roomID, query string) *socket {
	c.e.t.Helper()
	s, _, err := c.dialRoom(roomID, query)
	if err != nil {
		c.e.t.Fatalf("dial: %v", err)
	}
	return s
}

func (s *socket) next() event {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(s.t.Context(), 5*time.Second)
	defer cancel()
	var e event
	if err := wsjson.Read(ctx, s.c, &e); err != nil {
		s.t.Fatalf("reading event: %v", err)
	}
	return e
}

// expect reads the next event, which must be of type typ, and decodes its data.
func (s *socket) expect(typ string, data any) event {
	s.t.Helper()
	e := s.next()
	if e.Type != typ {
		s.t.Fatalf("got %s event (%s), want %s", e.Type, e.Data, typ)
	}
	if data != nil {
		if err := json.Unmarshal(e.Data, data); err != nil {
			s.t.Fatal(err)
		}
	}
	return e
}

// closeStatus waits for the server to close the socket.
func (s *socket) closeStatus() websocket.StatusCode {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(s.t.Context(), 5*time.Second)
	defer cancel()
	for {
		var e event
		if err := wsjson.Read(ctx, s.c, &e); err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

func (e *env) room(t *testing.T, ownerID string) store.Room {
	t.Helper()
	r, err := e.db.CreateRoom(t.Context(), store.CreateRoomParams{
		ID: store.NewID(), Name: "Living room", OwnerID: ownerID, FairnessMode: store.FairnessRoundRobin, Settings: "{}", CreatedAt: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (e *env) queue(t *testing.T, room store.Room, userID string, title string) store.QueueItem {
	t.Helper()
	meta, _ := json.Marshal(provider.Track{
		Ref: provider.TrackRef{Provider: "fake", ID: "t01"}, Title: title,
		Artists: []provider.ArtistCredit{{Name: "Sine Language"}}, Album: provider.AlbumCredit{Title: "A440"},
		Duration: 20 * time.Second, Artwork: "al5",
	})
	it, err := e.db.AddQueueItem(t.Context(), store.AddQueueItemParams{
		ID: store.NewID(), RoomID: room.ID, AddedBy: userID, Provider: "fake", TrackID: "t01",
		Metadata: string(meta), LanePosition: 1024, Now: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func me(t *testing.T, c *client) httpapi.Me {
	t.Helper()
	var m httpapi.Me
	c.want(http.StatusOK, "GET", "/me", nil).decode(t, &m)
	return m
}

func TestRoomSocketRejects(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	room := e.room(t, me(t, alice).Id)

	if _, status, err := e.client().dialRoom(room.ID, ""); err == nil || status != http.StatusUnauthorized {
		t.Fatalf("signed out: %d, %v", status, err)
	}
	if _, status, err := alice.dialRoom("nope", ""); err == nil || status != http.StatusNotFound {
		t.Fatalf("unknown room: %d, %v", status, err)
	}
	if _, status, err := alice.dialRoom(room.ID, "", "Origin", "https://evil.example"); err == nil || status != http.StatusForbidden {
		t.Fatalf("foreign origin: %d, %v", status, err)
	}
	if _, _, err := alice.dialRoom(room.ID, "", "Origin", e.base); err != nil {
		t.Fatalf("own origin: %v", err)
	}
}

func TestRoomSocket(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	aliceMe := me(t, alice)
	room := e.room(t, aliceMe.Id)
	item := e.queue(t, room, aliceMe.Id, "Concert Pitch")

	a := alice.mustDial(room.ID, "")
	var hello httpapi.RoomHello
	a.expect("hello", &hello)
	if hello.RoomId != room.ID || hello.RoomName != "Living room" || hello.You != aliceMe.Id || len(hello.Members) != 1 || hello.Members[0].Id != aliceMe.Id {
		t.Fatalf("hello: %+v", hello)
	}
	var snap httpapi.QueueSnapshot
	a.expect("queue.updated", &snap)
	if snap.Version != 0 || len(snap.Items) != 1 {
		t.Fatalf("snapshot: %+v", snap)
	}
	tr := snap.Items[0].Track
	if snap.Items[0].Id != item.ID || tr.Title != "Concert Pitch" || tr.DurationMs != 20000 || len(tr.Artists) != 1 || tr.Album == nil || *tr.Album != "A440" || tr.LinkId != nil {
		t.Fatalf("item: %+v / %+v", snap.Items[0], tr)
	}
	var np httpapi.NowPlaying
	a.expect("nowplaying.updated", &np)
	if np.Item != nil {
		t.Fatalf("now playing: %+v", np)
	}

	// Queue changes push a new snapshot with a higher version.
	e.queue(t, room, aliceMe.Id, "Tuning Fork")
	if _, err := e.rooms.QueueChanged(t.Context(), room.ID); err != nil {
		t.Fatal(err)
	}
	if ev := a.expect("queue.updated", &snap); ev.Version != 1 || snap.Version != 1 || len(snap.Items) != 2 {
		t.Fatalf("after change: version %d, %+v", ev.Version, snap)
	}

	// Now playing.
	if err := e.db.SetQueueItemState(t.Context(), store.SetQueueItemStateParams{State: store.ItemPlaying, UpdatedAt: store.Now(), ID: item.ID}); err != nil {
		t.Fatal(err)
	}
	if err := e.rooms.NowPlayingChanged(t.Context(), room.ID); err != nil {
		t.Fatal(err)
	}
	a.expect("nowplaying.updated", &np)
	if np.Item == nil || np.Item.Id != item.ID || np.Item.State != httpapi.Playing {
		t.Fatalf("now playing: %+v", np)
	}

	// Presence: bob's first connection is announced, his second isn't, and
	// he leaves when his last one closes.
	bob := e.member(alice, "bob")
	bobMe := me(t, bob)
	b1 := bob.mustDial(room.ID, "")
	b1.expect("hello", &hello)
	if len(hello.Members) != 2 {
		t.Fatalf("bob's hello: %+v", hello.Members)
	}
	var who httpapi.User
	a.expect("member.joined", &who)
	if who.Id != bobMe.Id {
		t.Fatalf("joined: %+v", who)
	}
	b2 := bob.mustDial(room.ID, "")
	b2.expect("hello", nil)
	b1.c.Close(websocket.StatusNormalClosure, "")
	b2.c.Close(websocket.StatusNormalClosure, "")
	a.expect("member.left", &who)
	if who.Id != bobMe.Id {
		t.Fatalf("left: %+v", who)
	}

	// Link status reaches the owner's sockets.
	var l httpapi.ServiceLink
	alice.want(http.StatusCreated, "POST", "/links", httpapi.CreateLinkRequest{Provider: "fake", Fields: demoFields}).decode(t, &l)
	a.expect("link.status", &l)
	if l.Status != httpapi.ServiceLinkStatusOk || l.Provider != "fake" {
		t.Fatalf("link status: %+v", l)
	}
	e.fake.Revoke(fake.Username)
	sess, _ := e.links.Open(t.Context(), l.Id)
	sess.Search(t.Context(), provider.SearchQuery{Text: "x"}) //nolint:errcheck // expected to fail
	a.expect("link.status", &l)
	if l.Status != httpapi.ServiceLinkStatusNeedsRelink {
		t.Fatalf("after revoke: %+v", l)
	}
}

func TestRoomSocketResume(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	room := e.room(t, me(t, alice).Id)
	for range 3 {
		if _, err := e.rooms.QueueChanged(t.Context(), room.ID); err != nil {
			t.Fatal(err)
		}
	}

	// Up to date: no snapshot.
	s := alice.mustDial(room.ID, "since=3")
	s.expect("hello", nil)
	s.expect("nowplaying.updated", nil)

	// Stale, garbage, or from the future: a snapshot.
	for _, since := range []string{"since=2", "since=x", "since=99"} {
		s := alice.mustDial(room.ID, since)
		s.expect("hello", nil)
		if ev := s.expect("queue.updated", nil); ev.Version != 3 {
			t.Fatalf("%s: version %d", since, ev.Version)
		}
	}
}

func TestRoomSocketSessionEnds(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	room := e.room(t, me(t, alice).Id)
	s := alice.mustDial(room.ID, "")
	s.expect("hello", nil)
	alice.want(http.StatusNoContent, "POST", "/auth/logout", nil)
	if code := s.closeStatus(); code != 4001 {
		t.Fatalf("close code %d, want 4001", code)
	}
}
