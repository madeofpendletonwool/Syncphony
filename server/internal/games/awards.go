// SPDX-License-Identifier: AGPL-3.0-only

package games

import (
	"context"
	"encoding/json"

	"github.com/madeofpendletonwool/syncphony/server/internal/awards"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Awards hands out a night's awards (package awards), in rooms that play
// at any level but Off. It reads only what's already known about the
// night's songs: the night is over, so nothing's worth waiting on.
func (e *Engine) Awards(ctx context.Context, n store.Night) ([]awards.Award, error) {
	row, err := e.rooms.Get(ctx, n.RoomID)
	if err != nil {
		return nil, err
	}
	if !rooms.ParseSettings(row.Settings).Games.Awards() {
		return nil, nil
	}
	played, err := e.rooms.Plays(ctx, n.RoomID, n.StartedAt, n.EndedAt.Add(1))
	if err != nil {
		return nil, err
	}
	hearts, err := e.db.HeartCountsSince(ctx, store.HeartCountsSinceParams{RoomID: n.RoomID, Since: n.StartedAt})
	if err != nil {
		return nil, err
	}
	heartsOf := map[string]int{}
	for _, h := range hearts {
		heartsOf[h.QueueItemID] = int(h.Hearts)
	}
	plays := make([]awards.Play, 0, len(played))
	for _, p := range played {
		var t provider.Track
		_ = json.Unmarshal([]byte(p.Item.Metadata), &t)
		song := p.Item.Provider + "\x00" + p.Item.TrackID
		if t.ISRC != "" {
			song = "isrc\x00" + t.ISRC
		}
		ap := awards.Play{
			ItemID: p.Item.ID, Title: t.Title, Song: song,
			AddedAt: p.Item.AddedAt, StartedAt: p.StartedAt, EndedAt: p.EndedAt, EndReason: p.EndReason,
			Hearts: heartsOf[p.Item.ID],
		}
		if !p.Item.IsAutopilot() {
			ap.UserID = p.Item.AddedBy
		}
		for _, a := range t.Artists {
			ap.Artists = append(ap.Artists, a.Name)
		}
		if e.Facts != nil {
			if f, err := e.Facts.Song(ctx, p.Item, true); err == nil {
				ap.BPM, ap.Energy, ap.Rank, ap.Year, ap.Samples = f.BPM, f.Energy, f.Rank, f.Year, len(f.Samples)
			}
		}
		plays = append(plays, ap)
	}
	rows, err := e.db.GameScoresSince(ctx, store.GameScoresSinceParams{RoomID: n.RoomID, Since: n.StartedAt})
	if err != nil {
		return nil, err
	}
	scores := make([]awards.Score, 0, len(rows))
	for _, r := range rows {
		scores = append(scores, awards.Score{UserID: r.UserID, Points: int(r.Points)})
	}
	out := awards.Pick(plays, scores)
	if a, ok := e.bracketAward(ctx, n); ok {
		out = append(out, a)
	}
	return out, nil
}
