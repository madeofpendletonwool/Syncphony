// SPDX-License-Identifier: AGPL-3.0-only

package navidrome

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

var (
	_ provider.Cataloger = (*session)(nil)
	_ provider.Starrer   = (*session)(nil)
)

// catalogPage is how many songs a page of the catalog holds: search3's
// most, so a big library takes few requests.
const catalogPage = 500

// CatalogSongs implements provider.Cataloger with search3: an empty query
// matches every song, and Navidrome pages through them in a stable order.
// It's cheaper on a big library than walking artists and albums, which
// takes a request per album.
func (s *session) CatalogSongs(ctx context.Context, cursor string) (provider.Page[provider.Track], error) {
	offset := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 0 {
			return provider.Page[provider.Track]{}, fmt.Errorf("navidrome: bad catalog cursor %q", cursor)
		}
		offset = n
	}
	r, err := s.api.call(ctx, "search3", url.Values{
		"query": {""}, "artistCount": {"0"}, "albumCount": {"0"},
		"songCount": {strconv.Itoa(catalogPage)}, "songOffset": {strconv.Itoa(offset)},
	})
	if err != nil {
		return provider.Page[provider.Track]{}, err
	}
	var page provider.Page[provider.Track]
	if r.SearchResult3 == nil {
		return page, nil
	}
	page.Items = make([]provider.Track, 0, len(r.SearchResult3.Song))
	for _, so := range r.SearchResult3.Song {
		page.Items = append(page.Items, s.track(so))
	}
	if len(r.SearchResult3.Song) >= catalogPage {
		page.Next = strconv.Itoa(offset + len(r.SearchResult3.Song))
	}
	return page, nil
}

// LibraryChanged implements provider.Cataloger with Navidrome's
// getScanStatus, whose lastScan other Subsonic servers don't send.
func (s *session) LibraryChanged(ctx context.Context) (time.Time, error) {
	r, err := s.api.call(ctx, "getScanStatus", nil)
	if err != nil || r.ScanStatus == nil || r.ScanStatus.LastScan == "" {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, r.ScanStatus.LastScan)
	if err != nil {
		return time.Time{}, nil //nolint:nilerr // a server that says something else just can't say
	}
	return t.UTC(), nil
}
