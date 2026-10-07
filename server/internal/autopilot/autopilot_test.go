// SPDX-License-Identifier: AGPL-3.0-only

package autopilot_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/autopilot"
	"github.com/madeofpendletonwool/syncphony/server/internal/links"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/stats"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/vault"
)

// The fake's library: The Test Patterns (t01-t06), Null Island (t07-t12)
// and Sine Language (t13-t18). Each artist is most like the next.

type env struct {
	t        *testing.T
	db       *store.Store
	bus      *realtime.Local
	rooms    *rooms.Service
	q        *queue.Service
	pilot    *autopilot.Service
	presence *presence
	player   *player
	room     store.Room
	// alice owns the room.
	alice, bob member

	mu  sync.Mutex
	now time.Time
}

type member struct {
	store.User
	link string
}

type presence struct{ ids []string }

func (p *presence) Members(string) []string { return append([]string(nil), p.ids...) }

// player stands in for the playback engine: the room has a speaker or not.
type player struct{ speaker bool }

func (p *player) NowPlaying(_ context.Context, roomID string) (rooms.NowPlaying, error) {
	np := rooms.NowPlaying{RoomID: roomID}
	if p.speaker {
		np.Player = &rooms.Player{DeviceID: "phone"}
	}
	return np, nil
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, fake.Options{})
}

// newEnvWith is newEnv with the fake service set up as opts says.
func newEnvWith(t *testing.T, opts fake.Options) *env {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg, err := provider.NewRegistry(fake.New(opts))
	if err != nil {
		t.Fatal(err)
	}
	k, _ := vault.ParseKey(vault.GenerateKey())
	bus := realtime.NewLocal()
	e := &env{t: t, db: db, bus: bus, presence: &presence{}, player: &player{speaker: true}, now: time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)}
	ls := links.New(db, vault.New(k), reg, links.Config{BaseURL: "https://syncphony.example.com", Notifier: links.BusNotifier{Bus: bus}})
	e.rooms = rooms.New(db, bus)
	e.rooms.Now = e.clock
	e.q = queue.New(db, e.rooms, ls)
	e.q.Now = e.clock
	e.pilot = autopilot.New(db, e.rooms, e.q, ls, e.presence)
	e.pilot.Player = e.player
	e.pilot.Rand = func(int) int { return 0 }
	e.pilot.Now = e.clock
	t.Cleanup(e.pilot.Close)
	e.alice = e.member(ls, "alice")
	e.bob = e.member(ls, "bob")
	e.presence.ids = []string{e.alice.ID, e.bob.ID}
	e.room, err = e.rooms.Create(t.Context(), e.alice.ID, "Living room", "", rooms.Settings{Autopilot: rooms.Autopilot{On: true}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// clock advances a second on every read, so items get distinct times.
func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = e.now.Add(time.Second)
	return e.now
}

func (e *env) member(ls *links.Service, name string) member {
	e.t.Helper()
	u, err := e.db.CreateUser(e.t.Context(), store.CreateUserParams{
		ID: store.NewID(), Username: name, DisplayName: name, Color: "#000000", Role: store.RoleMember, CreatedAt: store.Now(),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	l, err := ls.LinkWithCredentials(e.t.Context(), u.ID, "fake", map[string]string{"username": fake.Username, "password": fake.Password})
	if err != nil {
		e.t.Fatal(err)
	}
	return member{User: u, link: l.ID}
}

func (e *env) settings(a rooms.Autopilot) {
	e.t.Helper()
	if _, err := e.rooms.Update(e.t.Context(), rooms.Actor{UserID: e.alice.ID}, e.room.ID, rooms.Update{Autopilot: &a}); err != nil {
		e.t.Fatal(err)
	}
}

// add queues a fake track for m and returns its item.
func (e *env) add(m member, trackID string) store.QueueItem {
	e.t.Helper()
	snap, err := e.q.Add(e.t.Context(), e.room.ID, m.ID, []queue.TrackRef{{LinkID: m.link, TrackID: trackID}})
	if err != nil {
		e.t.Fatalf("Add: %v", err)
	}
	for _, it := range snap.Items {
		if it.TrackID == trackID && it.AddedBy == m.ID && it.State == store.ItemQueued {
			return it
		}
	}
	e.t.Fatalf("%s isn't queued", trackID)
	return store.QueueItem{}
}

// play plays an item through to its end, as the playback engine would:
// finished, or skipped.
func (e *env) play(itemID, reason string) {
	e.t.Helper()
	_, err := e.q.Change(e.t.Context(), e.room.ID, func(q *store.Queries, _ store.Room) error {
		ctx := e.t.Context()
		start := e.clock()
		if err := q.SetQueueItemState(ctx, store.SetQueueItemStateParams{State: store.ItemPlaying, UpdatedAt: start, ID: itemID}); err != nil {
			return err
		}
		if _, err := q.StartPlay(ctx, store.StartPlayParams{ID: store.NewID(), RoomID: e.room.ID, QueueItemID: itemID, StartedAt: start}); err != nil {
			return err
		}
		state := store.ItemPlayed
		if reason != store.EndFinished {
			state = store.ItemSkipped
		}
		end := e.clock()
		if err := q.SetQueueItemState(ctx, store.SetQueueItemStateParams{State: state, UpdatedAt: end, ID: itemID}); err != nil {
			return err
		}
		return q.EndOpenPlays(ctx, store.EndOpenPlaysParams{
			EndedAt: sql.NullTime{Time: end, Valid: true}, EndReason: sql.NullString{String: reason, Valid: true}, RoomID: e.room.ID,
		})
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

// fill runs autopilot and returns what it queued, or nil.
func (e *env) fill() *store.QueueItem {
	e.t.Helper()
	if err := e.pilot.Fill(e.t.Context(), e.room.ID); err != nil {
		e.t.Fatalf("Fill: %v", err)
	}
	snap, err := e.rooms.QueueSnapshot(e.t.Context(), e.room.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	var out *store.QueueItem
	for _, it := range snap.Items {
		if it.IsAutopilot() && it.State == store.ItemQueued {
			if out != nil {
				e.t.Fatalf("more than one autopilot song is waiting")
			}
			out = &it
		}
	}
	return out
}

func TestFillsWhenDry(t *testing.T) {
	e := newEnv(t)
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	seed, _ := e.q.Item(t.Context(), e.room.ID, mustHistory(e)[0].QueueItem.ID)

	got := e.fill()
	if got == nil {
		t.Fatal("autopilot added nothing")
	}
	// The rest of The Test Patterns are most like t01.
	if got.TrackID != "t02" || got.AddedBy != e.alice.ID || got.LinkID.String != e.alice.link {
		t.Errorf("added %s for %s from %s, want t02 for alice from her link", got.TrackID, got.AddedBy, got.LinkID.String)
	}
	info, ok := queue.ParseAutopilot(*got)
	if !ok || info.SeedItemID != seed.ID || info.SeedTitle != "Reference Tone" || info.SeedArtist != "The Test Patterns" {
		t.Errorf("info = %+v, %v", info, ok)
	}
	// One at a time: with a song waiting, it adds nothing more.
	if again := e.fill(); again == nil || again.ID != got.ID {
		t.Errorf("a second fill changed the queue: %+v", again)
	}
}

func TestOnlyWhenItShould(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		e := newEnv(t)
		e.settings(rooms.Autopilot{On: false})
		e.play(e.add(e.alice, "t01").ID, store.EndFinished)
		if got := e.fill(); got != nil {
			t.Errorf("added %s with autopilot off", got.TrackID)
		}
	})
	t.Run("no speaker", func(t *testing.T) {
		e := newEnv(t)
		e.player.speaker = false
		e.play(e.add(e.alice, "t01").ID, store.EndFinished)
		if got := e.fill(); got != nil {
			t.Errorf("added %s with nobody listening", got.TrackID)
		}
	})
	t.Run("nothing played", func(t *testing.T) {
		e := newEnv(t)
		if got := e.fill(); got != nil {
			t.Errorf("added %s to a room with no history", got.TrackID)
		}
	})
	t.Run("songs waiting", func(t *testing.T) {
		e := newEnv(t)
		e.play(e.add(e.alice, "t01").ID, store.EndFinished)
		e.add(e.bob, "t13")
		if got := e.fill(); got != nil {
			t.Errorf("added %s with bob's song waiting", got.TrackID)
		}
	})
}

func TestMembersGoFirst(t *testing.T) {
	e := newEnv(t)
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	auto := e.fill()
	mine := e.add(e.bob, "t13")
	snap, err := e.rooms.QueueSnapshot(t.Context(), e.room.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(snap.UpNext, ","), mine.ID+","+auto.ID; got != want {
		t.Errorf("up next = %s, want bob's song then autopilot's", got)
	}
}

func TestNotAnyonesTurn(t *testing.T) {
	e := newEnv(t)
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	e.play(e.add(e.bob, "t13").ID, store.EndFinished)
	// Autopilot's song is for bob (he played last), and plays.
	auto := e.fill()
	if auto.AddedBy != e.bob.ID {
		t.Fatalf("autopilot chose for %s, want bob", auto.AddedBy)
	}
	e.play(auto.ID, store.EndFinished)
	// Alice has still waited longest since her turn.
	b := e.add(e.bob, "t15")
	a := e.add(e.alice, "t03")
	snap, err := e.rooms.QueueSnapshot(t.Context(), e.room.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(snap.UpNext, ","), a.ID+","+b.ID; got != want {
		t.Errorf("up next = %s, want alice then bob", got)
	}
	// And it isn't counted as bob's in stats.
	plays, err := e.rooms.Plays(t.Context(), e.room.ID, time.Time{}, rooms.Forever)
	if err != nil {
		t.Fatal(err)
	}
	sum := stats.Summarize(plays)
	if sum.Plays != 3 {
		t.Errorf("room plays = %d, want 3", sum.Plays)
	}
	for _, p := range sum.People {
		if p.Plays != 1 {
			t.Errorf("%s has %d plays, want 1", p.UserID, p.Plays)
		}
	}
}

func TestTakesTurns(t *testing.T) {
	e := newEnv(t)
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	e.play(e.add(e.bob, "t13").ID, store.EndFinished)
	var got []string
	for range 4 {
		it := e.fill()
		if it == nil {
			t.Fatal("autopilot added nothing")
		}
		who := "alice"
		if it.AddedBy == e.bob.ID {
			who = "bob"
		}
		got = append(got, who+":"+it.TrackID)
		e.play(it.ID, store.EndFinished)
	}
	// Bob played last, so he's first; then they alternate, each seeded by
	// their own song, never repeating one.
	if want := "bob:t14,alice:t02,bob:t15,alice:t03"; strings.Join(got, ",") != want {
		t.Errorf("autopilot played %s, want %s", strings.Join(got, ","), want)
	}
}

func TestPrefersPeopleHere(t *testing.T) {
	e := newEnv(t)
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	e.play(e.add(e.bob, "t13").ID, store.EndFinished)
	e.presence.ids = []string{e.alice.ID} // bob went home
	if it := e.fill(); it == nil || it.AddedBy != e.alice.ID {
		t.Errorf("autopilot chose for %+v, want alice, who's here", it)
	}
}

func TestDiscovery(t *testing.T) {
	e := newEnv(t)
	e.settings(rooms.Autopilot{On: true, Adventure: rooms.AdventureDiscovery})
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	e.pilot.Rand = func(n int) int { return n - 1 }
	it := e.fill()
	// Other artists only, reaching to the end of the list: Sine Language.
	if it == nil || it.TrackID != "t18" {
		t.Errorf("discovery added %+v, want t18", it)
	}
}

func TestAvoidsSkippedArtists(t *testing.T) {
	e := newEnv(t)
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	first := e.fill()
	if first.TrackID != "t02" {
		t.Fatalf("first pick %s, want t02", first.TrackID)
	}
	e.play(first.ID, store.EndSkipped)
	// The room skipped The Test Patterns: move on to Null Island.
	if it := e.fill(); it == nil || it.TrackID != "t07" {
		t.Errorf("after a skip, added %+v, want t07", it)
	}
}

func TestRemovedSongsStayGone(t *testing.T) {
	e := newEnv(t)
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	first := e.fill()
	// Anyone may remove autopilot's song, even though it's for alice; nobody may move it.
	if _, err := e.q.Move(t.Context(), e.room.ID, e.alice.ID, first.ID, 0); !errors.Is(err, queue.ErrForbidden) {
		t.Errorf("Move: got %v, want ErrForbidden", err)
	}
	if _, err := e.q.Remove(t.Context(), e.room.ID, e.bob.ID, first.ID); err != nil {
		t.Fatalf("bob removing autopilot's song: %v", err)
	}
	if it := e.fill(); it == nil || it.TrackID != "t03" {
		t.Errorf("after removing t02, added %+v, want t03", it)
	}
}

func TestRunsOutGracefully(t *testing.T) {
	e := newEnv(t)
	// The room has heard the whole library.
	for i := 1; i <= 18; i++ {
		e.play(e.add(e.alice, fmt.Sprintf("t%02d", i)).ID, store.EndFinished)
	}
	sub := e.bus.Subscribe(realtime.RoomTopic(e.room.ID))
	defer sub.Close()
	if it := e.fill(); it != nil {
		t.Errorf("added %s, which the room just heard", it.TrackID)
	}
	if it := e.fill(); it != nil {
		t.Errorf("added %s on the second try", it.TrackID)
	}
	notices := 0
	for len(sub.C) > 0 {
		if ev := <-sub.C; ev.Type == realtime.PlaybackNotice {
			notices++
		}
	}
	if notices != 1 {
		t.Errorf("%d notices, want 1: tell the room once, not on every check", notices)
	}
}

func TestRandomWhenNothingIsLike(t *testing.T) {
	e := newEnv(t)
	// Bob has played all of Sine Language and The Test Patterns: everything
	// like his songs. Only random songs (Null Island) are left.
	for i := 1; i <= 18; i++ {
		if i < 7 || i > 12 {
			e.play(e.add(e.bob, fmt.Sprintf("t%02d", i)).ID, store.EndFinished)
		}
	}
	it := e.fill()
	// The fake's random order starts t01, t08: t08 is the first not heard.
	if it == nil || it.TrackID != "t08" || it.AddedBy != e.bob.ID {
		t.Fatalf("added %+v, want t08 for bob", it)
	}
	if info, _ := queue.ParseAutopilot(*it); info.SeedItemID != "" {
		t.Errorf("a random song has seed %+v", info)
	}
}

func mustHistory(e *env) []store.ListHistoryRow {
	e.t.Helper()
	h, err := e.db.ListHistory(e.t.Context(), store.ListHistoryParams{RoomID: e.room.ID, Limit: 10})
	if err != nil {
		e.t.Fatal(err)
	}
	return h
}

func TestKickedByQueueChanges(t *testing.T) {
	e := newEnv(t)
	e.q.OnChange = e.pilot.Kick
	// The song ending empties the queue, which kicks autopilot.
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, err := e.rooms.QueueSnapshot(t.Context(), e.room.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(snap.UpNext) == 1 {
			return
		}
		if len(snap.UpNext) > 1 {
			t.Fatalf("autopilot queued %d songs, want 1", len(snap.UpNext))
		}
		if time.Now().After(deadline) {
			t.Fatal("autopilot never filled the queue")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
