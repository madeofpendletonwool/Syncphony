// SPDX-License-Identifier: AGPL-3.0-only

package games_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/games"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// gameNight plays every game, starting rounds only by hand.
var gameNight = rooms.Games{Level: rooms.GamesNight, Frequency: new(0)}

func quick(c *games.Config) {
	c.Queue = games.QueueConfig{ShowFor: 80 * time.Millisecond, MatchGap: time.Millisecond}
}

// graph is a music graph of similar artists, listed one way round.
type graph map[string][]string

func (g graph) Known(context.Context, string) ([]string, error) { return g["known"], nil }

func (g graph) Similar(_ context.Context, artist string, _ bool) ([]games.Neighbour, error) {
	var out []games.Neighbour
	for _, n := range g[artist] {
		out = append(out, games.Neighbour{Name: n, Score: 0.6})
	}
	return out, nil
}

func (graph) Themes(context.Context, string) quiz.ThemePool {
	return quiz.ThemePool{Producers: []string{"Brian Eno"}, Genres: []string{"art rock"}}
}

// queueGame waits for a queue game event that passes ok.
func (f *fixture) queueGame(t *testing.T, ok func(games.QueueGame) bool) games.QueueGame {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e := <-f.sub.C:
			if g, is := e.Data.(games.QueueGame); is {
				if testing.Verbose() && g.Bracket != nil {
					t.Logf("bracket %s current %+v rounds %+v entries %+v", g.State, g.Bracket.Current, g.Bracket.Rounds, g.Entries)
				}
				if ok(g) {
					return g
				}
			}
		case <-timeout:
			t.Fatal("no such queue game")
		}
	}
}

func inState(state string) func(games.QueueGame) bool {
	return func(g games.QueueGame) bool { return g.State == state }
}

// add queues a song by an artist, as someone, as the queue would.
func (f *fixture) add(t *testing.T, who, title, artist string) store.QueueItem {
	t.Helper()
	meta, _ := json.Marshal(provider.Track{Title: title, Artists: []provider.ArtistCredit{{Name: artist}}})
	it, err := f.db.AddQueueItem(t.Context(), store.AddQueueItemParams{
		ID: store.NewID(), RoomID: f.room.ID, AddedBy: who, Provider: "fake", TrackID: title, Metadata: string(meta),
		LanePosition: int64(len(f.items) + 1), Now: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.items = append(f.items, it)
	return it
}

func TestConnect(t *testing.T) {
	f := setup(t, gameNight, quick)
	ctx := t.Context()
	// Abba — Blondie — Cher — Devo, and Abba — Xtc on the side.
	f.engine.Artists = graph{"known": {"Abba", "Devo", "Blondie"}, "Abba": {"Blondie", "Xtc"}, "Blondie": {"Cher"}, "Devo": {"Cher"}}
	f.engine.Queue = queue.New(f.db, f.rooms, nil)
	g, err := f.engine.StartQueue(ctx, f.room.ID, store.User{ID: "ann"}, rooms.GameConnect, games.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := g.Connect
	ends := []string{c.From, c.To}
	slices.Sort(ends)
	if ends[0] != "Abba" || ends[1] != "Devo" || len(c.Path) != 4 {
		t.Fatalf("connect %s to %s by %v", c.From, c.To, c.Path)
	}
	if _, err := f.engine.StartQueue(ctx, f.room.ID, store.User{ID: "ann"}, rooms.GameTheme, games.QueueOptions{}); !errors.Is(err, games.ErrGameRunning) {
		t.Errorf("a theme round beside it: %v", err)
	}
	g, err = f.engine.Hint(ctx, f.room.ID, g.ID, store.User{ID: "bob"}, false)
	if err != nil || len(g.Connect.Chains[0].Hints) != 1 || g.Connect.Chains[0].Hints[0] != c.Path[1] {
		t.Fatalf("hint: %+v %v", g.Connect, err)
	}
	// Bob queues a song that doesn't link, then the chain, one at a time.
	f.add(t, "bob", "Nope", "Zappa")
	f.engine.QueueChanged(f.room.ID)
	g = f.queueGame(t, func(g games.QueueGame) bool { return len(g.Connect.Misses) == 1 })
	if m := g.Connect.Misses[0]; m.Reason != "not close enough" || m.After != c.From {
		t.Errorf("miss %+v", g.Connect.Misses[0])
	}
	for i, artist := range c.Path[1:] {
		f.add(t, "bob", "Song "+artist, artist)
		f.engine.QueueChanged(f.room.ID)
		if i < len(c.Path)-2 {
			f.queueGame(t, func(g games.QueueGame) bool { return len(g.Connect.Chains[0].Links) == i+1 })
		}
	}
	g = f.queueGame(t, inState(games.StateReveal))
	ch := g.Connect.Chains[0]
	if !ch.Done || len(ch.Links) != 3 || g.Connect.Winner != 0 {
		t.Fatalf("chain %+v", g.Connect)
	}
	// Hinted, so each link scores less; then the bonus for getting there.
	if want := 3*games.HintedPoints + games.ConnectedBonus; g.Points["bob"] != want {
		t.Errorf("bob scored %d, want %d", g.Points["bob"], want)
	}
	if s := f.scores(t); len(s.Players) != 1 || s.Players[0].UserID != "bob" {
		t.Errorf("scores %+v", s)
	}
	f.queueGame(t, inState(games.StateDone))
	if len(f.engine.QueueGames(f.room.ID)) != 0 {
		t.Error("still up")
	}
}

func TestConnectTeams(t *testing.T) {
	f := setup(t, gameNight, quick)
	ctx := t.Context()
	f.engine.Artists = graph{"known": {"Abba", "Devo"}, "Abba": {"Blondie"}, "Blondie": {"Cher"}, "Devo": {"Cher"}}
	g, err := f.engine.StartQueue(ctx, f.room.ID, store.User{ID: "ann"}, rooms.GameConnect, games.QueueOptions{Teams: 2})
	if err != nil {
		t.Fatal(err)
	}
	// Ann's team gets there; Bob's doesn't start.
	for _, artist := range g.Connect.Path[1:] {
		f.add(t, "ann", "A "+artist, artist)
		f.engine.QueueChanged(f.room.ID)
		g = f.queueGame(t, func(g games.QueueGame) bool { return linked(g, artist) })
	}
	if !g.Connect.Chains[0].Done || g.Connect.Chains[1].Done || g.State != games.StateOpen {
		t.Fatalf("one team there: %+v", g)
	}
	g, err = f.engine.CloseQueue(ctx, f.room.ID, g.ID, store.User{ID: "ann"})
	if err != nil || g.Connect.Winner != 0 {
		t.Fatalf("closed: %+v %v", g.Connect, err)
	}
	if want := 3*games.LinkPoints + games.ConnectedBonus + games.RaceBonus; g.Points["ann"] != want {
		t.Errorf("ann scored %d, want %d", g.Points["ann"], want)
	}
}

func linked(g games.QueueGame, artist string) bool {
	if g.Connect == nil {
		return false
	}
	for _, ch := range g.Connect.Chains {
		if slices.ContainsFunc(ch.Links, func(l games.Link) bool { return l.Artist == artist }) {
			return true
		}
	}
	return false
}

func TestConnectNeedsAGraph(t *testing.T) {
	f := setup(t, gameNight, quick)
	f.engine.Artists = graph{"known": {"Abba", "Blondie"}, "Abba": {"Blondie"}}
	var invalid *games.InvalidInputError
	if _, err := f.engine.StartQueue(t.Context(), f.room.ID, store.User{ID: "ann"}, rooms.GameConnect, games.QueueOptions{}); !errors.As(err, &invalid) {
		t.Errorf("artists one apart: %v", err)
	}
	// The slot's free again.
	f.engine.Artists = graph{"known": {"Abba", "Devo"}, "Abba": {"Blondie"}, "Blondie": {"Cher"}, "Devo": {"Cher"}}
	if _, err := f.engine.StartQueue(t.Context(), f.room.ID, store.User{ID: "ann"}, rooms.GameConnect, games.QueueOptions{}); err != nil {
		t.Error(err)
	}
}

// upNext is the room's play order, by item.
func (f *fixture) upNext(t *testing.T) []string {
	t.Helper()
	snap, err := f.rooms.QueueSnapshot(t.Context(), f.room.ID)
	if err != nil {
		t.Fatal(err)
	}
	return snap.UpNext
}

// eventually waits for ok.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for range 300 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(what)
}

func TestThemeRound(t *testing.T) {
	f := setup(t, gameNight, quick)
	ctx := t.Context()
	f.engine.Facts = byTrack{
		"bob": {Song: quiz.Song{Title: "Song bob"}, Notes: true, CoverOf: &quiz.Original{Title: "Original"}},
		"ann": {Song: quiz.Song{Title: "Song ann"}, Notes: true},
	}
	f.engine.Queue = queue.New(f.db, f.rooms, nil)
	other := f.add(t, "guest", "Other", "Someone")
	g, err := f.engine.StartQueue(ctx, f.room.ID, store.User{ID: "ann"}, rooms.GameTheme, games.QueueOptions{Theme: quiz.ThemeCover})
	if err != nil {
		t.Fatal(err)
	}
	if g.Theme == nil || g.Theme.Prompt != "A cover" || g.State != games.StateOpen {
		t.Fatalf("%+v", g)
	}
	bob, ann := f.items[0], f.items[1]
	if _, err := f.engine.Enter(ctx, f.room.ID, g.ID, store.User{ID: "bob"}, false, ann.ID); err == nil {
		t.Error("bob entered ann's song")
	}
	if _, err := f.engine.Enter(ctx, f.room.ID, g.ID, store.User{ID: "bob"}, false, bob.ID); err != nil {
		t.Fatal(err)
	}
	g, err = f.engine.Enter(ctx, f.room.ID, g.ID, store.User{ID: "ann"}, false, ann.ID)
	if err != nil || len(g.Entries) != 2 {
		t.Fatalf("%+v %v", g.Entries, err)
	}
	if g.Entries[0].Fit != quiz.FitYes || g.Entries[1].Fit != quiz.FitNo || g.Entries[1].Note != "Not a cover, as far as we know" {
		t.Errorf("fits %+v", g.Entries)
	}
	// Entries wait out of the play order.
	if up := f.upNext(t); !slices.Equal(up, []string{other.ID}) {
		t.Errorf("up next while entering: %v", up)
	}
	g, err = f.engine.CloseQueue(ctx, f.room.ID, g.ID, store.User{ID: "ann"})
	if err != nil || g.State != games.StatePlaying {
		t.Fatalf("closed: %+v %v", g, err)
	}
	// The block plays first, in the order entered.
	eventually(t, "the block isn't at the front", func() bool {
		up := f.upNext(t)
		return len(up) == 3 && up[0] == bob.ID && up[1] == ann.ID
	})
	if err := f.db.AddHeart(ctx, store.AddHeartParams{QueueItemID: ann.ID, UserID: "bob", CreatedAt: store.Now()}); err != nil {
		t.Fatal(err)
	}
	f.play(0)
	f.play(1)
	f.rooms.PublishNowPlaying(rooms.NowPlaying{RoomID: f.room.ID, State: "playing", Item: &other, At: store.Now()})
	g = f.queueGame(t, inState(games.StateReveal))
	if !slices.Equal(g.Winners, []int{1}) || g.Entries[1].Hearts != 1 {
		t.Fatalf("winners %v, entries %+v", g.Winners, g.Entries)
	}
	if g.Points["bob"] != games.ThemeFits || g.Points["ann"] != games.ThemeFavored {
		t.Errorf("points %v", g.Points)
	}
}

func TestThemeAutoEntry(t *testing.T) {
	f := setup(t, gameNight, quick)
	ctx := t.Context()
	f.engine.Queue = queue.New(f.db, f.rooms, nil)
	g, err := f.engine.StartQueue(ctx, f.room.ID, store.User{ID: "ann"}, rooms.GameTheme, games.QueueOptions{Theme: quiz.ThemeNineties})
	if err != nil {
		t.Fatal(err)
	}
	// Songs from before it started aren't entered; the first one after is.
	first := f.add(t, "bob", "Fresh", "Bowie")
	f.add(t, "bob", "Fresher", "Bowie")
	f.engine.QueueChanged(f.room.ID)
	g = f.queueGame(t, func(g games.QueueGame) bool { return len(g.Entries) == 1 })
	if g.Entries[0].ItemID != first.ID || g.Entries[0].Fit != quiz.FitNo {
		t.Errorf("entry %+v", g.Entries[0])
	}
	// Taking it out of the queue takes it out of the game.
	if err := f.db.RemoveQueueItem(ctx, store.RemoveQueueItemParams{ID: first.ID, UpdatedAt: store.Now()}); err != nil {
		t.Fatal(err)
	}
	f.engine.QueueChanged(f.room.ID)
	f.queueGame(t, func(g games.QueueGame) bool { return len(g.Entries) == 0 })
	// With nobody in, closing ends it.
	if g, err = f.engine.CloseQueue(ctx, f.room.ID, g.ID, store.User{ID: "ann"}); err != nil || g.State != games.StateDone {
		t.Errorf("closed empty: %+v %v", g, err)
	}
	if _, err := f.engine.CloseQueue(ctx, f.room.ID, g.ID, store.User{ID: "bob"}); !errors.Is(err, games.ErrNoGame) {
		t.Errorf("closing it again: %v", err)
	}
}

func TestDraw(t *testing.T) {
	for _, tc := range []struct{ n, rounds, byes int }{{2, 1, 0}, {3, 2, 1}, {4, 2, 0}, {5, 3, 3}, {8, 3, 0}, {11, 4, 5}, {16, 4, 0}} {
		order := rand.Perm(tc.n) //nolint:gosec // a test's shuffle
		rs := games.Draw(order)
		if len(rs) != tc.rounds {
			t.Errorf("%d entries: %d rounds", tc.n, len(rs))
			continue
		}
		byes, seen := 0, map[int]bool{}
		for _, m := range rs[0] {
			seen[m.A] = true
			if m.Bye {
				byes++
				if m.Winner != m.A || m.B != -1 {
					t.Errorf("%d entries: bye %+v", tc.n, m)
				}
			} else {
				seen[m.B] = true
			}
		}
		if byes != tc.byes || len(seen) != tc.n {
			t.Errorf("%d entries: %d byes, %d placed", tc.n, byes, len(seen))
		}
		// Two byes side by side meet in the next round; a third waits for
		// the first round's one match.
		if tc.n == 5 && (rs[1][0].A < 0 || rs[1][0].B < 0 || rs[1][1].A < 0 || rs[1][1].B >= 0) {
			t.Errorf("5 entries: round 2 %+v", rs[1])
		}
	}
}

// trims records bracket songs cut short.
type trims struct {
	mu   sync.Mutex
	cuts []string
}

func (tr *trims) Seek(context.Context, string, string, time.Duration) error { return nil }

func (tr *trims) Cut(_ context.Context, _, itemID string) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.cuts = append(tr.cuts, itemID)
	return nil
}

func TestBracket(t *testing.T) {
	f := setup(t, gameNight, func(c *games.Config) {
		quick(c)
		c.Queue.MatchCap = 20 * time.Millisecond
	})
	ctx := t.Context()
	f.engine.Queue = queue.New(f.db, f.rooms, nil)
	tr := &trims{}
	f.engine.Trim = tr
	f.add(t, "guest", "Song guest", "Eno")
	g, err := f.engine.StartQueue(ctx, f.room.ID, store.User{ID: "ann"}, rooms.GameBracket, games.QueueOptions{Size: 4, Short: true})
	if err != nil {
		t.Fatal(err)
	}
	for i, who := range []string{"bob", "ann", "guest"} {
		if g, err = f.engine.Enter(ctx, f.room.ID, g.ID, store.User{ID: who}, false, f.items[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	if up := f.upNext(t); len(up) != 0 {
		t.Errorf("entries in the play order: %v", up)
	}
	g, err = f.engine.CloseQueue(ctx, f.room.ID, g.ID, store.User{ID: "ann"})
	if err != nil || g.State != games.StatePlaying || len(g.Bracket.Rounds) != 2 || !g.Bracket.Rounds[0][0].Bye {
		t.Fatalf("drawn: %+v %v", g.Bracket, err)
	}
	m := g.Bracket.Rounds[0][1]
	if g.Bracket.Current == nil || *g.Bracket.Current != (games.MatchRef{Round: 0, Match: 1}) {
		t.Fatalf("first match: %+v", g.Bracket.Current)
	}
	a, b := g.Entries[m.A], g.Entries[m.B]
	eventually(t, "the match isn't at the front", func() bool {
		up := f.upNext(t)
		return len(up) == 2 && up[0] == a.ItemID && up[1] == b.ItemID
	})
	// The room hearts B; both play, cut short, then a normal song.
	if err := f.db.AddHeart(ctx, store.AddHeartParams{QueueItemID: b.ItemID, UserID: a.UserID, CreatedAt: store.Now()}); err != nil {
		t.Fatal(err)
	}
	f.playItem(t, a.ItemID)
	f.playItem(t, b.ItemID)
	eventually(t, "the songs weren't cut short", func() bool {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		return len(tr.cuts) == 2
	})
	f.playItem(t, "")
	g = f.queueGame(t, func(g games.QueueGame) bool { return g.Bracket.Last != nil })
	if w := g.Bracket.Rounds[0][1]; w.Winner != m.B || w.HeartsB != 1 || g.Points[b.UserID] != games.MatchWin {
		t.Fatalf("match %+v, points %v", w, g.Points)
	}
	// After a song between, the final: the bye's song, waiting all along,
	// against the winner's, queued again.
	other := f.add(t, "bob", "Between", "Bowie")
	f.playItem(t, other.ID)
	g = f.queueGame(t, func(g games.QueueGame) bool {
		return g.Bracket.Current != nil && g.Bracket.Current.Round == 1 && g.Entries[g.Bracket.Rounds[1][0].B].ItemID != b.ItemID
	})
	final := g.Bracket.Rounds[1][0]
	fa, fb := g.Entries[final.A], g.Entries[final.B]
	if fb.UserID != b.UserID || fb.Played {
		t.Fatalf("final %+v: %+v vs %+v", final, fa, fb)
	}
	eventually(t, "the final isn't at the front", func() bool {
		up := f.upNext(t)
		return len(up) >= 2 && up[0] == fa.ItemID && up[1] == fb.ItemID
	})
	if err := f.db.AddHeart(ctx, store.AddHeartParams{QueueItemID: fa.ItemID, UserID: "bob", CreatedAt: store.Now()}); err != nil {
		t.Fatal(err)
	}
	f.playItem(t, fa.ItemID)
	f.playItem(t, fb.ItemID)
	f.playItem(t, "")
	g = f.queueGame(t, inState(games.StateReveal))
	if g.Bracket.Champion != final.A || g.Points[fa.UserID] != games.MatchWin+games.ChampionBonus {
		t.Fatalf("champion %d, points %v", g.Bracket.Champion, g.Points)
	}
	eventually(t, "no champion kept", func() bool {
		return f.engine.Champion(ctx, f.room.ID, time.Time{}) == fa.ItemID
	})
	raw, err := f.engine.NightBracket(ctx, store.Night{RoomID: f.room.ID})
	if err != nil || !strings.Contains(raw, fa.ItemID) {
		t.Errorf("the night's bracket: %s %v", raw, err)
	}
}

func TestBracketWalkover(t *testing.T) {
	f := setup(t, gameNight, quick)
	ctx := t.Context()
	f.engine.Queue = queue.New(f.db, f.rooms, nil)
	g, err := f.engine.StartQueue(ctx, f.room.ID, store.User{ID: "ann"}, rooms.GameBracket, games.QueueOptions{Size: 4})
	if err != nil {
		t.Fatal(err)
	}
	for i, who := range []string{"bob", "ann"} {
		if g, err = f.engine.Enter(ctx, f.room.ID, g.ID, store.User{ID: who}, false, f.items[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.engine.Enter(ctx, f.room.ID, g.ID, store.User{ID: "bob"}, false, f.items[0].ID); err == nil {
		t.Error("entered twice")
	}
	// Ann's song leaves the queue before the final.
	if err := f.db.RemoveQueueItem(ctx, store.RemoveQueueItemParams{ID: f.items[1].ID, UpdatedAt: store.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.CloseQueue(ctx, f.room.ID, g.ID, store.User{ID: "ann"}); err != nil {
		t.Fatal(err)
	}
	g = f.queueGame(t, inState(games.StateReveal))
	final := g.Bracket.Rounds[0][0]
	if !final.Walkover || g.Entries[g.Bracket.Champion].UserID != "bob" {
		t.Errorf("final %+v, champion %d", final, g.Bracket.Champion)
	}
}

// playItem publishes a song as playing, as the playback engine would; ""
// for nothing. It's out of the queue from then on.
func (f *fixture) playItem(t *testing.T, itemID string) {
	t.Helper()
	np := rooms.NowPlaying{RoomID: f.room.ID, State: "playing", At: store.Now()}
	if itemID != "" {
		it, err := f.db.GetQueueItem(t.Context(), itemID)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.db.SetQueueItemState(t.Context(), store.SetQueueItemStateParams{State: store.ItemPlayed, UpdatedAt: store.Now(), ID: itemID}); err != nil {
			t.Fatal(err)
		}
		np.Item = &it
	}
	f.rooms.PublishNowPlaying(np)
}
