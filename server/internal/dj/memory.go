// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// How far back the memory reaches.
const (
	// foldReach is how many of a room's latest nights are folded in when
	// it has none yet: by then the oldest counts nightFade^30, about 0.00002.
	foldReach = 30
	// maxNightPlays bounds the plays read for one night.
	maxNightPlays = 2000
)

// Memory keeps each room's long-term taste: it folds in each night once it
// has ended (nights.Service ends them), and reads it back.
type Memory struct {
	DB *store.Store
	// Graph, if set, gives artists' tags, from its cache.
	Graph Graph
	// Now is the clock. Default store.Now.
	Now func() time.Time

	mu sync.Mutex // one fold at a time
}

// Load folds in the room's nights that ended since it last looked, and
// returns its long-term taste.
func (m *Memory) Load(ctx context.Context, roomID string) (LongTerm, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	room, err := m.DB.GetDJRoom(ctx, roomID)
	if err != nil && !store.IsNotFound(err) {
		return LongTerm{}, err
	}
	room.RoomID = roomID
	nights, err := m.DB.NightsAfter(ctx, store.NightsAfterParams{RoomID: roomID, After: room.FoldedThrough, Limit: foldReach})
	if err != nil {
		return LongTerm{}, err
	}
	rows, err := m.DB.ListDJAffinities(ctx, roomID)
	if err != nil || len(nights) == 0 {
		return readLongTerm(room, rows), err
	}
	slices.Reverse(nights) // oldest first
	for _, n := range nights {
		night, err := m.night(ctx, n)
		if err != nil {
			return LongTerm{}, err
		}
		room.Nights++
		rows = fold(roomID, rows, night, room.Nights, n.EndedAt, func(a string) []musicgraph.Tag { return m.tags(ctx, a) })
		room.FoldedThrough = n.EndedAt
	}
	now := store.Now
	if m.Now != nil {
		now = m.Now
	}
	err = m.DB.Tx(ctx, func(q *store.Queries) error {
		if err := q.DeleteDJAffinities(ctx, roomID); err != nil {
			return err
		}
		for _, r := range rows {
			if err := q.AddDJAffinity(ctx, store.AddDJAffinityParams(r)); err != nil {
				return err
			}
		}
		return q.PutDJRoom(ctx, store.PutDJRoomParams{RoomID: roomID, Nights: room.Nights, FoldedThrough: room.FoldedThrough, UpdatedAt: now()})
	})
	return readLongTerm(room, rows), err
}

// night reads what the room did in a night: its plays, newest first, and
// their hearts.
func (m *Memory) night(ctx context.Context, n store.Night) (Input, error) {
	plays, err := m.DB.ListPlaysBetween(ctx, store.ListPlaysBetweenParams{
		RoomID: n.RoomID, FromTime: n.StartedAt, ToTime: n.EndedAt.Add(time.Microsecond), Limit: maxNightPlays,
	})
	if err != nil {
		return Input{}, err
	}
	counts, err := m.DB.HeartCountsSince(ctx, store.HeartCountsSinceParams{RoomID: n.RoomID, Since: n.StartedAt})
	if err != nil {
		return Input{}, err
	}
	in := Input{Hearts: map[string]int{}, Now: n.EndedAt}
	for i := len(plays) - 1; i >= 0; i-- {
		in.History = append(in.History, store.ListHistoryRow{PlayHistory: plays[i].PlayHistory, QueueItem: plays[i].QueueItem})
	}
	for _, c := range counts {
		in.Hearts[c.QueueItemID] = int(c.Hearts)
	}
	if m.Graph != nil {
		in.Related = Related(ctx, m.Graph)
	}
	return in, nil
}

// tags returns an artist's tags, by name, if the graph has them cached.
func (m *Memory) tags(ctx context.Context, artist string) []musicgraph.Tag {
	if m.Graph == nil {
		return nil
	}
	a, ok, err := m.Graph.CachedArtist(ctx, musicgraph.ArtistRef{Name: artist})
	if !ok || err != nil {
		return nil
	}
	return a.Tags
}

// Related reports whether two artists (ArtistKey) are alike, as far as
// the graph's cache knows: either lists the other among its similar
// artists. It's for Input.Related.
func Related(ctx context.Context, g Graph) func(a, b string) bool {
	similar := map[string][]string{}
	of := func(a string) []string {
		if s, ok := similar[a]; ok {
			return s
		}
		var out []string
		if art, ok, err := g.CachedArtist(ctx, musicgraph.ArtistRef{Name: a}); ok && err == nil {
			for _, s := range art.Similar {
				out = append(out, ArtistKey(s.Artist.Name))
			}
		}
		similar[a] = out
		return out
	}
	return func(a, b string) bool {
		return slices.Contains(of(a), b) || slices.Contains(of(b), a)
	}
}
