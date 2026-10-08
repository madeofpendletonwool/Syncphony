// SPDX-License-Identifier: AGPL-3.0-only

package playback_test

import (
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/links"
	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/playback"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/vault"
)

type env struct {
	t   *testing.T
	db  *store.Store
	bus *realtime.Local
	// presence is who's in the room, for skip votes.
	presence *realtime.Presence
	rooms    *rooms.Service
	q        *queue.Service
	links    *links.Service
	p        *playback.Engine
	room     store.Room
	// alice owns the room; bob and carol are members. Each has a link to
	// the stream provider, and alice one to the remote provider too.
	alice, bob, carol member
	aliceRemote       string

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
	e := &env{t: t, db: db, bus: realtime.NewLocal(), presence: realtime.NewPresence(), now: time.Date(2026, 10, 5, 20, 0, 0, 0, time.UTC)}
	reg, err := provider.NewRegistry(
		fake.New(fake.Options{Now: e.clock}),
		fake.New(fake.Options{ID: "fakeremote", Name: "Fake Remote", Playback: provider.PlaybackRemote, Now: e.clock}),
	)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := vault.ParseKey(vault.GenerateKey())
	e.links = links.New(db, vault.New(k), reg, links.Config{BaseURL: "https://syncphony.example.com", Now: e.clock})
	e.rooms = rooms.New(db, e.bus)
	e.rooms.Now = e.clock
	e.q = queue.New(db, e.rooms, e.links)
	e.q.Now = e.clock
	e.alice, e.bob, e.carol = e.member("alice"), e.member("bob"), e.member("carol")
	e.aliceRemote = e.link(e.alice.ID, "fakeremote")
	e.room, err = e.rooms.Create(t.Context(), e.alice.ID, "Living room", "", rooms.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	e.start()
	return e
}

// start (re)starts the engine, as a server restart would.
func (e *env) start() {
	if e.p != nil {
		e.p.Close()
	}
	e.p = playback.New(e.db, e.rooms, e.q, e.links, playback.Config{
		LoadTimeout: 10 * time.Second, PlayerTimeout: time.Minute, EndGrace: 5 * time.Second, RemotePoll: time.Second,
		Presence: e.presence, Matcher: match.New(e.db, e.links, e.presence), Now: e.clock,
	})
	e.t.Cleanup(e.p.Close)
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func (e *env) member(name string) member {
	e.t.Helper()
	u, err := e.db.CreateUser(e.t.Context(), store.CreateUserParams{
		ID: store.NewID(), Username: name, DisplayName: name, Color: "#000000", Role: store.RoleMember, CreatedAt: e.clock(),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return member{User: u, link: e.link(u.ID, "fake")}
}

func (e *env) link(userID, providerID string) string {
	e.t.Helper()
	l, err := e.links.LinkWithCredentials(e.t.Context(), userID, providerID, map[string]string{"username": fake.Username, "password": fake.Password})
	if err != nil {
		e.t.Fatal(err)
	}
	return l.ID
}

// add queues fake tracks for m (t01 "Reference Tone" is 20s).
func (e *env) add(m member, trackIDs ...string) {
	e.t.Helper()
	e.addVia(m, m.link, trackIDs...)
}

func (e *env) addVia(m member, linkID string, trackIDs ...string) {
	e.t.Helper()
	refs := make([]queue.TrackRef, len(trackIDs))
	for i, id := range trackIDs {
		refs[i] = queue.TrackRef{LinkID: linkID, TrackID: id}
	}
	// Distinct add times, so the fair order is predictable.
	e.advance(time.Millisecond)
	if _, err := e.q.Add(e.t.Context(), e.room.ID, m.ID, refs); err != nil {
		e.t.Fatalf("add: %v", err)
	}
}

func (e *env) np() rooms.NowPlaying {
	e.t.Helper()
	np, err := e.p.NowPlaying(e.t.Context(), e.room.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return np
}

// waitFor polls until cond holds; the engine reacts to queue changes on
// another goroutine.
func (e *env) waitFor(what string, cond func(rooms.NowPlaying) bool) rooms.NowPlaying {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		np := e.np()
		if cond(np) {
			return np
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("waiting for %s; now playing: %s", what, describe(np))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func describe(np rooms.NowPlaying) string {
	s := np.State
	if np.Item != nil {
		s += " " + np.Item.TrackID + " by " + np.Item.AddedBy
	}
	return s
}

// playing reports whether track is current in state.
func playing(state, track string) func(rooms.NowPlaying) bool {
	return func(np rooms.NowPlaying) bool {
		return np.State == state && np.Item != nil && np.Item.TrackID == track
	}
}

func (e *env) claim(m member, device string) rooms.NowPlaying {
	e.t.Helper()
	np, err := e.p.Claim(e.t.Context(), e.room.ID, m.ID, device, m.Username+"'s phone")
	if err != nil {
		e.t.Fatalf("claim: %v", err)
	}
	return np
}

func (e *env) report(m member, device, event string, pos time.Duration) rooms.NowPlaying {
	e.t.Helper()
	cur := e.np()
	if cur.Item == nil {
		e.t.Fatalf("report %s with nothing playing", event)
	}
	np, err := e.p.Report(e.t.Context(), e.room.ID, m.ID, playback.Report{DeviceID: device, ItemID: cur.Item.ID, Event: event, Position: pos})
	if err != nil {
		e.t.Fatalf("report %s: %v", event, err)
	}
	return np
}

func (e *env) command(m member, c playback.Command) (rooms.NowPlaying, error) {
	return e.p.Command(e.t.Context(), e.room.ID, m.ID, c)
}

// must runs a command as alice, the owner.
func (e *env) must(c playback.Command) rooms.NowPlaying {
	e.t.Helper()
	np, err := e.command(e.alice, c)
	if err != nil {
		e.t.Fatalf("%s: %v", c.Action, err)
	}
	return np
}

// notices collects playback notices pushed to the room.
func (e *env) notices() func() []string {
	sub := e.bus.Subscribe(realtime.RoomTopic(e.room.ID))
	e.t.Cleanup(sub.Close)
	var got []string
	return func() []string {
		for {
			select {
			case ev := <-sub.C:
				if n, ok := ev.Data.(rooms.Notice); ok {
					got = append(got, n.Message)
				}
			default:
				return got
			}
		}
	}
}

func TestStreamPlayback(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	// A speaker in an empty room waits.
	if np := e.claim(e.alice, "phone"); np.State != playback.StateIdle || np.Player == nil || np.Player.DeviceID != "phone" {
		t.Fatalf("claim: %s %+v", describe(np), np.Player)
	}
	// Songs arriving start the room: alice's first, waiting for the speaker to load it.
	e.add(e.alice, "t01", "t02")
	np := e.waitFor("alice's song to load", playing(playback.StateLoading, "t01"))
	if np.Driver != string(provider.PlaybackStream) {
		t.Errorf("driver %q", np.Driver)
	}
	e.add(e.bob, "t04")
	np = e.waitFor("bob's song to be next", func(np rooms.NowPlaying) bool { return np.Next != nil && np.Next.TrackID == "t04" })

	rev := np.Revision
	if np = e.report(e.alice, "phone", playback.EventPlaying, 0); np.State != playback.StatePlaying || np.Revision != rev {
		t.Fatalf("after playing report: %s rev %d (was %d)", describe(np), np.Revision, rev)
	}
	// The position runs with the clock.
	e.advance(4 * time.Second)
	if np = e.np(); np.Position != 4*time.Second {
		t.Errorf("position %v, want 4s", np.Position)
	}
	if np = e.report(e.alice, "phone", playback.EventProgress, 3500*time.Millisecond); np.Position != 3500*time.Millisecond {
		t.Errorf("after progress report: %v", np.Position)
	}

	// Pause, seek and play each bump the revision for the speaker.
	if np, _ = e.command(e.bob, playback.Command{Action: playback.ActionPause}); np.State != playback.StatePaused || np.Revision != rev+1 {
		t.Fatalf("pause: %s rev %d", describe(np), np.Revision)
	}
	e.advance(10 * time.Second)
	if np = e.np(); np.Position != 3500*time.Millisecond {
		t.Errorf("paused position moved: %v", np.Position)
	}
	if np, _ = e.command(e.bob, playback.Command{Action: playback.ActionSeek, Position: 15 * time.Second}); np.Position != 15*time.Second || np.Revision != rev+2 {
		t.Fatalf("seek: %v rev %d", np.Position, np.Revision)
	}
	if np, _ = e.command(e.bob, playback.Command{Action: playback.ActionSeek, Position: time.Hour}); np.Position != 20*time.Second {
		t.Errorf("seek past the end: %v", np.Position)
	}
	if np, _ = e.command(e.bob, playback.Command{Action: playback.ActionPlay}); np.State != playback.StatePlaying || np.Revision != rev+4 {
		t.Fatalf("play: %s rev %d", describe(np), np.Revision)
	}

	// Ended moves on in fair order: bob hasn't had a turn, so he's before alice's second.
	np = e.report(e.alice, "phone", playback.EventEnded, 20*time.Second)
	if !playing(playback.StateLoading, "t04")(np) || np.Next == nil || np.Next.TrackID != "t02" {
		t.Fatalf("after ended: %s, next %v", describe(np), np.Next)
	}
	first, _ := e.db.ListHistory(ctx, store.ListHistoryParams{RoomID: e.room.ID, Limit: 10})
	if len(first) != 2 || first[1].PlayHistory.EndReason.String != store.EndFinished || first[1].QueueItem.State != store.ItemPlayed || first[0].PlayHistory.EndedAt.Valid {
		t.Fatalf("history: %+v", first)
	}

	e.report(e.alice, "phone", playback.EventPlaying, 0)
	np = e.report(e.alice, "phone", playback.EventEnded, 20*time.Second)
	e.report(e.alice, "phone", playback.EventPlaying, 0)
	np = e.report(e.alice, "phone", playback.EventEnded, 20*time.Second)
	if np.State != playback.StateIdle || np.Item != nil {
		t.Fatalf("after the queue ran out: %s", describe(np))
	}
	if _, err := e.command(e.alice, playback.Command{Action: playback.ActionPause}); !errors.Is(err, playback.ErrNothingPlaying) {
		t.Errorf("pause while idle: %v", err)
	}
}

func TestReportsAndSpeakers(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.add(e.alice, "t01")
	e.add(e.bob, "t04")

	if _, err := e.command(e.bob, playback.Command{Action: playback.ActionPlay}); !errors.Is(err, playback.ErrNoPlayer) {
		t.Fatalf("play with no speaker: %v", err)
	}
	np := e.claim(e.alice, "phone")
	if !playing(playback.StateLoading, "t01")(np) {
		t.Fatalf("claim with songs waiting: %s", describe(np))
	}
	e.report(e.alice, "phone", playback.EventPlaying, 0)

	// Only the speaker reports.
	if _, err := e.p.Report(ctx, e.room.ID, e.bob.ID, playback.Report{DeviceID: "phone", ItemID: np.Item.ID, Event: playback.EventEnded}); !errors.Is(err, playback.ErrNotPlayer) {
		t.Errorf("bob reporting as alice's phone: %v", err)
	}
	if _, err := e.p.Report(ctx, e.room.ID, e.alice.ID, playback.Report{DeviceID: "laptop", ItemID: np.Item.ID, Event: playback.EventEnded}); !errors.Is(err, playback.ErrNotPlayer) {
		t.Errorf("another device: %v", err)
	}
	// A report about another song is stale and changes nothing.
	got, err := e.p.Report(ctx, e.room.ID, e.alice.ID, playback.Report{DeviceID: "phone", ItemID: "old", Event: playback.EventEnded})
	if err != nil || !playing(playback.StatePlaying, "t01")(got) {
		t.Errorf("stale report: %s, %v", describe(got), err)
	}
	if _, err := e.p.Report(ctx, e.room.ID, e.alice.ID, playback.Report{DeviceID: "phone", ItemID: np.Item.ID, Event: "exploded"}); err == nil {
		t.Error("unknown event accepted")
	}

	// Bob takes over: his device has to load the song where it is.
	e.advance(5 * time.Second)
	np = e.claim(e.bob, "tablet")
	if !playing(playback.StateLoading, "t01")(np) || np.Position != 5*time.Second || np.Player.UserID != e.bob.ID {
		t.Fatalf("takeover: %s at %v by %+v", describe(np), np.Position, np.Player)
	}
	if _, err := e.p.Release(ctx, e.room.ID, e.alice.ID, "phone"); !errors.Is(err, playback.ErrNotPlayer) {
		t.Errorf("releasing a device that isn't the speaker: %v", err)
	}
	if _, err := e.p.Release(ctx, e.room.ID, e.carol.ID, "tablet"); !errors.Is(err, playback.ErrForbidden) {
		t.Errorf("carol releasing bob's tablet: %v", err)
	}
	// The owner may release anyone's; the song pauses.
	np, err = e.p.Release(ctx, e.room.ID, e.alice.ID, "tablet")
	if err != nil || np.Player != nil || np.State != playback.StatePaused {
		t.Fatalf("release: %s %+v %v", describe(np), np.Player, err)
	}
	if r, _ := e.db.GetRoom(ctx, e.room.ID); r.PlayerDeviceID.Valid {
		t.Errorf("player still stored: %+v", r.PlayerDeviceID)
	}
	if _, err := e.p.Claim(ctx, e.room.ID, e.alice.ID, "", "x"); err == nil {
		t.Error("claim with no device ID accepted")
	}
}

func TestPermissions(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	owner := rooms.Update{Permissions: rooms.Permissions{PlayPause: rooms.Owner, Skip: rooms.Owner, Speaker: rooms.Owner}}
	if _, err := e.rooms.Update(ctx, rooms.Actor{UserID: e.alice.ID}, e.room.ID, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := e.rooms.Update(ctx, rooms.Actor{UserID: e.bob.ID}, e.room.ID, owner); !errors.Is(err, rooms.ErrForbidden) {
		t.Errorf("bob changing alice's room: %v", err)
	}
	if _, err := e.p.Claim(ctx, e.room.ID, e.bob.ID, "tablet", ""); !errors.Is(err, playback.ErrForbidden) {
		t.Errorf("bob claiming: %v", err)
	}
	e.claim(e.alice, "phone")
	e.add(e.bob, "t04")
	e.add(e.carol, "t07")
	np := e.waitFor("bob's song", playing(playback.StateLoading, "t04"))

	if _, err := e.command(e.carol, playback.Command{Action: playback.ActionPause}); !errors.Is(err, playback.ErrForbidden) {
		t.Errorf("carol pausing: %v", err)
	}
	if _, err := e.command(e.carol, playback.Command{Action: playback.ActionSkip}); !errors.Is(err, playback.ErrForbidden) {
		t.Errorf("carol skipping bob's song: %v", err)
	}
	// Seeking is still open to everyone; each permission stands alone.
	if _, err := e.command(e.carol, playback.Command{Action: playback.ActionSeek, Position: time.Second}); err != nil {
		t.Errorf("carol seeking: %v", err)
	}
	if _, err := e.command(e.carol, playback.Command{Action: playback.ActionVoteSkip}); err == nil {
		t.Error("voting in a room that doesn't vote")
	}
	// Anyone may skip their own song. Skipping twice with the same item
	// ID skips once.
	skip := playback.Command{Action: playback.ActionSkip, ItemID: np.Item.ID}
	if np, err := e.command(e.bob, skip); err != nil || !playing(playback.StateLoading, "t07")(np) {
		t.Fatalf("bob skipping his own: %s %v", describe(np), err)
	}
	if np, err := e.command(e.alice, skip); err != nil || !playing(playback.StateLoading, "t07")(np) {
		t.Fatalf("second skip of the same song: %s %v", describe(np), err)
	}
	h, _ := e.db.ListHistory(ctx, store.ListHistoryParams{RoomID: e.room.ID, Limit: 10})
	if len(h) != 2 || h[1].PlayHistory.EndReason.String != store.EndSkipped || h[1].QueueItem.State != store.ItemSkipped {
		t.Fatalf("history: %+v", h)
	}
	if _, err := e.command(e.alice, playback.Command{Action: "rewind"}); err == nil {
		t.Error("unknown action accepted")
	}
}

func TestFailuresSkip(t *testing.T) {
	e := newEnv(t)
	// Every fake link has the same songs: without this, failures would
	// play from someone else's (see TestFallback).
	if _, err := e.rooms.Update(t.Context(), rooms.Actor{UserID: e.alice.ID}, e.room.ID, rooms.Update{Matching: &rooms.Matching{Fallback: new(false)}}); err != nil {
		t.Fatal(err)
	}
	notices := e.notices()
	e.claim(e.alice, "phone")
	e.add(e.alice, "t01", "t02", "t03", "t04")
	e.waitFor("first song", playing(playback.StateLoading, "t01"))

	// The speaker can't play it: skipped, with a notice.
	np := e.report(e.alice, "phone", playback.EventError, 0)
	if !playing(playback.StateLoading, "t02")(np) {
		t.Fatalf("after error: %s", describe(np))
	}
	// The speaker tries but it never starts: skipped after the load timeout.
	e.report(e.alice, "phone", playback.EventProgress, 0)
	e.advance(11 * time.Second)
	e.p.Tick(t.Context())
	e.waitFor("third song", playing(playback.StateLoading, "t03"))
	// A third failure in a row stops the room instead of burning the queue.
	e.report(e.alice, "phone", playback.EventError, 0)
	np = e.np()
	if np.State != playback.StateIdle || np.Item != nil {
		t.Fatalf("after three failures: %s", describe(np))
	}
	got := notices()
	if len(got) != 4 || !strings.Contains(got[0], "Couldn't play “Reference Tone”") || !strings.Contains(got[1], "too long to start") || !strings.Contains(got[3], "Stopped after 3") {
		t.Fatalf("notices: %q", got)
	}
	// Songs arriving don't restart a stopped room; play does.
	e.add(e.bob, "t05")
	time.Sleep(50 * time.Millisecond)
	if np := e.np(); np.State != playback.StateIdle {
		t.Fatalf("restarted by a queue change: %s", describe(np))
	}
	if np, err := e.command(e.alice, playback.Command{Action: playback.ActionPlay}); err != nil || np.State != playback.StateLoading {
		t.Fatalf("play after stopping: %s %v", describe(np), err)
	}
	// A song that starts resets the count: fail, start, fail, fail is
	// never three in a row.
	e.add(e.carol, "t07", "t08")
	e.report(e.alice, "phone", playback.EventError, 0)
	e.report(e.alice, "phone", playback.EventPlaying, 0)
	e.report(e.alice, "phone", playback.EventError, 0)
	np = e.report(e.alice, "phone", playback.EventError, 0)
	got = notices()
	if np.State != playback.StateLoading || len(got) != 7 || strings.Contains(got[6], "Stopped") {
		t.Fatalf("after fail, start, fail, fail: %s, notices %q", describe(np), got)
	}
}

func TestSilentSpeaker(t *testing.T) {
	e := newEnv(t)
	notices := e.notices()
	e.claim(e.alice, "phone")
	e.add(e.alice, "t01", "t02", "t03", "t04")
	e.waitFor("first song", playing(playback.StateLoading, "t01"))

	// The speaker never says a word about the song (its tab is asleep, or
	// gone): the song isn't to blame, so the room pauses on it rather than
	// skipping through the queue.
	e.advance(11 * time.Second)
	e.p.Tick(t.Context())
	np := e.np()
	if !playing(playback.StatePaused, "t01")(np) || np.Position != 0 {
		t.Fatalf("after a silent load: %s at %v", describe(np), np.Position)
	}
	if got := notices(); len(got) != 1 || !strings.Contains(got[0], "alice's phone didn't start “Reference Tone”") {
		t.Fatalf("notices: %q", got)
	}
	// Play tries the same song again, waiting for the speaker to start it.
	if np = e.must(playback.Command{Action: playback.ActionPlay}); !playing(playback.StateLoading, "t01")(np) {
		t.Fatalf("play after a silent load: %s", describe(np))
	}
	if np = e.report(e.alice, "phone", playback.EventPlaying, 0); !playing(playback.StatePlaying, "t01")(np) {
		t.Fatalf("speaker woke up: %s", describe(np))
	}
}

func TestPlayNow(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.add(e.alice, "t01")
	e.add(e.bob, "t04", "t05")
	e.add(e.carol, "t07")
	snap, _ := e.rooms.QueueSnapshot(ctx, e.room.ID)
	id := func(track string) string {
		for _, it := range snap.Items {
			if it.TrackID == track {
				return it.ID
			}
		}
		t.Fatalf("no %s", track)
		return ""
	}

	if _, err := e.command(e.bob, playback.Command{Action: playback.ActionPlayNow, ItemID: id("t05")}); !errors.Is(err, playback.ErrNoPlayer) {
		t.Fatalf("play now with no speaker: %v", err)
	}
	// Pressing play on a song with no speaker: this device becomes the
	// speaker and plays it, without starting (and skipping) another first.
	np, err := e.p.ClaimAndPlay(ctx, e.room.ID, e.carol.ID, "tablet", "carol's tablet", id("t07"))
	if err != nil || !playing(playback.StateLoading, "t07")(np) || np.Player == nil || np.Player.DeviceID != "tablet" {
		t.Fatalf("claim and play: %s %+v %v", describe(np), np.Player, err)
	}
	if h, _ := e.db.ListHistory(ctx, store.ListHistoryParams{RoomID: e.room.ID, Limit: 10}); len(h) != 1 {
		t.Fatalf("history after claim and play: %+v", h)
	}
	e.claim(e.alice, "phone")
	e.report(e.alice, "phone", playback.EventPlaying, 0)

	// Bob's second song jumps everyone and the current song is skipped.
	np, err = e.command(e.bob, playback.Command{Action: playback.ActionPlayNow, ItemID: id("t05")})
	if err != nil || !playing(playback.StateLoading, "t05")(np) {
		t.Fatalf("play now: %s %v", describe(np), err)
	}
	h, _ := e.db.ListHistory(ctx, store.ListHistoryParams{RoomID: e.room.ID, Limit: 10})
	if len(h) != 2 || h[1].PlayHistory.EndReason.String != store.EndSkipped {
		t.Fatalf("history: %+v", h)
	}
	// Only queued songs can be played now.
	if _, err := e.command(e.bob, playback.Command{Action: playback.ActionPlayNow, ItemID: id("t07")}); err == nil {
		t.Error("played a song that already played")
	}
	if _, err := e.command(e.bob, playback.Command{Action: playback.ActionPlayNow}); err == nil {
		t.Error("play now with no item")
	}

	// It takes the skip permission.
	owner := rooms.Update{Permissions: rooms.Permissions{Skip: rooms.Owner}}
	if _, err := e.rooms.Update(ctx, rooms.Actor{UserID: e.alice.ID}, e.room.ID, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := e.command(e.carol, playback.Command{Action: playback.ActionPlayNow, ItemID: id("t04")}); !errors.Is(err, playback.ErrForbidden) {
		t.Errorf("carol in an owner-skips room: %v", err)
	}
	if np := e.must(playback.Command{Action: playback.ActionPlayNow, ItemID: id("t04")}); !playing(playback.StateLoading, "t04")(np) {
		t.Errorf("the owner: %s", describe(np))
	}
}

func TestPlayNowVote(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	vote := rooms.Update{Permissions: rooms.Permissions{Skip: rooms.Vote}}
	if _, err := e.rooms.Update(ctx, rooms.Actor{UserID: e.alice.ID}, e.room.ID, vote); err != nil {
		t.Fatal(err)
	}
	for _, m := range []member{e.alice, e.bob, e.carol} {
		e.presence.Join(e.room.ID, m.ID)
	}
	notices := e.notices()
	e.claim(e.alice, "phone")
	e.add(e.alice, "t01")
	e.add(e.bob, "t04", "t05")
	e.waitFor("alice's song", playing(playback.StateLoading, "t01"))
	e.waitFor("bob's song to be next", func(np rooms.NowPlaying) bool { return np.Next != nil && np.Next.TrackID == "t04" })
	snap, _ := e.rooms.QueueSnapshot(ctx, e.room.ID)
	var t05 string
	for _, it := range snap.Items {
		if it.TrackID == "t05" {
			t05 = it.ID
		}
	}
	playNow := playback.Command{Action: playback.ActionPlayNow, ItemID: t05}

	// Bob asks; the room has to agree (2 of 3).
	np, err := e.command(e.bob, playNow)
	if err != nil || !playing(playback.StateLoading, "t01")(np) {
		t.Fatalf("asking: %s %v", describe(np), err)
	}
	if v := np.PlayNow; v == nil || v.ItemID != t05 || v.By != e.bob.ID || len(v.Voters) != 1 || v.Needed != 2 {
		t.Fatalf("request: %+v", np.PlayNow)
	}
	if got := notices(); len(got) != 1 || got[0] != "bob wants to play “Pluge” now" {
		t.Fatalf("notices: %q", got)
	}
	// Only one request at a time.
	if _, err := e.command(e.carol, playback.Command{Action: playback.ActionPlayNow, ItemID: np.Next.ID}); err == nil {
		t.Error("a second request while one is open")
	}
	// Carol agrees and agreeing passes it.
	np, err = e.command(e.carol, playback.Command{Action: playback.ActionVotePlayNow, ItemID: t05})
	if err != nil || !playing(playback.StateLoading, "t05")(np) || np.PlayNow != nil {
		t.Fatalf("after carol agreed: %s %+v %v", describe(np), np.PlayNow, err)
	}
	if got := notices(); len(got) != 2 || !strings.Contains(got[1], "the room agreed") {
		t.Fatalf("notices: %q", got)
	}

	// Asking again, then taking it back, ends the request.
	e.add(e.carol, "t07")
	snap, _ = e.rooms.QueueSnapshot(ctx, e.room.ID)
	var t07 string
	for _, it := range snap.Items {
		if it.TrackID == "t07" {
			t07 = it.ID
		}
	}
	if np, _ = e.command(e.carol, playback.Command{Action: playback.ActionPlayNow, ItemID: t07}); np.PlayNow == nil {
		t.Fatal("no request")
	}
	if np, _ = e.command(e.carol, playback.Command{Action: playback.ActionUnvotePlayNow, ItemID: t07}); np.PlayNow != nil {
		t.Fatalf("withdrawn request still open: %+v", np.PlayNow)
	}
	// A request lapses.
	if _, err := e.command(e.carol, playback.Command{Action: playback.ActionPlayNow, ItemID: t07}); err != nil {
		t.Fatal(err)
	}
	e.advance(playback.PlayNowTimeout)
	e.p.Tick(ctx)
	if np = e.np(); np.PlayNow != nil {
		t.Fatalf("lapsed request still open: %+v", np.PlayNow)
	}
	// The owner just plays it.
	if np = e.must(playback.Command{Action: playback.ActionPlayNow, ItemID: t07}); !playing(playback.StateLoading, "t07")(np) {
		t.Fatalf("the owner: %s", describe(np))
	}
}

func TestPrevious(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.add(e.alice, "t01")
	e.add(e.bob, "t04")
	e.add(e.carol, "t07")
	e.claim(e.alice, "phone")
	e.report(e.alice, "phone", playback.EventPlaying, 0)
	previous := playback.Command{Action: playback.ActionPrevious}
	if _, err := e.command(e.bob, previous); err == nil {
		t.Fatal("went back from the first song")
	}
	e.report(e.alice, "phone", playback.EventEnded, 0)
	e.waitFor("bob's song", playing(playback.StateLoading, "t04"))
	e.report(e.alice, "phone", playback.EventPlaying, 0)

	// Back to alice's song; bob's waits at the front, ahead of carol's.
	np, err := e.command(e.bob, previous)
	if err != nil || !playing(playback.StateLoading, "t01")(np) || np.Next == nil || np.Next.TrackID != "t04" {
		t.Fatalf("going back: %s next %+v %v", describe(np), np.Next, err)
	}
	// Bob's interrupted play is forgotten, not counted as a skip.
	h, _ := e.db.ListHistory(ctx, store.ListHistoryParams{RoomID: e.room.ID, Limit: 10})
	if len(h) != 2 || h[0].QueueItem.TrackID != "t01" || h[1].QueueItem.TrackID != "t01" || h[1].PlayHistory.EndReason.String != store.EndFinished {
		t.Fatalf("history: %+v", h)
	}
	e.report(e.alice, "phone", playback.EventEnded, 0)
	e.waitFor("bob's song again", playing(playback.StateLoading, "t04"))
	e.report(e.alice, "phone", playback.EventEnded, 0)
	e.waitFor("carol's song", playing(playback.StateLoading, "t07"))

	// Going back twice puts both songs back, the latest first.
	e.must(previous)
	e.advance(time.Second)
	if np := e.must(previous); !playing(playback.StateLoading, "t01")(np) {
		t.Fatalf("back twice: %s", describe(np))
	}
	snap, _ := e.rooms.QueueSnapshot(ctx, e.room.ID)
	var order []string
	for _, id := range snap.UpNext {
		for _, it := range snap.Items {
			if it.ID == id {
				order = append(order, it.TrackID)
			}
		}
	}
	if !slices.Equal(order, []string{"t04", "t07"}) {
		t.Fatalf("up next: %v", order)
	}

	// It takes the skip permission.
	owner := rooms.Update{Permissions: rooms.Permissions{Skip: rooms.Owner}}
	if _, err := e.rooms.Update(ctx, rooms.Actor{UserID: e.alice.ID}, e.room.ID, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := e.command(e.carol, previous); !errors.Is(err, playback.ErrForbidden) {
		t.Errorf("carol in an owner-skips room: %v", err)
	}
}

func TestPreviousVote(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	vote := rooms.Update{Permissions: rooms.Permissions{Skip: rooms.Vote}}
	if _, err := e.rooms.Update(ctx, rooms.Actor{UserID: e.alice.ID}, e.room.ID, vote); err != nil {
		t.Fatal(err)
	}
	for _, m := range []member{e.alice, e.bob, e.carol} {
		e.presence.Join(e.room.ID, m.ID)
	}
	notices := e.notices()
	e.claim(e.alice, "phone")
	e.add(e.alice, "t01")
	e.add(e.bob, "t04", "t05")
	e.waitFor("alice's song", playing(playback.StateLoading, "t01"))
	e.report(e.alice, "phone", playback.EventEnded, 0)
	e.waitFor("bob's song", playing(playback.StateLoading, "t04"))
	previous := playback.Command{Action: playback.ActionPrevious}

	// Bob asks the room to go back to alice's song.
	np, err := e.command(e.bob, previous)
	if err != nil || !playing(playback.StateLoading, "t04")(np) {
		t.Fatalf("asking: %s %v", describe(np), err)
	}
	v := np.PlayNow
	if v == nil || !v.Back || v.Item == nil || v.Item.TrackID != "t01" || v.ItemID != v.Item.ID || len(v.Voters) != 1 || v.Needed != 2 {
		t.Fatalf("request: %+v", v)
	}
	if got := notices(); len(got) != 1 || got[0] != "bob wants to go back to “Reference Tone”" {
		t.Fatalf("notices: %q", got)
	}
	// Carol agrees and it goes back.
	np, err = e.command(e.carol, playback.Command{Action: playback.ActionVotePlayNow, ItemID: v.ItemID})
	if err != nil || !playing(playback.StateLoading, "t01")(np) || np.PlayNow != nil || np.Next == nil || np.Next.TrackID != "t04" {
		t.Fatalf("after carol agreed: %s %+v %v", describe(np), np.PlayNow, err)
	}
	if got := notices(); len(got) != 2 || got[1] != "Back to “Reference Tone”: the room agreed" {
		t.Fatalf("notices: %q", got)
	}

	// A request to go back ends when the song does.
	e.report(e.alice, "phone", playback.EventEnded, 0)
	e.waitFor("bob's song again", playing(playback.StateLoading, "t04"))
	if np, _ = e.command(e.carol, previous); np.PlayNow == nil {
		t.Fatal("no request")
	}
	e.report(e.alice, "phone", playback.EventEnded, 0)
	if np = e.waitFor("bob's next song", playing(playback.StateLoading, "t05")); np.PlayNow != nil {
		t.Fatalf("request outlived its song: %+v", np.PlayNow)
	}
	// The owner just goes back.
	if np = e.must(previous); !playing(playback.StateLoading, "t04")(np) {
		t.Fatalf("the owner: %s", describe(np))
	}
}

func TestUnlinkedSongSkipped(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.add(e.bob, "t04")
	e.add(e.alice, "t01")
	// Bob unlinks his service: his queued song can't play any more.
	if err := e.links.Unlink(ctx, e.bob.ID, e.bob.link); err != nil {
		t.Fatal(err)
	}
	notices := e.notices()
	np := e.claim(e.alice, "phone")
	if !playing(playback.StateLoading, "t01")(np) {
		t.Fatalf("after skipping the unlinked song: %s", describe(np))
	}
	if got := notices(); len(got) != 1 || !strings.Contains(got[0], "couldn't start") {
		t.Fatalf("notices: %q", got)
	}
}

func TestWatchdogs(t *testing.T) {
	e := newEnv(t)
	notices := e.notices()
	e.claim(e.alice, "phone")
	e.add(e.alice, "t01", "t02")
	e.waitFor("first song", playing(playback.StateLoading, "t01"))
	e.report(e.alice, "phone", playback.EventPlaying, 0)

	// The speaker's "ended" got lost: the engine moves on once the song is
	// well past its end. Reporting progress keeps the speaker alive.
	e.advance(20 * time.Second)
	e.report(e.alice, "phone", playback.EventProgress, 20*time.Second)
	e.p.Tick(t.Context())
	if np := e.np(); !playing(playback.StatePlaying, "t01")(np) {
		t.Fatalf("moved on before the grace period: %s", describe(np))
	}
	e.advance(6 * time.Second)
	e.p.Tick(t.Context())
	np := e.waitFor("second song", playing(playback.StateLoading, "t02"))
	if h, _ := e.db.ListHistory(t.Context(), store.ListHistoryParams{RoomID: e.room.ID, Limit: 10}); h[1].PlayHistory.EndReason.String != store.EndFinished {
		t.Errorf("end reason %q", h[1].PlayHistory.EndReason.String)
	}

	// The speaker goes quiet for too long while playing: pause.
	e.report(e.alice, "phone", playback.EventPlaying, 0)
	e.advance(61 * time.Second)
	e.p.Tick(t.Context())
	if np = e.np(); np.State != playback.StatePaused || np.Player == nil {
		t.Fatalf("after the speaker went quiet: %s %+v", describe(np), np.Player)
	}
	if got := notices(); len(got) != 1 || !strings.Contains(got[0], "lost touch with alice's phone") {
		t.Fatalf("notices: %q", got)
	}
}

func TestRemotePlayback(t *testing.T) {
	e := newEnv(t)
	e.addVia(e.alice, e.aliceRemote, "t01") // 20s, played by the service itself
	e.add(e.bob, "t04")

	// Remote songs start on the service's player straight away.
	np := e.claim(e.alice, "phone")
	if !playing(playback.StatePlaying, "t01")(np) || np.Driver != string(provider.PlaybackRemote) {
		t.Fatalf("claim: %s via %q", describe(np), np.Driver)
	}
	e.advance(5 * time.Second)
	e.p.Tick(t.Context())
	if np = e.np(); np.Position != 5*time.Second {
		t.Errorf("position %v", np.Position)
	}
	// Pausing pauses the remote: polling while paused doesn't advance.
	e.must(playback.Command{Action: playback.ActionPause})
	e.advance(30 * time.Second)
	e.p.Tick(t.Context())
	if np = e.np(); !playing(playback.StatePaused, "t01")(np) || np.Position != 5*time.Second {
		t.Fatalf("paused: %s at %v", describe(np), np.Position)
	}
	e.must(playback.Command{Action: playback.ActionSeek, Position: 18 * time.Second})
	e.must(playback.Command{Action: playback.ActionPlay})
	// The remote reaches the end; the next poll moves on to bob's streamed song.
	e.advance(3 * time.Second)
	e.p.Tick(t.Context())
	np = e.waitFor("bob's song", playing(playback.StateLoading, "t04"))
	if np.Driver != string(provider.PlaybackStream) {
		t.Errorf("driver %q", np.Driver)
	}
}

func TestRestart(t *testing.T) {
	e := newEnv(t)
	e.claim(e.alice, "phone")
	e.add(e.alice, "t01", "t02")
	e.waitFor("first song", playing(playback.StateLoading, "t01"))
	e.report(e.alice, "phone", playback.EventPlaying, 0)

	// After a restart the song is still current but paused, and the
	// speaker has to claim the room again.
	e.start()
	np := e.np()
	if !playing(playback.StatePaused, "t01")(np) || np.Player != nil {
		t.Fatalf("after restart: %s %+v", describe(np), np.Player)
	}
	if r, _ := e.db.GetRoom(t.Context(), e.room.ID); r.PlayerDeviceID.Valid {
		t.Errorf("stale player kept: %+v", r.PlayerDeviceID)
	}
	// The speaker hasn't started it since: play waits for it to.
	e.claim(e.alice, "phone")
	if np, err := e.command(e.alice, playback.Command{Action: playback.ActionPlay}); err != nil || !playing(playback.StateLoading, "t01")(np) {
		t.Fatalf("resume: %s %v", describe(np), err)
	}
}

func TestStream(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.claim(e.alice, "phone")
	e.add(e.alice, "t01")
	e.add(e.bob, "t04")
	e.waitFor("first song", playing(playback.StateLoading, "t01"))
	np := e.waitFor("next song", func(np rooms.NowPlaying) bool { return np.Next != nil })

	// Bob's link streams bob's song to alice's phone, from a byte offset.
	a, err := e.p.Stream(ctx, e.room.ID, np.Next.ID, provider.StreamOpts{Range: &provider.ByteRange{Start: 44, End: -1}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(a.Body)
	a.Body.Close()
	if a.ContentType != "audio/wav" || a.Offset != 44 || int64(len(b)) != a.Size-44 || a.ContentRange() == "" {
		t.Fatalf("stream: %s offset %d len %d size %d", a.ContentType, a.Offset, len(b), a.Size)
	}
	// Without a transcoder, formats the player can't take fail clearly.
	if _, err := e.p.Stream(ctx, e.room.ID, np.Item.ID, provider.StreamOpts{Accept: []string{"audio/mpeg"}}); err == nil || !strings.Contains(err.Error(), "no transcoder") {
		t.Errorf("unacceptable format: %v", err)
	}

	e.report(e.alice, "phone", playback.EventPlaying, 0)
	e.report(e.alice, "phone", playback.EventEnded, 20*time.Second)
	if _, err := e.p.Stream(ctx, e.room.ID, np.Item.ID, provider.StreamOpts{}); !errors.Is(err, playback.ErrNotStreamable) {
		t.Errorf("a song that already played: %v", err)
	}
	other, _ := e.rooms.Create(ctx, e.bob.ID, "Kitchen", "", rooms.Settings{})
	if _, err := e.p.Stream(ctx, other.ID, np.Next.ID, provider.StreamOpts{}); !errors.Is(err, queue.ErrNotFound) {
		t.Errorf("another room's song: %v", err)
	}
	if _, err := e.p.NowPlaying(ctx, "nope"); !errors.Is(err, rooms.ErrNotFound) {
		t.Errorf("missing room: %v", err)
	}
}

func TestVoteSkip(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	dave, erin := e.member("dave"), e.member("erin")
	vote := rooms.Update{Permissions: rooms.Permissions{Skip: rooms.Vote}}
	if _, err := e.rooms.Update(ctx, rooms.Actor{UserID: e.alice.ID}, e.room.ID, vote); err != nil {
		t.Fatal(err)
	}
	for _, m := range []member{e.alice, e.bob, e.carol, dave, erin} {
		e.presence.Join(e.room.ID, m.ID)
	}
	notices := e.notices()
	e.claim(e.alice, "phone")
	e.add(e.bob, "t04")
	e.add(e.carol, "t07")
	np := e.waitFor("bob's song", playing(playback.StateLoading, "t04"))
	// Bob queued it, so the other four vote: more than half is 3.
	if v := np.SkipVotes; v == nil || v.Needed != 3 || len(v.Voters) != 0 {
		t.Fatalf("tally: %+v", np.SkipVotes)
	}

	if _, err := e.command(e.carol, playback.Command{Action: playback.ActionSkip}); !errors.Is(err, playback.ErrForbidden) {
		t.Errorf("carol skipping without a vote: %v", err)
	}
	if _, err := e.command(e.bob, playback.Command{Action: playback.ActionVoteSkip}); err == nil {
		t.Error("bob voting on his own song")
	}
	voteFor := playback.Command{Action: playback.ActionVoteSkip, ItemID: np.Item.ID}
	np, err := e.command(e.carol, voteFor)
	if err != nil || !playing(playback.StateLoading, "t04")(np) || len(np.SkipVotes.Voters) != 1 {
		t.Fatalf("carol's vote: %s %+v %v", describe(np), np.SkipVotes, err)
	}
	// Voting twice counts once; taking it back and voting again is fine.
	np, _ = e.command(e.carol, voteFor)
	if len(np.SkipVotes.Voters) != 1 {
		t.Fatalf("carol voting twice: %+v", np.SkipVotes)
	}
	np, _ = e.command(e.carol, playback.Command{Action: playback.ActionUnvoteSkip})
	if len(np.SkipVotes.Voters) != 0 {
		t.Fatalf("carol's vote taken back: %+v", np.SkipVotes)
	}
	for _, m := range []member{e.carol, dave} {
		if _, err := e.command(m, voteFor); err != nil {
			t.Fatal(err)
		}
	}
	if np := e.np(); !playing(playback.StateLoading, "t04")(np) || len(np.SkipVotes.Voters) != 2 {
		t.Fatalf("two of three votes: %s %+v", describe(np), np.SkipVotes)
	}

	// Erin leaving makes two votes a majority of three.
	e.presence.Leave(e.room.ID, erin.ID)
	e.p.MembersChanged(e.room.ID)
	np = e.waitFor("the vote to pass", playing(playback.StateLoading, "t07"))
	if np.SkipVotes == nil || len(np.SkipVotes.Voters) != 0 {
		t.Fatalf("votes carried over to the next song: %+v", np.SkipVotes)
	}
	if got := notices(); len(got) != 1 || !strings.Contains(got[0], "voted") {
		t.Errorf("notices: %q", got)
	}
	// A vote for the song that was skipped is stale.
	if np, err := e.command(e.alice, voteFor); err != nil || len(np.SkipVotes.Voters) != 0 {
		t.Errorf("stale vote: %+v %v", np.SkipVotes, err)
	}
	h, _ := e.db.ListHistory(ctx, store.ListHistoryParams{RoomID: e.room.ID, Limit: 10})
	if len(h) != 2 || h[1].PlayHistory.EndReason.String != store.EndSkipped {
		t.Fatalf("history: %+v", h)
	}

	// The owner, and whoever queued the song, skip without a vote.
	e.add(e.bob, "t01")
	if _, err := e.command(e.carol, playback.Command{Action: playback.ActionSkip}); err != nil {
		t.Fatalf("carol skipping her own song: %v", err)
	}
	e.waitFor("bob's next song", playing(playback.StateLoading, "t01"))

	// Raising the bar to everyone, then voting with everyone.
	all := 99
	if _, err := e.rooms.Update(ctx, rooms.Actor{UserID: e.alice.ID}, e.room.ID, rooms.Update{SkipVotePercent: &all}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the new bar", func(np rooms.NowPlaying) bool { return np.SkipVotes != nil && np.SkipVotes.Needed == 3 })
	for _, m := range []member{e.alice, e.carol} {
		if _, err := e.command(m, playback.Command{Action: playback.ActionVoteSkip}); err != nil {
			t.Fatal(err)
		}
	}
	if np := e.np(); !playing(playback.StateLoading, "t01")(np) || len(np.SkipVotes.Voters) != 2 {
		t.Fatalf("two of three votes: %s %+v", describe(np), np.SkipVotes)
	}
	// Turning votes off drops the tally; turning them back on starts fresh.
	if _, err := e.rooms.Update(ctx, rooms.Actor{UserID: e.alice.ID}, e.room.ID, rooms.Update{Permissions: rooms.Permissions{Skip: rooms.Everyone}}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("votes off", func(np rooms.NowPlaying) bool { return np.SkipVotes == nil })
	if _, err := e.rooms.Update(ctx, rooms.Actor{UserID: e.alice.ID}, e.room.ID, vote); err != nil {
		t.Fatal(err)
	}
	e.waitFor("votes on", func(np rooms.NowPlaying) bool { return np.SkipVotes != nil && len(np.SkipVotes.Voters) == 0 })
}

func TestFallback(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	notices := e.notices()
	e.claim(e.alice, "phone")

	// Alice's song fails on the speaker. Her own other service has it, so
	// it plays from there, on its remote player.
	e.add(e.alice, "t01")
	e.waitFor("alice's song", playing(playback.StateLoading, "t01"))
	np := e.report(e.alice, "phone", playback.EventError, 0)
	if !playing(playback.StatePlaying, "t01")(np) || np.Item.ViaLinkID.String != e.aliceRemote || np.Driver != string(provider.PlaybackRemote) {
		t.Fatalf("from alice's other service: %s via %v (%s)", describe(np), np.Item.ViaLinkID, np.Driver)
	}
	if got := notices(); len(got) != 1 || !strings.Contains(got[0], "from alice's Fake Remote") {
		t.Fatalf("notices: %q", got)
	}
	e.must(playback.Command{Action: playback.ActionSkip})

	// Carol isn't here and has only one link: nowhere else to look, so her
	// song is skipped.
	e.add(e.carol, "t04", "t05")
	e.waitFor("carol's song", playing(playback.StateLoading, "t04"))
	np = e.report(e.alice, "phone", playback.EventError, 0)
	if !playing(playback.StateLoading, "t05")(np) || np.Item.ViaLinkID.Valid {
		t.Fatalf("with nowhere to look: %s", describe(np))
	}

	// Bob joins: carol's next song plays from bob's link, from the start.
	e.presence.Join(e.room.ID, e.bob.ID)
	rev := np.Revision
	np = e.report(e.alice, "phone", playback.EventError, 5*time.Second)
	if !playing(playback.StateLoading, "t05")(np) || np.Item.ViaLinkID.String != e.bob.link || np.Item.ViaTrackID.String != "t05" ||
		np.Position != 0 || np.Revision <= rev {
		t.Fatalf("from bob's: %s via %v, at %v, revision %d", describe(np), np.Item.ViaLinkID, np.Position, np.Revision)
	}
	if got := notices(); !strings.Contains(got[len(got)-1], "from bob's Fake") {
		t.Fatalf("notices: %q", got)
	}
	// The stand-in is in the queue too, and streams from bob's link.
	snap, _ := e.rooms.QueueSnapshot(ctx, e.room.ID)
	if i := slices.IndexFunc(snap.Items, func(it store.QueueItem) bool { return it.ID == np.Item.ID }); i < 0 || snap.Items[i].ViaLinkID.String != e.bob.link {
		t.Fatalf("snapshot: %+v", snap.Items)
	}
	a, err := e.p.Stream(ctx, e.room.ID, np.Item.ID, provider.StreamOpts{})
	if err != nil {
		t.Fatal(err)
	}
	a.Body.Close()
	// One stand-in per song: failing again skips it.
	if np = e.report(e.alice, "phone", playback.EventError, 0); np.State != playback.StateIdle {
		t.Fatalf("after the stand-in failed: %s", describe(np))
	}

	// A song whose link is gone stands in before it starts.
	e.add(e.carol, "t07", "t08")
	e.waitFor("carol's song", playing(playback.StateLoading, "t07"))
	if err := e.links.Unlink(ctx, e.carol.ID, e.carol.link); err != nil {
		t.Fatal(err)
	}
	e.report(e.alice, "phone", playback.EventEnded, 0)
	np = e.waitFor("carol's next song", playing(playback.StateLoading, "t08"))
	if np.Item.ViaLinkID.String != e.bob.link {
		t.Fatalf("unlinked song: via %v", np.Item.ViaLinkID)
	}

	// The room can turn it off.
	if _, err := e.rooms.Update(ctx, rooms.Actor{UserID: e.alice.ID}, e.room.ID, rooms.Update{Matching: &rooms.Matching{Fallback: new(false)}}); err != nil {
		t.Fatal(err)
	}
	e.add(e.bob, "t06")
	e.report(e.alice, "phone", playback.EventEnded, 0)
	e.waitFor("bob's song", playing(playback.StateLoading, "t06"))
	if np := e.report(e.alice, "phone", playback.EventError, 0); np.State != playback.StateIdle {
		t.Fatalf("with fallback off: %s", describe(np))
	}
}
