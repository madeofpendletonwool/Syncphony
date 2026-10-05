// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func user(t *testing.T, s *store.Store, username string) store.User {
	t.Helper()
	u, err := s.CreateUser(t.Context(), store.CreateUserParams{
		ID: store.NewID(), Username: username, DisplayName: username, Color: "#7c3aed", Role: store.RoleMember, CreatedAt: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for range 2 {
		s, err := store.Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Ping(t.Context()); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

func TestTimesRoundTrip(t *testing.T) {
	s := open(t)
	u := user(t, s, "alice")
	got, err := s.GetUser(t.Context(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, off := got.CreatedAt.Zone(); !got.CreatedAt.Equal(u.CreatedAt) || off != 0 {
		t.Fatalf("created_at %v, wrote %v", got.CreatedAt, u.CreatedAt)
	}
}

func TestUsernameUnique(t *testing.T) {
	s := open(t)
	user(t, s, "alice")
	_, err := s.CreateUser(t.Context(), store.CreateUserParams{
		ID: store.NewID(), Username: "alice", DisplayName: "Other", Color: "#000", Role: store.RoleMember, CreatedAt: store.Now(),
	})
	if err == nil {
		t.Fatal("duplicate username accepted")
	}
}

func TestCheckConstraints(t *testing.T) {
	s := open(t)
	_, err := s.CreateUser(t.Context(), store.CreateUserParams{
		ID: store.NewID(), Username: "bob", DisplayName: "Bob", Color: "#000", Role: "superuser", CreatedAt: store.Now(),
	})
	if err == nil {
		t.Fatal("bad role accepted")
	}
}

func TestRedeemInvite(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	admin := user(t, s, "admin")
	now := store.Now()
	for _, code := range []string{"good", "expired"} {
		expires := now.Add(time.Hour)
		if code == "expired" {
			expires = now.Add(-time.Second)
		}
		if _, err := s.CreateInvite(ctx, store.CreateInviteParams{
			Code: code, CreatedBy: sql.NullString{String: admin.ID, Valid: true}, Role: store.RoleMember, CreatedAt: now, ExpiresAt: expires,
		}); err != nil {
			t.Fatal(err)
		}
	}
	alice := user(t, s, "alice")
	inv, err := s.RedeemInvite(ctx, "good", alice.ID, now)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if inv.UsedBy.String != alice.ID || !inv.UsedAt.Valid {
		t.Fatalf("redeemed invite: %+v", inv)
	}
	bob := user(t, s, "bob")
	for _, code := range []string{"good", "expired", "missing"} {
		_, err := s.RedeemInvite(ctx, code, bob.ID, now)
		if !store.IsNotFound(err) {
			t.Errorf("redeem %s: got %v, want not found", code, err)
		}
	}
}

func TestSessions(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	u := user(t, s, "alice")
	now := store.Now()
	for i, exp := range []time.Duration{time.Hour, -time.Minute} {
		if _, err := s.CreateSession(ctx, store.CreateSessionParams{
			TokenHash: []byte{byte(i)}, UserID: u.ID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(exp),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.GetSession(ctx, store.GetSessionParams{TokenHash: []byte{0}, Now: now}); err != nil {
		t.Errorf("live session: %v", err)
	}
	if _, err := s.GetSession(ctx, store.GetSessionParams{TokenHash: []byte{1}, Now: now}); !store.IsNotFound(err) {
		t.Errorf("expired session: got %v, want not found", err)
	}
	n, err := s.DeleteExpiredSessions(ctx, now)
	if err != nil || n != 1 {
		t.Errorf("DeleteExpiredSessions = %d, %v; want 1", n, err)
	}
}

func TestForeignKeys(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	_, err := s.CreateServiceLink(ctx, store.CreateServiceLinkParams{
		ID: store.NewID(), UserID: "nobody", Provider: "fake", AccountID: "demo", AccountLabel: "demo", EncryptedCredentials: []byte("x"), Now: store.Now(),
	})
	if err == nil {
		t.Fatal("link for a missing user accepted: foreign keys are off")
	}
}

func TestDeletingLinkKeepsQueueSnapshot(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	u := user(t, s, "alice")
	now := store.Now()
	link, err := s.CreateServiceLink(ctx, store.CreateServiceLinkParams{
		ID: store.NewID(), UserID: u.ID, Provider: "fake", AccountID: "demo", AccountLabel: "demo", EncryptedCredentials: []byte("sealed"), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	room := newRoom(t, s, u)
	item := addItem(t, s, room, u, link.ID, 1024)
	if err := s.DeleteServiceLink(ctx, store.DeleteServiceLinkParams{ID: link.ID, UserID: u.ID}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetQueueItem(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LinkID.Valid || got.Metadata != item.Metadata {
		t.Fatalf("after deleting the link: %+v", got)
	}
}

func newRoom(t *testing.T, s *store.Store, owner store.User) store.Room {
	t.Helper()
	r, err := s.CreateRoom(t.Context(), store.CreateRoomParams{
		ID: store.NewID(), Name: "Living room", OwnerID: owner.ID, FairnessMode: store.FairnessRoundRobin, Settings: "{}", CreatedAt: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func addItem(t *testing.T, s *store.Store, room store.Room, u store.User, linkID string, pos int64) store.QueueItem {
	t.Helper()
	it, err := s.AddQueueItem(t.Context(), store.AddQueueItemParams{
		ID: store.NewID(), RoomID: room.ID, AddedBy: u.ID, Provider: "fake",
		LinkID: sql.NullString{String: linkID, Valid: linkID != ""}, TrackID: "t01",
		Metadata: `{"title":"Reference Tone"}`, LanePosition: pos, Now: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestQueue(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	alice, bob := user(t, s, "alice"), user(t, s, "bob")
	room := newRoom(t, s, alice)

	next := func(u store.User) int64 {
		t.Helper()
		n, err := s.NextLanePosition(ctx, store.NextLanePositionParams{RoomID: room.ID, AddedBy: u.ID})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	var items []store.QueueItem
	for _, u := range []store.User{alice, alice, bob} {
		items = append(items, addItem(t, s, room, u, "", next(u)))
	}
	if items[0].LanePosition != 1024 || items[1].LanePosition != 2048 || items[2].LanePosition != 1024 {
		t.Fatalf("lane positions %d %d %d", items[0].LanePosition, items[1].LanePosition, items[2].LanePosition)
	}

	// Move alice's second item ahead of her first.
	if err := s.MoveQueueItem(ctx, store.MoveQueueItemParams{LanePosition: 512, UpdatedAt: store.Now(), ID: items[1].ID}); err != nil {
		t.Fatal(err)
	}
	up, err := s.ListUpcoming(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	var aliceLane []string
	for _, it := range up {
		if it.AddedBy == alice.ID {
			aliceLane = append(aliceLane, it.ID)
		}
	}
	if len(up) != 3 || len(aliceLane) != 2 || aliceLane[0] != items[1].ID {
		t.Fatalf("upcoming: %d items, alice's lane %v", len(up), aliceLane)
	}

	// Only one item may play at a time.
	if err := s.SetQueueItemState(ctx, store.SetQueueItemStateParams{State: store.ItemPlaying, UpdatedAt: store.Now(), ID: items[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetQueueItemState(ctx, store.SetQueueItemStateParams{State: store.ItemPlaying, UpdatedAt: store.Now(), ID: items[2].ID}); err == nil {
		t.Fatal("two items playing in one room")
	}
	playing, err := s.GetPlaying(ctx, room.ID)
	if err != nil || playing.ID != items[0].ID {
		t.Fatalf("GetPlaying = %v, %v", playing.ID, err)
	}

	// History.
	h, err := s.StartPlay(ctx, store.StartPlayParams{ID: store.NewID(), RoomID: room.ID, QueueItemID: items[0].ID, StartedAt: store.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndPlay(ctx, store.EndPlayParams{EndedAt: sql.NullTime{Time: store.Now(), Valid: true}, EndReason: sql.NullString{String: store.EndFinished, Valid: true}, ID: h.ID}); err != nil {
		t.Fatal(err)
	}
	hist, err := s.ListHistory(ctx, store.ListHistoryParams{RoomID: room.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].QueueItem.ID != items[0].ID || hist[0].PlayHistory.EndReason.String != store.EndFinished {
		t.Fatalf("history: %+v", hist)
	}

	// Alice's lane no longer has the playing item.
	lane, err := s.ListLane(ctx, store.ListLaneParams{RoomID: room.ID, AddedBy: alice.ID})
	if err != nil || len(lane) != 1 || lane[0].ID != items[1].ID {
		t.Fatalf("ListLane = %v, %v", lane, err)
	}

	// The latest play per user.
	later := h.StartedAt.Add(time.Minute)
	if _, err := s.StartPlay(ctx, store.StartPlayParams{ID: store.NewID(), RoomID: room.ID, QueueItemID: items[2].ID, StartedAt: h.StartedAt.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPlay(ctx, store.StartPlayParams{ID: store.NewID(), RoomID: room.ID, QueueItemID: items[1].ID, StartedAt: later}); err != nil {
		t.Fatal(err)
	}
	last, err := s.LastPlayedByUser(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]time.Time{}
	for _, r := range last {
		got[r.UserID] = r.StartedAt
	}
	if len(got) != 2 || !got[alice.ID].Equal(later) || !got[bob.ID].Equal(h.StartedAt.Add(-time.Hour)) {
		t.Fatalf("LastPlayedByUser = %v", last)
	}
}

func TestTx(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	boom := errors.New("boom")
	err := s.Tx(ctx, func(q *store.Queries) error {
		if _, err := q.CreateUser(ctx, store.CreateUserParams{
			ID: store.NewID(), Username: "ghost", DisplayName: "Ghost", Color: "#000", Role: store.RoleMember, CreatedAt: store.Now(),
		}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx = %v", err)
	}
	if n, _ := s.CountUsers(ctx); n != 0 {
		t.Fatalf("rolled-back user persisted: %d users", n)
	}
}

// TestConcurrentWrites checks that writers wait for each other instead of
// failing with SQLITE_BUSY.
func TestConcurrentWrites(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			errs <- s.Tx(ctx, func(q *store.Queries) error {
				n, err := q.CountUsers(ctx)
				if err != nil {
					return err
				}
				_, err = q.CreateUser(ctx, store.CreateUserParams{
					ID: store.NewID(), Username: store.NewID(), DisplayName: "x", Color: "#000", Role: store.RoleMember,
					CreatedAt: store.Now().Add(time.Duration(n)),
				})
				return err
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}
