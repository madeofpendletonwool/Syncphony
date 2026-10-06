// SPDX-License-Identifier: AGPL-3.0-only

package queue_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
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
	"github.com/madeofpendletonwool/syncphony/server/internal/vault"
)

type env struct {
	t     *testing.T
	db    *store.Store
	bus   realtime.Bus
	q     *queue.Service
	links *links.Service
	room  store.Room
	// alice owns the room.
	alice, bob member

	mu  sync.Mutex
	now time.Time
}

type member struct {
	store.User
	link string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// The fake won't play t05, as Spotify won't play some tracks.
	// fake2 has the same catalog (and ISRCs) under another service.
	reg, err := provider.NewRegistry(fake.New(fake.Options{NotPlayable: []string{"t05"}}), fake.New(fake.Options{ID: "fake2", Name: "Fake 2"}))
	if err != nil {
		t.Fatal(err)
	}
	k, _ := vault.ParseKey(vault.GenerateKey())
	bus := realtime.NewLocal()
	e := &env{t: t, db: db, bus: bus, now: time.Date(2026, 10, 5, 20, 0, 0, 0, time.UTC)}
	e.links = links.New(db, vault.New(k), reg, links.Config{BaseURL: "https://syncphony.example.com", Notifier: links.BusNotifier{Bus: bus}})
	e.q = queue.New(db, rooms.New(db, bus), e.links)
	e.q.Now = e.clock
	e.alice, e.bob = e.member("alice"), e.member("bob")
	e.room = e.newRoom(e.alice.ID, store.FairnessRoundRobin)
	return e
}

// clock advances a second on every read, so items get distinct times.
func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = e.now.Add(time.Second)
	return e.now
}

func (e *env) member(name string) member {
	e.t.Helper()
	u, err := e.db.CreateUser(e.t.Context(), store.CreateUserParams{
		ID: store.NewID(), Username: name, DisplayName: name, Color: "#000000", Role: store.RoleMember, CreatedAt: store.Now(),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	l, err := e.links.LinkWithCredentials(e.t.Context(), u.ID, "fake", map[string]string{"username": fake.Username, "password": fake.Password})
	if err != nil {
		e.t.Fatal(err)
	}
	return member{User: u, link: l.ID}
}

func (e *env) newRoom(owner, mode string) store.Room {
	e.t.Helper()
	r, err := e.db.CreateRoom(e.t.Context(), store.CreateRoomParams{
		ID: store.NewID(), Name: "Living room", OwnerID: owner, FairnessMode: mode, Settings: "{}", CreatedAt: store.Now(),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

// add queues fake tracks (t01, t02, ...) for m and returns the snapshot.
func (e *env) add(m member, trackIDs ...string) rooms.QueueSnapshot {
	e.t.Helper()
	snap, err := e.q.Add(e.t.Context(), e.room.ID, m.ID, refs(m, trackIDs...))
	if err != nil {
		e.t.Fatalf("Add: %v", err)
	}
	return snap
}

func refs(m member, trackIDs ...string) []queue.TrackRef {
	out := make([]queue.TrackRef, len(trackIDs))
	for i, id := range trackIDs {
		out[i] = queue.TrackRef{LinkID: m.link, TrackID: id}
	}
	return out
}

// upNext describes the play order as "user:track" pairs.
func upNext(snap rooms.QueueSnapshot, ms ...member) string {
	names := map[string]string{}
	for _, m := range ms {
		names[m.ID] = m.Username
	}
	byID := map[string]store.QueueItem{}
	for _, it := range snap.Items {
		byID[it.ID] = it
	}
	var out []string
	for _, id := range snap.UpNext {
		it := byID[id]
		out = append(out, names[it.AddedBy]+":"+it.TrackID)
	}
	return strings.Join(out, " ")
}

// itemID finds the queued item for track in m's lane.
func itemID(t *testing.T, snap rooms.QueueSnapshot, m member, track string) string {
	t.Helper()
	for _, it := range snap.Items {
		if it.AddedBy == m.ID && it.TrackID == track && it.State == store.ItemQueued {
			return it.ID
		}
	}
	t.Fatalf("no queued %s for %s", track, m.Username)
	return ""
}

func TestAddInterleavesLanes(t *testing.T) {
	e := newEnv(t)
	sub := e.bus.Subscribe(realtime.RoomTopic(e.room.ID))
	defer sub.Close()

	e.add(e.alice, "t01", "t02", "t03")
	snap := e.add(e.bob, "t04")
	if got, want := upNext(snap, e.alice, e.bob), "alice:t01 bob:t04 alice:t02 alice:t03"; got != want {
		t.Errorf("up next = %q, want %q", got, want)
	}
	if snap.Version != 2 {
		t.Errorf("version %d after two adds, want 2", snap.Version)
	}
	// Metadata is snapshotted from the service.
	if it := snap.Items[slices.IndexFunc(snap.Items, func(it store.QueueItem) bool { return it.TrackID == "t04" })]; !strings.Contains(it.Metadata, `"Title":"SMPTE"`) || it.Provider != "fake" {
		t.Errorf("item %+v", it)
	}

	for want := int64(1); want <= 2; want++ {
		select {
		case ev := <-sub.C:
			if ev.Type != realtime.QueueUpdated || ev.Version != want || len(ev.Data.(rooms.QueueSnapshot).UpNext) == 0 {
				t.Errorf("event %d: %+v", want, ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("no event for version %d", want)
		}
	}
}

func TestAddErrors(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	for _, tc := range []struct {
		name string
		room string
		refs []queue.TrackRef
		want func(error) bool
	}{
		{"nothing", e.room.ID, nil, isInvalid},
		{"too many", e.room.ID, make([]queue.TrackRef, queue.MaxAdd+1), isInvalid},
		{"missing track ID", e.room.ID, []queue.TrackRef{{LinkID: e.alice.link}}, isInvalid},
		{"someone else's link", e.room.ID, refs(e.bob, "t01"), is(links.ErrNotFound)},
		{"unknown track, after a good one", e.room.ID, refs(e.alice, "t01", "nope"), is(provider.ErrNotFound)},
		{"unknown room", "nope", refs(e.alice, "t01"), is(rooms.ErrNotFound)},
		{"a song the service won't play", e.room.ID, refs(e.alice, "t05"), is(provider.ErrNotPlayable)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := e.q.Add(ctx, tc.room, e.alice.ID, tc.refs); !tc.want(err) {
				t.Errorf("got %v", err)
			}
		})
	}
	snap, err := rooms.New(e.db, e.bus).QueueSnapshot(ctx, e.room.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Items) != 0 || snap.Version != 0 {
		t.Errorf("failed adds changed the queue: %d items, version %d", len(snap.Items), snap.Version)
	}
}

func TestAddNotPlayable(t *testing.T) {
	e := newEnv(t)
	_, err := e.q.Add(t.Context(), e.room.ID, e.alice.ID, refs(e.alice, "t05"))
	var np *queue.NotPlayableError
	if !errors.As(err, &np) || np.Service != "Fake" || !strings.Contains(err.Error(), "Fake won't let Syncphony play") {
		t.Fatalf("adding t05: %v", err)
	}
	// Adding several songs doesn't check them: playback skips t05 later.
	snap := e.add(e.alice, "t04", "t05")
	if got, want := upNext(snap, e.alice), "alice:t04 alice:t05"; got != want {
		t.Errorf("up next = %q, want %q", got, want)
	}
}

func isInvalid(err error) bool {
	var invalid *queue.InvalidInputError
	return errors.As(err, &invalid)
}

func is(target error) func(error) bool { return func(err error) bool { return errors.Is(err, target) } }

func TestMove(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.add(e.alice, "t01", "t02", "t03")
	snap := e.add(e.bob, "t04")

	snap, err := e.q.Move(ctx, e.room.ID, e.alice.ID, itemID(t, snap, e.alice, "t03"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := upNext(snap, e.alice, e.bob), "alice:t03 bob:t04 alice:t01 alice:t02"; got != want {
		t.Errorf("after moving t03 to the front: %q, want %q", got, want)
	}
	// Past the end means the end.
	if snap, err = e.q.Move(ctx, e.room.ID, e.alice.ID, itemID(t, snap, e.alice, "t03"), 99); err != nil {
		t.Fatal(err)
	}
	if got, want := upNext(snap, e.alice, e.bob), "alice:t01 bob:t04 alice:t02 alice:t03"; got != want {
		t.Errorf("after moving t03 to the end: %q, want %q", got, want)
	}

	if _, err := e.q.Move(ctx, e.room.ID, e.bob.ID, itemID(t, snap, e.alice, "t01"), 0); !errors.Is(err, queue.ErrForbidden) {
		t.Errorf("moving someone else's song: %v", err)
	}
	// The owner can remove anyone's songs, but not reorder their lane.
	if _, err := e.q.Move(ctx, e.room.ID, e.alice.ID, itemID(t, snap, e.bob, "t04"), 0); !errors.Is(err, queue.ErrForbidden) {
		t.Errorf("owner reordering someone's lane: %v", err)
	}
	other := e.newRoom(e.bob.ID, store.FairnessRoundRobin)
	if _, err := e.q.Move(ctx, other.ID, e.alice.ID, itemID(t, snap, e.alice, "t01"), 0); !errors.Is(err, queue.ErrNotFound) {
		t.Errorf("moving an item through another room: %v", err)
	}
}

func TestRemove(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.add(e.alice, "t01", "t02")
	snap := e.add(e.bob, "t04", "t05")

	if _, err := e.q.Remove(ctx, e.room.ID, e.bob.ID, itemID(t, snap, e.alice, "t01")); !errors.Is(err, queue.ErrForbidden) {
		t.Errorf("removing someone else's song: %v", err)
	}
	snap, err := e.q.Remove(ctx, e.room.ID, e.bob.ID, itemID(t, snap, e.bob, "t04"))
	if err != nil {
		t.Fatalf("removing your own: %v", err)
	}
	removed := itemID(t, snap, e.alice, "t01")
	if _, err = e.q.Remove(ctx, e.room.ID, e.alice.ID, itemID(t, snap, e.bob, "t05")); err != nil {
		t.Fatalf("owner removing someone's song: %v", err)
	}
	if snap, err = e.q.Remove(ctx, e.room.ID, e.alice.ID, removed); err != nil {
		t.Fatal(err)
	}
	if got, want := upNext(snap, e.alice, e.bob), "alice:t02"; got != want {
		t.Errorf("up next %q, want %q", got, want)
	}
	if _, err := e.q.Remove(ctx, e.room.ID, e.alice.ID, removed); !errors.Is(err, queue.ErrNotQueued) {
		t.Errorf("removing twice: %v", err)
	}
	if _, err := e.q.Remove(ctx, e.room.ID, e.alice.ID, "nope"); !errors.Is(err, queue.ErrNotFound) {
		t.Errorf("removing a missing item: %v", err)
	}
}

// TestPlayback drives the queue the way the playback engine will, through
// Change, and checks that what's playing and who played last shape the order.
func TestPlayback(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.add(e.alice, "t01", "t02")
	snap := e.add(e.bob, "t04", "t05")
	carol := e.member("carol")

	// Play alice's first song.
	play := func(id string) rooms.QueueSnapshot {
		t.Helper()
		snap, err := e.q.Change(ctx, e.room.ID, func(q *store.Queries, _ store.Room) error {
			if p, err := q.GetPlaying(ctx, e.room.ID); err == nil {
				if err := q.SetQueueItemState(ctx, store.SetQueueItemStateParams{State: store.ItemPlayed, UpdatedAt: e.clock(), ID: p.ID}); err != nil {
					return err
				}
			}
			if err := q.SetQueueItemState(ctx, store.SetQueueItemStateParams{State: store.ItemPlaying, UpdatedAt: e.clock(), ID: id}); err != nil {
				return err
			}
			_, err := q.StartPlay(ctx, store.StartPlayParams{ID: store.NewID(), RoomID: e.room.ID, QueueItemID: id, StartedAt: e.clock()})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	snap = play(snap.UpNext[0])
	if got, want := upNext(snap, e.alice, e.bob), "bob:t04 alice:t02 bob:t05"; got != want {
		t.Errorf("while alice plays: %q, want %q", got, want)
	}
	// Carol arrives mid-song. Bob hasn't had a turn either and has waited
	// longer, so he's first, but carol goes before alice.
	snap, err := e.q.Add(ctx, e.room.ID, carol.ID, refs(carol, "t07"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := upNext(snap, e.alice, e.bob, carol), "bob:t04 carol:t07 alice:t02 bob:t05"; got != want {
		t.Errorf("after carol joins: %q, want %q", got, want)
	}
	snap = play(snap.UpNext[0])
	snap = play(snap.UpNext[0])
	// Alice played longest ago now, so she's ahead of bob again.
	if got, want := upNext(snap, e.alice, e.bob, carol), "alice:t02 bob:t05"; got != want {
		t.Errorf("after carol and bob play: %q, want %q", got, want)
	}
	// Alice queues more; her lane continues after her turn.
	snap, err = e.q.Add(ctx, e.room.ID, e.alice.ID, refs(e.alice, "t03"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := upNext(snap, e.alice, e.bob, carol), "alice:t02 bob:t05 alice:t03"; got != want {
		t.Errorf("after alice adds: %q, want %q", got, want)
	}
	if _, err := e.q.Remove(ctx, e.room.ID, e.alice.ID, func() string {
		p, _ := e.db.GetPlaying(ctx, e.room.ID)
		return p.ID
	}()); !errors.Is(err, queue.ErrNotQueued) {
		t.Errorf("removing the playing song: %v", err)
	}
}

func TestFIFORoom(t *testing.T) {
	e := newEnv(t)
	e.room = e.newRoom(e.alice.ID, store.FairnessFIFO)
	e.add(e.alice, "t01", "t02")
	snap := e.add(e.bob, "t04")
	if got, want := upNext(snap, e.alice, e.bob), "alice:t01 alice:t02 bob:t04"; got != want {
		t.Errorf("up next %q, want %q", got, want)
	}
}

// TestConcurrentChanges checks that changes are serialized: every change
// gets its own version and lanes keep distinct positions.
func TestConcurrentChanges(t *testing.T) {
	e := newEnv(t)
	const n = 20
	var wg sync.WaitGroup
	versions := make(chan int64, n)
	for i := range n {
		m := e.alice
		if i%2 == 1 {
			m = e.bob
		}
		wg.Go(func() {
			snap, err := e.q.Add(t.Context(), e.room.ID, m.ID, refs(m, "t01"))
			if err != nil {
				t.Error(err)
				return
			}
			versions <- snap.Version
		})
	}
	wg.Wait()
	close(versions)
	seen := map[int64]bool{}
	for v := range versions {
		if seen[v] {
			t.Errorf("version %d used twice", v)
		}
		seen[v] = true
	}
	snap, err := rooms.New(e.db, e.bus).QueueSnapshot(t.Context(), e.room.ID)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]bool{}
	for _, it := range snap.Items {
		k := fmt.Sprint(it.AddedBy, "/", it.LanePosition)
		if pos[k] {
			t.Errorf("two items at lane position %d", it.LanePosition)
		}
		pos[k] = true
	}
	if len(snap.Items) != n || len(snap.UpNext) != n || snap.Version != n {
		t.Errorf("%d items, %d up next, version %d; want %d each", len(snap.Items), len(snap.UpNext), snap.Version, n)
	}
}

// setSettings stores raw room settings, as rooms.Service would.
func (e *env) setSettings(raw string) {
	e.t.Helper()
	r, err := e.db.UpdateRoom(e.t.Context(), store.UpdateRoomParams{Name: e.room.Name, FairnessMode: e.room.FairnessMode, Settings: raw, ID: e.room.ID})
	if err != nil {
		e.t.Fatal(err)
	}
	e.room = r
}

func TestRepeatGuard(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.setSettings(`{"fairness":{"repeatWindowMinutes":60}}`)
	e.add(e.alice, "t01", "t02")

	var repeat *queue.RepeatError
	if _, err := e.q.Add(ctx, e.room.ID, e.bob.ID, refs(e.bob, "t01")); !errors.As(err, &repeat) || repeat.Minutes != 60 || !strings.Contains(err.Error(), "hour") {
		t.Fatalf("queued song again: %v", err)
	}
	// A bigger add leaves the repeats out, including one given twice.
	snap := e.add(e.bob, "t01", "t03", "t04", "t04")
	var bobs []string
	for _, it := range snap.Items {
		if it.AddedBy == e.bob.ID {
			bobs = append(bobs, it.TrackID)
		}
	}
	if got := strings.Join(bobs, " "); got != "t03 t04" {
		t.Fatalf("bob's lane: %q", got)
	}
	// The same song from another service is a repeat too (same ISRC).
	l2, err := e.links.LinkWithCredentials(ctx, e.bob.ID, "fake2", map[string]string{"username": fake.Username, "password": fake.Password})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.Add(ctx, e.room.ID, e.bob.ID, []queue.TrackRef{{LinkID: l2.ID, TrackID: "t02"}}); !errors.As(err, &repeat) {
		t.Fatalf("same song on another service: %v", err)
	}

	// Once t01 has played and the window has passed, it can come back.
	id := itemID(t, snap, e.alice, "t01")
	if _, err := e.q.Change(ctx, e.room.ID, func(q *store.Queries, _ store.Room) error {
		if err := q.SetQueueItemState(ctx, store.SetQueueItemStateParams{State: store.ItemPlayed, UpdatedAt: e.clock(), ID: id}); err != nil {
			return err
		}
		_, err := q.StartPlay(ctx, store.StartPlayParams{ID: store.NewID(), RoomID: e.room.ID, QueueItemID: id, StartedAt: e.clock()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.Add(ctx, e.room.ID, e.bob.ID, refs(e.bob, "t01")); !errors.As(err, &repeat) {
		t.Fatalf("just played: %v", err)
	}
	e.mu.Lock()
	e.now = e.now.Add(61 * time.Minute)
	e.mu.Unlock()
	e.add(e.bob, "t01")

	// With the guard off, anything goes.
	e.setSettings(`{}`)
	e.add(e.alice, "t03")
}

func TestFairnessOptions(t *testing.T) {
	e := newEnv(t)
	e.room = e.newRoom(e.alice.ID, store.FairnessFIFO)
	e.setSettings(`{"fairness":{"maxInARow":1}}`)
	e.add(e.alice, "t01", "t02", "t03")
	snap := e.add(e.bob, "t04")
	if got, want := upNext(snap, e.alice, e.bob), "alice:t01 bob:t04 alice:t02 alice:t03"; got != want {
		t.Errorf("FIFO with a cap: %q, want %q", got, want)
	}
	e.room = e.newRoom(e.alice.ID, store.FairnessRoundRobin)
	e.setSettings(fmt.Sprintf(`{"fairness":{"weights":{%q:2}}}`, e.bob.ID))
	e.add(e.alice, "t01", "t02")
	snap = e.add(e.bob, "t04", "t06", "t07")
	if got, want := upNext(snap, e.alice, e.bob), "alice:t01 bob:t04 bob:t06 alice:t02 bob:t07"; got != want {
		t.Errorf("bob weighted 2: %q, want %q", got, want)
	}
}
