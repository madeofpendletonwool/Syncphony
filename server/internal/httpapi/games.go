// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/awards"
	"github.com/madeofpendletonwool/syncphony/server/internal/games"
	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
)

// Music party games (Phase 10, ADR 0015): rounds, answers and scores.

// ErrHiddenForRound is something a round keeps back until its reveal.
var ErrHiddenForRound = errors.New("that's hidden until the round's reveal")

// GetGameRound returns the room's round, if one is up.
func (s *Server) GetGameRound(_ context.Context, req GetGameRoundRequestObject) (GetGameRoundResponseObject, error) {
	if s.Games == nil {
		return GetGameRound204Response{}, nil
	}
	rd, ok := s.Games.Current(req.RoomId)
	if !ok {
		return GetGameRound204Response{}, nil
	}
	return GetGameRound200JSONResponse(toGameRound(rd)), nil
}

// StartGameRound starts a round about the song that's playing.
func (s *Server) StartGameRound(ctx context.Context, req StartGameRoundRequestObject) (StartGameRoundResponseObject, error) {
	u, err := member(ctx)
	if err != nil {
		return nil, err
	}
	if s.Games == nil {
		return nil, games.ErrGamesOff
	}
	var kind string
	if req.Body != nil && req.Body.Kind != nil {
		kind = string(*req.Body.Kind)
	}
	rd, err := s.Games.Start(ctx, req.RoomId, u, kind)
	if err != nil {
		return nil, err
	}
	return StartGameRound201JSONResponse(toGameRound(*rd)), nil
}

// AnswerGameRound takes an answer to the open round.
func (s *Server) AnswerGameRound(ctx context.Context, req AnswerGameRoundRequestObject) (AnswerGameRoundResponseObject, error) {
	if s.Games == nil {
		return nil, games.ErrNoRound
	}
	sess := sessionFrom(ctx)
	resp := quiz.Response{Choice: req.Body.Choice, Number: req.Body.Number}
	if req.Body.Text != nil {
		resp.Text = *req.Body.Text
	}
	a, err := s.Games.Answer(ctx, req.RoomId, req.RoundId, sess.User, sess.Guest != nil, resp)
	if err != nil {
		return nil, err
	}
	return AnswerGameRound200JSONResponse{RoundId: req.RoundId, At: a.At}, nil
}

// GetGameScores returns tonight's scores, as far as the caller may see them.
func (s *Server) GetGameScores(ctx context.Context, req GetGameScoresRequestObject) (GetGameScoresResponseObject, error) {
	if s.Games == nil {
		return GetGameScores200JSONResponse{RoomId: req.RoomId, Mode: GameScoresMode(rooms.ScoresOff), Players: []GamePlayer{}}, nil
	}
	sc, err := s.Games.Scores(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	var you string
	if sess, ok := ctx.Value(ctxSession).(*auth.Session); ok && sess != nil {
		you = sess.User.ID
	}
	return GetGameScores200JSONResponse(toGameScores(sc, you)), nil
}

func toGameRound(rd games.Round) GameRound {
	q := rd.Question
	out := GameRound{
		Id: rd.ID, RoomId: rd.RoomID, ItemId: rd.ItemID, Kind: GameKind(rd.Kind), Topic: nonEmpty(q.Topic),
		Mode: GameRoundMode(rd.Mode), State: GameRoundState(rd.State), Prompt: q.Prompt, Answer: GameRoundAnswer(q.Answer),
		Choices: q.Choices, StartedBy: nonEmpty(rd.StartedBy), OpensAt: rd.OpensAt, ClosesAt: rd.ClosesAt, DoneAt: rd.DoneAt,
		Answered: []string{}, Hides: []GameRoundHides{}, Guests: rd.Guests, TvOnly: rd.TVOnly,
		Scores: GameRoundScores(rd.Scores), Difficulty: float32(q.Difficulty),
	}
	if out.Choices == nil {
		out.Choices = []string{}
	}
	for _, h := range q.Hides {
		out.Hides = append(out.Hides, GameRoundHides(h))
	}
	answers := make([]*games.Answer, 0, len(rd.Answers))
	for _, a := range rd.Answers {
		answers = append(answers, a)
	}
	slices.SortFunc(answers, func(a, b *games.Answer) int { return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.UserID, b.UserID)) })
	for _, a := range answers {
		out.Answered = append(out.Answered, a.UserID)
	}
	// The answer only from the reveal on.
	if rd.State != games.StateReveal && rd.State != games.StateDone {
		return out
	}
	out.Correct, out.Reveal = &q.Correct, nonEmpty(q.Reveal)
	if q.Answer == quiz.AnswerChoice || len(q.Choices) > 0 {
		out.CorrectIndex = &q.CorrectIndex
	}
	slices.SortStableFunc(answers, func(a, b *games.Answer) int { return cmp.Compare(b.Points, a.Points) })
	results := make([]GameResult, 0, len(answers))
	for _, a := range answers {
		results = append(results, GameResult{UserId: a.UserID, Correct: a.Correct, Points: a.Points, Answer: nonEmpty(answerText(q, a.Response))})
	}
	out.Results = &results
	return out
}

// answerText is an answer as words.
func answerText(q quiz.Question, r quiz.Response) string {
	switch {
	case r.Number != nil:
		return strconv.Itoa(*r.Number)
	case r.Choice != nil && *r.Choice >= 0 && *r.Choice < len(q.Choices):
		return q.Choices[*r.Choice]
	}
	return r.Text
}

// toGameScores is the scores as you may see them: everyone's on a board,
// your own while they're private, none while they're off.
func toGameScores(sc games.Scores, you string) GameScores {
	out := GameScores{RoomId: sc.RoomID, Mode: GameScoresMode(sc.Mode), Players: []GamePlayer{}}
	for _, p := range sc.Players {
		if sc.Mode == rooms.ScoresOff || (sc.Mode == rooms.ScoresPrivate && p.UserID != you) {
			continue
		}
		out.Players = append(out.Players, GamePlayer{UserId: p.UserID, Points: p.Points, Correct: p.Correct, Answered: p.Answered})
	}
	return out
}

func toRoomGames(g rooms.Games) RoomGames {
	out := RoomGames{
		Level: RoomGamesLevel(g.LevelOf()), Enabled: map[string]bool{}, Frequency: g.Every(), Guests: g.GuestsAnswer(),
		Scores: RoomGamesScores(g.ScoreMode()), TvOnly: g.TVOnly, BreaksPerHour: rooms.DefaultBreaksPerHour,
	}
	// Shown whatever the level; it only applies at Game night.
	if g.BreaksPerHour != nil {
		out.BreaksPerHour = *g.BreaksPerHour
	}
	for _, k := range rooms.GameKinds {
		if g.Allowed(k) {
			out.Enabled[k] = g.On(k)
		}
	}
	return out
}

// fromRoomGames reads a change to a room's games: what's left out is the
// level's default.
func fromRoomGames(c RoomGamesChange) rooms.Games {
	g := rooms.Games{Frequency: c.Frequency, BreaksPerHour: c.BreaksPerHour}
	if c.Level != nil {
		g.Level = string(*c.Level)
	}
	if c.Enabled != nil {
		g.Enabled = *c.Enabled
	}
	if c.Guests != nil {
		g.NoGuests = !*c.Guests
	}
	if c.Scores != nil {
		g.Scores = string(*c.Scores)
	}
	if c.TvOnly != nil {
		g.TVOnly = *c.TvOnly
	}
	return g
}

func toAwards(as []awards.Award) []Award {
	out := make([]Award, len(as))
	for i, a := range as {
		out[i] = Award{Kind: AwardKind(a.Kind), Title: a.Title, UserId: a.UserID, ItemId: nonEmpty(a.ItemID), Reason: a.Reason}
	}
	return out
}

// --- Hiding answers ------------------------------------------------------

// hidden reports what a room's round keeps back about one of its songs:
// quiz.HideSong, HideNotes or HideLyrics.
func (s *Server) hidden(roomID, itemID, what string) bool {
	if s.Games == nil {
		return false
	}
	item, hides := s.Games.Hidden(roomID)
	return item != "" && item == itemID && slices.Contains(hides, what)
}

// hideSong leaves a song out of an item while a round asks about it:
// "Mystery song", with no artists, album or artwork.
func hideSong(it QueueItem) QueueItem {
	it.Track.Title = "Mystery song"
	it.Track.Artists, it.Track.ArtistIds = []string{}, &[]string{}
	it.Track.Album, it.Track.AlbumId, it.Track.Artwork = nil, nil, nil
	return it
}

// nowPlayingFor is a room's playback as the speaker or someone else sees
// it: everyone but the speaker gets a mystery song while a round hides it.
func (s *Server) nowPlayingFor(np rooms.NowPlaying, speaker bool) NowPlaying {
	out := toNowPlaying(np)
	if speaker || out.Item == nil || !s.hidden(np.RoomID, out.Item.Id, quiz.HideSong) {
		return out
	}
	out.Item = ptr(hideSong(*out.Item))
	return out
}

// queueFor is a room's queue as the speaker or someone else sees it.
func (s *Server) queueFor(snap rooms.QueueSnapshot, speaker bool) QueueSnapshot {
	out := toQueueSnapshot(snap)
	if speaker || s.Games == nil {
		return out
	}
	item, hides := s.Games.Hidden(snap.RoomID)
	if item == "" || !slices.Contains(hides, quiz.HideSong) {
		return out
	}
	for i := range out.Items {
		if out.Items[i].Id == item {
			out.Items[i] = hideSong(out.Items[i])
		}
	}
	return out
}
