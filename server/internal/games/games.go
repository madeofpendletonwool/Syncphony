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
)

// InvalidInputError is an answer of the wrong kind.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

// Round states.
const (
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

// room is one room's games: the current round, and what's played since
// the last one.
type room struct {
	np    rooms.NowPlaying
	round *Round
	// songs counts songs that started since the last round.
	songs int
	// starting is set while a round is being made, so two aren't.
	starting bool
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
}

// Answer is someone's answer to a round.
type Answer struct {
	UserID   string
	Response quiz.Response
	// At is when the server got it (the latest change).
	At        time.Time
	Correct   bool
	Closeness float64
	Points    int
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
	before := idOf(r.np.Item)
	r.np = np
	now := idOf(np.Item)
	if now == before {
		return
	}
	if rd := r.round; rd != nil && rd.ItemID != now && (rd.State == StateAnnounce || rd.State == StateOpen) {
		e.reveal(rd)
	}
	if now == "" {
		return
	}
	r.songs++
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
	rd, err := e.make(ctx, roomID, *np.Item, g, "", "")
	if errors.Is(err, ErrNoQuestion) {
		return nil, nil // the next song may do
	}
	return rd, err
}

// Start starts a round now, of a kind or ("") any the song can carry. Who
// may is set by the room's Start rounds permission; admins always may.
func (e *Engine) Start(ctx context.Context, roomID string, by store.User, kind string) (*Round, error) {
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
	return e.make(ctx, roomID, *np.Item, st.Games, by.ID, kind)
}

// make makes a round about the playing song and starts it.
func (e *Engine) make(ctx context.Context, roomID string, it store.QueueItem, g rooms.Games, by, kind string) (*Round, error) {
	if e.Facts == nil {
		return nil, ErrNoQuestion
	}
	f, err := e.song(ctx, it)
	if err != nil {
		return nil, err
	}
	kinds := g.Kinds()
	if kind != "" {
		kinds = []string{kind}
	}
	ready := quiz.Ready(f)
	kinds = slices.DeleteFunc(kinds, func(k string) bool {
		// Games that pause the music wait for the engine to learn how
		// (MAD-791, MAD-793); queue games aren't about one song.
		return !slices.Contains(ready, k) || slices.Contains(rooms.GameBreaks, k)
	})
	if len(kinds) == 0 {
		return nil, ErrNoQuestion
	}
	pool := e.Facts.Pool(ctx, roomID, it, f)
	pool.ThisYear = e.cfg.Now().Year()

	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.roomOf(roomID)
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
		// A few tries: a lyric question needs its line far enough ahead.
		for range 4 {
			q, ok := quiz.Ask(k, f, pool, e.rng)
			if !ok {
				break
			}
			opens, closes, ok := e.times(r.np, f, q, mode, now)
			if !ok {
				continue
			}
			rd := &Round{
				ID: store.NewID(), RoomID: roomID, ItemID: it.ID, Kind: k, Mode: mode, State: StateAnnounce,
				Question: q, StartedBy: by, StartedAt: now, OpensAt: opens, ClosesAt: closes, DoneAt: closes.Add(e.cfg.RevealFor),
				Guests: g.GuestsAnswer(), TVOnly: g.TVOnly, Scores: g.ScoreMode(), Answers: map[string]*Answer{},
			}
			r.round, r.songs = rd, 0
			e.publish(rd)
			e.hide(rd)
			e.at(rd, opens, StateAnnounce, func() { e.open(rd) })
			e.at(rd, closes, StateOpen, func() { e.reveal(rd) })
			return rd, nil
		}
	}
	return nil, ErrNoQuestion
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

// times works out when a round's answers open and close. With a beat map,
// answers open on a section's start and close on the next section
// boundary, or a downbeat, so the reveal lands with the music; without
// one, they're fixed timers. A lyric question closes as the singer gets
// to the line. ok is false if the song doesn't have long enough left.
func (e *Engine) times(np rooms.NowPlaying, f quiz.Facts, q quiz.Question, mode string, now time.Time) (opens, closes time.Time, ok bool) {
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
	opens = now.Add(e.cfg.Announce)
	if q.AtMs > 0 {
		closes = at(q.AtMs)
		// Enough time to read the line and type.
		return opens, closes, closes.Sub(opens) >= 6*time.Second && (end.IsZero() || !closes.After(end))
	}
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
	return opens, closes, closes.Sub(opens) >= window/2
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

// open opens a round for answers. The caller holds mu.
func (e *Engine) open(rd *Round) {
	rd.State = StateOpen
	e.publish(rd)
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
	for _, a := range rd.Answers {
		a.Correct, a.Closeness = quiz.Check(rd.Question, a.Response)
		a.Points = Points(a.Closeness, a.At, rd.OpensAt, rd.ClosesAt)
	}
	e.publish(rd)
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

// done takes a round down. The caller holds mu.
func (e *Engine) done(rd *Round) {
	rd.State = StateDone
	e.publish(rd)
	e.byRoom[rd.RoomID].round = nil
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
			StartedBy: nullString(rd.StartedBy), StartedAt: rd.StartedAt, RevealedAt: rd.ClosesAt,
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
	if r == nil || r.round == nil {
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
}

// Player is one person's score tonight.
type Player struct {
	UserID            string
	Points            int
	Correct, Answered int
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
