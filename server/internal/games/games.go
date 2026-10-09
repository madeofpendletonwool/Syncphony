// SPDX-License-Identifier: AGPL-3.0-only

// Package games runs a room's music party games (Phase 10, ADR 0015). The
// server is the authority, as with playback: it starts each round, times
// it to the song's beat map, takes answers by when it receives them,
// scores them, and keeps the answer from every screen until the reveal.
// Clients only show rounds and send answers.
//
// One round runs at a time per room, in memory like playback state:
// announce → open → reveal → done. Finished rounds and their answers are
// kept, for the night's scores and its awards. Each game (guess the year,
// liner-notes trivia…) is a round kind, drawing its question from package
// quiz.
package games

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Errors returned by Engine.
var (
	ErrNoRound        = errors.New("that round isn't open")
	ErrRoundRunning   = errors.New("a round is already running")
	ErrForbidden      = errors.New("you can't start rounds in this room")
	ErrGamesOff       = errors.New("games are off in this room")
	ErrGuestsCantPlay = errors.New("guests can't answer in this room")
	ErrNothingPlaying = errors.New("nothing's playing to ask about")
	ErrNoQuestion     = errors.New("there's nothing to ask about this song yet")
	ErrAnswered       = errors.New("you've answered this one")
)

// InvalidInputError is an answer of the wrong kind.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

// Round states. A round about a moment later in the song (a lyric) waits
// pending, unseen, until its announce.
const (
	StatePending  = "pending"
	StateAnnounce = "announce"
	StateOpen     = "open"
	StateReveal   = "reveal"
	StateDone     = "done"
)

// Round modes: how loudly a round runs.
const (
	// ModeAmbient: a question you can ignore, on phones and the big screen.
	ModeAmbient = "ambient"
	// ModeRound: a round with a countdown and a reveal on the big screen.
	ModeRound = "round"
)

// Config tunes the engine. Zero values take the defaults.
type Config struct {
	// Announce is how long a round shows its question before answers
	// open. Default 4s.
	Announce time.Duration
	// AmbientWindow and RoundWindow are how long answers stay open, before
	// lining up with the music. Defaults 30s and 20s.
	AmbientWindow, RoundWindow time.Duration
	// RevealFor is how long the reveal stays up. Default 10s.
	RevealFor time.Duration
	// LyricLead is how long before its line beat the singer shows the
	// line with words blanked. Default 8s.
	LyricLead time.Duration
	// Tune times name that tune; zero values take its defaults.
	Tune TuneConfig
	// Now is the clock. Default store.Now.
	Now func() time.Time
	// Seed seeds the engine's choices, for tests. 0 is random.
	Seed uint64
}

// Engine runs every room's rounds.
type Engine struct {
	cfg   Config
	db    *store.Store
	bus   realtime.Bus
	rooms *rooms.Service
	// Facts finds what's known about songs, for questions. Nil runs no
	// trivia: there's nothing to ask.
	Facts Facts
	// OnHide, if set, is called when a round starts or stops hiding the
	// playing song, so screens can be sent it again without (or with) it.
	OnHide func(ctx context.Context, roomID string)
	// Music pauses and resumes the music for rounds that stop it (finish
	// the lyric, name that tune). Nil runs none of them.
	Music Music
	// Clips cuts songs' clips, and Tunes finds songs to cut them from, for
	// name that tune. Nil runs none.
	Clips Clips
	Tunes Tunes

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	rng    *rand.Rand
	byRoom map[string]*room
	ready  *readiness
	// timers are the rounds' pending state changes, stopped on Close.
	timers map[*time.Timer]struct{}
}

// Facts finds what's known about a song and its neighbourhood. Sources is
// the production one.
type Facts interface {
	// Song returns what's known about a queued song. cachedOnly asks only
	// what's already been found, without asking any service.
	Song(ctx context.Context, it store.QueueItem, cachedOnly bool) (quiz.Facts, error)
	// Pool finds wrong answers for questions about a song in a room.
	Pool(ctx context.Context, roomID string, it store.QueueItem, f quiz.Facts) quiz.Pool
}

// Music pauses a room's music for a round, and plays it on after.
// playback.Engine is one.
type Music interface {
	// Break pauses itemID where it is, if it's the song playing.
	Break(ctx context.Context, roomID, itemID string) error
	// Resume plays itemID on from at, if it's still the room's song.
	Resume(ctx context.Context, roomID, itemID string, at time.Duration) error
}

// room is one room's games: the current round, and what's played since
// the last one.
type room struct {
	np rooms.NowPlaying
	// prev is the song that played before this one, for higher or lower.
	prev  *store.QueueItem
	round *Round
	// songs counts songs that started since the last round.
	songs int
	// starting is set while a round is being made, so two aren't.
	starting bool
	// tune is the next name that tune, cut ahead; preparing is set while
	// it's being found. tuned are the songs lately used as tunes.
	tune      *prepared
	preparing bool
	tuned     []string
	// set is the set of tunes running, if one is.
	set *Set
}

// Round is a round of a game.
type Round struct {
	ID, RoomID string
	// ItemID is the song it's about.
	ItemID   string
	Kind     string
	Mode     string
	State    string
	Question quiz.Question
	// StartedBy is who started it, "" if the room's frequency did.
	StartedBy string
	StartedAt time.Time
	// OpensAt is when answers open; ClosesAt when they close and the
	// answer is revealed; DoneAt when the reveal comes down.
	OpensAt, ClosesAt, DoneAt time.Time
	// Guests may answer; TVOnly keeps it off phones.
	Guests, TVOnly bool
	// Scores is who sees scores (rooms.Scores*).
	Scores  string
	Answers map[string]*Answer
	// Breaks is set when the round stops the music while answers are open.
	Breaks bool
	// Resume plays the music on from ResumeAt once the round's over: at
	// its reveal, or when it's done if ResumeOnDone (after a tune's reveal
	// clip, or a set's last tune).
	Resume       bool
	ResumeAt     time.Duration
	ResumeOnDone bool
	// Clips are a tune's clips, longer each time, and RevealClip the one
	// played at its reveal. Shown is how many of Clips are out so far: a
	// clip's ID goes to screens only once it's time to play it.
	Clips      []Clip
	RevealClip *Clip
	Shown      int
	// Tune is the song a tune round is about: not the one playing.
	Tune *provider.Track
	// Set is the set of tunes it's part of, if any.
	Set *Set
}

// Clip is one of a round's clips: Length of the song, played At.
type Clip struct {
	ID     string
	Length time.Duration
	At     time.Time
}

// ClipsOut are a round's clips screens may have now: those whose time has
// come, and from the reveal on, the reveal clip.
func (rd Round) ClipsOut() []Clip {
	out := slices.Clone(rd.Clips[:min(rd.Shown, len(rd.Clips))])
	if rd.RevealClip != nil && (rd.State == StateReveal || rd.State == StateDone) {
		out = append(out, *rd.RevealClip)
	}
	return out
}

// Answer is someone's answer to a round.
type Answer struct {
	UserID   string
	Response quiz.Response
	// At is when the server got it (the latest change).
	At        time.Time
	Correct   bool
	Closeness float64
	// Closest is set on the guesses nearest a number answer.
	Closest bool
	Points  int
}

// New returns an Engine, and registers it for rooms' playback, so rounds
// start as songs do. Call Close when done.
func New(db *store.Store, bus realtime.Bus, rs *rooms.Service, cfg Config) *Engine {
	if cfg.Announce == 0 {
		cfg.Announce = 4 * time.Second
	}
	if cfg.AmbientWindow == 0 {
		cfg.AmbientWindow = 30 * time.Second
	}
	if cfg.RoundWindow == 0 {
		cfg.RoundWindow = 20 * time.Second
	}
	if cfg.RevealFor == 0 {
		cfg.RevealFor = 10 * time.Second
	}
	if cfg.LyricLead == 0 {
		cfg.LyricLead = 8 * time.Second
	}
	cfg.Tune = cfg.Tune.withDefaults()
	if cfg.Now == nil {
		cfg.Now = store.Now
	}
	seed := cfg.Seed
	if seed == 0 {
		seed = rand.Uint64() //nolint:gosec // picking questions, not secrets
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		cfg: cfg, db: db, bus: bus, rooms: rs, ctx: ctx, cancel: cancel,
		rng:    rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), //nolint:gosec // as above
		byRoom: map[string]*room{}, ready: newReadiness(),
		timers: map[*time.Timer]struct{}{},
	}
	prev := rs.OnNowPlaying
	rs.OnNowPlaying = func(np rooms.NowPlaying) {
		if prev != nil {
			prev(np)
		}
		e.NowPlaying(np)
	}
	return e
}

// Close stops background work. Rounds in progress are dropped.
func (e *Engine) Close() {
	e.cancel()
	e.mu.Lock()
	for t := range e.timers {
		t.Stop()
	}
	clear(e.timers)
	e.mu.Unlock()
	e.wg.Wait()
}

func (e *Engine) roomOf(id string) *room {
	r := e.byRoom[id]
	if r == nil {
		r = &room{}
		e.byRoom[id] = r
	}
	return r
}

// background runs fn in the background until Close.
func (e *Engine) background(fn func(ctx context.Context)) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		fn(e.ctx)
	}()
}

// NowPlaying follows a room's playback: a new song counts toward the next
// round, and may start one; a round about a song that's no longer playing
// is revealed at once.
func (e *Engine) NowPlaying(np rooms.NowPlaying) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx.Err() != nil {
		return
	}
	r := e.roomOf(np.RoomID)
	before := r.np.Item
	r.np = np
	now := idOf(np.Item)
	if now == idOf(before) {
		return
	}
	if before != nil {
		r.prev = before
	}
	if rd := r.round; rd != nil && rd.ItemID != now {
		switch rd.State {
		case StatePending:
			// Its line won't be sung now. Nobody saw it.
			rd.State = StateDone
			r.round = nil
		case StateAnnounce, StateOpen:
			e.reveal(rd)
		}
	}
	if now == "" {
		return
	}
	r.songs++
	e.prepare(np.RoomID, r)
	if r.round == nil && !r.starting {
		id := np.RoomID
		r.starting = true
		e.background(func(ctx context.Context) {
			defer e.doneStarting(id)
			if _, err := e.auto(ctx, id); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("games: starting a round", "room", id, "err", err)
			}
		})
	}
}

func (e *Engine) doneStarting(roomID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.roomOf(roomID).starting = false
}

// auto starts a round if the room's frequency says one is due. It reads
// the room's settings as the song starts, so a level change applies from
// the next song.
func (e *Engine) auto(ctx context.Context, roomID string) (*Round, error) {
	row, err := e.rooms.Get(ctx, roomID)
	if err != nil {
		return nil, err
	}
	g := rooms.ParseSettings(row.Settings).Games
	e.mu.Lock()
	r := e.roomOf(roomID)
	due := g.Plays() && g.Every() > 0 && r.songs >= g.Every() && r.round == nil
	np := r.np
	e.mu.Unlock()
	if !due || np.Item == nil {
		return nil, nil
	}
	rd, err := e.makeRound(ctx, roomID, *np.Item, g, "", "", nil)
	if errors.Is(err, ErrNoQuestion) {
		return nil, nil // the next song may do
	}
	return rd, err
}

// Start starts a round now, of a kind or ("") any the song can carry. Who
// may is set by the room's Start rounds permission; admins always may.
// set > 0 starts a set of that many tunes (SetSizes), back to back.
func (e *Engine) Start(ctx context.Context, roomID string, by store.User, kind string, set int) (*Round, error) {
	row, err := e.rooms.Get(ctx, roomID)
	if err != nil {
		return nil, err
	}
	st := rooms.ParseSettings(row.Settings)
	if !st.Games.Plays() {
		return nil, ErrGamesOff
	}
	if by.Role != store.RoleAdmin && !rooms.Allowed(st.Permissions.StartRounds, row.OwnerID, by.ID) {
		return nil, ErrForbidden
	}
	if set > 0 {
		if kind == "" {
			kind = rooms.GameTune
		}
		if kind != rooms.GameTune {
			return nil, &InvalidInputError{"only name that tune runs in sets"}
		}
		if !slices.Contains(SetSizes, set) {
			return nil, &InvalidInputError{"a set is 5 or 10 tunes"}
		}
	}
	if kind != "" && !st.Games.On(kind) {
		return nil, &InvalidInputError{"that game is off in this room"}
	}
	e.mu.Lock()
	r := e.roomOf(roomID)
	if r.round != nil || r.starting {
		e.mu.Unlock()
		return nil, ErrRoundRunning
	}
	np := r.np
	if np.Item == nil {
		e.mu.Unlock()
		return nil, ErrNothingPlaying
	}
	r.starting = true
	e.mu.Unlock()
	defer e.doneStarting(roomID)
	var s *Set
	if set > 0 {
		s = &Set{ID: store.NewID(), Size: set, StartedBy: by.ID, Points: map[string]int{}}
	}
	return e.makeRound(ctx, roomID, *np.Item, st.Games, by.ID, kind, s)
}

// makeRound makes a round about the playing song, or for name that tune
// a song cut ahead, and starts it. It returns a copy: the round itself
// moves on under mu. A round in a set (of tunes) goes on the
// set's break; the set's first takes one from the hour's budget.
func (e *Engine) makeRound(ctx context.Context, roomID string, it store.QueueItem, g rooms.Games, by, kind string, set *Set) (*Round, error) {
	if e.Facts == nil {
		return nil, ErrNoQuestion
	}
	f, err := e.song(ctx, it)
	var ready []string
	if err == nil {
		ready = quiz.Ready(f)
	}
	kinds := g.Kinds()
	if kind != "" {
		kinds = []string{kind}
	}
	breaks := (set != nil && set.Number > 0) || e.breaksLeft(ctx, roomID, g)
	tune := e.readyTune(roomID)
	kinds = slices.DeleteFunc(kinds, func(k string) bool {
		if k == rooms.GameTune {
			// About a song cut ahead, so it needs its clips, a speaker to
			// play them, and a break.
			return tune == nil || e.Music == nil || !breaks
		}
		if !slices.Contains(ready, k) || set != nil {
			return true
		}
		// Games that stop the music need a player and the hour's budget.
		// Queue games aren't about one song.
		return slices.Contains(rooms.GameBreaks, k) && (e.Music == nil || !breaks)
	})
	if len(kinds) == 0 {
		return nil, ErrNoQuestion
	}
	var pool, tunePool quiz.Pool
	if len(ready) > 0 {
		pool = e.Facts.Pool(ctx, roomID, it, f)
	}
	if tune != nil && slices.Contains(kinds, rooms.GameTune) {
		tunePool = e.Facts.Pool(ctx, roomID, tune.item, tune.facts)
	}
	pool.ThisYear = e.cfg.Now().Year()

	e.mu.Lock()
	r := e.roomOf(roomID)
	prev := r.prev
	e.mu.Unlock()
	if prev != nil && prev.ID != it.ID {
		pf, ok := e.ready.get(key(*prev))
		if !ok {
			pf, err = e.Facts.Song(ctx, *prev, true)
			ok = err == nil
		}
		if ok && pf.Year > 0 {
			pool.Previous = &quiz.Dated{Song: pf.Song, Year: pf.Year}
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if r.round != nil {
		return nil, ErrRoundRunning
	}
	if idOf(r.np.Item) != it.ID {
		return nil, ErrNoQuestion // the song changed while we looked
	}
	e.rng.Shuffle(len(kinds), func(i, j int) { kinds[i], kinds[j] = kinds[j], kinds[i] })
	now := e.cfg.Now()
	mode := ModeRound
	if g.LevelOf() == rooms.GamesAmbient {
		mode = ModeAmbient
	}
	for _, k := range kinds {
		if k == rooms.GameTune {
			if r.tune != tune {
				continue // used meanwhile
			}
			q, ok := quiz.NameTune(tune.facts, tunePool, g.TuneOf().Typed, tune.spot, e.rng)
			if !ok {
				r.tune = nil // its pool won't grow; find another
				e.prepare(roomID, r)
				continue
			}
			out := snapshot(e.tuneRound(roomID, r, tune, q, g, by, set, now))
			return &out, nil
		}
		// A lyric question is about a line far enough ahead to show it in time.
		pool.AfterMs = (position(r.np, now) + e.lead(k) + time.Second).Milliseconds()
		q, ok := quiz.Ask(k, f, pool, e.rng)
		if !ok {
			continue
		}
		start, opens, closes, ok := e.times(r.np, f, q, mode, now)
		if !ok {
			continue
		}
		rd := &Round{
			ID: store.NewID(), RoomID: roomID, ItemID: it.ID, Kind: k, Mode: mode, State: StateAnnounce,
			Question: q, StartedBy: by, StartedAt: now, OpensAt: opens, ClosesAt: closes, DoneAt: closes.Add(e.cfg.RevealFor),
			Guests: g.GuestsAnswer(), TVOnly: g.TVOnly, Scores: g.ScoreMode(), Answers: map[string]*Answer{},
			Breaks: slices.Contains(rooms.GameBreaks, k),
		}
		if rd.Breaks {
			// The music comes back on the line, as the answer.
			rd.Resume, rd.ResumeAt = true, time.Duration(q.AtMs)*time.Millisecond
		}
		r.round, r.songs = rd, 0
		if start.After(now) {
			// Wait, unseen, until it's time to show the line.
			rd.State = StatePending
			e.at(rd, start, StatePending, func() { e.announce(rd) })
		} else {
			e.announce(rd)
		}
		e.at(rd, opens, StateAnnounce, func() { e.open(rd) })
		e.at(rd, closes, StateOpen, func() { e.reveal(rd) })
		out := snapshot(rd)
		return &out, nil
	}
	return nil, ErrNoQuestion
}

// lead is how far ahead of now a game's question has to be about: the
// announce, and for beat the singer, the time to read the line.
func (e *Engine) lead(kind string) time.Duration {
	switch kind {
	case rooms.GameLyrics:
		return e.cfg.Announce + e.cfg.LyricLead
	case rooms.GameFinishLyric:
		return e.cfg.Announce
	}
	return 0
}

// breaksLeft reports whether the hour's budget of rounds that stop the
// music has any left.
func (e *Engine) breaksLeft(ctx context.Context, roomID string, g rooms.Games) bool {
	if g.Breaks() == 0 {
		return false
	}
	n, err := e.db.CountGameRoundsSince(ctx, store.CountGameRoundsSinceParams{
		RoomID: roomID, Since: e.cfg.Now().Add(-time.Hour), Kinds: rooms.GameBreaks,
	})
	return err == nil && n < int64(g.Breaks())
}

// announce shows a round's question. The caller holds mu.
func (e *Engine) announce(rd *Round) {
	rd.State = StateAnnounce
	e.publish(rd)
	e.hide(rd)
}

// song returns what's known about a song: from the cache made as it was
// queued, or asked for now, briefly.
func (e *Engine) song(ctx context.Context, it store.QueueItem) (quiz.Facts, error) {
	if f, ok := e.ready.get(key(it)); ok {
		return f, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	f, err := e.Facts.Song(ctx, it, false)
	if err != nil {
		return quiz.Facts{}, ErrNoQuestion
	}
	e.ready.put(key(it), f)
	return f, nil
}

// Song positions are kept a little clear of the song's end.
const endMargin = 3 * time.Second

// times works out when a round is announced, and when its answers open
// and close. With a beat map, answers open on a section's start and close
// on the next section boundary, or a downbeat, so the reveal lands with
// the music; without one, they're fixed timers. Beat the singer opens a
// little before its line and closes as the singer gets there; finish the
// lyric opens as its line would begin, with the music stopped. ok is
// false if the song doesn't have long enough left.
func (e *Engine) times(np rooms.NowPlaying, f quiz.Facts, q quiz.Question, mode string, now time.Time) (start, opens, closes time.Time, ok bool) {
	pos := position(np, now)
	at := func(ms int64) time.Time { return now.Add(time.Duration(ms)*time.Millisecond - pos) }
	ms := func(t time.Time) int64 { return (pos + t.Sub(now)).Milliseconds() }
	window := e.cfg.RoundWindow
	if mode == ModeAmbient {
		window = e.cfg.AmbientWindow
	}
	end := time.Time{}
	if f.DurationMs > 0 {
		end = at(f.DurationMs).Add(-endMargin)
	}
	inSong := func(t time.Time) bool { return end.IsZero() || !t.After(end) }
	switch q.Kind {
	case rooms.GameLyrics:
		closes = at(q.AtMs)
		opens = closes.Add(-e.cfg.LyricLead)
		start = opens.Add(-e.cfg.Announce)
		return start, opens, closes, !start.Before(now) && inSong(closes)
	case rooms.GameFinishLyric:
		opens = at(q.AtMs)
		start = opens.Add(-e.cfg.Announce)
		return start, opens, opens.Add(e.cfg.RoundWindow), !start.Before(now) && inSong(opens)
	}
	start, opens = now, now.Add(e.cfg.Announce)
	// Open on the next section start if it's soon.
	if s, found := next(sectionStarts(f), ms(opens)); found && at(s).Sub(opens) <= 8*time.Second {
		opens = at(s)
	}
	closes = opens.Add(window)
	if s, found := next(sectionStarts(f), ms(closes)); found && at(s).Sub(closes) <= 8*time.Second {
		closes = at(s)
	} else if b, found := next(f.Bars, ms(closes)); found && at(b).Sub(closes) <= 4*time.Second {
		closes = at(b)
	}
	if !end.IsZero() && closes.After(end) {
		closes = end
	}
	return start, opens, closes, closes.Sub(opens) >= window/2
}

func sectionStarts(f quiz.Facts) []int64 {
	out := make([]int64, 0, len(f.Sections))
	for _, s := range f.Sections {
		out = append(out, s.StartMs)
	}
	return out
}

// next is the first of times (ascending ms) at or after ms.
func next(times []int64, ms int64) (int64, bool) {
	for _, t := range times {
		if t >= ms {
			return t, true
		}
	}
	return 0, false
}

// position is the song's playback position at now.
func position(np rooms.NowPlaying, now time.Time) time.Duration {
	if np.State == "playing" {
		return np.Position + now.Sub(np.At)
	}
	return np.Position
}

// at runs fn at t, if the round is still in state then. The caller holds mu.
func (e *Engine) at(rd *Round, t time.Time, state string, fn func()) {
	var timer *time.Timer
	timer = time.AfterFunc(max(t.Sub(e.cfg.Now()), 0), func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.timers, timer)
		// Close cancels before it stops timers, so a change that sees the
		// engine running starts its background work before Close waits.
		if e.ctx.Err() != nil || rd.State != state || e.byRoom[rd.RoomID] == nil || e.byRoom[rd.RoomID].round != rd {
			return
		}
		fn()
	})
	e.timers[timer] = struct{}{}
}

// open opens a round for answers, stopping the music if it's that kind
// of round. The caller holds mu.
func (e *Engine) open(rd *Round) {
	rd.State = StateOpen
	if len(rd.Clips) > 0 {
		rd.Shown = 1
	}
	if rd.Breaks && rd.ResumeOnDone {
		// A tune picks the music up again where it stopped, if it was
		// playing; a set's tunes, where its first stopped it, and only
		// after its last.
		r := e.byRoom[rd.RoomID]
		resume, at := r.np.State == "playing", position(r.np, e.cfg.Now())
		if s := rd.Set; s != nil && s.Number > 1 {
			resume, at = s.resume, s.resumeAt
		} else if s := r.set; s != nil && rd.Set != nil && s.ID == rd.Set.ID {
			s.resume, s.resumeAt = resume, at
		}
		rd.Resume, rd.ResumeAt = resume && (rd.Set == nil || rd.Set.Number >= rd.Set.Size), at
	}
	e.publish(rd)
	if rd.Breaks && e.Music != nil {
		e.background(func(ctx context.Context) {
			if err := e.Music.Break(ctx, rd.RoomID, rd.ItemID); err != nil {
				slog.Warn("games: stopping the music", "room", rd.RoomID, "err", err)
			}
		})
	}
}

// reveal closes a round, scores it, keeps it, and shows the answer. The
// caller holds mu.
func (e *Engine) reveal(rd *Round) {
	now := e.cfg.Now()
	if now.Before(rd.ClosesAt) {
		rd.ClosesAt = now
		rd.DoneAt = now.Add(e.cfg.RevealFor)
	}
	rd.State = StateReveal
	e.hide(rd)
	rd.Shown = len(rd.Clips)
	for _, a := range rd.Answers {
		a.Correct, a.Closeness = quiz.Check(rd.Question, a.Response)
		if len(rd.Clips) > 0 {
			a.Points = TunePoints(a.Closeness, a.At, rd.Clips)
		} else {
			a.Points = Points(a.Closeness, a.At, rd.OpensAt, rd.ClosesAt)
		}
	}
	closest(rd)
	if s := e.byRoom[rd.RoomID].set; s != nil && rd.Set != nil && s.ID == rd.Set.ID {
		for _, a := range rd.Answers {
			s.Points[a.UserID] += a.Points
		}
		rd.Set = s.snapshot()
	}
	e.publish(rd)
	if !rd.ResumeOnDone {
		e.resume(rd)
	}
	e.at(rd, rd.DoneAt, StateReveal, func() { e.done(rd) })
	// Keep it, then send the night's scores. In the background: it's I/O,
	// and the caller holds mu.
	answers := make([]Answer, 0, len(rd.Answers))
	for _, a := range rd.Answers {
		answers = append(answers, *a)
	}
	saved := *rd
	e.background(func(ctx context.Context) {
		if err := e.save(ctx, saved, answers); err != nil {
			slog.Warn("games: keeping a round", "room", saved.RoomID, "err", err)
			return
		}
		if saved.Scores != rooms.ScoresOff && len(answers) > 0 {
			if s, err := e.Scores(ctx, saved.RoomID); err == nil {
				e.bus.Publish(realtime.RoomTopic(saved.RoomID), realtime.Event{Type: realtime.GameScores, Data: s})
			}
		}
	})
}

// hide tells OnHide a round hides, or has stopped hiding, the song. The
// caller holds mu.
func (e *Engine) hide(rd *Round) {
	if e.OnHide == nil || !slices.Contains(rd.Question.Hides, quiz.HideSong) {
		return
	}
	e.background(func(ctx context.Context) { e.OnHide(ctx, rd.RoomID) })
}

// resume plays the music on after a round that stopped it. The caller
// holds mu.
func (e *Engine) resume(rd *Round) {
	if !rd.Breaks || !rd.Resume || e.Music == nil {
		return
	}
	e.background(func(ctx context.Context) {
		if err := e.Music.Resume(ctx, rd.RoomID, rd.ItemID, rd.ResumeAt); err != nil {
			slog.Warn("games: resuming the music", "room", rd.RoomID, "err", err)
		}
	})
}

// done takes a round down: then a set plays its next tune, or the music
// comes back. The caller holds mu.
func (e *Engine) done(rd *Round) {
	rd.State = StateDone
	e.publish(rd)
	r := e.byRoom[rd.RoomID]
	r.round = nil
	if s := r.set; s != nil && rd.Set != nil && rd.Set.ID == s.ID {
		if s.Number < s.Size && idOf(r.np.Item) == rd.ItemID {
			e.nextTune(rd.RoomID, r, s, rd)
			return
		}
		r.set = nil
	}
	if rd.ResumeOnDone {
		e.resume(rd)
	}
}

// Bonuses on a number answer: exactly right, and nearest in the room.
const (
	ExactBonus   = 250
	ClosestBonus = 100
)

// closest marks the guesses nearest a number answer, and adds their
// bonuses: an exact one scores ExactBonus more; otherwise the nearest
// that scored at all, ClosestBonus. The caller holds mu.
func closest(rd *Round) {
	q := rd.Question
	if q.Answer != quiz.AnswerNumber {
		return
	}
	want, err := strconv.Atoi(q.Correct)
	if err != nil {
		return
	}
	best := -1
	for _, a := range rd.Answers {
		if a.Response.Number != nil && (best < 0 || abs(*a.Response.Number-want) < best) {
			best = abs(*a.Response.Number - want)
		}
	}
	for _, a := range rd.Answers {
		if a.Response.Number == nil || abs(*a.Response.Number-want) != best {
			continue
		}
		a.Closest = true
		switch {
		case best == 0:
			a.Points += ExactBonus
		case a.Points > 0:
			a.Points += ClosestBonus
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Points scores an answer: a right one 500, plus up to 500 more the
// sooner it came in; a near one (a year a little off) a share of that.
func Points(closeness float64, at, opens, closes time.Time) int {
	if closeness <= 0 {
		return 0
	}
	speed := 1.0
	if w := closes.Sub(opens); w > 0 {
		speed = 1 - float64(at.Sub(opens))/float64(w)
	}
	speed = math.Max(0, math.Min(1, speed))
	return int(math.Round(closeness * (500 + 500*speed)))
}

func (e *Engine) save(ctx context.Context, rd Round, answers []Answer) error {
	q, err := json.Marshal(rd.Question)
	if err != nil {
		return err
	}
	return e.db.Tx(ctx, func(tx *store.Queries) error {
		err := tx.CreateGameRound(ctx, store.CreateGameRoundParams{
			ID: rd.ID, RoomID: rd.RoomID, QueueItemID: nullString(rd.ItemID), Kind: rd.Kind, Question: string(q),
			StartedBy: nullString(rd.StartedBy), StartedAt: rd.StartedAt, RevealedAt: rd.ClosesAt, SetID: nullString(setID(rd)),
		})
		if err != nil {
			return err
		}
		for _, a := range answers {
			resp, err := json.Marshal(a.Response)
			if err != nil {
				return err
			}
			err = tx.CreateGameAnswer(ctx, store.CreateGameAnswerParams{
				RoundID: rd.ID, UserID: a.UserID, Answer: string(resp), Correct: a.Correct, Points: int64(a.Points), AnsweredAt: a.At,
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// Answer takes someone's answer to the open round. They may change it
// until the round closes; the latest counts, timed by when it arrived.
func (e *Engine) Answer(_ context.Context, roomID, roundID string, by store.User, guest bool, resp quiz.Response) (Answer, error) {
	now := e.cfg.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.byRoom[roomID]
	if r == nil || r.round == nil || r.round.ID != roundID || r.round.State != StateOpen || !now.Before(r.round.ClosesAt) {
		return Answer{}, ErrNoRound
	}
	rd := r.round
	if guest && !rd.Guests {
		return Answer{}, ErrGuestsCantPlay
	}
	if err := valid(rd.Question, resp); err != nil {
		return Answer{}, err
	}
	_, again := rd.Answers[by.ID]
	if again && rd.Kind == rooms.GameTune {
		// The clip gets longer: no waiting to change a guess.
		return Answer{}, ErrAnswered
	}
	a := &Answer{UserID: by.ID, Response: resp, At: now}
	rd.Answers[by.ID] = a
	if !again {
		e.publish(rd) // how many have answered
	}
	return *a, nil
}

func valid(q quiz.Question, r quiz.Response) error {
	switch q.Answer {
	case quiz.AnswerChoice:
		if r.Choice == nil || *r.Choice < 0 || *r.Choice >= len(q.Choices) {
			return &InvalidInputError{"pick one of the choices"}
		}
	case quiz.AnswerNumber:
		if r.Number == nil && (r.Choice == nil || *r.Choice < 0 || *r.Choice >= len(q.Choices)) {
			return &InvalidInputError{"answer with a number"}
		}
	case quiz.AnswerText, quiz.AnswerSong:
		if len(r.Text) == 0 || len(r.Text) > 200 {
			return &InvalidInputError{"answer in 1 to 200 characters"}
		}
	}
	return nil
}

// Current returns a room's round, if one is up.
func (e *Engine) Current(roomID string) (Round, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.byRoom[roomID]
	if r == nil || r.round == nil || r.round.State == StatePending {
		return Round{}, false
	}
	return snapshot(r.round), true
}

// Hidden reports what a room's round keeps back about a song until its
// reveal: the song, its liner notes or its lyrics (quiz.Hide*).
func (e *Engine) Hidden(roomID string) (itemID string, hides []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.byRoom[roomID]
	if r == nil || r.round == nil || (r.round.State != StateAnnounce && r.round.State != StateOpen) {
		return "", nil
	}
	return r.round.ItemID, r.round.Question.Hides
}

// HiddenLine reports the lyric line a room's round keeps back until its
// reveal: the one sung at atMs in itemID. ok is false if there's none.
func (e *Engine) HiddenLine(roomID string) (itemID string, atMs int64, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.byRoom[roomID]
	if r == nil || r.round == nil || (r.round.State != StateAnnounce && r.round.State != StateOpen) ||
		!slices.Contains(r.round.Question.Hides, quiz.HideLine) {
		return "", 0, false
	}
	return r.round.ItemID, r.round.Question.AtMs, true
}

// RoomDeleted forgets a room.
func (e *Engine) RoomDeleted(roomID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r := e.byRoom[roomID]; r != nil && r.round != nil {
		r.round.State = StateDone
	}
	delete(e.byRoom, roomID)
}

// publish sends a round to the room. The caller holds mu.
func (e *Engine) publish(rd *Round) {
	e.bus.Publish(realtime.RoomTopic(rd.RoomID), realtime.Event{Type: realtime.GameRound, Data: snapshot(rd)})
}

// snapshot copies a round, so it can leave the lock.
func snapshot(rd *Round) Round {
	out := *rd
	out.Clips = slices.Clone(rd.Clips)
	out.Set = rd.Set.snapshot()
	out.Answers = make(map[string]*Answer, len(rd.Answers))
	for k, a := range rd.Answers {
		c := *a
		out.Answers[k] = &c
	}
	return out
}

// Scores are the night's game scores in a room.
type Scores struct {
	RoomID string
	// Mode is who sees them (rooms.Scores*).
	Mode    string
	Players []Player
	// Best is the night's longest higher-or-lower streak, if anyone has
	// one going or had one.
	Best *Streak
}

// Player is one person's score tonight.
type Player struct {
	UserID            string
	Points            int
	Correct, Answered int
	// Streak is how many higher-or-lower rounds they've got right in a
	// row, up to now; BestStreak their longest tonight.
	Streak, BestStreak int
}

// Streak is someone's run of right higher-or-lower answers.
type Streak struct {
	UserID string
	Count  int
}

// Scores returns the room's game scores since its last night ended, best
// first.
func (e *Engine) Scores(ctx context.Context, roomID string) (Scores, error) {
	row, err := e.rooms.Get(ctx, roomID)
	if err != nil {
		return Scores{}, err
	}
	since := time.Time{}
	if last, err := e.db.LastNight(ctx, roomID); err == nil {
		since = last.EndedAt
	} else if !store.IsNotFound(err) {
		return Scores{}, err
	}
	out := Scores{RoomID: roomID, Mode: rooms.ParseSettings(row.Settings).Games.ScoreMode(), Players: []Player{}}
	rows, err := e.db.GameScoresSince(ctx, store.GameScoresSinceParams{RoomID: roomID, Since: since})
	if err != nil {
		return Scores{}, err
	}
	for _, r := range rows {
		out.Players = append(out.Players, Player{UserID: r.UserID, Points: int(r.Points), Correct: int(r.Correct), Answered: int(r.Answered)})
	}
	// Higher-or-lower streaks: a wrong answer ends one, sitting a round out doesn't.
	hl, err := e.db.HigherLowerAnswersSince(ctx, store.HigherLowerAnswersSinceParams{RoomID: roomID, Since: since})
	if err != nil {
		return Scores{}, err
	}
	cur, best := map[string]int{}, map[string]int{}
	for _, a := range hl {
		if a.Correct {
			cur[a.UserID]++
			best[a.UserID] = max(best[a.UserID], cur[a.UserID])
		} else {
			cur[a.UserID] = 0
		}
	}
	for i := range out.Players {
		p := &out.Players[i]
		p.Streak, p.BestStreak = cur[p.UserID], best[p.UserID]
		if p.BestStreak > 0 && (out.Best == nil || p.BestStreak > out.Best.Count) {
			out.Best = &Streak{UserID: p.UserID, Count: p.BestStreak}
		}
	}
	return out, nil
}

func idOf(it *store.QueueItem) string {
	if it == nil {
		return ""
	}
	return it.ID
}

// key identifies the song a queue item plays, for the facts cache.
func key(it store.QueueItem) string { return it.Provider + "\x00" + it.TrackID }

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// queuedTrack is the provider.Track a queue item was queued as.
func queuedTrack(it store.QueueItem) provider.Track {
	var t provider.Track
	_ = json.Unmarshal([]byte(it.Metadata), &t)
	t.Ref = provider.TrackRef{Provider: it.Provider, LinkID: it.LinkID.String, ID: it.TrackID}
	return t
}
