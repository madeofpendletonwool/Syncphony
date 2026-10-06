// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/stats"
)

// GetHistory returns a page of the songs a room played.
func (s *Server) GetHistory(ctx context.Context, req GetHistoryRequestObject) (GetHistoryResponseObject, error) {
	hq := rooms.HistoryQuery{Limit: 20}
	if req.Params.Limit != nil {
		hq.Limit = min(max(*req.Params.Limit, 1), 100)
	}
	if req.Params.Before != nil {
		hq.Before = *req.Params.Before
	}
	if req.Params.UserId != nil {
		hq.UserID = *req.Params.UserId
	}
	ps, err := s.Rooms.History(ctx, req.RoomId, hq)
	if err != nil {
		return nil, err
	}
	out := make(GetHistory200JSONResponse, len(ps))
	for i, p := range ps {
		out[i] = toPlayedItem(p)
	}
	return out, nil
}

// GetRoomStats sums up the songs a room played in a time range.
func (s *Server) GetRoomStats(ctx context.Context, req GetRoomStatsRequestObject) (GetRoomStatsResponseObject, error) {
	from, to := time.Time{}, rooms.Forever
	if req.Params.From != nil {
		from = *req.Params.From
	}
	if req.Params.To != nil {
		to = *req.Params.To
	}
	plays, err := s.Rooms.Plays(ctx, req.RoomId, from, to)
	if err != nil {
		return nil, err
	}
	sum := stats.Summarize(plays)
	out := RoomStats{
		Plays: sum.Plays, Skipped: sum.Skipped, ListeningMs: sum.Listening.Milliseconds(),
		TopTracks: toTrackCounts(sum.TopTracks), TopArtists: toArtistCounts(sum.TopArtists), People: []PersonStats{},
	}
	for _, p := range sum.People {
		out.People = append(out.People, PersonStats{
			UserId: p.UserID, Plays: p.Plays, Skipped: p.Skipped, ListeningMs: p.Listening.Milliseconds(),
			TopTracks: toTrackCounts(p.TopTracks), TopArtists: toArtistCounts(p.TopArtists),
		})
	}
	if sum.First != nil {
		out.First, out.Last = ptr(toPlayedItem(*sum.First)), ptr(toPlayedItem(*sum.Last))
	}
	return GetRoomStats200JSONResponse(out), nil
}

// ListSessions returns a room's listening sessions, newest first.
func (s *Server) ListSessions(ctx context.Context, req ListSessionsRequestObject) (ListSessionsResponseObject, error) {
	limit := 20
	if req.Params.Limit != nil {
		limit = min(max(*req.Params.Limit, 1), 100)
	}
	rows, err := s.Rooms.PlayTimes(ctx, req.RoomId)
	if err != nil {
		return nil, err
	}
	spans := make([]stats.Span, len(rows))
	for i, r := range rows {
		spans[i] = stats.Span{Start: r.StartedAt, End: r.EndedAt.Time, User: r.AddedBy}
	}
	sessions := stats.Sessions(spans)
	// A night is a session that ended: the host ended it, or the room went
	// quiet. Each carries its song of the night.
	ns, err := s.Nights.List(ctx, req.RoomId, 2*limit)
	if err != nil {
		return nil, err
	}
	out := make(ListSessions200JSONResponse, 0, min(len(sessions), limit))
	for _, ss := range sessions[:min(len(sessions), limit)] {
		ls := ListeningSession{StartedAt: ss.Start, EndedAt: ss.End, Plays: ss.Plays, People: ss.People}
		for _, n := range ns {
			if !n.StartedAt.Before(ss.Start) && !n.StartedAt.After(ss.End) {
				ls.Night = ptr(toNight(n))
				break
			}
		}
		out = append(out, ls)
	}
	return out, nil
}

func toPlayedItem(p rooms.Played) PlayedItem {
	return PlayedItem{Item: toQueueItem(p.Item), StartedAt: p.StartedAt, EndedAt: p.EndedAt, EndReason: PlayedItemEndReason(p.EndReason)}
}

func toTrackCounts(tcs []stats.TrackCount) []TrackCount {
	out := make([]TrackCount, len(tcs))
	for i, tc := range tcs {
		out[i] = TrackCount{Item: toQueueItem(tc.Item), Plays: tc.Plays}
	}
	return out
}

func toArtistCounts(acs []stats.ArtistCount) []ArtistCount {
	out := make([]ArtistCount, len(acs))
	for i, ac := range acs {
		out[i] = ArtistCount{Name: ac.Name, Plays: ac.Plays}
	}
	return out
}
