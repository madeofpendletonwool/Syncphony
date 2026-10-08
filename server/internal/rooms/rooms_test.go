// SPDX-License-Identifier: AGPL-3.0-only

package rooms_test

import (
	"errors"
	"path/filepath"
	"strings"
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
		{`{}`, rooms.Permissions{PlayPause: "everyone", Seek: "everyone", Skip: "everyone", Speaker: "everyone", StartRounds: "owner"}, 50},
		{`not json`, rooms.Permissions{PlayPause: "everyone", Seek: "everyone", Skip: "everyone", Speaker: "everyone", StartRounds: "owner"}, 50},
		// Rooms from before permissions had one setting for everything.
		{`{"controls":"owner"}`, rooms.Permissions{PlayPause: "owner", Seek: "owner", Skip: "owner", Speaker: "owner", StartRounds: "owner"}, 50},
		{
			`{"controls":"owner","permissions":{"skip":"vote","seek":"everyone"},"skipVotePercent":66}`,
			rooms.Permissions{PlayPause: "owner", Seek: "everyone", Skip: "vote", Speaker: "owner", StartRounds: "owner"},
			66,
		},
		{`{"permissions":{"playPause":"vote"},"skipVotePercent":100}`, rooms.Permissions{PlayPause: "everyone", Seek: "everyone", Skip: "everyone", Speaker: "everyone", StartRounds: "owner"}, 50},
		{`{"skipVotePercent":0}`, rooms.Permissions{PlayPause: "everyone", Seek: "everyone", Skip: "everyone", Speaker: "everyone", StartRounds: "owner"}, 0},
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

	r, err = s.Update(ctx, rooms.Actor{UserID: owner.ID}, r.ID, rooms.Update{Permissions: rooms.Permissions{Skip: rooms.Vote}})
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
	r, err = s.Update(ctx, rooms.Actor{UserID: owner.ID}, r.ID, rooms.Update{Fairness: &rooms.Fairness{MaxInARow: 2, Weights: map[string]int{"x": 1, "y": 3}}})
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
	r, err = s.Update(ctx, rooms.Actor{UserID: owner.ID}, r.ID, rooms.Update{Autopilot: &rooms.Autopilot{On: true, Adventure: rooms.AdventureDiscovery}})
	if err != nil {
		t.Fatal(err)
	}
	if a := rooms.ParseSettings(r.Settings).Autopilot; !a.On || a.Adventure != rooms.AdventureDiscovery || a.ExploreLevel() != rooms.ExploreDiscovery {
		t.Errorf("autopilot: %+v", a)
	}
	// Explore decides, and the adventure follows the half it falls in.
	for _, c := range []struct {
		explore   int
		adventure string
	}{{0, rooms.AdventureSimilar}, {49, rooms.AdventureSimilar}, {50, rooms.AdventureDiscovery}, {100, rooms.AdventureDiscovery}} {
		r, err = s.Update(ctx, rooms.Actor{UserID: owner.ID}, r.ID, rooms.Update{Autopilot: &rooms.Autopilot{On: true, Adventure: rooms.AdventureSimilar, Explore: new(c.explore)}})
		if err != nil {
			t.Fatal(err)
		}
		if a := rooms.ParseSettings(r.Settings).Autopilot; a.ExploreLevel() != c.explore || a.Adventure != c.adventure {
			t.Errorf("explore %d: %+v, want %s", c.explore, a, c.adventure)
		}
	}
	// The energy curve is on unless turned off.
	if a := rooms.ParseSettings(r.Settings).Autopilot; !a.EnergyCurveOn() {
		t.Errorf("energy curve off by default: %+v", a)
	}
	r, err = s.Update(ctx, rooms.Actor{UserID: owner.ID}, r.ID, rooms.Update{Autopilot: &rooms.Autopilot{On: true, EnergyCurve: new(false)}})
	if err != nil {
		t.Fatal(err)
	}
	if a := rooms.ParseSettings(r.Settings).Autopilot; a.EnergyCurveOn() {
		t.Errorf("energy curve still on: %+v", a)
	}
	var invalid *rooms.InvalidInputError
	for _, u := range []rooms.Update{
		{Autopilot: &rooms.Autopilot{On: true, Adventure: "wild"}},
		{Autopilot: &rooms.Autopilot{On: true, Explore: new(101)}},
		{Autopilot: &rooms.Autopilot{On: true, Explore: new(-1)}},
		{Permissions: rooms.Permissions{Seek: rooms.Vote}},
		{Permissions: rooms.Permissions{Skip: "anyone"}},
		{SkipVotePercent: new(100)},
		{SkipVotePercent: new(-1)},
		{Fairness: &rooms.Fairness{MaxInARow: 11}},
		{Fairness: &rooms.Fairness{Cooldown: -1}},
		{Fairness: &rooms.Fairness{Weights: map[string]int{"x": 5}}},
		{Fairness: &rooms.Fairness{RepeatWindowMinutes: 24*60 + 1}},
	} {
		if _, err := s.Update(ctx, rooms.Actor{UserID: owner.ID}, r.ID, u); !errors.As(err, &invalid) {
			t.Errorf("%+v: %v", u, err)
		}
	}
}

// Rooms set up before explore read it from their adventure.
func TestExploreFromAdventure(t *testing.T) {
	for raw, want := range map[string]int{
		`{"version":1}`: rooms.ExploreSimilar,
		`{"version":1,"autopilot":{"on":true,"adventure":"discovery"}}`:              rooms.ExploreDiscovery,
		`{"version":1,"autopilot":{"on":true,"adventure":"discovery","explore":10}}`: 10,
		`{"version":1,"autopilot":{"on":true,"explore":500}}`:                        rooms.ExploreSimilar,
	} {
		if got := rooms.ParseSettings(raw).Autopilot.ExploreLevel(); got != want {
			t.Errorf("%s: explore %d, want %d", raw, got, want)
		}
	}
}

func TestSettingsVersion(t *testing.T) {
	// Version 0 had only controls; from version 1 it means nothing.
	if p := rooms.ParseSettings(`{"controls":"owner"}`).Permissions; p.Seek != rooms.Owner {
		t.Errorf("v0 controls: %+v", p)
	}
	if p := rooms.ParseSettings(`{"version":1,"controls":"owner"}`).Permissions; p.Seek != rooms.Everyone {
		t.Errorf("v1 with controls: %+v", p)
	}
	if v := rooms.ParseSettings(`{}`).Version; v != rooms.SettingsVersion {
		t.Errorf("parsed version %d", v)
	}

	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner, err := db.CreateUser(t.Context(), store.CreateUserParams{ID: store.NewID(), Username: "o", DisplayName: "O", Color: "#000000", Role: store.RoleMember, CreatedAt: store.Now()})
	if err != nil {
		t.Fatal(err)
	}
	r, err := rooms.New(db, realtime.NewLocal()).Create(t.Context(), owner.ID, "Room", "", rooms.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Settings, `"version":2`) {
		t.Errorf("saved settings %s", r.Settings)
	}
}
