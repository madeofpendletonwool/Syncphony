// SPDX-License-Identifier: AGPL-3.0-only

package games

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Queue games (MAD-794, MAD-795, MAD-796): Game night's games played
// through the queue, across songs, beside the rounds. Connect the artists
// links two artists with a chain of queued songs; a theme round has
// everyone queue a song to a prompt, then plays them as a block; a bracket
// battle plays people's songs against each other, a match at a time,
// through the night. The room hearts its favourites, as it always can.
//
// A room runs one connect or theme game at a time, and one bracket. Like
// rounds they live in memory. Songs entered in a theme round or a bracket
// are held out of the play order until their turn (queue_items.game_held),
// then put at the front (front_at): the block, or a match's two songs back
// to back. Each game's points are kept as a round, so they count in the
// night's scores and awards.

// QueueConfig times queue games. Zero values take the defaults.
type QueueConfig struct {
	// ConnectFor is how long the room has to connect the artists. Default
	// 30 minutes.
	ConnectFor time.Duration
	// ThemeEntries and BracketEntries are how long entries stay open.
	// Defaults 3 and 5 minutes.
	ThemeEntries, BracketEntries time.Duration
	// ShowFor is how long a queue game's result stays up. Default 20s.
	ShowFor time.Duration
	// MatchCap is how long a short bracket match plays each song, from its
	// peak. Default 90s.
	MatchCap time.Duration
	// MatchGap is the least time between a bracket's matches. Zero is an
	// hour over the room's breaks per hour.
	MatchGap time.Duration
}

func (c QueueConfig) withDefaults() QueueConfig {
	if c.ConnectFor == 0 {
		c.ConnectFor = 30 * time.Minute
	}
	if c.ThemeEntries == 0 {
		c.ThemeEntries = 3 * time.Minute
	}
	if c.BracketEntries == 0 {
		c.BracketEntries = 5 * time.Minute
	}
	if c.ShowFor == 0 {
		c.ShowFor = 20 * time.Second
	}
	if c.MatchCap == 0 {
		c.MatchCap = 90 * time.Second
	}
	return c
}

// Errors returned by queue games, besides the rounds' own.
var (
	ErrGameRunning = errors.New("a game like that is already running")
	ErrNoGame      = errors.New("that game isn't running")
	ErrNotEntering = errors.New("entries are closed")
)

// StatePlaying is a queue game playing out: a theme's block, or a
// bracket's matches. It comes after StateOpen.
const StatePlaying = "playing"

// Queue holds songs out of the play order for queue games, and plays them
// at the front. queue.Service is one.
type Queue interface {
	// Hold holds waiting songs out of the play order, or lets them back.
	Hold(ctx context.Context, roomID string, itemIDs []string, held bool) error
	// PlayNext puts waiting songs at the front, in order.
	PlayNext(ctx context.Context, roomID string, itemIDs []string) error
	// Again queues a song again, held, as whoever queued it, and returns
	// the new item's ID.
	Again(ctx context.Context, roomID, itemID string) (string, error)
}

// Trim plays part of a song, for bracket battles' short versions.
// playback.Engine is one.
type Trim interface {
	// Seek moves itemID to at, if it's the room's song.
	Seek(ctx context.Context, roomID, itemID string, at time.Duration) error
	// Cut ends itemID as if it had played through, if it's the room's song.
	Cut(ctx context.Context, roomID, itemID string) error
}

// QueueGame is a queue game.
type QueueGame struct {
	ID, RoomID string
	// Kind is rooms.GameConnect, GameTheme or GameBracket.
	Kind  string
	State string
	// StartedBy is who started it.
	StartedBy string
	StartedAt time.Time
	// ClosesAt is when entries close (theme, bracket), or the room's time
	// to connect the artists is up.
	ClosesAt time.Time
	// DoneAt is when its result comes down, once it has one.
	DoneAt time.Time
	// Guests may play; Scores is who sees scores (rooms.Scores*).
	Guests bool
	Scores string
	// Points are each player's points in it so far.
	Points map[string]int
	// Theme is a theme round's prompt, or a bracket's, if it has one.
	Theme *quiz.Theme
	// Entries are a theme round's or a bracket's songs, in the order they
	// were entered.
	Entries []Entry
	// Winners are a theme round's most-hearted entries (indices into
	// Entries), from its reveal on.
	Winners []int
	Connect *Connect
	Bracket *Bracket

	// seen are the queue items it's looked at.
	seen map[string]bool
	// songs counts songs that started since a bracket's last match.
	songs int
}

// Entry is a song entered in a theme round or a bracket.
type Entry struct {
	UserID, ItemID string
	Song           quiz.Song
	// Fit says whether it fits the theme (quiz.Fit*), with a Note when it
	// doesn't or there's no telling; "" without a theme.
	Fit, Note string
	// Played is set once it's started playing in the game.
	Played bool
	// Hearts are its hearts, once it's judged.
	Hearts int
}

// QueueOptions set up a queue game.
type QueueOptions struct {
	// Theme is a theme kind (quiz.Theme*) for a theme round or a bracket.
	// "" picks one for a theme round, and none for a bracket.
	Theme string
	// Size is a bracket's most entries: 4, 8 or 16.
	Size int
	// Short plays a bracket's matches MatchCap of each song, from its peak.
	Short bool
	// Teams is how many teams race to connect the artists: 1, the room
	// together, or 2.
	Teams int
}

// BracketSizes are how many songs a bracket may have.
var BracketSizes = []int{4, 8, 16}

// MaxTeams is the most teams that race to connect the artists.
const MaxTeams = 2

// StartQueue starts a queue game: rooms.GameConnect, GameTheme or
// GameBracket. Who may is set by the room's Start rounds permission.
func (e *Engine) StartQueue(ctx context.Context, roomID string, by store.User, kind string, o QueueOptions) (QueueGame, error) {
	if !slices.Contains(rooms.QueueGames, kind) {
		return QueueGame{}, &InvalidInputError{"queue games are connect, theme and bracket"}
	}
	row, err := e.rooms.Get(ctx, roomID)
	if err != nil {
		return QueueGame{}, err
	}
	st := rooms.ParseSettings(row.Settings)
	g := st.Games
	if !g.Plays() {
		return QueueGame{}, ErrGamesOff
	}
	if by.Role != store.RoleAdmin && !rooms.Allowed(st.Permissions.StartRounds, row.OwnerID, by.ID) {
		return QueueGame{}, ErrForbidden
	}
	if !g.On(kind) {
		return QueueGame{}, &InvalidInputError{"that game is off in this room"}
	}
	if o.Theme != "" && (kind == rooms.GameConnect || !slices.Contains(quiz.ThemeKinds, o.Theme)) {
		return QueueGame{}, &InvalidInputError{"that's not a theme"}
	}
	now := e.cfg.Now()
	qg := &QueueGame{
		ID: store.NewID(), RoomID: roomID, Kind: kind, State: StateOpen, StartedBy: by.ID, StartedAt: now,
		Guests: g.GuestsAnswer(), Scores: g.ScoreMode(), Points: map[string]int{}, seen: map[string]bool{},
	}
	switch kind {
	case rooms.GameConnect:
		if e.Artists == nil {
			return QueueGame{}, ErrNoQuestion
		}
		if o.Teams == 0 {
			o.Teams = 1
		}
		if o.Teams < 1 || o.Teams > MaxTeams {
			return QueueGame{}, &InvalidInputError{"connect the artists is for the room together, or 2 teams"}
		}
		if !e.claim(roomID, kind) {
			return QueueGame{}, ErrGameRunning
		}
		c, err := e.pair(ctx, roomID, o.Teams)
		if err != nil {
			e.unclaim(roomID, kind)
			return QueueGame{}, err
		}
		qg.Connect, qg.ClosesAt = c, now.Add(e.cfg.Queue.ConnectFor)
	case rooms.GameTheme, rooms.GameBracket:
		if e.Queue == nil {
			return QueueGame{}, ErrNoQuestion
		}
		if kind == rooms.GameBracket && !slices.Contains(BracketSizes, o.Size) {
			return QueueGame{}, &InvalidInputError{"a bracket is 4, 8 or 16 songs"}
		}
		if kind == rooms.GameTheme || o.Theme != "" {
			t, err := e.theme(ctx, roomID, o.Theme)
			if err != nil {
				return QueueGame{}, err
			}
			qg.Theme = &t
		}
		if !e.claim(roomID, kind) {
			return QueueGame{}, ErrGameRunning
		}
		qg.ClosesAt = now.Add(e.cfg.Queue.ThemeEntries)
		if kind == rooms.GameBracket {
			qg.ClosesAt = now.Add(e.cfg.Queue.BracketEntries)
			qg.Bracket = &Bracket{Size: o.Size, Short: o.Short, Champion: -1}
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.roomOf(roomID)
	r.claimed = slices.DeleteFunc(r.claimed, func(k string) bool { return k == kind })
	if kind == rooms.GameBracket {
		r.bracket = qg
	} else {
		r.play = qg
	}
	e.publishQueue(qg)
	e.qat(qg, qg.ClosesAt, StateOpen, func() { e.closeEntries(qg) })
	return qg.snapshot(), nil
}

// slot is which of a room's queue games a kind takes: a bracket's own, or
// the one connect and theme games share.
func slot(kind string) string {
	if kind == rooms.GameBracket {
		return rooms.GameBracket
	}
	return rooms.GameConnect
}

// claim reserves the room's slot for a kind of queue game while it's set
// up, so two don't start at once.
func (e *Engine) claim(roomID, kind string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.roomOf(roomID)
	taken := r.play
	if slot(kind) == rooms.GameBracket {
		taken = r.bracket
	}
	if taken != nil || slices.ContainsFunc(r.claimed, func(k string) bool { return slot(k) == slot(kind) }) {
		return false
	}
	r.claimed = append(r.claimed, kind)
	return true
}

func (e *Engine) unclaim(roomID, kind string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.roomOf(roomID)
	r.claimed = slices.DeleteFunc(r.claimed, func(k string) bool { return k == kind })
}

// theme makes a theme round's prompt, of a kind or ("") any.
func (e *Engine) theme(ctx context.Context, roomID, kind string) (quiz.Theme, error) {
	var p quiz.ThemePool
	if e.Artists != nil {
		p = e.Artists.Themes(ctx, roomID)
	}
	p.ThisYear = e.cfg.Now().Year()
	e.mu.Lock()
	defer e.mu.Unlock()
	if kind == "" {
		return quiz.PickTheme(p, e.rng), nil
	}
	t, ok := quiz.MakeTheme(kind, p, e.rng)
	if !ok {
		return quiz.Theme{}, &InvalidInputError{"tonight's songs don't give that theme anything to name yet"}
	}
	return t, nil
}

// QueueGames returns a room's queue games that are up.
func (e *Engine) QueueGames(roomID string) []QueueGame {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.byRoom[roomID]
	if r == nil {
		return nil
	}
	var out []QueueGame
	for _, g := range []*QueueGame{r.play, r.bracket} {
		if g != nil {
			out = append(out, g.snapshot())
		}
	}
	return out
}

// queueGame is one of a room's queue games, by ID. The caller holds mu.
func (e *Engine) queueGame(roomID, id string) *QueueGame {
	r := e.byRoom[roomID]
	if r == nil {
		return nil
	}
	for _, g := range []*QueueGame{r.play, r.bracket} {
		if g != nil && g.ID == id {
			return g
		}
	}
	return nil
}

// --- Entries ---------------------------------------------------------------

// Bracket entries each person may have.
const bracketPerPerson = 2

// Enter enters one of someone's queued songs in a theme round or a
// bracket while its entries are open. A theme round takes one song each,
// so entering another swaps it; a bracket takes up to two. The song is
// held out of the play order until the game plays it.
func (e *Engine) Enter(ctx context.Context, roomID, gameID string, by store.User, guest bool, itemID string) (QueueGame, error) {
	it, err := e.db.GetQueueItem(ctx, itemID)
	if store.IsNotFound(err) || (err == nil && it.RoomID != roomID) {
		return QueueGame{}, &InvalidInputError{"that song isn't in this room"}
	} else if err != nil {
		return QueueGame{}, err
	}
	if it.AddedBy != by.ID || it.IsAutopilot() {
		return QueueGame{}, &InvalidInputError{"enter one of your own songs"}
	}
	if it.State != store.ItemQueued {
		return QueueGame{}, &InvalidInputError{"enter a song that's waiting in the queue"}
	}
	e.mu.Lock()
	g := e.queueGame(roomID, gameID)
	var r *room
	if g != nil {
		r = e.byRoom[roomID]
	}
	e.mu.Unlock()
	if g == nil || g.Kind == rooms.GameConnect {
		return QueueGame{}, ErrNoGame
	}
	if guest && !g.Guests {
		return QueueGame{}, ErrGuestsCantPlay
	}
	r.work.Lock()
	defer r.work.Unlock()
	if err := e.enter(ctx, g, it); err != nil {
		return QueueGame{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return g.snapshot(), nil
}

// enter enters a song. The caller holds the room's work lock.
func (e *Engine) enter(ctx context.Context, g *QueueGame, it store.QueueItem) error {
	e.mu.Lock()
	if g.State != StateOpen {
		e.mu.Unlock()
		return ErrNotEntering
	}
	if r := e.byRoom[g.RoomID]; r != nil && entered(r, it.ID) {
		e.mu.Unlock()
		return &InvalidInputError{"that song's already entered"}
	}
	mine := 0
	var swap string
	for _, en := range g.Entries {
		if en.UserID == it.AddedBy {
			mine++
			swap = en.ItemID
		}
	}
	switch {
	case g.Kind == rooms.GameTheme && mine > 0:
		// Swapped below.
	case g.Kind == rooms.GameBracket && mine >= bracketPerPerson:
		e.mu.Unlock()
		return &InvalidInputError{"you've entered two songs already"}
	case g.Kind == rooms.GameBracket && len(g.Entries) >= g.Bracket.Size:
		e.mu.Unlock()
		return &InvalidInputError{"the bracket's full"}
	default:
		swap = ""
	}
	theme := g.Theme
	e.mu.Unlock()

	if err := e.Queue.Hold(ctx, g.RoomID, []string{it.ID}, true); err != nil {
		return err
	}
	t := queuedTrack(it)
	en := Entry{UserID: it.AddedBy, ItemID: it.ID, Song: quiz.Song{Title: t.Title, Artist: mainArtist(t)}}
	if theme != nil {
		en.Fit, en.Note = quiz.FitUnknown, ""
		if e.Facts != nil {
			if f, err := e.song(ctx, it); err == nil {
				en.Fit, en.Note = theme.Fits(f)
			}
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if g.State != StateOpen {
		// Entries closed meanwhile.
		e.background(func(ctx context.Context) { e.release(ctx, g.RoomID, []string{it.ID}) })
		return ErrNotEntering
	}
	g.seen[it.ID] = true
	if swap != "" {
		g.Entries = slices.DeleteFunc(g.Entries, func(x Entry) bool { return x.ItemID == swap })
		e.background(func(ctx context.Context) { e.release(ctx, g.RoomID, []string{swap}) })
	}
	g.Entries = append(g.Entries, en)
	e.publishQueue(g)
	if g.Kind == rooms.GameBracket && len(g.Entries) >= g.Bracket.Size {
		e.closeEntries(g) // full
	}
	return nil
}

// entered reports whether a song is entered in any of a room's games. The
// caller holds mu.
func entered(r *room, itemID string) bool {
	for _, g := range []*QueueGame{r.play, r.bracket} {
		if g != nil && slices.ContainsFunc(g.Entries, func(en Entry) bool { return en.ItemID == itemID }) {
			return true
		}
	}
	return false
}

// Withdraw takes one of someone's songs out of a game whose entries are
// open, and lets it back into the play order.
func (e *Engine) Withdraw(ctx context.Context, roomID, gameID string, by store.User, itemID string) (QueueGame, error) {
	e.mu.Lock()
	g := e.queueGame(roomID, gameID)
	if g == nil {
		e.mu.Unlock()
		return QueueGame{}, ErrNoGame
	}
	if g.State != StateOpen {
		e.mu.Unlock()
		return QueueGame{}, ErrNotEntering
	}
	i := slices.IndexFunc(g.Entries, func(en Entry) bool { return en.ItemID == itemID })
	if i < 0 || (g.Entries[i].UserID != by.ID && by.Role != store.RoleAdmin) {
		e.mu.Unlock()
		return QueueGame{}, &InvalidInputError{"that song isn't one of your entries"}
	}
	g.Entries = slices.Delete(g.Entries, i, i+1)
	e.publishQueue(g)
	out := g.snapshot()
	e.mu.Unlock()
	e.release(ctx, roomID, []string{itemID})
	return out, nil
}

// release lets songs a game held back into the play order.
func (e *Engine) release(ctx context.Context, roomID string, itemIDs []string) {
	if len(itemIDs) == 0 {
		return
	}
	if err := e.Queue.Hold(ctx, roomID, itemIDs, false); err != nil {
		slog.Warn("games: letting songs back into the queue", "room", roomID, "err", err)
	}
}

// closeEntries closes a theme round's or a bracket's entries: the theme's
// block goes to the front of the queue, and the bracket is drawn. Without
// enough entries, the game's over. The caller holds mu.
func (e *Engine) closeEntries(g *QueueGame) {
	if g.State != StateOpen {
		return
	}
	switch g.Kind {
	case rooms.GameConnect:
		e.revealConnect(g)
		return
	case rooms.GameTheme:
		if len(g.Entries) == 0 {
			e.endQueue(g)
			return
		}
		g.State = StatePlaying
		ids := g.items()
		e.background(func(ctx context.Context) {
			if err := e.Queue.PlayNext(ctx, g.RoomID, ids); err != nil {
				slog.Warn("games: playing a theme round's block", "room", g.RoomID, "err", err)
			}
		})
	case rooms.GameBracket:
		if len(g.Entries) < 2 {
			e.background(func(ctx context.Context) { e.release(ctx, g.RoomID, g.items()) })
			e.endQueue(g)
			return
		}
		g.State = StatePlaying
		e.draw(g)
	}
	e.publishQueue(g)
}

// CloseQueue moves a queue game on by hand: closes its entries, reveals
// it, or ends a bracket where it stands. Its starter, the room's owner
// or an admin may.
func (e *Engine) CloseQueue(ctx context.Context, roomID, gameID string, by store.User) (QueueGame, error) {
	row, err := e.rooms.Get(ctx, roomID)
	if err != nil {
		return QueueGame{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	g := e.queueGame(roomID, gameID)
	if g == nil {
		return QueueGame{}, ErrNoGame
	}
	if g.StartedBy != by.ID && row.OwnerID != by.ID && by.Role != store.RoleAdmin {
		return QueueGame{}, ErrForbidden
	}
	switch {
	case g.State == StateOpen:
		e.closeEntries(g)
	case g.State == StatePlaying && g.Kind == rooms.GameTheme:
		e.judgeTheme(g)
	case g.State == StatePlaying && g.Kind == rooms.GameBracket:
		e.abandonBracket(g)
	}
	return g.snapshot(), nil
}

// endQueue takes a queue game down. The caller holds mu.
func (e *Engine) endQueue(g *QueueGame) {
	g.State = StateDone
	e.publishQueue(g)
	if r := e.byRoom[g.RoomID]; r != nil {
		if r.play == g {
			r.play = nil
		}
		if r.bracket == g {
			r.bracket = nil
		}
	}
}

// showResult puts a queue game's result up, then takes it down. The
// caller holds mu.
func (e *Engine) showResult(g *QueueGame) {
	g.State, g.DoneAt = StateReveal, e.cfg.Now().Add(e.cfg.Queue.ShowFor)
	e.publishQueue(g)
	e.qat(g, g.DoneAt, StateReveal, func() { e.endQueue(g) })
}

func (g *QueueGame) items() []string {
	out := make([]string, 0, len(g.Entries))
	for _, en := range g.Entries {
		out = append(out, en.ItemID)
	}
	return out
}

// --- Following the queue -----------------------------------------------------

// watchQueue looks at songs queued since each of the room's queue games
// started: links in a chain, or entries (the first song each person queues
// once a theme round or a bracket opens is entered for them). Entries that
// left the queue are dropped.
func (e *Engine) watchQueue(ctx context.Context, roomID string, snap rooms.QueueSnapshot) {
	e.mu.Lock()
	r := e.byRoom[roomID]
	if r == nil || (r.play == nil && r.bracket == nil) {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()
	r.work.Lock()
	defer r.work.Unlock()
	queued := map[string]bool{}
	for _, it := range snap.Items {
		queued[it.ID] = it.State == store.ItemQueued
	}
	for _, g := range []*QueueGame{r.play, r.bracket} {
		if g == nil {
			continue
		}
		e.mu.Lock()
		var fresh []store.QueueItem
		for _, it := range snap.Items {
			// Songs a game holds are a game's already.
			if g.seen[it.ID] || it.IsAutopilot() || it.AddedAt.Before(g.StartedAt) || it.GameHeld || entered(r, it.ID) {
				continue
			}
			g.seen[it.ID] = true
			fresh = append(fresh, it)
		}
		state, kind := g.State, g.Kind
		if state == StateOpen && kind != rooms.GameConnect {
			// Entries taken out of the queue are out of the game.
			gone := slices.DeleteFunc(g.items(), func(id string) bool { return queued[id] })
			if len(gone) > 0 {
				g.Entries = slices.DeleteFunc(g.Entries, func(en Entry) bool { return slices.Contains(gone, en.ItemID) })
				e.publishQueue(g)
			}
		}
		e.mu.Unlock()
		slices.SortFunc(fresh, func(a, b store.QueueItem) int { return a.AddedAt.Compare(b.AddedAt) })
		for _, it := range fresh {
			if !e.mayPlay(ctx, g, it.AddedBy) {
				continue
			}
			switch {
			case kind == rooms.GameConnect && state == StateOpen:
				e.link(ctx, g, it)
			case state == StateOpen && it.State == store.ItemQueued && !e.hasEntry(g, it.AddedBy):
				if err := e.enter(ctx, g, it); err != nil && !errors.Is(err, ErrNotEntering) {
					var invalid *InvalidInputError
					if !errors.As(err, &invalid) {
						slog.Warn("games: entering a song", "room", roomID, "err", err)
					}
				}
			}
		}
	}
}

// mayPlay reports whether someone may play a game: guests only if it
// lets them.
func (e *Engine) mayPlay(ctx context.Context, g *QueueGame, userID string) bool {
	if g.Guests {
		return true
	}
	_, err := e.db.GetGuest(ctx, userID)
	return store.IsNotFound(err)
}

func (e *Engine) hasEntry(g *QueueGame, userID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.ContainsFunc(g.Entries, func(en Entry) bool { return en.UserID == userID })
}

// queueNowPlaying follows a room's playback for its queue games: a
// theme's block and a bracket's matches play out as songs do. The caller
// holds mu.
func (e *Engine) queueNowPlaying(r *room, np rooms.NowPlaying) {
	now := idOf(np.Item)
	if g := r.play; g != nil && g.Kind == rooms.GameTheme && g.State == StatePlaying {
		e.themePlaying(g, now)
	}
	if g := r.bracket; g != nil && g.State == StatePlaying {
		e.bracketPlaying(g, now)
	}
}

// waiting reports in the background which of a game's songs are still
// waiting in the queue, then calls fn with them, holding mu.
func (e *Engine) waiting(g *QueueGame, ids []string, fn func(waiting map[string]bool)) {
	e.background(func(ctx context.Context) {
		snap, err := e.rooms.QueueSnapshot(ctx, g.RoomID)
		if err != nil {
			slog.Warn("games: reading the queue", "room", g.RoomID, "err", err)
			return
		}
		out := map[string]bool{}
		for _, it := range snap.Items {
			if it.State == store.ItemQueued && slices.Contains(ids, it.ID) {
				out[it.ID] = true
			}
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.ctx.Err() == nil {
			fn(out)
		}
	})
}

// --- Theme rounds ------------------------------------------------------------

// Theme round points: an entry that fits, one there's no telling about,
// and the room's favourite.
const (
	ThemeFits    = 200
	ThemeMaybe   = 100
	ThemeFavored = 500
)

// themePlaying follows a theme round's block. Once every entry has played
// (or left the queue), the hearts decide. The caller holds mu.
func (e *Engine) themePlaying(g *QueueGame, now string) {
	if i := slices.IndexFunc(g.Entries, func(en Entry) bool { return en.ItemID == now }); i >= 0 {
		if !g.Entries[i].Played {
			g.Entries[i].Played = true
			e.publishQueue(g)
		}
		return
	}
	var left []string
	for _, en := range g.Entries {
		if !en.Played {
			left = append(left, en.ItemID)
		}
	}
	e.waiting(g, left, func(waiting map[string]bool) {
		if g.State == StatePlaying && len(waiting) == 0 {
			e.judgeTheme(g)
		}
	})
}

// judgeTheme counts each entry's hearts, crowns the favourite and scores
// the round. The caller holds mu.
func (e *Engine) judgeTheme(g *QueueGame) {
	if g.State != StatePlaying {
		return
	}
	g.State = StateReveal // judged once
	ids := g.items()
	e.background(func(ctx context.Context) {
		hearts := e.hearts(ctx, ids)
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.queueGame(g.RoomID, g.ID) != g {
			return
		}
		top := 0
		for i := range g.Entries {
			en := &g.Entries[i]
			en.Hearts = hearts[en.ItemID]
			top = max(top, en.Hearts)
			switch en.Fit {
			case quiz.FitYes:
				g.Points[en.UserID] += ThemeFits
			case quiz.FitUnknown:
				g.Points[en.UserID] += ThemeMaybe
			}
		}
		g.Winners = nil
		won := map[string]bool{}
		for i, en := range g.Entries {
			if top > 0 && en.Hearts == top {
				g.Winners = append(g.Winners, i)
				g.Points[en.UserID] += ThemeFavored
				won[en.UserID] = true
			}
		}
		e.saveQueue(g, "", g.StartedAt, g.Points, won)
		e.showResult(g)
	})
}

// hearts counts songs' hearts.
func (e *Engine) hearts(ctx context.Context, ids []string) map[string]int {
	out := map[string]int{}
	if len(ids) == 0 {
		return out
	}
	rows, err := e.db.CountHeartsFor(ctx, ids)
	if err != nil {
		slog.Warn("games: counting hearts", "err", err)
		return out
	}
	for _, r := range rows {
		out[r.QueueItemID] = int(r.Hearts)
	}
	return out
}

// --- Keeping score -----------------------------------------------------------

// saveQueue keeps points from a queue game as a round, so they count in
// the night's scores and awards, then sends the scores. won is who it
// counts as right for. The caller holds mu.
func (e *Engine) saveQueue(g *QueueGame, itemID string, startedAt time.Time, points map[string]int, won map[string]bool) {
	saved := g.snapshot()
	points = maps.Clone(points)
	won = maps.Clone(won)
	at := e.cfg.Now()
	id := store.NewID()
	e.background(func(ctx context.Context) {
		q, err := json.Marshal(saved)
		if err != nil {
			return
		}
		err = e.db.Tx(ctx, func(tx *store.Queries) error {
			err := tx.CreateGameRound(ctx, store.CreateGameRoundParams{
				ID: id, RoomID: saved.RoomID, QueueItemID: nullString(itemID), Kind: saved.Kind, Question: string(q),
				StartedBy: nullString(saved.StartedBy), StartedAt: startedAt, RevealedAt: at,
			})
			if err != nil {
				return err
			}
			for u, p := range points {
				err := tx.CreateGameAnswer(ctx, store.CreateGameAnswerParams{
					RoundID: id, UserID: u, Answer: "{}", Correct: won[u], Points: int64(p), AnsweredAt: at,
				})
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			slog.Warn("games: keeping a queue game", "room", saved.RoomID, "err", err)
			return
		}
		if saved.Scores != rooms.ScoresOff && len(points) > 0 {
			if s, err := e.Scores(ctx, saved.RoomID); err == nil {
				e.bus.Publish(realtime.RoomTopic(saved.RoomID), realtime.Event{Type: realtime.GameScores, Data: s})
			}
		}
	})
}

// --- Plumbing ----------------------------------------------------------------

// qat runs fn at t, if the game is still the room's and in state then.
// The caller holds mu.
func (e *Engine) qat(g *QueueGame, t time.Time, state string, fn func()) {
	var timer *time.Timer
	timer = time.AfterFunc(max(t.Sub(e.cfg.Now()), 0), func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(e.timers, timer)
		if e.ctx.Err() != nil || g.State != state || e.queueGame(g.RoomID, g.ID) != g {
			return
		}
		fn()
	})
	e.timers[timer] = struct{}{}
}

// publishQueue sends a queue game to the room. The caller holds mu.
func (e *Engine) publishQueue(g *QueueGame) {
	e.bus.Publish(realtime.RoomTopic(g.RoomID), realtime.Event{Type: realtime.GameQueue, Data: g.snapshot()})
}

// snapshot copies a queue game, so it can leave the lock.
func (g *QueueGame) snapshot() QueueGame {
	out := *g
	out.seen = nil
	out.Points = maps.Clone(g.Points)
	out.Entries = slices.Clone(g.Entries)
	out.Winners = slices.Clone(g.Winners)
	out.Connect = g.Connect.snapshot()
	out.Bracket = g.Bracket.snapshot()
	return out
}

// dropQueueGames ends a room's queue games where they stand, and lets
// their songs back into the play order: the night's over. The caller
// holds mu.
func (e *Engine) dropQueueGames(r *room) {
	for _, g := range []*QueueGame{r.play, r.bracket} {
		if g == nil {
			continue
		}
		var held []string
		for _, en := range g.Entries {
			if !en.Played {
				held = append(held, en.ItemID)
			}
		}
		if e.Queue != nil {
			roomID := g.RoomID
			e.background(func(ctx context.Context) { e.release(ctx, roomID, held) })
		}
		e.endQueue(g)
	}
}
