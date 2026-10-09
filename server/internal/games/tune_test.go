// SPDX-License-Identifier: AGPL-3.0-only

package games_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/clips"
	"github.com/madeofpendletonwool/syncphony/server/internal/games"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/transcode"
)

// tuneFacts knows the tunes ("tune1", "tune2"…) by their track IDs, and
// has songs by similar artists for wrong answers.
type tuneFacts struct{}

func (tuneFacts) Song(_ context.Context, it store.QueueItem, _ bool) (quiz.Facts, error) {
	if !strings.HasPrefix(it.TrackID, "tune") {
		return quiz.Facts{Song: quiz.Song{Title: "Heroes", Artist: "Bowie"}, Year: 1977}, nil
	}
	return quiz.Facts{
		Song: quiz.Song{Title: "Tune " + it.TrackID, Artist: "Blondie"}, DurationMs: 240_000,
		Sections: []quiz.Section{{StartMs: 0, Energy: 0.3}, {StartMs: 61_000, Energy: 0.9}, {StartMs: 120_000, Energy: 0.5}},
		Bars:     []int64{0, 30_000, 62_000, 64_000},
	}, nil
}

func (tuneFacts) Pool(context.Context, string, store.QueueItem, quiz.Facts) quiz.Pool {
	return quiz.Pool{Songs: []quiz.Song{{Title: "Call Me", Artist: "Blondie"}, {Title: "Psycho Killer", Artist: "Talking Heads"}, {Title: "Roadrunner", Artist: "The Modern Lovers"}}}
}

// await waits for the music to have been asked n things.
func (m *music) await(t *testing.T, n int) []string {
	t.Helper()
	for range 100 {
		if got := m.seen(); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return m.seen()
}

// tunes are six songs to name.
type tunes struct{}

func (tunes) Tunes(_ context.Context, roomID, _ string) ([]games.Tune, error) {
	var out []games.Tune
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("tune%d", i)
		meta, _ := json.Marshal(provider.Track{Title: "Tune " + id, Artists: []provider.ArtistCredit{{Name: "Blondie"}}})
		out = append(out, games.Tune{
			Item: store.QueueItem{RoomID: roomID, Provider: "fake", TrackID: id, Metadata: string(meta)},
			Song: clips.Song{LinkID: "link", TrackID: id},
		})
	}
	return out, nil
}

// cutter records the clips it's asked to cut, and cuts them at once.
type cutter struct {
	mu   sync.Mutex
	cuts map[string][]transcode.Cut
	n    int
}

func (c *cutter) Cut(_ string, song clips.Song, cuts []transcode.Cut) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cuts == nil {
		c.cuts = map[string][]transcode.Cut{}
	}
	c.cuts[song.TrackID] = slices.Clone(cuts)
	ids := make([]string, len(cuts))
	for i := range ids {
		c.n++
		ids[i] = fmt.Sprintf("clip%d", c.n)
	}
	return ids
}

func (*cutter) Ready([]string) (bool, bool) { return true, false }

func (c *cutter) of(trackID string) []transcode.Cut {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cuts[trackID]
}

var tuneOnly = rooms.Games{
	Level: rooms.GamesNight, Frequency: new(0),
	Enabled: map[string]bool{"year": false, "liner": false, "sample": false, "lyrics": false, "finish_lyric": false},
}

func setupTunes(t *testing.T) (*fixture, *music, *cutter) {
	t.Helper()
	f := setup(t, tuneOnly)
	f.engine.Close()
	e := games.New(f.db, f.bus, f.rooms, games.Config{
		Announce: 30 * time.Millisecond, RevealFor: 60 * time.Millisecond, Seed: 1,
		Tune: games.TuneConfig{
			Clips: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond},
			Gap:   40 * time.Millisecond, Last: 60 * time.Millisecond, Reveal: 50 * time.Millisecond, Board: 20 * time.Millisecond,
		},
	})
	m, c := &music{}, &cutter{}
	e.Facts, e.Music, e.Clips, e.Tunes = tuneFacts{}, m, c, tunes{}
	t.Cleanup(e.Close)
	f.engine = e
	return f, m, c
}

// speak plays a song with a speaker in the room, who can play clips.
func (f *fixture) speak(i int) {
	it := f.items[i]
	f.rooms.PublishNowPlaying(rooms.NowPlaying{
		RoomID: f.room.ID, State: "playing", Item: &it, At: store.Now(), Position: 90 * time.Second,
		Player: &rooms.Player{UserID: "ann", DeviceID: "phone"},
	})
}

// start starts a tune round, once one's been cut.
func (f *fixture) start(t *testing.T, set int) *games.Round {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		rd, err := f.engine.Start(t.Context(), f.room.ID, store.User{ID: "ann"}, rooms.GameTune, set)
		if err == nil {
			return rd
		}
		if (!errors.Is(err, games.ErrNoQuestion) && !errors.Is(err, games.ErrRoundRunning)) || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNameThatTune(t *testing.T) {
	f, m, c := setupTunes(t)
	ctx := t.Context()
	f.speak(0)
	rd := f.start(t, 0)
	if rd.Kind != rooms.GameTune || rd.ItemID != f.items[0].ID || rd.Tune == nil || !strings.HasPrefix(rd.Tune.Title, "Tune tune") {
		t.Fatalf("%+v", rd)
	}
	q := rd.Question
	if q.Answer != quiz.AnswerChoice || len(q.Choices) != 4 || q.Correct != "“"+rd.Tune.Title+"” by Blondie" || len(q.Hides) != 0 {
		t.Fatalf("question %+v", q)
	}
	if len(rd.ClipsOut()) != 0 || len(rd.Clips) != 3 || rd.RevealClip == nil {
		t.Fatalf("clips out before it opens: %+v", rd.ClipsOut())
	}
	// Clips come from the loudest section, on its bar, all from one spot.
	cuts := c.of(strings.TrimPrefix(rd.Tune.Title, "Tune "))
	if len(cuts) != 4 || cuts[0].Start != 62*time.Second || cuts[3].Start != 62*time.Second || cuts[3].Length != 50*time.Millisecond {
		t.Errorf("cuts %+v", cuts)
	}

	rd2 := f.round(t, games.StateOpen)
	if out := rd2.ClipsOut(); len(out) != 1 || out[0].ID != rd.Clips[0].ID {
		t.Errorf("clips out as it opens: %+v", out)
	}
	if got := m.await(t, 1); !slices.Equal(got, []string{"break " + f.items[0].ID}) {
		t.Errorf("music: %v", got)
	}
	right := q.CorrectIndex
	if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: "bob"}, false, quiz.Response{Choice: &right}); err != nil {
		t.Fatal(err)
	}
	// One answer each: no waiting for a longer clip to change it.
	if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: "bob"}, false, quiz.Response{Choice: new(0)}); !errors.Is(err, games.ErrAnswered) {
		t.Errorf("a second answer: %v", err)
	}
	// Ann waits for the last clip.
	for {
		rd2 = f.round(t, games.StateOpen)
		if len(rd2.ClipsOut()) == 3 {
			break
		}
	}
	if _, err := f.engine.Answer(ctx, f.room.ID, rd.ID, store.User{ID: "ann"}, false, quiz.Response{Choice: &right}); err != nil {
		t.Fatal(err)
	}

	rd2 = f.round(t, games.StateReveal)
	if out := rd2.ClipsOut(); len(out) != 4 || out[3].ID != rd.RevealClip.ID {
		t.Errorf("clips at the reveal: %+v", out)
	}
	if bob, ann := rd2.Answers["bob"], rd2.Answers["ann"]; bob.Points != 1000 || ann.Points != 600 {
		t.Errorf("bob %d, ann %d", bob.Points, ann.Points)
	}
	// The music waits for the reveal's clip, then comes back where it stopped.
	if got := m.seen(); len(got) != 1 {
		t.Errorf("music at the reveal: %v", got)
	}
	f.round(t, games.StateDone)
	if got := m.await(t, 2); !slices.Equal(got, []string{"break " + f.items[0].ID, "resume " + f.items[0].ID}) || m.resume < 90*time.Second || m.resume > 92*time.Second {
		t.Errorf("music after: %v at %v", got, m.resume)
	}
}

func TestTuneSet(t *testing.T) {
	f, m, _ := setupTunes(t)
	ctx := t.Context()
	f.speak(0)
	rd := f.start(t, 5)
	if rd.Set == nil || rd.Set.Number != 1 || rd.Set.Size != 5 {
		t.Fatalf("set %+v", rd.Set)
	}
	seen := map[string]bool{}
	for n := 1; n <= 5; n++ {
		open := f.round(t, games.StateOpen)
		if open.Set == nil || open.Set.Number != n || open.Set.ID != rd.Set.ID {
			t.Fatalf("tune %d: set %+v", n, open.Set)
		}
		if seen[open.Tune.Title] {
			t.Errorf("tune %d: %s again", n, open.Tune.Title)
		}
		seen[open.Tune.Title] = true
		if n == 1 {
			right := open.Question.CorrectIndex
			if _, err := f.engine.Answer(ctx, f.room.ID, open.ID, store.User{ID: "bob"}, false, quiz.Response{Choice: &right}); err != nil {
				t.Fatal(err)
			}
		}
		rev := f.round(t, games.StateReveal)
		if rev.Set.Points["bob"] != 1000 {
			t.Errorf("tune %d: board %v", n, rev.Set.Points)
		}
		f.round(t, games.StateDone)
		// The music stays stopped until the set's over.
		if n < 5 && slices.ContainsFunc(m.seen(), func(c string) bool { return strings.HasPrefix(c, "resume") }) {
			t.Fatalf("tune %d: the music came back: %v", n, m.seen())
		}
	}
	if got := m.await(t, 6); got[len(got)-1] != "resume "+f.items[0].ID || slices.Index(got, got[len(got)-1]) != len(got)-1 {
		t.Errorf("music: %v", got)
	}
	if _, ok := f.engine.Current(f.room.ID); ok {
		t.Error("a sixth tune")
	}
	// The set counts once toward the hour's breaks.
	n, err := f.db.CountGameRoundsSince(ctx, store.CountGameRoundsSinceParams{RoomID: f.room.ID, Since: time.Time{}, Kinds: rooms.GameBreaks})
	if err != nil || n != 1 {
		t.Errorf("breaks: %d, %v", n, err)
	}
}

func TestTuneNeedsASpeaker(t *testing.T) {
	f, _, _ := setupTunes(t)
	f.play(0) // no speaker: nobody can play the clips
	time.Sleep(50 * time.Millisecond)
	if _, err := f.engine.Start(t.Context(), f.room.ID, store.User{ID: "ann"}, rooms.GameTune, 0); !errors.Is(err, games.ErrNoQuestion) {
		t.Errorf("a tune without a speaker: %v", err)
	}
	var invalid *games.InvalidInputError
	if _, err := f.engine.Start(t.Context(), f.room.ID, store.User{ID: "ann"}, rooms.GameTune, 3); !errors.As(err, &invalid) {
		t.Errorf("a set of 3: %v", err)
	}
	if _, err := f.engine.Start(t.Context(), f.room.ID, store.User{ID: "ann"}, rooms.GameYear, 5); !errors.As(err, &invalid) {
		t.Errorf("a set of years: %v", err)
	}
}

func TestTunePoints(t *testing.T) {
	at := time.Unix(1000, 0)
	cs := []games.Clip{{At: at}, {At: at.Add(5 * time.Second)}, {At: at.Add(10 * time.Second)}}
	for _, tc := range []struct {
		after     time.Duration
		closeness float64
		want      int
	}{
		{0, 1, 1000},
		{4 * time.Second, 1, 1000},
		{5 * time.Second, 1, 800},
		{12 * time.Second, 1, 600},
		{12 * time.Second, 0.5, 300},
		{0, 0, 0},
	} {
		if got := games.TunePoints(tc.closeness, at.Add(tc.after), cs); got != tc.want {
			t.Errorf("after %v at %v: %d, want %d", tc.after, tc.closeness, got, tc.want)
		}
	}
}
