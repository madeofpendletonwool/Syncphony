// SPDX-License-Identifier: AGPL-3.0-only

package musicbrainz

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// What the music knowledge layer (internal/musicgraph) asks MusicBrainz:
// an artist's ID from their name, their genres and tags, and when a
// recording first came out. Each is one request, at the rate limit.

// Tag is a genre or tag MusicBrainz editors put on an artist, with how
// many did.
type Tag struct {
	Name  string
	Count int
	// Genre is true for MusicBrainz's curated genres, false for free tags.
	Genre bool
}

// minArtistScore is the least search score FindArtist trusts.
const minArtistScore = 90

// FindArtist returns the MBID of the artist called name: the best search
// result whose name is the same, once simplified. It's
// provider.ErrNotFound if there's none.
func (s *Service) FindArtist(ctx context.Context, name string) (string, error) {
	var r struct {
		Artists []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Score int    `json:"score"`
		} `json:"artists"`
	}
	err := s.mb.get(ctx, "artist", url.Values{"query": {"artist:" + quote(name)}, "limit": {"5"}}, &r)
	if err != nil && !errors.Is(err, errNotFound) {
		return "", err
	}
	want := match.Simplify(name)
	for _, a := range r.Artists {
		if a.Score >= minArtistScore && match.Simplify(a.Name) == want {
			return a.ID, nil
		}
	}
	return "", fmt.Errorf("musicbrainz artist %q: %w", name, provider.ErrNotFound)
}

// ArtistTags returns an artist's genres and tags, most applied first.
// A tag that's also a genre is listed once, as the genre.
func (s *Service) ArtistTags(ctx context.Context, mbid string) ([]Tag, error) {
	type tag struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	var r struct {
		Genres []tag `json:"genres"`
		Tags   []tag `json:"tags"`
	}
	err := s.mb.get(ctx, "artist/"+url.PathEscape(mbid), url.Values{"inc": {"genres+tags"}}, &r)
	if errors.Is(err, errNotFound) {
		return nil, fmt.Errorf("musicbrainz artist %s: %w", mbid, provider.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var out []Tag
	genre := map[string]bool{}
	for _, g := range r.Genres {
		genre[g.Name] = true
		out = append(out, Tag{Name: g.Name, Count: g.Count, Genre: true})
	}
	for _, t := range r.Tags {
		if !genre[t.Name] && t.Count > 0 {
			out = append(out, Tag{Name: t.Name, Count: t.Count})
		}
	}
	slices.SortStableFunc(out, func(a, b Tag) int { return cmp.Compare(b.Count, a.Count) })
	return out, nil
}

// FirstReleased returns the date a recording first came out: YYYY,
// YYYY-MM or YYYY-MM-DD, or "" if MusicBrainz doesn't know.
func (s *Service) FirstReleased(ctx context.Context, recordingMBID string) (string, error) {
	var r struct {
		FirstReleaseDate string `json:"first-release-date"`
	}
	err := s.mb.get(ctx, "recording/"+url.PathEscape(recordingMBID), url.Values{}, &r)
	if errors.Is(err, errNotFound) {
		return "", fmt.Errorf("musicbrainz recording %s: %w", recordingMBID, provider.ErrNotFound)
	}
	return r.FirstReleaseDate, err
}
