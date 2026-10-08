// SPDX-License-Identifier: AGPL-3.0-only

package musicbrainz

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// What artist and album pages ask MusicBrainz: who an artist is, who's in
// the band, their genres and their releases; and where an album came out.

// Profile is what MusicBrainz knows about an artist, for their page.
type Profile struct {
	Artist
	// Members are a group's members, current first.
	Members []Member
	// Genres are MusicBrainz's curated genres, most applied first.
	Genres []string
	// ReleaseGroups are their albums, singles and EPs, oldest first.
	ReleaseGroups []ReleaseGroup
}

// Member is someone in a group.
type Member struct {
	Name string
	MBID string
	// Begin and End are when they were in it, as precise as known.
	Begin, End string
	Ended      bool
	// Roles are what they played or sang ("guitar", "lead vocals").
	Roles []string
}

// ReleaseGroup is an album, single or EP, across its editions.
type ReleaseGroup struct {
	MBID  string
	Title string
	// PrimaryType is "Album", "Single", "EP", "Broadcast" or "Other";
	// SecondaryTypes refine it: "Compilation", "Live", "Soundtrack"...
	PrimaryType    string
	SecondaryTypes []string
	// FirstReleased is YYYY, YYYY-MM or YYYY-MM-DD, or empty.
	FirstReleased string
}

// Kind is what a page calls it: "album", "single", "ep", "compilation" or
// "live".
func (rg ReleaseGroup) Kind() string { return Kind(rg.PrimaryType, rg.SecondaryTypes) }

// Kind names a release from its MusicBrainz types. Compilations and live
// albums are told apart from studio albums, whatever their length.
func Kind(primary string, secondary []string) string {
	for _, s := range secondary {
		switch strings.ToLower(s) {
		case "compilation":
			return "compilation"
		case "live":
			return "live"
		}
	}
	switch strings.ToLower(primary) {
	case "single":
		return "single"
	case "ep":
		return "ep"
	}
	return "album"
}

// maxReleaseGroups is how many of an artist's release groups are read:
// one page.
const maxReleaseGroups = 100

// Profile looks up an artist: two requests, at the rate limit. The
// release groups are left out if they can't be read.
func (s *Service) Profile(ctx context.Context, mbid string) (Profile, error) {
	var a profileDetails
	err := s.mb.get(ctx, "artist/"+url.PathEscape(mbid), url.Values{"inc": {"url-rels+artist-rels+genres"}}, &a)
	if errors.Is(err, errNotFound) {
		return Profile{}, fmt.Errorf("musicbrainz artist %s: %w", mbid, provider.ErrNotFound)
	}
	if err != nil {
		return Profile{}, err
	}
	p := a.profile()

	var rgs struct {
		ReleaseGroups []releaseGroup `json:"release-groups"`
	}
	switch err := s.mb.get(ctx, "release-group", url.Values{"artist": {mbid}, "limit": {fmt.Sprint(maxReleaseGroups)}}, &rgs); {
	case err == nil:
		for _, rg := range rgs.ReleaseGroups {
			p.ReleaseGroups = append(p.ReleaseGroups, rg.group())
		}
		slices.SortStableFunc(p.ReleaseGroups, func(a, b ReleaseGroup) int {
			return cmp.Compare(a.FirstReleased, b.FirstReleased)
		})
	case !errors.Is(err, errNotFound):
		return p, err
	}
	return p, nil
}

// Album is what MusicBrainz knows about an album, across its editions.
type Album struct {
	ReleaseGroup
	// Labels it came out on, the first edition's first.
	Labels []string
	Genres []string
	// WikidataID is the album's Wikidata item, and WikipediaURL a
	// Wikipedia article MusicBrainz links directly.
	WikidataID   string
	WikipediaURL string
}

// minAlbumScore is the least search score FindAlbum trusts.
const minAlbumScore = 85

// maxLabels caps an album's labels: reissues add more.
const maxLabels = 3

// FindAlbum finds an album by its artist and title: up to three requests,
// at the rate limit. Edition notes ("Deluxe Edition") don't count. It's
// provider.ErrNotFound if there's no such album.
func (s *Service) FindAlbum(ctx context.Context, artist, title string) (Album, error) {
	want, _ := match.Title(title)
	if want == "" || strings.TrimSpace(artist) == "" {
		return Album{}, fmt.Errorf("musicbrainz album %q: %w", title, provider.ErrNotFound)
	}
	var r struct {
		ReleaseGroups []struct {
			releaseGroup
			Score        int            `json:"score"`
			ArtistCredit []artistCredit `json:"artist-credit"`
		} `json:"release-groups"`
	}
	q := "releasegroup:" + quote(searchTitleOf(title)) + " AND artist:" + quote(artist)
	err := s.mb.get(ctx, "release-group", url.Values{"query": {q}, "limit": {"5"}}, &r)
	if err != nil && !errors.Is(err, errNotFound) {
		return Album{}, err
	}
	id := ""
	for _, rg := range r.ReleaseGroups {
		if t, _ := match.Title(rg.Title); rg.Score >= minAlbumScore && t == want &&
			match.Simplify(creditName(rg.ArtistCredit)) == match.Simplify(artist) {
			id = rg.ID
			break
		}
	}
	if id == "" {
		return Album{}, fmt.Errorf("musicbrainz album %q by %q: %w", title, artist, provider.ErrNotFound)
	}

	var rg struct {
		releaseGroup
		Genres    []genre    `json:"genres"`
		Relations []relation `json:"relations"`
	}
	err = s.mb.get(ctx, "release-group/"+url.PathEscape(id), url.Values{"inc": {"url-rels+genres"}}, &rg)
	if errors.Is(err, errNotFound) {
		return Album{}, fmt.Errorf("musicbrainz release group %s: %w", id, provider.ErrNotFound)
	}
	if err != nil {
		return Album{}, err
	}
	out := Album{ReleaseGroup: rg.group(), Genres: genreNames(rg.Genres)}
	out.WikidataID, out.WikipediaURL = links(rg.Relations)

	var rels struct {
		Releases []releaseDetails `json:"releases"`
	}
	switch err := s.mb.get(ctx, "release", url.Values{"release-group": {id}, "inc": {"labels"}, "limit": {"25"}}, &rels); {
	case err == nil:
		slices.SortStableFunc(rels.Releases, func(a, b releaseDetails) int { return cmp.Compare(dateKey(a.Date), dateKey(b.Date)) })
		for _, r := range rels.Releases {
			for _, l := range r.release().Labels {
				if len(out.Labels) < maxLabels && !slices.Contains(out.Labels, l) {
					out.Labels = append(out.Labels, l)
				}
			}
		}
	case !errors.Is(err, errNotFound):
		return out, err
	}
	return out, nil
}

// searchTitleOf is an album title without its edition notes, to search for.
func searchTitleOf(title string) string {
	if i := strings.IndexAny(title, "(["); i > 0 {
		return strings.TrimSpace(title[:i])
	}
	return title
}

// dateKey sorts unknown dates last.
func dateKey(date string) string {
	if date == "" {
		return "9999"
	}
	return date
}

// links finds the Wikidata item and Wikipedia article among relations.
func links(rels []relation) (wikidataID, wikipediaURL string) {
	for _, r := range rels {
		if r.URL == nil {
			continue
		}
		switch r.Type {
		case "wikidata":
			if i := strings.LastIndex(r.URL.Resource, "/"); i >= 0 && wikidataID == "" {
				wikidataID = r.URL.Resource[i+1:]
			}
		case "wikipedia":
			if wikipediaURL == "" {
				wikipediaURL = r.URL.Resource
			}
		}
	}
	return wikidataID, wikipediaURL
}

// The JSON shapes Profile and FindAlbum read.

type genre struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// genreNames are genres' names, most applied first.
func genreNames(gs []genre) []string {
	gs = slices.Clone(gs)
	slices.SortStableFunc(gs, func(a, b genre) int { return cmp.Compare(b.Count, a.Count) })
	var out []string
	for _, g := range gs {
		if g.Count > 0 {
			out = append(out, g.Name)
		}
	}
	return out
}

type releaseGroup struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	PrimaryType    string   `json:"primary-type"`
	SecondaryTypes []string `json:"secondary-types"`
	FirstReleased  string   `json:"first-release-date"`
}

func (rg releaseGroup) group() ReleaseGroup {
	return ReleaseGroup{
		MBID: rg.ID, Title: rg.Title, PrimaryType: rg.PrimaryType,
		SecondaryTypes: rg.SecondaryTypes, FirstReleased: rg.FirstReleased,
	}
}

type profileDetails struct {
	artistDetails
	Genres []genre `json:"genres"`
}

// memberOf is the relationship between a group and its members. Seen
// from the group, it points backward.
const memberOf = "member of band"

func (a profileDetails) profile() Profile {
	p := Profile{Artist: *a.artist(), Genres: genreNames(a.Genres)}
	seen := map[string]int{}
	for _, r := range a.Relations {
		if r.Type != memberOf || r.Direction != "backward" || r.Artist == nil {
			continue
		}
		// Someone who left and came back is listed once, as they are now.
		m := Member{
			Name: cmp.Or(r.TargetCredit, r.Artist.Name), MBID: r.Artist.ID,
			Begin: r.Begin, End: r.End, Ended: r.Ended, Roles: roles(r.Attributes),
		}
		if i, ok := seen[m.MBID]; ok {
			if !m.Ended || p.Members[i].Ended {
				m.Begin = earliestDate(p.Members[i].Begin, m.Begin)
				p.Members[i] = m
			}
			continue
		}
		seen[m.MBID] = len(p.Members)
		p.Members = append(p.Members, m)
	}
	slices.SortStableFunc(p.Members, func(a, b Member) int {
		if a.Ended != b.Ended {
			if a.Ended {
				return 1
			}
			return -1
		}
		return cmp.Compare(dateKey(a.Begin), dateKey(b.Begin))
	})
	return p
}

// roles are a membership's attributes that say what someone did:
// instruments and vocals, not "original" or "founder".
func roles(attrs []string) []string {
	var out []string
	for _, a := range attrs {
		switch a {
		case "original", "founder", "additional", "guest", "minor":
		default:
			out = append(out, a)
		}
	}
	return out
}

// earliestDate of two MusicBrainz dates; empty ones don't count.
func earliestDate(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	case b < a:
		return b
	}
	return a
}
