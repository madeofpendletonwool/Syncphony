// SPDX-License-Identifier: AGPL-3.0-only

package rooms_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func TestUnknownRoom(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bus := realtime.NewLocal()
	sub := bus.Subscribe(realtime.RoomTopic("nope"))
	defer sub.Close()
	s := rooms.New(db, bus)
	if _, err := s.Get(t.Context(), "nope"); !errors.Is(err, rooms.ErrNotFound) {
		t.Errorf("Get: %v", err)
	}
	if _, err := s.QueueSnapshot(t.Context(), "nope"); !errors.Is(err, rooms.ErrNotFound) {
		t.Errorf("QueueSnapshot: %v", err)
	}
	if _, err := s.QueueChanged(t.Context(), "nope"); !errors.Is(err, rooms.ErrNotFound) {
		t.Errorf("QueueChanged: %v", err)
	}
	select {
	case e := <-sub.C:
		t.Errorf("published %+v for a missing room", e)
	default:
	}
}
