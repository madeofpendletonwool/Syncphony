// SPDX-License-Identifier: AGPL-3.0-only

package fake

import (
	"context"
	"fmt"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// The fake's taste is simple: the artists are in a ring, and each one is
// most like the next. A track is most like the rest of its artist's songs.

// SimilarToTrack implements provider.Recommender: the rest of the track's
// artist's songs, then the next artist's.
func (s *session) SimilarToTrack(ctx context.Context, trackID string, limit int) ([]provider.Track, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	t, ok := get[*track](library, trackID)
	if !ok {
		return nil, fmt.Errorf("fake: track %q: %w", trackID, provider.ErrNotFound)
	}
	var out []*track
	for _, al := range t.album.artist.albums {
		for _, o := range al.tracks {
			if o != t {
				out = append(out, o)
			}
		}
	}
	out = append(out, songsBy(nextArtist(t.album.artist))...)
	return s.tracks(out, limit), nil
}

// SimilarToArtist implements provider.Recommender: the other artists'
// songs, going round the ring.
func (s *session) SimilarToArtist(ctx context.Context, artistID string, limit int) ([]provider.Track, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	a, ok := get[*artist](library, artistID)
	if !ok {
		return nil, fmt.Errorf("fake: artist %q: %w", artistID, provider.ErrNotFound)
	}
	var out []*track
	for o := nextArtist(a); o != a; o = nextArtist(o) {
		out = append(out, songsBy(o)...)
	}
	return s.tracks(out, limit), nil
}

// TopTracks implements provider.Recommender: the first song of each of the
// artist's albums. An artist the fake doesn't have has no top tracks.
func (s *session) TopTracks(ctx context.Context, name string, limit int) ([]provider.Track, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	var out []*track
	for _, a := range library.artists {
		if strings.EqualFold(a.name, strings.TrimSpace(name)) {
			for _, al := range a.albums {
				out = append(out, al.tracks[0])
			}
		}
	}
	return s.tracks(out, limit), nil
}

// RandomTracks implements provider.Recommender. It's the same "random"
// order every time, so tests can rely on it.
func (s *session) RandomTracks(ctx context.Context, limit int) ([]provider.Track, error) {
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	n := len(library.tracks)
	out := make([]*track, n)
	for i := range out {
		out[i] = library.tracks[i*7%n]
	}
	return s.tracks(out, limit), nil
}

func nextArtist(a *artist) *artist {
	for i, o := range library.artists {
		if o == a {
			return library.artists[(i+1)%len(library.artists)]
		}
	}
	return a
}

func songsBy(a *artist) []*track {
	var out []*track
	for _, al := range a.albums {
		out = append(out, al.tracks...)
	}
	return out
}

func (s *session) tracks(ts []*track, limit int) []provider.Track {
	if limit > 0 {
		ts = ts[:min(len(ts), limit)]
	}
	out := make([]provider.Track, len(ts))
	for i, t := range ts {
		out[i] = s.track(t)
	}
	return out
}
