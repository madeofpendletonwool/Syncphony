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

func TestParseSettings(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    rooms.Permissions
		percent int
	}{
		{`{}`, rooms.Permissions{PlayPause: "everyone", Seek: "everyone", Skip: "everyone", Speaker: "everyone"}, 50},
		{`not json`, rooms.Permissions{PlayPause: "everyone", Seek: "everyone", Skip: "everyone", Speaker: "everyone"}, 50},
		// Rooms from before permissions had one setting for everything.
		{`{"controls":"owner"}`, rooms.Permissions{PlayPause: "owner", Seek: "owner", Skip: "owner", Speaker: "owner"}, 50},
		{
			`{"controls":"owner","permissions":{"skip":"vote","seek":"everyone"},"skipVotePercent":66}`,
			rooms.Permissions{PlayPause: "owner", Seek: "everyone", Skip: "vote", Speaker: "owner"},
			66,
		},
		{`{"permissions":{"playPause":"vote"},"skipVotePercent":100}`, rooms.Permissions{PlayPause: "everyone", Seek: "everyone", Skip: "everyone", Speaker: "everyone"}, 50},
		{`{"skipVotePercent":0}`, rooms.Permissions{PlayPause: "everyone", Seek: "everyone", Skip: "everyone", Speaker: "everyone"}, 0},
	} {
		st := rooms.ParseSettings(tc.raw)
		if st.Permissions != tc.want || *st.SkipVotePercent != tc.percent || st.Controls != "" {
			t.Errorf("%s: got %+v %d", tc.raw, st.Permissions, *st.SkipVotePercent)
		}
	}
}

func TestVotesNeeded(t *testing.T) {
	for _, tc := range []struct{ voters, percent, want int }{
		{0, 50, 1},
		{1, 50, 1},
		{2, 50, 2},
		{3, 50, 2},
		{4, 50, 3},
		{5, 50, 3},
		{3, 66, 2},
		{4, 66, 3},
		{6, 66, 4},
		{3, 99, 3},
		{10, 99, 10},
		{5, 0, 1},
	} {
		if got := rooms.VotesNeeded(tc.voters, tc.percent); got != tc.want {
			t.Errorf("VotesNeeded(%d, %d) = %d, want %d", tc.voters, tc.percent, got, tc.want)
		}
	}
}

func TestUpdateSettings(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := t.Context()
	owner, err := db.CreateUser(ctx, store.CreateUserParams{ID: store.NewID(), Username: "o", DisplayName: "O", Color: "#000000", Role: store.RoleMember, CreatedAt: store.Now()})
	if err != nil {
		t.Fatal(err)
	}
	bus := realtime.NewLocal()
	s := rooms.New(db, bus)
	r, err := s.Create(ctx, owner.ID, "Room", "", rooms.Settings{Permissions: rooms.Permissions{Seek: rooms.Owner}})
	if err != nil {
		t.Fatal(err)
	}
	sub := bus.Subscribe(realtime.RoomTopic(r.ID))
	defer sub.Close()
	var hooked store.Room
	s.OnUpdate = func(r store.Room) { hooked = r }

	r, err = s.Update(ctx, owner.ID, r.ID, rooms.Update{Permissions: rooms.Permissions{Skip: rooms.Vote}})
	if err != nil {
		t.Fatal(err)
	}
	st := rooms.ParseSettings(r.Settings)
	if st.Permissions.Seek != rooms.Owner || st.Permissions.Skip != rooms.Vote || st.Permissions.PlayPause != rooms.Everyone {
		t.Errorf("permissions: %+v", st.Permissions)
	}
	if hooked.ID != r.ID {
		t.Error("OnUpdate not called")
	}
	if e := <-sub.C; e.Type != realtime.RoomUpdated {
		t.Errorf("published %s", e.Type)
	}
	// New fairness options reorder the queue, so a snapshot goes out.
	r, err = s.Update(ctx, owner.ID, r.ID, rooms.Update{Fairness: &rooms.Fairness{MaxInARow: 2, Weights: map[string]int{"x": 1, "y": 3}}})
	if err != nil {
		t.Fatal(err)
	}
	if f := rooms.ParseSettings(r.Settings).Fairness; f.MaxInARow != 2 || len(f.Weights) != 1 || f.Weights["y"] != 3 {
		t.Errorf("fairness: %+v", f)
	}
	var types []string
	for range 2 {
		types = append(types, (<-sub.C).Type)
	}
	if types[0] != realtime.RoomUpdated || types[1] != realtime.QueueUpdated {
		t.Errorf("published %v", types)
	}
	// Autopilot defaults to similar, and comes through a round trip.
	if a := rooms.ParseSettings(r.Settings).Autopilot; a.On || a.Adventure != rooms.AdventureSimilar {
		t.Errorf("default autopilot: %+v", a)
	}
	r, err = s.Update(ctx, owner.ID, r.ID, rooms.Update{Autopilot: &rooms.Autopilot{On: true, Adventure: rooms.AdventureDiscovery}})
	if err != nil {
		t.Fatal(err)
	}
	if a := rooms.ParseSettings(r.Settings).Autopilot; !a.On || a.Adventure != rooms.AdventureDiscovery {
		t.Errorf("autopilot: %+v", a)
	}
	var invalid *rooms.InvalidInputError
	for _, u := range []rooms.Update{
		{Autopilot: &rooms.Autopilot{On: true, Adventure: "wild"}},
		{Permissions: rooms.Permissions{Seek: rooms.Vote}},
		{Permissions: rooms.Permissions{Skip: "anyone"}},
		{SkipVotePercent: new(100)},
		{SkipVotePercent: new(-1)},
		{Fairness: &rooms.Fairness{MaxInARow: 11}},
		{Fairness: &rooms.Fairness{Cooldown: -1}},
		{Fairness: &rooms.Fairness{Weights: map[string]int{"x": 5}}},
		{Fairness: &rooms.Fairness{RepeatWindowMinutes: 24*60 + 1}},
	} {
		if _, err := s.Update(ctx, owner.ID, r.ID, u); !errors.As(err, &invalid) {
			t.Errorf("%+v: %v", u, err)
		}
	}
}
