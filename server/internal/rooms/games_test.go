// SPDX-License-Identifier: AGPL-3.0-only

package rooms_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func TestGamesLevels(t *testing.T) {
	off := rooms.ParseSettings(`{}`).Games
	if off.LevelOf() != rooms.GamesOff || off.Plays() || off.Awards() || len(off.Kinds()) != 0 || off.ScoreMode() != rooms.ScoresOff {
		t.Errorf("a room before games: %+v", off)
	}
	recap := rooms.Games{Level: rooms.GamesRecap}
	if recap.Plays() || !recap.Awards() || len(recap.Kinds()) != 0 {
		t.Errorf("recap plays %v, awards %v, kinds %v", recap.Plays(), recap.Awards(), recap.Kinds())
	}
	ambient := rooms.Games{Level: rooms.GamesAmbient}
	if !slices.Equal(ambient.Kinds(), []string{"year", "liner", "sample"}) || ambient.Every() != 2 || ambient.ScoreMode() != "private" || ambient.Breaks() != 0 {
		t.Errorf("ambient: kinds %v, every %d, scores %s, breaks %d", ambient.Kinds(), ambient.Every(), ambient.ScoreMode(), ambient.Breaks())
	}
	night := rooms.Games{Level: rooms.GamesNight}
	if !slices.Equal(night.Kinds(), rooms.GameKinds) || night.Breaks() != rooms.DefaultBreaksPerHour || night.ScoreMode() != "board" {
		t.Errorf("game night: kinds %v, breaks %d", night.Kinds(), night.Breaks())
	}
	// A game the level doesn't allow stays off, switched on or not.
	tuned := rooms.Games{Level: rooms.GamesAmbient, Enabled: map[string]bool{"tune": true, "year": false}}
	if tuned.On("tune") || tuned.On("year") || !tuned.On("liner") {
		t.Errorf("switches: %v", tuned.Kinds())
	}
	zero := 0
	if g := (rooms.Games{Level: rooms.GamesRounds, Frequency: &zero}); g.Every() != 0 {
		t.Errorf("host-only rounds: every %d", g.Every())
	}
}

func TestGamesSettings(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.CreateUser(t.Context(), store.CreateUserParams{ID: "ann", Username: "ann", DisplayName: "Ann", Role: store.RoleMember, CreatedAt: store.Now()}); err != nil {
		t.Fatal(err)
	}
	s := rooms.New(db, realtime.NewLocal())
	r, err := s.Create(t.Context(), "ann", "Party", "", rooms.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if st := rooms.ParseSettings(r.Settings); st.Games.LevelOf() != rooms.GamesOff || st.Permissions.StartRounds != rooms.Owner || st.Version != rooms.SettingsVersion {
		t.Fatalf("a new room: %+v", st)
	}
	four := 4
	r, err = s.Update(t.Context(), rooms.Actor{UserID: "ann"}, r.ID, rooms.Update{
		Permissions: rooms.Permissions{StartRounds: rooms.Everyone},
		Games:       &rooms.Games{Level: rooms.GamesRounds, Enabled: map[string]bool{"year": true, "lyrics": false}, Frequency: &four, Scores: rooms.ScoresPrivate, TVOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := rooms.ParseSettings(r.Settings)
	g := st.Games
	if g.Level != "rounds" || g.Every() != 4 || g.On("lyrics") || !g.On("year") || g.ScoreMode() != "private" || !g.TVOnly || st.Permissions.StartRounds != rooms.Everyone {
		t.Errorf("after update: %+v", st)
	}
	// Year was on anyway: only the switch away from the default is kept.
	if _, ok := g.Enabled["year"]; ok || len(g.Enabled) != 1 {
		t.Errorf("kept switches %v", g.Enabled)
	}
	for _, bad := range []rooms.Games{
		{Level: "party"},
		{Level: rooms.GamesRounds, Enabled: map[string]bool{"karaoke": true}},
		{Level: rooms.GamesRounds, Frequency: new(-1)},
		{Level: rooms.GamesRounds, Scores: "loud"},
		{Level: rooms.GamesNight, BreaksPerHour: new(99)},
		{Level: rooms.GamesNight, Tune: &rooms.Tune{From: "radio"}},
		{Level: rooms.GamesNight, Tune: &rooms.Tune{Clip: "bridge"}},
	} {
		var invalid *rooms.InvalidInputError
		if _, err := s.Update(t.Context(), rooms.Actor{UserID: "ann"}, r.ID, rooms.Update{Games: &bad}); !errors.As(err, &invalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestTuneSettings(t *testing.T) {
	if got := (rooms.Games{}).TuneOf(); got != (rooms.Tune{From: rooms.TuneTonight, Clip: rooms.ClipChorus}) {
		t.Errorf("defaults %+v", got)
	}
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.CreateUser(t.Context(), store.CreateUserParams{ID: "ann", Username: "ann", DisplayName: "Ann", Role: store.RoleMember, CreatedAt: store.Now()}); err != nil {
		t.Fatal(err)
	}
	s := rooms.New(db, realtime.NewLocal())
	r, err := s.Create(t.Context(), "ann", "Party", "", rooms.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.Update(t.Context(), rooms.Actor{UserID: "ann"}, r.ID, rooms.Update{
		Games: &rooms.Games{Level: rooms.GamesNight, Tune: &rooms.Tune{From: rooms.TuneFavorites, Typed: true, Clip: rooms.ClipChorus}},
	})
	if err != nil {
		t.Fatal(err)
	}
	g := rooms.ParseSettings(r.Settings).Games
	if got := g.TuneOf(); got != (rooms.Tune{From: rooms.TuneFavorites, Typed: true, Clip: rooms.ClipChorus}) {
		t.Errorf("after update %+v", got)
	}
	// The default clip spot isn't kept, so it follows a later default.
	if g.Tune.Clip != "" {
		t.Errorf("kept %+v", g.Tune)
	}
	r, err = s.Update(t.Context(), rooms.Actor{UserID: "ann"}, r.ID, rooms.Update{
		Games: &rooms.Games{Level: rooms.GamesNight, Tune: &rooms.Tune{From: rooms.TuneTonight}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if g := rooms.ParseSettings(r.Settings).Games; g.Tune != nil {
		t.Errorf("all defaults, kept %+v", g.Tune)
	}
}
