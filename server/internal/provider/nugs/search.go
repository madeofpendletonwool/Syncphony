// SPDX-License-Identifier: AGPL-3.0-only

package nugs

import (
	"cmp"
	"context"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// results is a search, ranked and split by kind.
type results struct {
	tracks  []hit
	albums  []provider.Album
	artists []provider.Artist
}

// hit is one performance of a song: a track on a show. Search results
// don't say how long it is, so it's resolved through the show before it's
// returned.
type hit struct{ show, track string }

// search runs catalog.search, the apps' search. It finds artists, songs,
// venues and albums by name, but not shows by date.
func (p *Provider) search(ctx context.Context, token, text string) (*results, error) {
	query := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if query == "" {
		return &results{}, nil
	}
	if r, ok := p.searches.get(query); ok {
		return r, nil
	}
	var resp searchResponse
	if err := p.legacy(ctx, token, "catalog.search", url.Values{"searchStr": {query}}, &resp); err != nil {
		return nil, err
	}
	r := rank(resp, query)
	p.searches.put(query, r)
	return r, nil
}

// rank orders search results. Groups that match the query exactly come
// first, then ones that start with it, then the rest: "tweezer" lists
// Tweezer before Tweezer Reprise. Within a group, the newest performance
// comes first. Albums come before venues' shows.
func rank(resp searchResponse, query string) *results {
	var groups []searchGroup
	for _, t := range resp.Types {
		groups = append(groups, t.Groups...)
	}
	slices.SortStableFunc(groups, func(a, b searchGroup) int {
		return cmp.Compare(matchRank(a.Matched, query), matchRank(b.Matched, query))
	})
	r := &results{}
	var venues []provider.Album
	seenTrack, seenAlbum, seenArtist := map[hit]bool{}, map[string]bool{}, map[string]bool{}
	for _, g := range groups {
		items := slices.Clone(g.Items)
		slices.SortStableFunc(items, func(a, b searchItem) int { return newestFirst(a.PerformanceDate, b.PerformanceDate) })
		switch g.MatchType {
		case matchSong:
			for _, it := range items {
				h := hit{show: it.ContainerID.id(), track: it.TrackID.id()}
				if h.show == "" || h.track == "" || seenTrack[h] {
					continue
				}
				seenTrack[h] = true
				r.tracks = append(r.tracks, h)
			}
		case matchArtist:
			// Every item is one of the artist's shows.
			for _, it := range items {
				id := it.ArtistID.id()
				if id == "" {
					continue
				}
				if !seenArtist[id] {
					seenArtist[id] = true
					r.artists = append(r.artists, provider.Artist{ID: id, Name: cmp.Or(strings.TrimSpace(it.ArtistName), strings.TrimSpace(g.Matched))})
				}
				break
			}
		case matchAlbum, matchVenue:
			for _, it := range items {
				id := it.ContainerID.id()
				if id == "" || seenAlbum[id] {
					continue
				}
				seenAlbum[id] = true
				al := itemAlbum(it)
				if g.MatchType == matchAlbum {
					r.albums = append(r.albums, al)
				} else {
					venues = append(venues, al)
				}
			}
		}
	}
	r.albums = append(r.albums, venues...)
	return r
}

// matchRank is how well a group's name matches the query: lower is better.
func matchRank(matched, query string) int {
	m := strings.ToLower(strings.Join(strings.Fields(matched), " "))
	switch {
	case m == query:
		return 0
	case strings.HasPrefix(m, query):
		return 1
	case strings.Contains(m, query):
		return 2
	}
	return 3
}

// newestFirst compares performance dates, newest first and undated last.
func newestFirst(a, b string) int {
	ta, oka := parseDate(a)
	tb, okb := parseDate(b)
	switch {
	case oka && okb:
		return tb.Compare(ta)
	case oka:
		return -1
	case okb:
		return 1
	}
	return 0
}

// dateLayouts are the ways nugs.net writes performance dates.
var dateLayouts = []string{"1/2/2006", "2006-01-02T15:04:05", "2006-01-02", "2006/01/02"}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// showTitle names a show by its date and venue, "2024-12-31 · Madison
// Square Garden, New York, NY", so the many nights a song was played can be
// told apart. Albums, and shows without a date, keep their own title.
func showTitle(title, date, venue, city, state string) string {
	d, ok := parseDate(date)
	var place []string
	for _, s := range []string{venue, city, state} {
		if s = strings.TrimSpace(s); s != "" {
			place = append(place, s)
		}
	}
	if !ok || len(place) == 0 {
		return strings.TrimSpace(title)
	}
	return d.Format("2006-01-02") + " · " + strings.Join(place, ", ")
}

// showYear is the year a show was played, or 0.
func showYear(date string, year flexString) int {
	if d, ok := parseDate(date); ok {
		return d.Year()
	}
	return year.int()
}

// itemAlbum is a search result's show, as an album.
func itemAlbum(it searchItem) provider.Album {
	return provider.Album{
		ID:      it.ContainerID.id(),
		Title:   showTitle(it.ContainerName, it.PerformanceDate, it.VenueName, it.VenueCity, it.VenueState),
		Artists: credits(it.ArtistID, it.ArtistName),
		Year:    showYear(it.PerformanceDate, ""),
		Artwork: artRef(it.Img),
	}
}

// toAlbum is a show, as an album.
func toAlbum(sh *show) provider.Album {
	return provider.Album{
		ID:         sh.ContainerID.id(),
		Title:      showTitle(sh.ContainerInfo, sh.PerformanceDate, sh.VenueName, sh.VenueCity, sh.VenueState),
		Artists:    credits(sh.ArtistID, sh.ArtistName),
		Year:       showYear(sh.PerformanceDate, sh.PerformanceYear),
		TrackCount: len(sh.setlist()),
		Artwork:    artRef(sh.Img),
	}
}

func credits(id flexString, name string) []provider.ArtistCredit {
	if name = strings.TrimSpace(name); name == "" {
		return nil
	}
	return []provider.ArtistCredit{{ID: id.id(), Name: name}}
}

// pageOf returns one page of all, and whether more follow.
func pageOf[T any](all []T, offset, limit int) ([]T, bool) {
	if offset >= len(all) {
		return nil, false
	}
	end := min(offset+limit, len(all))
	return all[offset:end], end < len(all)
}
