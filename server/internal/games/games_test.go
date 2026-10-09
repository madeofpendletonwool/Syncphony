// SPDX-License-Identifier: AGPL-3.0-only

package games_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

// byTrack knows each song by its track ID.
type byTrack map[string]quiz.Facts

func (b byTrack) Song(_ context.Context, it store.QueueItem, _ bool) (quiz.Facts, error) {
	return b[it.TrackID], nil
}

func (byTrack) Pool(context.Context, string, store.QueueItem, quiz.Facts) quiz.Pool {
	return quiz.Pool{}
}

// music records what the engine asks of the room's music.
type music struct {
	mu     sync.Mutex
	calls  []string
	resume time.Duration
}

func (m *music) Break(_ context.Context, _, itemID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "break "+itemID)
	return nil
}

func (m *music) Resume(_ context.Context, _, itemID string, at time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "resume "+itemID)
	m.resume = at
	return nil
}

func (m *music) seen() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.calls)
}

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
	e := games.New(db, bus, rs, games.Config{Announce: 30 * time.Millisecond, RoundWindow: 300 * time.Millisecond, AmbientWindow: 300 * time.Millisecond, RevealFor: 60 * time.Millisecond, LyricLead: 200 * time.Millisecond, Seed: 1})
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
	if !ann.Correct || ann.Points < 500+games.ExactBonus || bob.Correct || bob.Points <= 0 || bob.Points >= ann.Points {
		t.Errorf("ann %+v, bob %+v", ann, bob)
	}
	if !ann.Closest || bob.Closest {
		t.Errorf("closest: ann %v, bob %v", ann.Closest, bob.Closest)
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

func TestClosestGuessScoresABonus(t *testing.T) {
	f := setup(t, yearOnly)
	ctx := t.Context()
	f.play(0)
	rd := f.round(t, games.StateOpen)
	for who, year := range map[string]int{"ann": 1980, "bob": 1981} {
		if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: who}, false, quiz.Response{Number: &year}); err != nil {
			t.Fatal(err)
		}
	}
	rd = f.round(t, games.StateReveal)
	ann, bob := rd.Answers["ann"], rd.Answers["bob"]
	if !ann.Closest || bob.Closest || ann.Correct {
		t.Errorf("ann %+v, bob %+v", ann, bob)
	}
	// Ann answered first and nearer, and the nearest scores a bonus on top.
	if ann.Points != games.Points(ann.Closeness, ann.At, rd.OpensAt, rd.ClosesAt)+games.ClosestBonus {
		t.Errorf("ann's points %d", ann.Points)
	}
}

func TestHigherOrLower(t *testing.T) {
	g := yearOnly
	g.Frequency = new(0)
	f := setup(t, g)
	ctx := t.Context()
	f.engine.Facts = byTrack{
		"bob": {Song: quiz.Song{Title: "Heroes", Artist: "Bowie"}, Year: 1977},
		"ann": {Song: quiz.Song{Title: "Smells Like Teen Spirit", Artist: "Nirvana"}, Year: 1991},
	}
	f.play(0)
	f.play(1)
	time.Sleep(20 * time.Millisecond)
	// Guess the year or higher or lower, at random: start rounds until it's the latter.
	for range 12 {
		rd, err := f.engine.Start(ctx, f.room.ID, store.User{ID: "ann"}, rooms.GameYear)
		if err != nil {
			t.Fatal(err)
		}
		if rd.Question.Topic != quiz.TopicHigherLower {
			f.round(t, games.StateDone)
			continue
		}
		if !strings.Contains(rd.Question.Prompt, "Heroes") || rd.Question.Correct != "Newer" {
			t.Fatalf("%+v", rd.Question)
		}
		f.round(t, games.StateOpen)
		if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: "bob"}, false, quiz.Response{Choice: new(rd.Question.CorrectIndex)}); err != nil {
			t.Fatal(err)
		}
		f.round(t, games.StateReveal)
		if s := f.scores(t); s.Best == nil || s.Best.UserID != "bob" || s.Best.Count != 1 {
			t.Errorf("best streak %+v", s.Best)
		}
		return
	}
	t.Fatal("never asked higher or lower")
}

func TestStreaks(t *testing.T) {
	f := setup(t, yearOnly)
	ctx := t.Context()
	at := store.Now().Add(-time.Hour)
	hl := `{"Kind":"year","Topic":"higher_lower"}`
	// Bob: right, right, wrong, right. Ann: right, sits two out, right.
	for i, answers := range []map[string]bool{
		{"bob": true, "ann": true}, {"bob": true}, {"bob": false}, {"bob": true, "ann": true},
	} {
		id := store.NewID()
		if err := f.db.CreateGameRound(ctx, store.CreateGameRoundParams{
			ID: id, RoomID: f.room.ID, Kind: rooms.GameYear, Question: hl, StartedAt: at.Add(time.Duration(i) * time.Minute), RevealedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
		for who, right := range answers {
			if err := f.db.CreateGameAnswer(ctx, store.CreateGameAnswerParams{RoundID: id, UserID: who, Answer: "{}", Correct: right, AnsweredAt: at}); err != nil {
				t.Fatal(err)
			}
		}
	}
	s, err := f.engine.Scores(ctx, f.room.ID)
	if err != nil {
		t.Fatal(err)
	}
	streaks := map[string][2]int{}
	for _, p := range s.Players {
		streaks[p.UserID] = [2]int{p.Streak, p.BestStreak}
	}
	if streaks["bob"] != [2]int{1, 2} || streaks["ann"] != [2]int{2, 2} || s.Best == nil || s.Best.Count != 2 {
		t.Errorf("streaks %v, best %+v", streaks, s.Best)
	}
}

// lyrics are a song whose fourth line is the one to ask about, 1.5s in.
var lyrics = quiz.Facts{
	Song: quiz.Song{Title: "Song", Artist: "Band"}, DurationMs: 200_000,
	Lyrics: []quiz.Line{
		{Ms: 0, Text: "first line of the song"},
		{Ms: 300, Text: "second line goes here"},
		{Ms: 600, Text: "third one comes along"},
		{Ms: 1500, Text: "dancing under neon lights tonight"},
	},
}

func TestBeatTheSingerWaitsForItsLine(t *testing.T) {
	g := rooms.Games{Level: rooms.GamesRounds, Frequency: new(0), Enabled: map[string]bool{"year": false, "liner": false, "sample": false}}
	f := setup(t, g)
	ctx := t.Context()
	f.engine.Facts = byTrack{"bob": lyrics}
	f.play(0)
	time.Sleep(20 * time.Millisecond)
	rd, err := f.engine.Start(ctx, f.room.ID, store.User{ID: "ann"}, rooms.GameLyrics)
	if err != nil {
		t.Fatal(err)
	}
	if rd.State != games.StatePending || rd.Question.AtMs != 1500 || rd.Question.Topic != quiz.TopicBlanks {
		t.Fatalf("%+v", rd)
	}
	// Nobody sees it until it's time to show the line.
	if _, ok := f.engine.Current(f.room.ID); ok {
		t.Error("a pending round is showing")
	}
	if _, _, ok := f.engine.HiddenLine(f.room.ID); ok {
		t.Error("a pending round hides its line")
	}
	f.round(t, games.StateAnnounce)
	open := f.round(t, games.StateOpen)
	if item, at, ok := f.engine.HiddenLine(f.room.ID); !ok || item != rd.ItemID || at != 1500 {
		t.Errorf("hidden line %s %d %v", item, at, ok)
	}
	// It closes as the singer gets to the line.
	if d := open.ClosesAt.Sub(open.OpensAt); d != 200*time.Millisecond {
		t.Errorf("open for %v", d)
	}
	if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: "bob"}, false, quiz.Response{Text: rd.Question.Correct}); err != nil {
		t.Fatal(err)
	}
	if rev := f.round(t, games.StateReveal); !rev.Answers["bob"].Correct {
		t.Errorf("%+v", rev.Answers["bob"])
	}
}

func TestPendingRoundGoesWithItsSong(t *testing.T) {
	g := rooms.Games{Level: rooms.GamesRounds, Frequency: new(0), Enabled: map[string]bool{"year": false, "liner": false, "sample": false}}
	f := setup(t, g)
	f.engine.Facts = byTrack{"bob": lyrics}
	f.play(0)
	time.Sleep(20 * time.Millisecond)
	if _, err := f.engine.Start(t.Context(), f.room.ID, store.User{ID: "ann"}, rooms.GameLyrics); err != nil {
		t.Fatal(err)
	}
	f.play(1)
	time.Sleep(20 * time.Millisecond)
	if _, err := f.engine.Start(t.Context(), f.room.ID, store.User{ID: "ann"}, ""); errors.Is(err, games.ErrRoundRunning) {
		t.Error("the skipped song's round is still running")
	}
}

func TestFinishTheLyricStopsTheMusic(t *testing.T) {
	g := rooms.Games{Level: rooms.GamesNight, Frequency: new(0), BreaksPerHour: new(1), Enabled: map[string]bool{
		"year": false, "liner": false, "sample": false, "lyrics": false, "tune": false,
	}}
	f := setup(t, g)
	ctx := t.Context()
	f.engine.Facts = byTrack{"bob": lyrics}
	m := &music{}
	f.play(0)
	time.Sleep(20 * time.Millisecond)
	// Without a way to stop the music, there's no such round.
	if _, err := f.engine.Start(ctx, f.room.ID, store.User{ID: "ann"}, rooms.GameFinishLyric); !errors.Is(err, games.ErrNoQuestion) {
		t.Fatalf("no music: %v", err)
	}
	f.engine.Music = m
	rd, err := f.engine.Start(ctx, f.room.ID, store.User{ID: "ann"}, rooms.GameFinishLyric)
	if err != nil {
		t.Fatal(err)
	}
	if !rd.Breaks || rd.Question.AtMs != 1500 || !strings.Contains(rd.Question.Prompt, "third one comes along") {
		t.Fatalf("%+v", rd)
	}
	f.round(t, games.StateOpen)
	f.round(t, games.StateReveal)
	f.round(t, games.StateDone)
	time.Sleep(20 * time.Millisecond)
	item := f.items[0].ID
	if got := m.seen(); !slices.Equal(got, []string{"break " + item, "resume " + item}) || m.resume != 1500*time.Millisecond {
		t.Errorf("music %v, resumed at %v", got, m.resume)
	}
	// One break an hour, and it's had it.
	if _, err := f.engine.Start(ctx, f.room.ID, store.User{ID: "ann"}, rooms.GameFinishLyric); !errors.Is(err, games.ErrNoQuestion) {
		t.Errorf("a second break: %v", err)
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
