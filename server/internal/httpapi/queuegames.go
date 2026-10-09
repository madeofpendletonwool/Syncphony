// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"cmp"
	"context"
	"slices"

	"github.com/madeofpendletonwool/syncphony/server/internal/games"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
)

// Queue games (MAD-794, MAD-795, MAD-796, ADR 0015): connect the artists,
// theme rounds and bracket battles.

// GetQueueGames returns the room's queue games that are up.
func (s *Server) GetQueueGames(_ context.Context, req GetQueueGamesRequestObject) (GetQueueGamesResponseObject, error) {
	out := QueueGames{Games: []QueueGame{}}
	if s.Games != nil {
		for _, g := range s.Games.QueueGames(req.RoomId) {
			out.Games = append(out.Games, toQueueGame(g))
		}
	}
	return GetQueueGames200JSONResponse(out), nil
}

// StartQueueGame starts a queue game.
func (s *Server) StartQueueGame(ctx context.Context, req StartQueueGameRequestObject) (StartQueueGameResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	if s.Games == nil {
		return nil, games.ErrGamesOff
	}
	b := req.Body
	o := games.QueueOptions{}
	if b.Theme != nil {
		o.Theme = string(*b.Theme)
	}
	if b.Size != nil {
		o.Size = int(*b.Size)
	}
	if b.Short != nil {
		o.Short = *b.Short
	}
	if b.Teams != nil {
		o.Teams = *b.Teams
	}
	g, err := s.Games.StartQueue(ctx, req.RoomId, u, string(b.Kind), o)
	if err != nil {
		return nil, err
	}
	return StartQueueGame201JSONResponse(toQueueGame(g)), nil
}

// EnterQueueGame enters one of your songs in a theme round or a bracket.
func (s *Server) EnterQueueGame(ctx context.Context, req EnterQueueGameRequestObject) (EnterQueueGameResponseObject, error) {
	if s.Games == nil {
		return nil, games.ErrNoGame
	}
	sess := sessionFrom(ctx)
	g, err := s.Games.Enter(ctx, req.RoomId, req.GameId, sess.User, sess.Guest != nil, req.Body.ItemId)
	if err != nil {
		return nil, err
	}
	return EnterQueueGame200JSONResponse(toQueueGame(g)), nil
}

// WithdrawQueueGameEntry takes one of your songs out of a game.
func (s *Server) WithdrawQueueGameEntry(ctx context.Context, req WithdrawQueueGameEntryRequestObject) (WithdrawQueueGameEntryResponseObject, error) {
	if s.Games == nil {
		return nil, games.ErrNoGame
	}
	g, err := s.Games.Withdraw(ctx, req.RoomId, req.GameId, sessionFrom(ctx).User, req.ItemId)
	if err != nil {
		return nil, err
	}
	return WithdrawQueueGameEntry200JSONResponse(toQueueGame(g)), nil
}

// HintQueueGame gives your team a hint for connect the artists.
func (s *Server) HintQueueGame(ctx context.Context, req HintQueueGameRequestObject) (HintQueueGameResponseObject, error) {
	if s.Games == nil {
		return nil, games.ErrNoGame
	}
	sess := sessionFrom(ctx)
	g, err := s.Games.Hint(ctx, req.RoomId, req.GameId, sess.User, sess.Guest != nil)
	if err != nil {
		return nil, err
	}
	return HintQueueGame200JSONResponse(toQueueGame(g)), nil
}

// CloseQueueGame moves a queue game on.
func (s *Server) CloseQueueGame(ctx context.Context, req CloseQueueGameRequestObject) (CloseQueueGameResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	if s.Games == nil {
		return nil, games.ErrNoGame
	}
	g, err := s.Games.CloseQueue(ctx, req.RoomId, req.GameId, u)
	if err != nil {
		return nil, err
	}
	return CloseQueueGame200JSONResponse(toQueueGame(g)), nil
}

func toQueueGame(g games.QueueGame) QueueGame {
	out := QueueGame{
		Id: g.ID, RoomId: g.RoomID, Kind: QueueGameKind(g.Kind), State: QueueGameState(g.State), StartedBy: g.StartedBy,
		StartedAt: g.StartedAt, ClosesAt: g.ClosesAt, Guests: g.Guests, Scores: QueueGameScores(g.Scores),
		Points: []GameSetPlayer{}, Entries: []GameEntry{},
	}
	if !g.DoneAt.IsZero() {
		out.DoneAt = &g.DoneAt
	}
	for u, p := range g.Points {
		out.Points = append(out.Points, GameSetPlayer{UserId: u, Points: p})
	}
	slices.SortFunc(out.Points, func(a, b GameSetPlayer) int {
		return cmp.Or(cmp.Compare(b.Points, a.Points), cmp.Compare(a.UserId, b.UserId))
	})
	if t := g.Theme; t != nil {
		out.Theme = &GameTheme{Kind: ThemeKind(t.Kind), Prompt: t.Prompt}
	}
	// A theme round's hearts, once it's judged; a bracket's are its matches'.
	judged := g.Kind == rooms.GameTheme && (g.State == games.StateReveal || g.State == games.StateDone)
	for _, en := range g.Entries {
		e := GameEntry{UserId: en.UserID, ItemId: en.ItemID, Title: en.Song.Title, Artist: nonEmpty(en.Song.Artist), Played: en.Played}
		if en.Fit != "" {
			e.Fit, e.Note = ptr(GameEntryFit(en.Fit)), nonEmpty(en.Note)
		}
		if judged {
			e.Hearts = &en.Hearts
		}
		out.Entries = append(out.Entries, e)
	}
	if len(g.Winners) > 0 {
		out.Winners = ptr(slices.Clone(g.Winners))
	}
	if c := g.Connect; c != nil {
		out.Connect = toGameConnect(*c, g.State == games.StateReveal || g.State == games.StateDone)
	}
	if b := g.Bracket; b != nil {
		out.Bracket = toGameBracket(*b)
	}
	return out
}

// toGameConnect is a connect game; the shortest way only once it's over.
func toGameConnect(c games.Connect, over bool) *GameConnect {
	out := &GameConnect{
		From: c.From, To: c.To, Hops: max(len(c.Path)-1, 0), Chains: []GameChain{}, Teams: map[string]int{}, Misses: []GameMiss{},
	}
	for u, t := range c.Teams {
		out.Teams[u] = t
	}
	for _, ch := range c.Chains {
		gc := GameChain{Links: []GameLink{}, Hints: slices.Clone(ch.Hints), Done: ch.Done}
		if gc.Hints == nil {
			gc.Hints = []string{}
		}
		for _, l := range ch.Links {
			gc.Links = append(gc.Links, GameLink{
				UserId: l.UserID, ItemId: l.ItemID, Title: l.Song.Title, Artist: l.Artist, Score: float32(l.Score), Points: l.Points,
			})
		}
		out.Chains = append(out.Chains, gc)
	}
	for _, m := range c.Misses {
		out.Misses = append(out.Misses, GameMiss{UserId: m.UserID, ItemId: m.ItemID, Artist: m.Artist, After: m.After, Reason: m.Reason})
	}
	if over {
		out.Path = ptr(slices.Clone(c.Path))
		if c.Winner >= 0 {
			out.Winner = &c.Winner
		}
	}
	return out
}

func toGameBracket(b games.Bracket) *GameBracket {
	out := &GameBracket{Size: b.Size, Short: b.Short, Rounds: [][]GameMatch{}}
	for _, r := range b.Rounds {
		ms := make([]GameMatch, 0, len(r))
		for _, m := range r {
			state := GameMatchStateWaiting
			switch m.State {
			case games.MatchUp:
				state = GameMatchStateUp
			case games.MatchDone:
				state = GameMatchStateDone
			}
			ms = append(ms, GameMatch{
				A: m.A, B: m.B, Winner: m.Winner, State: state, HeartsA: m.HeartsA, HeartsB: m.HeartsB,
				Bye: m.Bye, Walkover: m.Walkover, Toss: m.Toss,
			})
		}
		out.Rounds = append(out.Rounds, ms)
	}
	if c := b.Current; c != nil {
		out.Current = &GameMatchRef{Round: c.Round, Match: c.Match}
	}
	if l := b.Last; l != nil {
		out.Last = &GameMatchRef{Round: l.Round, Match: l.Match}
	}
	if !b.NextAt.IsZero() {
		out.NextAt = &b.NextAt
	}
	if b.Champion >= 0 {
		out.Champion = &b.Champion
	}
	return out
}
