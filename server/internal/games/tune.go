// SPDX-License-Identifier: AGPL-3.0-only

package games

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/clips"
	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/transcode"
)

// Name that tune (MAD-793): at Game night the music stops, the speaker
// plays a clip of another song, a second long, then two, then four, and
// phones name it. The sooner, the more it scores. At the reveal the clip
// plays on a few seconds from the same spot, then the room's music comes
// back where it stopped. A host can start a set of 5 or 10 tunes in a
// row, with the set's board between them.
//
// Each tune's clips are cut a song ahead (package clips), so a round never
// waits on a stream: the engine finds the room's next tune as each song
// starts, and starts a tune round only once its clips are cut.

// TuneConfig times name that tune. Zero values take the defaults.
type TuneConfig struct {
	// Clips are the clips' lengths, in the order they play. Default 1s,
	// 2s, 4s.
	Clips []time.Duration
	// Gap is how long after each clip, but the last, before the next.
	// Default 5s.
	Gap time.Duration
	// Last is how long answers stay open after the last clip. Default 8s.
	Last time.Duration
	// Reveal is how long the reveal's clip is. Default 8s.
	Reveal time.Duration
	// Board is how long a set's board stays up after each reveal, before
	// the next tune. Default 5s.
	Board time.Duration
}

func (c TuneConfig) withDefaults() TuneConfig {
	if len(c.Clips) == 0 {
		c.Clips = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	}
	if c.Gap == 0 {
		c.Gap = 5 * time.Second
	}
	if c.Last == 0 {
		c.Last = 8 * time.Second
	}
	if c.Reveal == 0 {
		c.Reveal = 8 * time.Second
	}
	if c.Board == 0 {
		c.Board = 5 * time.Second
	}
	return c
}

// SetSizes are how many tunes a set may have.
var SetSizes = []int{5, 10}

// Clips cuts songs' clips. clips.Service is one.
type Clips interface {
	// Cut starts cutting clips of a song for a room and returns their IDs.
	Cut(roomID string, song clips.Song, cuts []transcode.Cut) []string
	// Ready reports whether clips are all cut, or any failed.
	Ready(ids []string) (ready, failed bool)
}

// Tunes finds songs for name that tune. Sources is the production one.
type Tunes interface {
	// Tunes are songs a room could name from a clip, from where its
	// settings say (rooms.Tune*), in the order to try them. Only songs
	// that stream: a remote service's songs can't be clipped.
	Tunes(ctx context.Context, roomID, from string) ([]Tune, error)
}

// Tune is a song that can be clipped.
type Tune struct {
	// Item is its queue item, or one made up (with no ID) for a song the
	// room has never queued.
	Item store.QueueItem
	// Song is where it streams from.
	Song clips.Song
}

// prepared is a room's next tune: found, with its clips being cut.
type prepared struct {
	item  store.QueueItem
	facts quiz.Facts
	// spot is where in the song the clips are from (rooms.Clip*).
	spot string
	// clips are the round's clips' IDs, in order, and reveal the reveal's.
	clips  []string
	reveal string
	at     time.Time
}

// tuneTTL is how long a prepared tune's clips are kept for a round.
const tuneTTL = 15 * time.Minute

// tunesTried is how many candidates finding a tune looks at, at most; and
// tunesKept, how many songs used as tunes a room remembers, so they don't
// come back.
const (
	tunesTried = 6
	tunesKept  = 100
)

// prepare finds the room's next tune in the background, unless it has
// one, or is finding one. The caller holds mu.
func (e *Engine) prepare(roomID string, r *room) {
	if e.Clips == nil || e.Tunes == nil || e.Facts == nil || r.preparing || e.ctx.Err() != nil {
		return
	}
	if r.tune != nil && e.cfg.Now().Sub(r.tune.at) < tuneTTL {
		return
	}
	r.tune, r.preparing = nil, true
	e.background(func(ctx context.Context) {
		p, err := e.findTune(ctx, roomID)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Debug("games: finding a tune", "room", roomID, "err", err)
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if r := e.byRoom[roomID]; r != nil {
			r.tune, r.preparing = p, false
		}
	})
}

// findTune finds a song for the room's next tune and starts cutting its
// clips. It's nil if the room doesn't play name that tune, or there's no
// song to clip.
func (e *Engine) findTune(ctx context.Context, roomID string) (*prepared, error) {
	row, err := e.rooms.Get(ctx, roomID)
	if err != nil {
		return nil, err
	}
	g := rooms.ParseSettings(row.Settings).Games
	if !g.On(rooms.GameTune) {
		return nil, nil
	}
	t := g.TuneOf()
	cands, err := e.Tunes.Tunes(ctx, roomID, t.From)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	r := e.roomOf(roomID)
	skip := slices.Clone(r.tuned)
	if r.np.Item != nil {
		skip = append(skip, key(*r.np.Item))
	}
	e.mu.Unlock()
	tc := e.cfg.Tune
	longest := max(slices.Max(tc.Clips), tc.Reveal)
	tried := 0
	for _, c := range cands {
		if slices.Contains(skip, key(c.Item)) {
			continue
		}
		if tried++; tried > tunesTried {
			break
		}
		f, err := e.tuneFacts(ctx, c.Item)
		if err != nil || !quiz.Tuneable(f) {
			continue
		}
		start := time.Duration(quiz.ClipStart(f, t.Clip, longest.Milliseconds())) * time.Millisecond
		cuts := make([]transcode.Cut, 0, len(tc.Clips)+1)
		for _, l := range tc.Clips {
			cuts = append(cuts, transcode.Cut{Start: start, Length: l})
		}
		cuts = append(cuts, transcode.Cut{Start: start, Length: tc.Reveal})
		song := c.Song
		song.Duration = time.Duration(f.DurationMs) * time.Millisecond
		ids := e.Clips.Cut(roomID, song, cuts)
		return &prepared{item: c.Item, facts: f, spot: t.Clip, clips: ids[:len(tc.Clips)], reveal: ids[len(tc.Clips)], at: e.cfg.Now()}, nil
	}
	return nil, nil
}

// tuneFacts are what's known about a tune's song, asked for now if
// they're not cached.
func (e *Engine) tuneFacts(ctx context.Context, it store.QueueItem) (quiz.Facts, error) {
	if f, ok := e.ready.get(key(it)); ok {
		return f, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	f, err := e.Facts.Song(ctx, it, false)
	if err != nil {
		return quiz.Facts{}, err
	}
	e.ready.put(key(it), f)
	return f, nil
}

// readyTune is the room's next tune, if its clips are cut and there's a
// speaker to play them. A tune whose clips failed is dropped, and another
// found.
func (e *Engine) readyTune(roomID string) *prepared {
	if e.Clips == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.roomOf(roomID)
	p := r.tune
	if p == nil || r.np.Player == nil {
		return nil
	}
	if e.cfg.Now().Sub(p.at) >= tuneTTL {
		e.prepare(roomID, r)
		return nil
	}
	ready, failed := e.Clips.Ready(append(slices.Clone(p.clips), p.reveal))
	if failed {
		r.tuned = remember(r.tuned, key(p.item))
		r.tune = nil
		e.prepare(roomID, r)
		return nil
	}
	if !ready {
		return nil
	}
	return p
}

func remember(keys []string, k string) []string {
	keys = append(keys, k)
	return keys[max(0, len(keys)-tunesKept):]
}

// tuneRound starts a tune round. The caller holds mu.
func (e *Engine) tuneRound(roomID string, r *room, p *prepared, q quiz.Question, g rooms.Games, by string, set *Set, now time.Time) *Round {
	tc := e.cfg.Tune
	opens := now.Add(e.cfg.Announce)
	at := opens
	cs := make([]Clip, len(p.clips))
	for i, id := range p.clips {
		cs[i] = Clip{ID: id, Length: tc.Clips[i], At: at}
		at = at.Add(tc.Clips[i] + tc.Gap)
	}
	last := cs[len(cs)-1]
	closes := last.At.Add(last.Length + tc.Last)
	done := closes.Add(max(e.cfg.RevealFor, tc.Reveal))
	if set != nil {
		done = done.Add(tc.Board)
	}
	track := queuedTrack(p.item)
	rd := &Round{
		ID: store.NewID(), RoomID: roomID, ItemID: idOf(r.np.Item), Kind: rooms.GameTune, Mode: ModeRound, State: StateAnnounce,
		Question: q, StartedBy: by, StartedAt: now, OpensAt: opens, ClosesAt: closes, DoneAt: done,
		Guests: g.GuestsAnswer(), TVOnly: g.TVOnly, Scores: g.ScoreMode(), Answers: map[string]*Answer{},
		Breaks: true, ResumeOnDone: true, Clips: cs, RevealClip: &Clip{ID: p.reveal, Length: tc.Reveal, At: closes},
		Tune: &track,
	}
	if set != nil {
		set.Number++
		r.set = set
		rd.Set = set.snapshot()
	}
	r.round, r.songs, r.tune = rd, 0, nil
	r.tuned = remember(r.tuned, key(p.item))
	e.announce(rd)
	e.at(rd, opens, StateAnnounce, func() { e.open(rd) })
	for i := 1; i < len(cs); i++ {
		e.at(rd, cs[i].At, StateOpen, func() {
			rd.Shown = i + 1
			e.publish(rd)
		})
	}
	e.at(rd, closes, StateOpen, func() { e.reveal(rd) })
	// The next tune is cut while this one plays.
	e.prepare(rd.RoomID, r)
	return rd
}

// TunePoints scores a tune answer by which clip it came in on: on the
// first, a right answer scores 1000; on the second, 800; after that, 600.
// Half right (the artist, typed) scores half.
func TunePoints(closeness float64, at time.Time, cs []Clip) int {
	if closeness <= 0 {
		return 0
	}
	stage := 0
	for i, c := range cs {
		if !at.Before(c.At) {
			stage = i
		}
	}
	bonus := []float64{500, 300, 100}[min(stage, 2)]
	return int(math.Round(closeness * (500 + bonus)))
}

// --- Sets ------------------------------------------------------------------

// Set is a set of tunes, run back to back with the music stopped.
type Set struct {
	ID string
	// Size is how many tunes; Number, which one this is (from 1).
	Size, Number int
	StartedBy    string
	// Points are each player's points in the set so far.
	Points map[string]int
	// resume and resumeAt are whether the music was playing when the set
	// stopped it, and where: the set's last tune picks it up there.
	resume   bool
	resumeAt time.Duration
}

func (s *Set) snapshot() *Set {
	if s == nil {
		return nil
	}
	c := *s
	c.Points = maps.Clone(s.Points)
	return &c
}

func setID(rd Round) string {
	if rd.Set == nil {
		return ""
	}
	return rd.Set.ID
}

// setWait is how long a set waits for its next tune's clips, and how
// often it looks, before it ends early.
const (
	setWait = 20 * time.Second
	setPoll = time.Second
)

// nextTune starts a set's next tune, once its clips are cut. If they
// never are, the set ends early and the music comes back. The caller
// holds mu.
func (e *Engine) nextTune(roomID string, r *room, s *Set, prev *Round) {
	r.starting = true
	itemID := prev.ItemID
	e.background(func(ctx context.Context) {
		defer e.doneStarting(roomID)
		deadline := e.cfg.Now().Add(setWait)
		for {
			rd, err := e.continueSet(ctx, roomID, itemID, s)
			if rd != nil || ctx.Err() != nil {
				return
			}
			if !errors.Is(err, ErrNoQuestion) || !e.cfg.Now().Before(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(setPoll):
			}
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if r := e.byRoom[roomID]; r != nil && r.set == s {
			r.set = nil
			e.resume(&Round{RoomID: roomID, ItemID: itemID, Breaks: true, Resume: s.resume, ResumeAt: s.resumeAt})
		}
	})
}

// continueSet starts a set's next tune, if the set's still on and its song
// is still the room's.
func (e *Engine) continueSet(ctx context.Context, roomID, itemID string, s *Set) (*Round, error) {
	row, err := e.rooms.Get(ctx, roomID)
	if err != nil {
		return nil, err
	}
	g := rooms.ParseSettings(row.Settings).Games
	e.mu.Lock()
	r := e.roomOf(roomID)
	np := r.np
	on := r.set == s && r.round == nil
	e.mu.Unlock()
	if !on || np.Item == nil || np.Item.ID != itemID || !g.On(rooms.GameTune) {
		return nil, errors.New("the set's over")
	}
	return e.makeRound(ctx, roomID, *np.Item, g, s.StartedBy, rooms.GameTune, s)
}
