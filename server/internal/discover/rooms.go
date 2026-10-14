// SPDX-License-Identifier: AGPL-3.0-only

package discover

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/dj"
	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// A room's taste as a set's source (MAD-826). A set can be made from a
// room's or a night's favorite artists (a snapshot: you pick from them),
// or linked to a room, whose taste its feed then mixes in (live). Either
// way it only reads the room: discovery never writes to the room's DJ
// memory, and the room's skips never reach a set.

// How much a linked room's taste counts, against the set's own seeds.
const (
	RoomLow    = "low"
	RoomMedium = "medium"
	RoomHigh   = "high"
)

var roomWeights = map[string]float64{RoomLow: 0.3, RoomMedium: 0.55, RoomHigh: 0.85}

const (
	// roomArtists is how many of a linked room's favorite artists a feed
	// walks out from, and similarPerRoom how many of each one's similar
	// artists.
	roomArtists    = 12
	similarPerRoom = 20
	// tasteTop is how many artists a room's taste lists.
	tasteTop = 40
	// roomHistory is how many of a room's latest plays its taste reads.
	roomHistory = 200
)

// ErrRoomNotFound is a room that isn't there, or that the user can't open.
var ErrRoomNotFound = errors.New("room not found")

// TasteArtist is an artist a room likes.
type TasteArtist struct {
	Name string
	// Weight is how much, 0 to 1 against the artist it likes most.
	Weight float64
	// Plays is how many of their songs played lately, or that night;
	// 0 when the liking is all from past nights.
	Plays int
}

// RoomTaste reads rooms' taste for a user, only rooms they can open.
// RoomSource is one.
type RoomTaste interface {
	// MayRead is ErrRoomNotFound unless the user can open the room.
	MayRead(ctx context.Context, userID, roomID string) error
	// Taste is the room's taste, most liked first: over past nights, and
	// lately. Mine narrows it to the user's own songs.
	Taste(ctx context.Context, userID, roomID string, mine bool) ([]TasteArtist, error)
	// NightTaste is the taste of one of the room's past nights.
	NightTaste(ctx context.Context, userID, roomID, nightID string, mine bool) ([]TasteArtist, error)
}

// RoomSource reads rooms' taste from the DJ's memory and the rooms'
// plays, read only.
type RoomSource struct {
	DB *store.Store
	// CanEnter reports whether a user may open a room (rooms.Service's).
	CanEnter func(ctx context.Context, userID string, r store.Room) (bool, error)
	// Graph, if set, says which artists are alike, so a song queued like
	// another counts for it.
	Graph dj.Graph
	// Now is the clock. Default store.Now.
	Now func() time.Time
}

// MayRead implements RoomTaste.
func (r *RoomSource) MayRead(ctx context.Context, userID, roomID string) error {
	room, err := r.DB.GetRoom(ctx, roomID)
	if store.IsNotFound(err) {
		return ErrRoomNotFound
	} else if err != nil {
		return err
	}
	ok, err := r.CanEnter(ctx, userID, room)
	if err != nil {
		return err
	}
	if !ok {
		return ErrRoomNotFound
	}
	return nil
}

// Taste implements RoomTaste: the room's long-term taste as the DJ keeps
// it, and its latest plays, whichever likes an artist more.
func (r *RoomSource) Taste(ctx context.Context, userID, roomID string, mine bool) ([]TasteArtist, error) {
	if err := r.MayRead(ctx, userID, roomID); err != nil {
		return nil, err
	}
	member := ""
	if mine {
		member = userID
	}
	lt, err := dj.Peek(ctx, r.DB, roomID, member)
	if err != nil {
		return nil, err
	}
	history, err := r.DB.ListHistory(ctx, store.ListHistoryParams{RoomID: roomID, Limit: roomHistory})
	if err != nil {
		return nil, err
	}
	now := store.Now
	if r.Now != nil {
		now = r.Now
	}
	in := dj.Input{History: history, Hearts: map[string]int{}, Now: now()}
	if len(history) > 0 {
		counts, err := r.DB.HeartCountsSince(ctx, store.HeartCountsSinceParams{RoomID: roomID, Since: history[len(history)-1].PlayHistory.StartedAt})
		if err != nil {
			return nil, err
		}
		for _, c := range counts {
			in.Hearts[c.QueueItemID] = int(c.Hearts)
		}
	}
	if r.Graph != nil {
		in.Related = dj.Related(ctx, r.Graph)
	}
	return tasteOf(lt, dj.NewProfile(in), member), nil
}

// NightTaste implements RoomTaste.
func (r *RoomSource) NightTaste(ctx context.Context, userID, roomID, nightID string, mine bool) ([]TasteArtist, error) {
	if err := r.MayRead(ctx, userID, roomID); err != nil {
		return nil, err
	}
	n, err := r.DB.GetNight(ctx, store.GetNightParams{ID: nightID, RoomID: roomID})
	if store.IsNotFound(err) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	in, err := dj.NightInput(ctx, r.DB, r.Graph, n)
	if err != nil {
		return nil, err
	}
	member := ""
	if mine {
		member = userID
	}
	return tasteOf(dj.LongTerm{}, dj.NewProfile(in), member), nil
}

// tasteOf merges a room's long-term taste and a profile of its plays:
// each artist as much as either likes them. Artists the room turned away
// lately are left out. member, if set, counts only their songs in the
// profile (the long-term taste is narrowed already).
func tasteOf(lt dj.LongTerm, p dj.Profile, member string) []TasteArtist {
	by := map[string]*TasteArtist{}
	put := func(k, name string, w float64, plays int) {
		if w <= 0 || k == "" {
			return
		}
		a, ok := by[k]
		if !ok {
			a = &TasteArtist{Name: name}
			by[k] = a
		}
		a.Weight = max(a.Weight, w)
		a.Plays += plays
	}
	for k, l := range lt.Artists {
		put(k, l.Name, l.Weight*(1-l.Veto), 0)
	}
	most := 0.0
	for _, t := range p.Artists {
		if member != "" {
			most = max(most, t.ByMember[member])
		}
	}
	for k, t := range p.Artists {
		if p.Avoid[k] {
			continue
		}
		w := t.Weight
		if member != "" {
			if most == 0 {
				continue
			}
			w = t.ByMember[member] / most
		}
		put(k, t.Artist.Name, w, t.Plays)
	}
	out := make([]TasteArtist, 0, len(by))
	for _, a := range by {
		out = append(out, *a)
	}
	slices.SortFunc(out, func(a, b TasteArtist) int {
		return cmp.Or(cmp.Compare(b.Weight, a.Weight), cmp.Compare(b.Plays, a.Plays), strings.Compare(a.Name, b.Name))
	})
	return out[:min(len(out), tasteTop)]
}

// RoomTaste returns a room's taste, or a past night's, for the user to
// make a set from.
func (s *Service) RoomTaste(ctx context.Context, userID, roomID, nightID string, mine bool) ([]TasteArtist, error) {
	if s.Rooms == nil {
		return []TasteArtist{}, nil
	}
	if nightID != "" {
		return s.Rooms.NightTaste(ctx, userID, roomID, nightID, mine)
	}
	return s.Rooms.Taste(ctx, userID, roomID, mine)
}

// roomSeeds are the favorite artists of the room a set is linked to, with
// how near they pull: their liking times the set's room weight. None if
// the set isn't linked, or the user can't open the room any more.
func (s *Service) roomSeeds(ctx context.Context, userID string, set Set) []TasteArtist {
	if s.Rooms == nil || !set.RoomID.Valid {
		return nil
	}
	taste, err := s.Rooms.Taste(ctx, userID, set.RoomID.String, false)
	if err != nil {
		if !errors.Is(err, ErrRoomNotFound) && ctx.Err() == nil {
			slog.Warn("discover: reading a linked room's taste", "set", set.ID, "err", err)
		}
		return nil
	}
	w := roomWeights[cmp.Or(set.RoomWeight, RoomMedium)]
	var out []TasteArtist
	for _, a := range taste {
		if set.isSeed(library.ArtistKey(a.Name)) {
			continue
		}
		a.Weight *= w
		out = append(out, a)
		if len(out) == roomArtists {
			break
		}
	}
	return out
}

// walkRoom reaches out from a linked room's artists, as from seeds that
// pull less.
func walkRoom(seeds []TasteArtist, lookup func(musicgraph.ArtistRef) (musicgraph.Artist, bool), names map[string]string,
	reach func(ref musicgraph.ArtistRef, seed string, near float64, hop int),
) {
	for _, ra := range seeds {
		k := library.ArtistKey(ra.Name)
		if _, isSeed := names[k]; isSeed || k == "" {
			continue
		}
		names[k] = ra.Name
		a, ok := lookup(musicgraph.ArtistRef{Name: ra.Name})
		if !ok {
			continue
		}
		for _, sim := range a.Similar[:min(len(a.Similar), similarPerRoom)] {
			reach(sim.Artist, k, sim.Score*ra.Weight, 1)
		}
	}
}
