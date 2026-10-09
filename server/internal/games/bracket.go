// SPDX-License-Identifier: AGPL-3.0-only

package games

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/bits"
	"slices"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/awards"
	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Bracket battles (MAD-796): a knockout across the night. People enter
// songs while the bracket's open; then each match puts two of them at the
// front of the queue, back to back, and the room hearts its favourite.
// The winner moves on. Matches are spread through the night, about the
// room's breaks per hour, with normal songs between. The final's winner is
// a candidate for song of the night, and the bracket goes in the recap.

// Bracket is a bracket battle's draw.
type Bracket struct {
	// Size is its most entries; Short plays matches MatchCap of each song.
	Size  int
	Short bool
	// Rounds are its rounds, the first to the final.
	Rounds [][]Match
	// Current is the match up now, if one is; Last, the one decided last.
	Current, Last *MatchRef
	// NextAt is when the next match may start, at the earliest.
	NextAt time.Time
	// Champion is the final's winner (an index into the game's Entries),
	// -1 until there is one.
	Champion int
}

// MatchRef is a match's place in a bracket.
type MatchRef struct{ Round, Match int }

// Match is two entries (indices into the game's Entries) facing off. A
// side still to be decided is -1; so is a bye's B.
type Match struct {
	A, B int
	// Winner is A or B once it's decided, -1 until then.
	Winner int
	// State is "" while it waits, MatchUp while it plays, MatchDone after.
	State            string
	HeartsA, HeartsB int
	// Bye: A went through unopposed. Walkover: the other song left the
	// queue. Toss: the hearts tied, and a coin decided.
	Bye, Walkover, Toss bool
	StartedAt           time.Time

	playedA, playedB bool
}

// Match states.
const (
	MatchUp   = "up"
	MatchDone = "done"
)

// Bracket points: a match won, and the final.
const (
	MatchWin      = 300
	ChampionBonus = 700
)

func (b *Bracket) snapshot() *Bracket {
	if b == nil {
		return nil
	}
	out := *b
	out.Rounds = make([][]Match, len(b.Rounds))
	for i, r := range b.Rounds {
		out.Rounds[i] = slices.Clone(r)
	}
	if b.Current != nil {
		c := *b.Current
		out.Current = &c
	}
	if b.Last != nil {
		l := *b.Last
		out.Last = &l
	}
	return &out
}

func (b *Bracket) match(ref MatchRef) *Match { return &b.Rounds[ref.Round][ref.Match] }

// Draw lays out a knockout for n entries, in the order given: the
// smallest power of two that fits them, with byes (the first matches) for
// the slots left over. Byes are decided already.
func Draw(order []int) [][]Match {
	n := len(order)
	size := 2
	if n > 2 {
		size = 1 << bits.Len(uint(n-1))
	}
	rounds := bits.Len(uint(size)) - 1
	out := make([][]Match, rounds)
	byes := size - n
	first := make([]Match, size/2)
	next := 0
	for i := range first {
		m := Match{A: order[next], B: -1, Winner: -1}
		next++
		if i < byes {
			m.Bye, m.Winner, m.State = true, m.A, MatchDone
		} else {
			m.B = order[next]
			next++
		}
		first[i] = m
	}
	out[0] = first
	for r := 1; r < rounds; r++ {
		out[r] = make([]Match, size>>(r+1))
		for i := range out[r] {
			out[r][i] = Match{A: -1, B: -1, Winner: -1}
		}
	}
	for i, m := range first {
		if m.Bye {
			advance(out, MatchRef{0, i})
		}
	}
	return out
}

// advance moves a decided match's winner on to its next match. It reports
// the champion, if that was the final.
func advance(rounds [][]Match, ref MatchRef) (champion int, final bool) {
	w := rounds[ref.Round][ref.Match].Winner
	if ref.Round == len(rounds)-1 {
		return w, true
	}
	next := &rounds[ref.Round+1][ref.Match/2]
	if ref.Match%2 == 0 {
		next.A = w
	} else {
		next.B = w
	}
	return -1, false
}

// nextUp is the next match ready to play: both its sides known.
func nextUp(rounds [][]Match) (MatchRef, bool) {
	for r, ms := range rounds {
		for i, m := range ms {
			if m.State == "" && m.A >= 0 && m.B >= 0 {
				return MatchRef{r, i}, true
			}
		}
	}
	return MatchRef{}, false
}

// draw draws a bracket whose entries closed, in a shuffled order. The
// first match can start at once. The caller holds mu.
func (e *Engine) draw(g *QueueGame) {
	order := make([]int, len(g.Entries))
	for i := range order {
		order[i] = i
	}
	e.rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	b := g.Bracket
	b.Rounds, b.NextAt = Draw(order), e.cfg.Now()
	g.songs = 1
	e.nextMatch(g)
}

// bracketPlaying follows a bracket's matches as songs start. The caller
// holds mu.
func (e *Engine) bracketPlaying(g *QueueGame, now string) {
	b := g.Bracket
	if b.Current == nil {
		if now != "" {
			g.songs++
		}
		e.nextMatch(g)
		return
	}
	m := b.match(*b.Current)
	a, bb := g.Entries[m.A], g.Entries[m.B]
	switch now {
	case a.ItemID, bb.ItemID:
		if now == a.ItemID {
			m.playedA, g.Entries[m.A].Played = true, true
		} else {
			m.playedB, g.Entries[m.B].Played = true, true
		}
		e.publishQueue(g)
		e.trim(g, now)
		return
	}
	if !m.playedA && !m.playedB {
		return // its songs are still at the front, waiting
	}
	var left []string
	if !m.playedA {
		left = append(left, a.ItemID)
	}
	if !m.playedB {
		left = append(left, bb.ItemID)
	}
	ref := *b.Current
	e.waiting(g, left, func(waiting map[string]bool) {
		if g.State == StatePlaying && b.Current != nil && *b.Current == ref && len(waiting) == 0 {
			e.judgeMatch(g, ref)
		}
	})
}

// nextMatch puts the next match's two songs at the front of the queue, if
// one's due: after NextAt, with a song between matches. A song that's left
// the queue loses by walkover. The caller holds mu.
func (e *Engine) nextMatch(g *QueueGame) {
	b := g.Bracket
	if b.Current != nil || g.songs < 1 || e.cfg.Now().Before(b.NextAt) {
		return
	}
	ref, ok := nextUp(b.Rounds)
	if !ok {
		return
	}
	m := b.match(ref)
	m.State, m.StartedAt = MatchUp, e.cfg.Now()
	b.Current = &ref
	e.publishQueue(g)
	sides := []int{m.A, m.B}
	ids := []string{g.Entries[m.A].ItemID, g.Entries[m.B].ItemID}
	e.waiting(g, ids, func(waiting map[string]bool) {
		if g.State != StatePlaying || b.Current == nil || *b.Current != ref {
			return
		}
		// A song that won a match already played, so it's queued again;
		// one that never played and isn't waiting has left the queue.
		var again []int
		for i, side := range sides {
			switch {
			case g.Entries[side].Played:
				again = append(again, side)
			case !waiting[ids[i]]:
				e.walkover(g, ref, sides[1-i])
				return
			}
		}
		roomID := g.RoomID
		e.background(func(ctx context.Context) {
			for _, side := range again {
				id, err := e.Queue.Again(ctx, roomID, ids[slices.Index(sides, side)])
				if err != nil {
					slog.Warn("games: queueing a bracket song again", "room", roomID, "err", err)
					return
				}
				e.mu.Lock()
				g.Entries[side].ItemID, g.Entries[side].Played = id, false
				ids[slices.Index(sides, side)] = id
				e.mu.Unlock()
			}
			if err := e.Queue.PlayNext(ctx, roomID, ids); err != nil {
				slog.Warn("games: playing a bracket match", "room", roomID, "err", err)
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			e.publishQueue(g)
		})
	})
}

// walkover decides a match whose other song left the queue. The caller
// holds mu.
func (e *Engine) walkover(g *QueueGame, ref MatchRef, winner int) {
	m := g.Bracket.match(ref)
	m.Walkover = true
	other := m.A
	if winner == m.A {
		other = m.B
	}
	e.decide(g, ref, winner, other)
}

// judgeMatch counts a played match's hearts and decides it: the most
// hearts, or a coin toss. The caller holds mu.
func (e *Engine) judgeMatch(g *QueueGame, ref MatchRef) {
	m := g.Bracket.match(ref)
	if m.State != MatchUp {
		return
	}
	m.State = MatchDone // judged once
	ids := []string{g.Entries[m.A].ItemID, g.Entries[m.B].ItemID}
	e.background(func(ctx context.Context) {
		hearts := e.hearts(ctx, ids)
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.queueGame(g.RoomID, g.ID) != g || g.State != StatePlaying {
			return
		}
		m.HeartsA, m.HeartsB = hearts[ids[0]], hearts[ids[1]]
		w, l := m.A, m.B
		switch {
		case m.HeartsB > m.HeartsA:
			w, l = m.B, m.A
		case m.HeartsA == m.HeartsB:
			m.Toss = true
			if e.rng.IntN(2) == 1 {
				w, l = m.B, m.A
			}
		}
		e.decide(g, ref, w, l)
	})
}

// decide records a match's winner, scores it, and moves the winner on;
// the final ends the bracket. The caller holds mu.
func (e *Engine) decide(g *QueueGame, ref MatchRef, winner, loser int) {
	b := g.Bracket
	m := b.match(ref)
	m.State, m.Winner = MatchDone, winner
	b.Current, b.Last = nil, &ref
	b.NextAt = e.cfg.Now().Add(e.matchGap(g.RoomID))
	g.songs = 0
	w, l := g.Entries[winner], g.Entries[loser]
	points := map[string]int{l.UserID: 0}
	points[w.UserID] += MatchWin
	champion, final := advance(b.Rounds, ref)
	if final {
		b.Champion = champion
		points[w.UserID] += ChampionBonus
	}
	for u, p := range points {
		g.Points[u] += p
	}
	e.saveQueue(g, w.ItemID, m.StartedAt, points, map[string]bool{w.UserID: true})
	if final {
		e.showResult(g)
		return
	}
	e.publishQueue(g)
	// Due mid-song, the next match goes to the front then, to play after it.
	e.qat(g, b.NextAt, StatePlaying, func() { e.nextMatch(g) })
}

// matchGap is how long between a bracket's matches: an hour over the
// room's breaks per hour, unless the config sets it. The caller holds mu,
// so it reads the room as last known.
func (e *Engine) matchGap(roomID string) time.Duration {
	if e.cfg.Queue.MatchGap > 0 {
		return e.cfg.Queue.MatchGap
	}
	breaks := rooms.DefaultBreaksPerHour
	if r := e.byRoom[roomID]; r != nil && r.breaks > 0 {
		breaks = r.breaks
	}
	return time.Hour / time.Duration(breaks)
}

// trim plays a short match's song from its peak, for MatchCap. The
// caller holds mu.
func (e *Engine) trim(g *QueueGame, itemID string) {
	if !g.Bracket.Short || e.Trim == nil {
		return
	}
	capFor := e.cfg.Queue.MatchCap
	roomID := g.RoomID
	e.background(func(ctx context.Context) {
		it, err := e.db.GetQueueItem(ctx, itemID)
		if err != nil {
			return
		}
		var start time.Duration
		if e.Facts != nil {
			if f, err := e.song(ctx, it); err == nil {
				start = time.Duration(quiz.ClipStart(f, rooms.ClipChorus, capFor.Milliseconds())) * time.Millisecond
				if d := time.Duration(f.DurationMs) * time.Millisecond; d > 0 && start+capFor > d {
					start = max(0, d-capFor)
				}
			}
		}
		if start > 0 {
			if err := e.Trim.Seek(ctx, roomID, itemID, start); err != nil {
				slog.Warn("games: starting a match song at its peak", "room", roomID, "err", err)
			}
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		e.qat(g, e.cfg.Now().Add(capFor), StatePlaying, func() {
			e.background(func(ctx context.Context) {
				if err := e.Trim.Cut(ctx, roomID, itemID); err != nil {
					slog.Warn("games: ending a short match song", "room", roomID, "err", err)
				}
			})
		})
	})
}

// abandonBracket ends a bracket where it stands, by hand. Its songs still
// waiting go back into the play order. The caller holds mu.
func (e *Engine) abandonBracket(g *QueueGame) {
	var held []string
	for _, en := range g.Entries {
		if !en.Played {
			held = append(held, en.ItemID)
		}
	}
	roomID := g.RoomID
	e.background(func(ctx context.Context) { e.release(ctx, roomID, held) })
	e.endQueue(g)
}

// --- The night's bracket -----------------------------------------------------

// lastBracket is the bracket a room played since a time, as it stood
// after its last match; nil if it played none.
func (e *Engine) lastBracket(ctx context.Context, roomID string, since time.Time) (*QueueGame, error) {
	row, err := e.db.LastGameRound(ctx, store.LastGameRoundParams{RoomID: roomID, Kind: rooms.GameBracket, Since: since})
	if store.IsNotFound(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var g QueueGame
	if err := json.Unmarshal([]byte(row.Question), &g); err != nil || g.Bracket == nil {
		return nil, err
	}
	return &g, nil
}

// Champion is the queue item that won the room's bracket since a time,
// for song of the night; "" if none has.
func (e *Engine) Champion(ctx context.Context, roomID string, since time.Time) string {
	g, err := e.lastBracket(ctx, roomID, since)
	if err != nil || g == nil || g.Bracket.Champion < 0 || g.Bracket.Champion >= len(g.Entries) {
		return ""
	}
	return g.Entries[g.Bracket.Champion].ItemID
}

// NightBracket is the night's bracket, as JSON to keep with the night, ""
// if the room played none. The night's over, so the room's queue games
// still running end where they stand.
func (e *Engine) NightBracket(ctx context.Context, n store.Night) (string, error) {
	e.mu.Lock()
	if r := e.byRoom[n.RoomID]; r != nil {
		e.dropQueueGames(r)
	}
	e.mu.Unlock()
	g, err := e.lastBracket(ctx, n.RoomID, n.StartedAt)
	if err != nil || g == nil {
		return "", err
	}
	raw, err := json.Marshal(g)
	return string(raw), err
}

// bracketAward is the night's bracket champion's award, if a bracket was
// won.
func (e *Engine) bracketAward(ctx context.Context, n store.Night) (awards.Award, bool) {
	g, err := e.lastBracket(ctx, n.RoomID, n.StartedAt)
	if err != nil || g == nil {
		return awards.Award{}, false
	}
	b := g.Bracket
	if b.Champion < 0 || b.Champion >= len(g.Entries) {
		return awards.Award{}, false
	}
	w := g.Entries[b.Champion]
	final := b.Rounds[len(b.Rounds)-1][0]
	reason := quote(w.Song.Title) + " won the bracket"
	if lost := final.A + final.B - b.Champion; final.A >= 0 && final.B >= 0 && lost >= 0 && lost < len(g.Entries) {
		reason += ", beating " + quote(g.Entries[lost].Song.Title) + " in the final"
	}
	return awards.Award{Kind: awards.BracketChamp, Title: awards.Titles[awards.BracketChamp], UserID: w.UserID, ItemID: w.ItemID, Reason: reason}, true
}

func quote(s string) string { return "“" + s + "”" }
