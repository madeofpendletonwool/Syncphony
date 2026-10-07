// SPDX-License-Identifier: AGPL-3.0-only

package suggest_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/links"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/suggest"
	"github.com/madeofpendletonwool/syncphony/server/internal/vault"
)

// The fake's library: The Test Patterns (t01-t06), Null Island (t07-t12)
// and Sine Language (t13-t18). Each artist is most like the next.

type env struct {
	t     *testing.T
	db    *store.Store
	rooms *rooms.Service
	q     *queue.Service
	links *links.Service
	sg    *suggest.Service
	room  store.Room
	// alice owns the room.
	alice, bob member
	// pick is what Rand returns, modulo n.
	pick int

	mu  sync.Mutex
	now time.Time
}

type member struct {
	store.User
	link string
}

// newEnv returns a room for alice and bob, each with a link to a fake
// service made with opts.
func newEnv(t *testing.T, opts fake.Options) *env {
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
	e := &env{t: t, db: db, now: time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)}
	e.links = links.New(db, vault.New(k), reg, links.Config{BaseURL: "https://syncphony.example.com", Notifier: links.BusNotifier{Bus: bus}})
	e.rooms = rooms.New(db, bus)
	e.rooms.Now = e.clock
	e.q = queue.New(db, e.rooms, e.links)
	e.q.Now = e.clock
	e.sg = suggest.New(db, e.rooms, e.links)
	e.sg.Rand = func(n int) int { return e.pick % n }
	e.alice = e.member(opts.ID, "alice")
	e.bob = e.member(opts.ID, "bob")
	e.room, err = e.rooms.Create(t.Context(), e.alice.ID, "Living room", "", rooms.Settings{})
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

func (e *env) member(providerID, name string) member {
	e.t.Helper()
	if providerID == "" {
		providerID = "fake"
	}
	u, err := e.db.CreateUser(e.t.Context(), store.CreateUserParams{
		ID: store.NewID(), Username: name, DisplayName: name, Color: "#000000", Role: store.RoleMember, CreatedAt: store.Now(),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	l, err := e.links.LinkWithCredentials(e.t.Context(), u.ID, providerID, map[string]string{"username": fake.Username, "password": fake.Password})
	if err != nil {
		e.t.Fatal(err)
	}
	return member{User: u, link: l.ID}
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

func (e *env) suggest(m member, scope suggest.Scope, origin suggest.Origin, limit int, refresh bool) []suggest.Suggestion {
	e.t.Helper()
	out, err := e.sg.Suggest(e.t.Context(), suggest.Query{RoomID: e.room.ID, UserID: m.ID, Scope: scope, Origin: origin, Limit: limit, Refresh: refresh})
	if err != nil {
		e.t.Fatalf("Suggest: %v", err)
	}
	return out
}

func ids(ss []suggest.Suggestion) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Track.Ref.ID
	}
	return out
}

func TestYourVibe(t *testing.T) {
	e := newEnv(t, fake.Options{})
	mine := e.add(e.alice, "t01")
	e.play(mine.ID, store.EndFinished)
	e.play(e.add(e.bob, "t13").ID, store.EndFinished)

	got := e.suggest(e.alice, suggest.ScopeMine, suggest.OriginHistory, 5, false)
	// The rest of The Test Patterns are most like t01. Bob's song doesn't
	// seed alice's vibe, and played songs aren't suggested.
	if want := []string{"t02", "t03", "t04", "t05", "t06"}; !slices.Equal(ids(got), want) {
		t.Fatalf("suggested %v, want %v", ids(got), want)
	}
	for _, s := range got {
		if s.Seed.ID != mine.ID {
			t.Errorf("%s is like %s, want alice's t01", s.Track.Ref.ID, s.Seed.TrackID)
		}
		// Every suggestion is from a link alice can add from.
		if s.Track.Ref.LinkID != e.alice.link {
			t.Errorf("%s is from link %s, want alice's", s.Track.Ref.ID, s.Track.Ref.LinkID)
		}
	}
}

func TestGroupVibe(t *testing.T) {
	e := newEnv(t, fake.Options{})
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	waiting := e.add(e.bob, "t13")

	got := e.suggest(e.alice, suggest.ScopeGroup, suggest.OriginHistory, 4, false)
	// What's waiting seeds first, then what played; seeds take turns.
	if want := []string{"t14", "t02", "t15", "t03"}; !slices.Equal(ids(got), want) {
		t.Fatalf("suggested %v, want %v", ids(got), want)
	}
	if got[0].Seed.ID != waiting.ID || got[0].Seed.AddedBy != e.bob.ID {
		t.Errorf("first suggestion's seed = %+v, want bob's waiting t13", got[0].Seed)
	}
	// Bob's taste, from alice's own link.
	if got[0].Track.Ref.LinkID != e.alice.link {
		t.Errorf("suggested from link %s, want alice's", got[0].Track.Ref.LinkID)
	}
	// Bob's vibe is only his.
	for _, s := range e.suggest(e.bob, suggest.ScopeMine, suggest.OriginHistory, 10, false) {
		if s.Seed.AddedBy != e.bob.ID || s.Track.Ref.ID == "t13" {
			t.Errorf("bob's vibe has %s, like %s", s.Track.Ref.ID, s.Seed.TrackID)
		}
	}
}

func TestQueueVibe(t *testing.T) {
	e := newEnv(t, fake.Options{})
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	waiting := e.add(e.bob, "t07")

	// The queue origin reads what's queued, not what played: a queued
	// change of vibe is the vibe.
	got := e.suggest(e.alice, suggest.ScopeGroup, suggest.OriginQueue, 4, false)
	if want := []string{"t08", "t09", "t10", "t11"}; !slices.Equal(ids(got), want) {
		t.Fatalf("suggested %v, want the rest of Null Island %v", ids(got), want)
	}
	for _, s := range got {
		if s.Seed.ID != waiting.ID {
			t.Errorf("%s is like %s, want bob's waiting t07", s.Track.Ref.ID, s.Seed.TrackID)
		}
	}

	// With nothing queued to go on, what played through stands in.
	e.play(waiting.ID, store.EndFinished)
	fallback := ids(e.suggest(e.alice, suggest.ScopeMine, suggest.OriginQueue, 2, false))
	if want := []string{"t02", "t03"}; !slices.Equal(fallback, want) {
		t.Fatalf("nothing waiting: suggested %v, want %v", fallback, want)
	}
}

func TestNothingToGoOn(t *testing.T) {
	e := newEnv(t, fake.Options{})
	if got := e.suggest(e.alice, suggest.ScopeGroup, suggest.OriginHistory, 10, false); len(got) != 0 {
		t.Errorf("an empty room suggested %v", ids(got))
	}
	// A skipped song says the room didn't want it: it doesn't seed.
	e.play(e.add(e.alice, "t01").ID, store.EndSkipped)
	if got := e.suggest(e.alice, suggest.ScopeMine, suggest.OriginHistory, 10, false); len(got) != 0 {
		t.Errorf("a skipped song seeded %v", ids(got))
	}
}

func TestSearchOnlyService(t *testing.T) {
	// A service that can't recommend, like Spotify: more by the artist.
	e := newEnv(t, fake.Options{ID: "spotty", Name: "Spotty", NoRecommendations: true})
	e.play(e.add(e.alice, "t07").ID, store.EndFinished)

	got := e.suggest(e.alice, suggest.ScopeMine, suggest.OriginHistory, 10, false)
	if want := []string{"t08", "t09", "t10", "t11", "t12"}; !slices.Equal(ids(got), want) {
		t.Fatalf("suggested %v, want the rest of Null Island %v", ids(got), want)
	}
}

func TestCachedUntilRefresh(t *testing.T) {
	e := newEnv(t, fake.Options{})
	e.play(e.add(e.alice, "t01").ID, store.EndFinished)
	first := ids(e.suggest(e.alice, suggest.ScopeMine, suggest.OriginHistory, 3, false))

	e.pick = 5
	if again := ids(e.suggest(e.alice, suggest.ScopeMine, suggest.OriginHistory, 3, false)); !slices.Equal(again, first) {
		t.Errorf("the same queue suggested %v, then %v", first, again)
	}
	if fresh := ids(e.suggest(e.alice, suggest.ScopeMine, suggest.OriginHistory, 3, true)); slices.Equal(fresh, first) {
		t.Errorf("refresh suggested the same %v", fresh)
	}
	// A queue change makes a new list: the song just queued isn't in it.
	e.pick = 0
	e.add(e.alice, "t02")
	if got := ids(e.suggest(e.alice, suggest.ScopeMine, suggest.OriginHistory, 3, false)); slices.Contains(got, "t02") {
		t.Errorf("suggested %v, with t02 already waiting", got)
	}
}

func TestBadScope(t *testing.T) {
	e := newEnv(t, fake.Options{})
	_, err := e.sg.Suggest(t.Context(), suggest.Query{RoomID: e.room.ID, UserID: e.alice.ID, Scope: "theirs"})
	if !errors.Is(err, suggest.ErrScope) {
		t.Errorf("err = %v, want ErrScope", err)
	}
	_, err = e.sg.Suggest(t.Context(), suggest.Query{RoomID: e.room.ID, UserID: e.alice.ID, Scope: suggest.ScopeMine, Origin: "now"})
	if !errors.Is(err, suggest.ErrOrigin) {
		t.Errorf("err = %v, want ErrOrigin", err)
	}
	// An unset origin reads as history.
	if _, err = e.sg.Suggest(t.Context(), suggest.Query{RoomID: e.room.ID, UserID: e.alice.ID, Scope: suggest.ScopeMine}); err != nil {
		t.Errorf("unset origin: err = %v", err)
	}
	_, err = e.sg.Suggest(t.Context(), suggest.Query{RoomID: "nope", UserID: e.alice.ID, Scope: suggest.ScopeMine})
	if !errors.Is(err, rooms.ErrNotFound) {
		t.Errorf("missing room: err = %v, want rooms.ErrNotFound", err)
	}
}
