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

// maxReleases is how many recordings' releases RecordingArt remembers.
const maxReleases = 2000

// RecordingArt returns the front cover of a release a recording came out
// on, for a song nobody queued: the other half of a sample, say. Finding
// the release is one request at the rate limit, remembered after.
func (s *Service) RecordingArt(ctx context.Context, recordingMBID string, size int) (artcache.Image, error) {
	s.mu.Lock()
	ids, ok := s.releases[recordingMBID]
	s.mu.Unlock()
	if !ok {
		var r struct {
			Releases []struct {
				ID           string `json:"id"`
				ReleaseGroup struct {
					ID string `json:"id"`
				} `json:"release-group"`
			} `json:"releases"`
		}
		err := s.mb.get(ctx, "recording/"+url.PathEscape(recordingMBID), url.Values{"inc": {"releases+release-groups"}}, &r)
		if err != nil && !errors.Is(err, errNotFound) {
			return artcache.Image{}, err
		}
		if len(r.Releases) > 0 {
			ids = IDs{Recording: recordingMBID, Release: r.Releases[0].ID, ReleaseGroup: r.Releases[0].ReleaseGroup.ID}
		}
		s.mu.Lock()
		if s.releases == nil || len(s.releases) >= maxReleases {
			s.releases = map[string]IDs{}
		}
		s.releases[recordingMBID] = ids
		s.mu.Unlock()
	}
	if ids.Release == "" {
		return artcache.Image{}, fmt.Errorf("cover art for recording %s: %w", recordingMBID, provider.ErrNotFound)
	}
	return s.caa.front(ctx, ids, size)
}
