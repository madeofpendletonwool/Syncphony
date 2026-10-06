// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"math/rand/v2"
	"sync"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/suggest"
)

// Quick picks for the search screen (MAD-728): songs like one song, and
// songs at random. Both ask every link the caller can use at once, and
// leave out the ones that fail.

// GetQueueItemSimilar returns songs like one the room queued: what
// "keep the vibe going" finds for a single seed.
func (s *Server) GetQueueItemSimilar(ctx context.Context, req GetQueueItemSimilarRequestObject) (GetQueueItemSimilarResponseObject, error) {
	it, err := s.Queue.Item(ctx, req.RoomId, req.ItemId)
	if err != nil {
		return nil, err
	}
	limit := 20
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	seed := suggest.SeedOf(it)
	lists, err := s.eachLink(ctx, func(ctx context.Context, l store.ServiceLink, recommends bool) []provider.Track {
		// A Finder isn't safe for concurrent use, so each link gets one.
		f := suggest.NewFinder(s.Links, rand.IntN)
		defer f.Close()
		if !recommends {
			// Services that only search have more by the same artist.
			sess, ok := f.Open(ctx, l.ID)
			if !ok {
				return nil
			}
			return f.ByArtist(ctx, sess, l, seed)
		}
		rec, sess, ok := f.Recommender(ctx, l.ID)
		if !ok {
			return nil
		}
		return f.Similar(ctx, rec, sess, l, seed, rooms.AdventureSimilar)
	})
	if err != nil {
		return nil, err
	}
	// Leave out the seed itself, and any song twice, however each
	// service spells it.
	seen := suggest.Seen{}
	seen.Item(it)
	out := TrackList{Tracks: []TrackResult{}}
	for _, t := range interleaveTracks(lists) {
		if seen.Has(t) {
			continue
		}
		seen.Track(t)
		out.Tracks = append(out.Tracks, toTrackResult(t))
		if len(out.Tracks) == limit {
			break
		}
	}
	return GetQueueItemSimilar200JSONResponse(out), nil
}

// GetRandomTracks returns songs picked at random, from every service the
// caller can use that picks them.
func (s *Server) GetRandomTracks(ctx context.Context, req GetRandomTracksRequestObject) (GetRandomTracksResponseObject, error) {
	limit := 5
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	lists, err := s.eachLink(ctx, func(ctx context.Context, l store.ServiceLink, recommends bool) []provider.Track {
		if !recommends {
			return nil
		}
		f := suggest.NewFinder(s.Links, rand.IntN)
		defer f.Close()
		rec, _, ok := f.Recommender(ctx, l.ID)
		if !ok {
			return nil
		}
		ts, err := rec.RandomTracks(ctx, limit)
		if err != nil {
			return nil
		}
		return ts
	})
	if err != nil {
		return nil, err
	}
	var all []provider.Track
	for _, ts := range lists {
		all = append(all, ts...)
	}
	rand.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] }) //nolint:gosec // picking songs, not secrets
	out := TrackList{Tracks: []TrackResult{}}
	for _, t := range all[:min(len(all), limit)] {
		out.Tracks = append(out.Tracks, toTrackResult(t))
	}
	return GetRandomTracks200JSONResponse(out), nil
}

// eachLink calls ask on every healthy link the caller can use, at once,
// and returns each one's songs. recommends is whether its service has the
// Recommendations capability.
func (s *Server) eachLink(ctx context.Context, ask func(ctx context.Context, l store.ServiceLink, recommends bool) []provider.Track) ([][]provider.Track, error) {
	ls, err := s.Links.Usable(ctx, sessionFrom(ctx).User.ID)
	if err != nil {
		return nil, err
	}
	lists := make([][]provider.Track, len(ls))
	var wg sync.WaitGroup
	for i, l := range ls {
		if l.Status != "ok" {
			continue
		}
		p, err := s.Links.Provider(l.Provider)
		if err != nil {
			continue
		}
		recommends := p.Info().Capabilities.Recommendations
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, searchTimeout)
			defer cancel()
			lists[i] = ask(ctx, l, recommends)
		})
	}
	wg.Wait()
	return lists, nil
}

// interleaveTracks takes the first of each list, then the second, and so on.
func interleaveTracks(lists [][]provider.Track) []provider.Track {
	var out []provider.Track
	for i := 0; ; i++ {
		more := false
		for _, ts := range lists {
			if i < len(ts) {
				out = append(out, ts[i])
				more = true
			}
		}
		if !more {
			return out
		}
	}
}
