// SPDX-License-Identifier: AGPL-3.0-only

package musicbrainz

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/madeofpendletonwool/syncphony/server/internal/artcache"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// What the Fresh feed (MAD-822, ADR 0017) asks MusicBrainz: an artist's
// latest release groups, and their covers.

// maxRecent is how many of an artist's recent release groups are read.
const maxRecent = 50

// RecentReleaseGroups returns the release groups an artist is credited on
// that first came out from one date to another (YYYY-MM-DD): one search,
// at the rate limit.
func (s *Service) RecentReleaseGroups(ctx context.Context, artistMBID, from, to string) ([]ReleaseGroup, error) {
	if artistMBID == "" {
		return nil, fmt.Errorf("musicbrainz: an artist with no MBID: %w", provider.ErrNotFound)
	}
	q := fmt.Sprintf("arid:%s AND firstreleasedate:[%s TO %s]", quote(artistMBID), from, to)
	var r struct {
		ReleaseGroups []releaseGroup `json:"release-groups"`
	}
	err := s.mb.get(ctx, "release-group", url.Values{"query": {q}, "limit": {fmt.Sprint(maxRecent)}}, &r)
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]ReleaseGroup, 0, len(r.ReleaseGroups))
	for _, rg := range r.ReleaseGroups {
		out = append(out, rg.group())
	}
	return out, nil
}

// ReleaseArt returns a release's front cover, else its release group's.
// Either may be empty.
func (s *Service) ReleaseArt(ctx context.Context, release, group string, size int) (artcache.Image, error) {
	if release == "" && group == "" {
		return artcache.Image{}, fmt.Errorf("cover art for no release: %w", provider.ErrNotFound)
	}
	return s.caa.front(ctx, IDs{Release: release, ReleaseGroup: group}, size)
}
