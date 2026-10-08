// SPDX-License-Identifier: AGPL-3.0-only

package games_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/awards"
	"github.com/madeofpendletonwool/syncphony/server/internal/games"
	"github.com/madeofpendletonwool/syncphony/server/internal/nights"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// facts knows every song came out in 1977.
type facts struct{}

func (facts) Song(context.Context, store.QueueItem, bool) (quiz.Facts, error) {
	return quiz.Facts{Song: quiz.Song{Title: "Heroes", Artist: "Bowie"}, Year: 1977, BPM: 113, Rank: 0.8}, nil
}

func (facts) Pool(context.Context, string, store.QueueItem, quiz.Facts) quiz.Pool { return quiz.Pool{} }

type fixture struct {
	db     *store.Store
	bus    *realtime.Local
	rooms  *rooms.Service
	engine *games.Engine
	room   store.Room
	sub    *realtime.Subscription
	items  []store.QueueItem
}

func setup(t *testing.T, g rooms.Games) *fixture {
	t.Helper()
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []string{"ann", "bob", "guest"} {
		if _, err := db.CreateUser(ctx, store.CreateUserParams{ID: id, Username: id, DisplayName: id, Color: "#000000", Role: store.RoleMember, CreatedAt: store.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	bus := realtime.NewLocal()
	rs := rooms.New(db, bus)
	room, err := rs.Create(ctx, "ann", "Party", "", rooms.Settings{Games: g})
	if err != nil {
		t.Fatal(err)
	}
	e := games.New(db, bus, rs, games.Config{Announce: 30 * time.Millisecond, RoundWindow: 300 * time.Millisecond, AmbientWindow: 300 * time.Millisecond, RevealFor: 60 * time.Millisecond, Seed: 1})
	e.Facts = facts{}
	t.Cleanup(e.Close)
	f := &fixture{db: db, bus: bus, rooms: rs, engine: e, room: room, sub: bus.Subscribe(realtime.RoomTopic(room.ID))}
	t.Cleanup(f.sub.Close)
	for i, who := range []string{"bob", "ann"} {
		meta, _ := json.Marshal(provider.Track{Title: "Song " + who, Artists: []provider.ArtistCredit{{Name: "Bowie"}}})
		it, err := db.AddQueueItem(ctx, store.AddQueueItemParams{
			ID: store.NewID(), RoomID: room.ID, AddedBy: who, Provider: "fake", TrackID: who, Metadata: string(meta),
			LanePosition: int64(i), Now: store.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		f.items = append(f.items, it)
	}
	return f
}

// play makes the room's playback state publish, as the playback engine would.
func (f *fixture) play(i int) {
	it := f.items[i]
	f.rooms.PublishNowPlaying(rooms.NowPlaying{RoomID: f.room.ID, State: "playing", Item: &it, At: store.Now()})
}

// round waits for the round to reach a state.
func (f *fixture) round(t *testing.T, state string) games.Round {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e := <-f.sub.C:
			if rd, ok := e.Data.(games.Round); ok && rd.State == state {
				return rd
			}
		case <-timeout:
			t.Fatalf("no round reached %s", state)
		}
	}
}

func (f *fixture) scores(t *testing.T) games.Scores {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e := <-f.sub.C:
			if s, ok := e.Data.(games.Scores); ok {
				return s
			}
		case <-timeout:
			t.Fatal("no scores")
		}
	}
}

var yearOnly = rooms.Games{Level: rooms.GamesRounds, Frequency: new(1), Enabled: map[string]bool{"liner": false, "sample": false, "lyrics": false}, NoGuests: true}

func TestRound(t *testing.T) {
	f := setup(t, yearOnly)
	ctx := t.Context()
	f.play(0)
	rd := f.round(t, games.StateAnnounce)
	if rd.Kind != rooms.GameYear || rd.Mode != games.ModeRound || rd.ItemID != f.items[0].ID || rd.Question.Correct != "1977" {
		t.Fatalf("%+v", rd)
	}
	if item, hides := f.engine.Hidden(f.room.ID); item != rd.ItemID || !slices.Contains(hides, quiz.HideNotes) {
		t.Errorf("hidden: %s %v", item, hides)
	}
	if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: "ann"}, false, quiz.Response{Number: new(1977)}); !errors.Is(err, games.ErrNoRound) {
		t.Errorf("an answer before it opened: %v", err)
	}
	rd = f.round(t, games.StateOpen)
	if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: "bob"}, false, quiz.Response{Number: new(1990)}); err != nil {
		t.Fatal(err)
	}
	// Bob changes his mind; the latest answer counts.
	if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: "bob"}, false, quiz.Response{Number: new(1979)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: "ann"}, false, quiz.Response{Number: new(1977)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: "guest"}, true, quiz.Response{Number: new(1977)}); !errors.Is(err, games.ErrGuestsCantPlay) {
		t.Errorf("a guest answered: %v", err)
	}
	var invalid *games.InvalidInputError
	if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: "ann"}, false, quiz.Response{Text: "1977"}); !errors.As(err, &invalid) {
		t.Errorf("a text answer to a number question: %v", err)
	}
	rd = f.round(t, games.StateReveal)
	ann, bob := rd.Answers["ann"], rd.Answers["bob"]
	if !ann.Correct || ann.Points < 500 || bob.Correct || bob.Points <= 0 || bob.Points >= ann.Points {
		t.Errorf("ann %+v, bob %+v", ann, bob)
	}
	if item, _ := f.engine.Hidden(f.room.ID); item != "" {
		t.Error("still hidden after the reveal")
	}
	s := f.scores(t)
	if s.Mode != rooms.ScoresBoard || len(s.Players) != 2 || s.Players[0].UserID != "ann" || s.Players[0].Correct != 1 {
		t.Errorf("scores %+v", s)
	}
	f.round(t, games.StateDone)
	if _, ok := f.engine.Current(f.room.ID); ok {
		t.Error("a round is still up")
	}
}

func TestStart(t *testing.T) {
	g := yearOnly
	g.Frequency = new(0) // only when someone starts one
	f := setup(t, g)
	ctx := t.Context()
	if _, err := f.engine.Start(ctx, f.room.ID, store.User{ID: "ann"}, ""); !errors.Is(err, games.ErrNothingPlaying) {
		t.Errorf("nothing playing: %v", err)
	}
	f.play(0)
	time.Sleep(50 * time.Millisecond)
	if _, ok := f.engine.Current(f.room.ID); ok {
		t.Fatal("a round started by itself")
	}
	if _, err := f.engine.Start(ctx, f.room.ID, store.User{ID: "bob"}, ""); !errors.Is(err, games.ErrForbidden) {
		t.Errorf("bob started one: %v", err)
	}
	if _, err := f.engine.Start(ctx, f.room.ID, store.User{ID: "bob", Role: store.RoleAdmin}, ""); err != nil {
		t.Errorf("an admin: %v", err)
	}
	if _, err := f.engine.Start(ctx, f.room.ID, store.User{ID: "ann"}, ""); !errors.Is(err, games.ErrRoundRunning) {
		t.Errorf("a second round: %v", err)
	}
	// The song changes: the round about it is revealed at once.
	f.round(t, games.StateAnnounce)
	f.play(1)
	if rd := f.round(t, games.StateReveal); rd.ItemID != f.items[0].ID {
		t.Errorf("%+v", rd)
	}
}

func TestGamesOff(t *testing.T) {
	f := setup(t, rooms.Games{})
	f.play(0)
	time.Sleep(50 * time.Millisecond)
	if _, ok := f.engine.Current(f.room.ID); ok {
		t.Error("a round in a room with games off")
	}
	if _, err := f.engine.Start(t.Context(), f.room.ID, store.User{ID: "ann"}, ""); !errors.Is(err, games.ErrGamesOff) {
		t.Errorf("%v", err)
	}
}

func TestPoints(t *testing.T) {
	opens := time.Unix(0, 0)
	closes := opens.Add(20 * time.Second)
	for _, tc := range []struct {
		closeness float64
		after     time.Duration
		want      int
	}{
		{1, 0, 1000},
		{1, 10 * time.Second, 750},
		{1, 20 * time.Second, 500},
		{0.5, 0, 500},
		{0, 0, 0},
	} {
		if got := games.Points(tc.closeness, opens.Add(tc.after), opens, closes); got != tc.want {
			t.Errorf("%v after %v: %d, want %d", tc.closeness, tc.after, got, tc.want)
		}
	}
}

func TestAwards(t *testing.T) {
	f := setup(t, yearOnly)
	ctx := t.Context()
	// Both songs play; ann wins the round in the first.
	f.play(0)
	rd := f.round(t, games.StateOpen)
	if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: "ann"}, false, quiz.Response{Number: new(1977)}); err != nil {
		t.Fatal(err)
	}
	f.round(t, games.StateReveal)
	f.scores(t)
	start := store.Now().Add(-time.Hour)
	for i, it := range f.items {
		p, err := f.db.StartPlay(ctx, store.StartPlayParams{ID: store.NewID(), RoomID: f.room.ID, QueueItemID: it.ID, StartedAt: start.Add(time.Duration(i) * 4 * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.db.EndPlay(ctx, store.EndPlayParams{EndedAt: nullTime(p.StartedAt.Add(4 * time.Minute)), EndReason: nullString(store.EndFinished), ID: p.ID}); err != nil {
			t.Fatal(err)
		}
	}
	ns := nights.New(f.db, f.bus)
	ns.Awards = f.engine.Awards
	n, err := ns.End(ctx, f.room.ID, store.User{ID: "ann"})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, a := range n.Awards {
		kinds[a.Kind] = a.UserID
	}
	if kinds[awards.TriviaChamp] != "ann" || kinds[awards.Opener] != "bob" {
		t.Errorf("awards %+v", n.Awards)
	}
	listed, err := ns.List(ctx, f.room.ID, 1)
	if err != nil || len(listed) != 1 || len(listed[0].Awards) != len(n.Awards) {
		t.Errorf("kept awards: %+v %v", listed, err)
	}
}

func nullTime(t time.Time) sql.NullTime { return sql.NullTime{Time: t, Valid: true} }

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
