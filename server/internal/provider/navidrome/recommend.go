// SPDX-License-Identifier: AGPL-3.0-only

package navidrome

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Navidrome recommends through its metadata agents: getSimilarSongs and
// getTopSongs ask Last.fm (or another agent) about the artist, and return
// the songs this server has. A server without an agent set up returns no
// songs, not an error. getRandomSongs needs nothing.

// maxRecommend caps how many songs one call asks for.
const maxRecommend = 100

// SimilarToTrack implements provider.Recommender with getSimilarSongs.
func (s *session) SimilarToTrack(ctx context.Context, trackID string, limit int) ([]provider.Track, error) {
	r, err := s.api.call(ctx, "getSimilarSongs", url.Values{"id": {trackID}, "count": {count(limit + 1)}})
	if err != nil {
		return nil, err
	}
	out := s.songs(r.SimilarSongs, trackID, limit)
	if len(out) == 0 {
		// Navidrome answers an unknown ID with no songs. Tell the two apart.
		if _, err := s.song(ctx, trackID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SimilarToArtist implements provider.Recommender with getSimilarSongs2.
func (s *session) SimilarToArtist(ctx context.Context, artistID string, limit int) ([]provider.Track, error) {
	r, err := s.api.call(ctx, "getSimilarSongs2", url.Values{"id": {artistID}, "count": {count(limit)}})
	if err != nil {
		return nil, err
	}
	return s.songs(r.SimilarSongs2, "", limit), nil
}

// TopTracks implements provider.Recommender with getTopSongs. An artist
// the agents don't know has no top songs.
func (s *session) TopTracks(ctx context.Context, artist string, limit int) ([]provider.Track, error) {
	r, err := s.api.call(ctx, "getTopSongs", url.Values{"artist": {artist}, "count": {count(limit)}})
	if errors.Is(err, provider.ErrNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return s.songs(r.TopSongs, "", limit), nil
}

// RandomTracks implements provider.Recommender with getRandomSongs.
func (s *session) RandomTracks(ctx context.Context, limit int) ([]provider.Track, error) {
	r, err := s.api.call(ctx, "getRandomSongs", url.Values{"size": {count(limit)}})
	if err != nil {
		return nil, err
	}
	return s.songs(r.RandomSongs, "", limit), nil
}

// songs converts a song list, leaving out the song skip.
func (s *session) songs(list *songList, skip string, limit int) []provider.Track {
	if list == nil {
		return nil
	}
	out := make([]provider.Track, 0, min(len(list.Song), max(limit, 0)))
	for _, so := range list.Song {
		if len(out) == limit {
			break
		}
		if so.ID != "" && so.ID != skip {
			out = append(out, s.track(so))
		}
	}
	return out
}

func count(limit int) string { return strconv.Itoa(min(max(limit, 1), maxRecommend)) }
