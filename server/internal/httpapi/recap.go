// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"

	"github.com/madeofpendletonwool/syncphony/server/internal/playlists"
	"github.com/madeofpendletonwool/syncphony/server/internal/recap"
)

// Syncphony Wrapped (MAD-722): a night's recap, as a story.

// GetRoomRecap makes the recap of the songs that started in [from, to).
func (s *Server) GetRoomRecap(ctx context.Context, req GetRoomRecapRequestObject) (GetRoomRecapResponseObject, error) {
	from, to := req.Params.From, req.Params.To
	plays, err := s.Rooms.Plays(ctx, req.RoomId, from, to)
	if err != nil {
		return nil, err
	}
	hearts, err := s.Nights.HeartsSince(ctx, req.RoomId, from)
	if err != nil {
		return nil, err
	}
	var facts recap.Facts
	if s.Graph != nil {
		facts = recap.Graph{G: s.Graph}
	}
	rc := recap.Make(ctx, plays, hearts, facts)

	stats, err := s.GetRoomStats(ctx, GetRoomStatsRequestObject{RoomId: req.RoomId, Params: GetRoomStatsParams{From: &from, To: &to}})
	if err != nil {
		return nil, err
	}
	out := Recap{
		Stats: RoomStats(stats.(GetRoomStats200JSONResponse)), Genres: toShares(rc.Genres), Decades: toShares(rc.Decades),
		Overlaps: []TasteOverlap{}, Playlists: []PlaylistRef{},
		TopAdder: toPersonCount(rc.TopAdder), MostHearted: toPersonCount(rc.MostHearted), Streak: toPersonCount(rc.Streak),
	}
	if rc.MostSkipped != nil {
		out.MostSkipped = &struct {
			Item  QueueItem `json:"item"`
			Skips int       `json:"skips"`
		}{Item: toQueueItem(rc.MostSkipped.Item), Skips: rc.MostSkipped.N}
	}
	for _, o := range rc.Overlaps {
		out.Overlaps = append(out.Overlaps, TasteOverlap{UserIds: o.UserIDs[:], Artists: o.Artists})
	}
	// The night that ended in this stretch, with its song of the night.
	ns, err := s.Nights.List(ctx, req.RoomId, 100)
	if err != nil {
		return nil, err
	}
	for _, n := range ns {
		if !n.StartedAt.Before(from) && n.StartedAt.Before(to) {
			out.Night = ptr(toNight(n))
			break
		}
	}
	// Playlists saved from it: the big screen sees those shared with the
	// room; people see theirs too.
	var a playlists.Actor
	if displayFrom(ctx) == nil {
		a = roomActor(sessionFrom(ctx).User)
	}
	ps, err := s.Playlists.ForNight(ctx, a, req.RoomId, from, to)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		out.Playlists = append(out.Playlists, PlaylistRef{Id: p.ID, Name: p.Name, OwnerId: p.OwnerID})
	}
	return GetRoomRecap200JSONResponse(out), nil
}

func toPersonCount(c *recap.Count) *PersonCount {
	if c == nil {
		return nil
	}
	return &PersonCount{UserId: c.UserID, Count: c.N}
}

func toShares(ss []recap.Share) []MixShare {
	out := make([]MixShare, len(ss))
	for i, sh := range ss {
		out[i] = MixShare{Name: sh.Name, Plays: sh.Plays}
	}
	return out
}
