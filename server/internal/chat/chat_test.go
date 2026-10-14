// SPDX-License-Identifier: AGPL-3.0-only

package chat_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/chat"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func setup(t *testing.T) (*chat.Service, *realtime.Local, store.Room, store.User, store.User) {
	t.Helper()
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "syncphony.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	user := func(name string) store.User {
		u, err := db.CreateUser(ctx, store.CreateUserParams{ID: store.NewID(), Username: name, DisplayName: name, Color: "#000", Role: store.RoleMember, CreatedAt: store.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	alice, bob := user("alice"), user("bob")
	r, err := db.CreateRoom(ctx, store.CreateRoomParams{ID: store.NewID(), Name: "Living room", OwnerID: alice.ID, FairnessMode: store.FairnessRoundRobin, Settings: "{}", CreatedAt: store.Now()})
	if err != nil {
		t.Fatal(err)
	}
	bus := realtime.NewLocal()
	return chat.New(db, bus), bus, r, alice, bob
}

func TestSendListDelete(t *testing.T) {
	ctx := t.Context()
	s, bus, room, alice, bob := setup(t)
	sub := bus.Subscribe(realtime.RoomTopic(room.ID))
	defer sub.Close()

	for _, body := range []string{"", " \n ", strings.Repeat("a", chat.MaxBody+1), "\xff"} {
		var invalid *chat.InvalidInputError
		if _, err := s.Send(ctx, room.ID, alice.ID, body, ""); !errors.As(err, &invalid) {
			t.Errorf("%q: %v", body, err)
		}
	}
	// An item that isn't in the room is left off.
	m, err := s.Send(ctx, room.ID, alice.ID, " hi ", "nope")
	if err != nil {
		t.Fatal(err)
	}
	if m.Body != "hi" || m.QueueItemID.Valid || m.Item != nil {
		t.Fatalf("sent: %+v", m)
	}
	if e := <-sub.C; e.Type != realtime.ChatMessage || e.Data.(chat.Message).ID != m.ID {
		t.Fatalf("event: %+v", e)
	}
	var ids []string
	for i := range 4 {
		m, err := s.Send(ctx, room.ID, bob.ID, strings.Repeat("x", i+1), "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
		<-sub.C
	}

	page, err := s.List(ctx, room.ID, "", 3)
	if err != nil || len(page) != 3 || page[0].ID != ids[3] || page[2].ID != ids[1] {
		t.Fatalf("page: %+v %v", page, err)
	}
	rest, err := s.List(ctx, room.ID, page[2].ID, 0)
	if err != nil || len(rest) != 2 || rest[1].ID != m.ID {
		t.Fatalf("rest: %+v %v", rest, err)
	}

	// Bob can't delete Alice's; the room's owner can delete Bob's.
	if err := s.Delete(ctx, room.ID, m.ID, bob.ID, false); !errors.Is(err, chat.ErrForbidden) {
		t.Fatalf("bob deleting alice's: %v", err)
	}
	if err := s.Delete(ctx, room.ID, ids[0], alice.ID, true); err != nil {
		t.Fatal(err)
	}
	if e := <-sub.C; e.Type != realtime.ChatMessageDeleted || e.Data.(chat.Deleted).ID != ids[0] {
		t.Fatalf("event: %+v", e)
	}
	if err := s.Delete(ctx, room.ID, ids[0], alice.ID, true); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("deleting twice: %v", err)
	}
	if err := s.Delete(ctx, "elsewhere", ids[1], bob.ID, false); !errors.Is(err, chat.ErrNotFound) {
		t.Fatalf("another room's: %v", err)
	}
	all, _ := s.List(ctx, room.ID, "", 0)
	if len(all) != 4 {
		t.Fatalf("after deleting: %d", len(all))
	}
}
