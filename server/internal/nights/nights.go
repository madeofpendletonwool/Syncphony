// SPDX-License-Identifier: AGPL-3.0-only

// Package nights crowns each night's song of the night (MAD-721). Anyone
// in a room can heart the song that's playing. A night ends when the host
// ends it, or when the room has been quiet for stats.SessionGap; its
// most-hearted song is then crowned, everyone in the room hears about it
// (the big screen makes a moment of it), and the night is kept for recaps.
package nights

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/awards"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/stats"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Errors returned by Service, besides rooms.ErrNotFound.
var (
	ErrNotFound      = errors.New("that song isn't in this room")
	ErrForbidden     = errors.New("guests can't heart songs in this room")
	ErrNotHost       = errors.New("only the room's owner or an admin can end the night")
	ErrNotTonight    = errors.New("that song didn't play tonight")
	ErrNothingPlayed = errors.New("nothing has played since the last night ended")
)

// InvalidInputError is a heart that can't be given, such as for your own song.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

// IdleAfter is how long a room is quiet before its night ends: the gap
// between listening sessions, so a night is a session in recaps.
const IdleAfter = stats.SessionGap

// maxNightPlays bounds the plays read to find where a night began.
const maxNightPlays = 2000

// Ended-by values.
const (
	EndedByHost = "host"
	EndedByIdle = "idle"
)

// Hearts is who hearted a song, in order. Published as realtime.HeartsUpdated.
type Hearts struct {
	RoomID, ItemID string
	UserIDs        []string
}

// Night is a night that ended, with its song of the night (Item, if any
// song got a heart) and its awards. Published as realtime.NightEnded.
type Night struct {
	store.Night
	Item   *store.QueueItem
	Awards []awards.Award
	// AwardItems are the songs the awards are for, by item ID.
	AwardItems map[string]store.QueueItem
}

// awardItems reads the songs a night's awards are for. Songs since
// deleted are left out.
func awardItems(ctx context.Context, q *store.Queries, as []awards.Award) map[string]store.QueueItem {
	out := map[string]store.QueueItem{}
	for _, a := range as {
		if a.ItemID == "" {
			continue
		}
		if it, err := q.GetQueueItem(ctx, a.ItemID); err == nil {
			out[a.ItemID] = it
		}
	}
	return out
}

// Service hearts songs and ends nights.
type Service struct {
	db  *store.Store
	bus realtime.Bus
	// Now is the clock. Default store.Now.
	Now func() time.Time
	// Awards, if set, hands out a night's awards as it ends (package
	// games). Nil gives none.
	Awards func(ctx context.Context, n store.Night) ([]awards.Award, error)
	// Champion, if set, names the queue item that won the room's bracket
	// battle since a time (package games), "" if none did. It's a
	// candidate for song of the night.
	Champion func(ctx context.Context, roomID string, since time.Time) string
	// Bracket, if set, gives the night's bracket battle as JSON to keep
	// for the recap, "" if it had none (package games).
	Bracket func(ctx context.Context, n store.Night) (string, error)
}

// New returns a Service.
func New(db *store.Store, bus realtime.Bus) *Service {
	return &Service{db: db, bus: bus, Now: store.Now}
}

// item returns one of a room's items.
func (s *Service) item(ctx context.Context, roomID, itemID string) (store.QueueItem, error) {
	it, err := s.db.GetQueueItem(ctx, itemID)
	if store.IsNotFound(err) || (err == nil && it.RoomID != roomID) {
		return it, ErrNotFound
	}
	return it, err
}

// Hearts returns who hearted one of a room's songs.
func (s *Service) Hearts(ctx context.Context, roomID, itemID string) (Hearts, error) {
	if _, err := s.item(ctx, roomID, itemID); err != nil {
		return Hearts{}, err
	}
	ids, err := s.db.ListHearts(ctx, itemID)
	return Hearts{RoomID: roomID, ItemID: itemID, UserIDs: ids}, err
}

// Heart hearts (on) or unhearts one of a room's songs for userID, and
// tells the room. Only a song that's playing or played tonight can get a
// heart, and not from whoever queued it, or from a guest in a room where
// guests don't vote.
func (s *Service) Heart(ctx context.Context, roomID, userID, itemID string, on bool) (Hearts, error) {
	room, err := s.db.GetRoom(ctx, roomID)
	if store.IsNotFound(err) {
		return Hearts{}, rooms.ErrNotFound
	} else if err != nil {
		return Hearts{}, err
	}
	it, err := s.item(ctx, roomID, itemID)
	if err != nil {
		return Hearts{}, err
	}
	if on {
		if err := s.mayHeart(ctx, room, userID, it); err != nil {
			return Hearts{}, err
		}
		err = s.db.AddHeart(ctx, store.AddHeartParams{QueueItemID: itemID, UserID: userID, CreatedAt: s.Now()})
	} else {
		err = s.db.RemoveHeart(ctx, store.RemoveHeartParams{QueueItemID: itemID, UserID: userID})
	}
	if err != nil {
		return Hearts{}, err
	}
	h, err := s.Hearts(ctx, roomID, itemID)
	if err != nil {
		return Hearts{}, err
	}
	s.bus.Publish(realtime.RoomTopic(roomID), realtime.Event{Type: realtime.HeartsUpdated, Data: h})
	return h, nil
}

func (s *Service) mayHeart(ctx context.Context, room store.Room, userID string, it store.QueueItem) error {
	if it.AddedBy == userID && !it.IsAutopilot() {
		return &InvalidInputError{"you queued this one; hearts are for everyone else's songs"}
	}
	switch _, err := s.db.GetGuest(ctx, userID); {
	case err == nil:
		if !rooms.ParseSettings(room.Settings).Guests.CanVote() {
			return ErrForbidden
		}
	case !store.IsNotFound(err):
		return err
	}
	first, err := s.db.FirstPlayOf(ctx, it.ID)
	if store.IsNotFound(err) {
		return ErrNotTonight // it hasn't played yet
	} else if err != nil {
		return err
	}
	last, err := s.db.LastNight(ctx, room.ID)
	if err == nil && !first.StartedAt.After(last.EndedAt) {
		return ErrNotTonight
	} else if err != nil && !store.IsNotFound(err) {
		return err
	}
	return nil
}

// End ends a room's night on its host's say-so: the room's owner, or an
// admin.
func (s *Service) End(ctx context.Context, roomID string, by store.User) (Night, error) {
	room, err := s.db.GetRoom(ctx, roomID)
	if store.IsNotFound(err) {
		return Night{}, rooms.ErrNotFound
	} else if err != nil {
		return Night{}, err
	}
	if room.OwnerID != by.ID && by.Role != store.RoleAdmin {
		return Night{}, ErrNotHost
	}
	n, ok, err := s.end(ctx, roomID, EndedByHost, s.Now())
	if err != nil {
		return Night{}, err
	}
	if !ok {
		return Night{}, ErrNothingPlayed
	}
	return n, nil
}

// end ends a room's night at a time, if anything played in it.
func (s *Service) end(ctx context.Context, roomID, by string, at time.Time) (Night, bool, error) {
	var out Night
	champion := ""
	if s.Champion != nil {
		since := time.Time{}
		if last, err := s.db.LastNight(ctx, roomID); err == nil {
			since = last.EndedAt
		}
		champion = s.Champion(ctx, roomID, since)
	}
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		since := time.Time{}
		if last, err := q.LastNight(ctx, roomID); err == nil {
			since = last.EndedAt
		} else if !store.IsNotFound(err) {
			return err
		}
		plays, err := q.PlaysSince(ctx, store.PlaysSinceParams{RoomID: roomID, Since: since, Limit: maxNightPlays})
		if err != nil || len(plays) == 0 {
			return err
		}
		plays = tonight(plays)
		start := plays[0].StartedAt
		counts, err := q.HeartCountsSince(ctx, store.HeartCountsSinceParams{RoomID: roomID, Since: start})
		if err != nil {
			return err
		}
		n := store.CreateNightParams{
			ID: store.NewID(), RoomID: roomID, StartedAt: start, EndedAt: at, EndedBy: by, Plays: int64(len(plays)),
		}
		if best, hearts := crown(plays, counts, champion); best != "" {
			n.QueueItemID, n.Hearts = sql.NullString{String: best, Valid: true}, hearts
		}
		row, err := q.CreateNight(ctx, n)
		if err != nil {
			return err
		}
		out = Night{Night: row}
		if row.QueueItemID.Valid {
			it, err := q.GetQueueItem(ctx, row.QueueItemID.String)
			if err != nil {
				return err
			}
			out.Item = &it
		}
		return nil
	})
	if err != nil || out.ID == "" {
		return Night{}, false, err
	}
	// After the night is kept: awards read the night's songs, and a
	// failure mustn't lose the night.
	if s.Awards != nil {
		as, err := s.Awards(ctx, out.Night)
		if err != nil {
			slog.Warn("nights: handing out awards", "room", roomID, "err", err)
		} else if len(as) > 0 {
			raw, _ := json.Marshal(as)
			if err := s.db.SetNightAwards(ctx, store.SetNightAwardsParams{Awards: string(raw), ID: out.ID}); err != nil {
				slog.Warn("nights: keeping awards", "room", roomID, "err", err)
			} else {
				out.Awards, out.Night.Awards = as, string(raw)
				out.AwardItems = awardItems(ctx, s.db.Queries, as)
			}
		}
	}
	if s.Bracket != nil {
		raw, err := s.Bracket(ctx, out.Night)
		switch {
		case err != nil:
			slog.Warn("nights: reading the bracket", "room", roomID, "err", err)
		case raw != "":
			if err := s.db.SetNightBracket(ctx, store.SetNightBracketParams{Bracket: raw, ID: out.ID}); err != nil {
				slog.Warn("nights: keeping the bracket", "room", roomID, "err", err)
			} else {
				out.Bracket = raw
			}
		}
	}
	s.bus.Publish(realtime.RoomTopic(roomID), realtime.Event{Type: realtime.NightEnded, Data: out})
	return out, true, nil
}

// tonight trims plays (oldest first) to the last session: the night is
// what played since the room last went quiet for IdleAfter.
func tonight(plays []store.PlayHistory) []store.PlayHistory {
	for i := len(plays) - 1; i > 0; i-- {
		if plays[i].StartedAt.Sub(endOf(plays[i-1])) >= IdleAfter {
			return plays[i:]
		}
	}
	return plays
}

// endOf is when a play ended, or started if it hasn't ended.
func endOf(p store.PlayHistory) time.Time {
	if p.EndedAt.Valid {
		return p.EndedAt.Time
	}
	return p.StartedAt
}

// crown picks the night's song: the most hearts, and of those the one
// that played first, so a late heart can't steal the crown from a song
// that earned its hearts earlier. The bracket's champion, if it played
// tonight, takes a tie. "" if nothing tonight got a heart.
func crown(plays []store.PlayHistory, counts []store.HeartCountsSinceRow, champion string) (string, int64) {
	started := map[string]time.Time{}
	for _, p := range plays {
		if _, ok := started[p.QueueItemID]; !ok {
			started[p.QueueItemID] = p.StartedAt
		}
	}
	counts = slices.DeleteFunc(slices.Clone(counts), func(c store.HeartCountsSinceRow) bool {
		_, ok := started[c.QueueItemID]
		return !ok || c.Hearts == 0
	})
	if _, ok := started[champion]; ok {
		var top, mine int64
		for _, c := range counts {
			top = max(top, c.Hearts)
			if c.QueueItemID == champion {
				mine = c.Hearts
			}
		}
		if mine > 0 && mine >= top {
			return champion, mine
		}
	}
	if len(counts) == 0 {
		return "", 0
	}
	best := slices.MinFunc(counts, func(a, b store.HeartCountsSinceRow) int {
		return cmp.Or(cmp.Compare(b.Hearts, a.Hearts), started[a.QueueItemID].Compare(started[b.QueueItemID]), cmp.Compare(a.QueueItemID, b.QueueItemID))
	})
	return best.QueueItemID, best.Hearts
}

// Sweep ends the night in every room that has gone quiet: nothing playing,
// and the last song ended IdleAfter ago.
func (s *Service) Sweep(ctx context.Context) error {
	rs, err := s.db.ListRooms(ctx)
	if err != nil {
		return err
	}
	now := s.Now()
	var errs []error
	for _, r := range rs {
		since := time.Time{}
		if last, err := s.db.LastNight(ctx, r.ID); err == nil {
			since = last.EndedAt
		} else if !store.IsNotFound(err) {
			errs = append(errs, err)
			continue
		}
		plays, err := s.db.PlaysSince(ctx, store.PlaysSinceParams{RoomID: r.ID, Since: since, Limit: 1})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(plays) == 0 || !plays[0].EndedAt.Valid || now.Sub(plays[0].EndedAt.Time) < IdleAfter {
			continue
		}
		// The night ended when its last song did.
		if _, _, err := s.end(ctx, r.ID, EndedByIdle, plays[0].EndedAt.Time); err != nil {
			errs = append(errs, err)
		} else {
			slog.Debug("nights: the room went quiet; ended its night", "room", r.ID)
		}
	}
	return errors.Join(errs...)
}

// List returns a room's nights, newest first, with their songs.
func (s *Service) List(ctx context.Context, roomID string, limit int) ([]Night, error) {
	if _, err := s.db.GetRoom(ctx, roomID); store.IsNotFound(err) {
		return nil, rooms.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	rows, err := s.db.ListNights(ctx, store.ListNightsParams{RoomID: roomID, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]Night, len(rows))
	for i, r := range rows {
		out[i] = Night{Night: r}
		_ = json.Unmarshal([]byte(r.Awards), &out[i].Awards)
		out[i].AwardItems = awardItems(ctx, s.db.Queries, out[i].Awards)
		if r.QueueItemID.Valid {
			it, err := s.db.GetQueueItem(ctx, r.QueueItemID.String)
			if err != nil {
				return nil, err
			}
			out[i].Item = &it
		}
	}
	return out, nil
}
