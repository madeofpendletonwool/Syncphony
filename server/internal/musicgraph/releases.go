// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// What the Fresh feed (MAD-822, ADR 0017) asks Deezer and ListenBrainz:
// artists' releases, and what came out lately.

// maxAlbums is how many of an artist's releases are read from Deezer:
// it lists albums first, then EPs and singles, newest first within each.
const maxAlbums = 100

// DeezerAlbum is a release on Deezer: an album, EP or single.
type DeezerAlbum struct {
	ID    int64
	Title string
	// Date is when it came out, YYYY-MM-DD.
	Date string
	// Type is Deezer's record type: album, ep, single or compile.
	Type string
	// Cover is its cover's image ID.
	Cover string
}

type dzAlbum struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	ReleaseDate string `json:"release_date"`
	RecordType  string `json:"record_type"`
	MD5Image    string `json:"md5_image"`
	CoverMedium string `json:"cover_medium"`
	Artist      struct {
		Name string `json:"name"`
	} `json:"artist"`
}

func (a dzAlbum) public() DeezerAlbum {
	return DeezerAlbum{ID: a.ID, Title: a.Title, Date: a.ReleaseDate, Type: a.RecordType, Cover: cmp.Or(a.MD5Image, imageID(a.CoverMedium))}
}

// ArtistAlbums lists an artist's releases on Deezer, found by name: two
// requests. It's provider.ErrNotFound if Deezer doesn't know them.
func (d *Deezer) ArtistAlbums(ctx context.Context, name string) ([]DeezerAlbum, error) {
	if name == "" {
		return nil, fmt.Errorf("deezer: an artist with no name: %w", provider.ErrNotFound)
	}
	a, err := d.findArtist(ctx, name)
	if err != nil {
		return nil, err
	}
	var found struct {
		Data []dzAlbum `json:"data"`
	}
	if err := d.get(ctx, "/artist/"+strconv.FormatInt(a.ID, 10)+"/albums", url.Values{"limit": {strconv.Itoa(maxAlbums)}}, &found); err != nil {
		return nil, err
	}
	out := make([]DeezerAlbum, 0, len(found.Data))
	for _, al := range found.Data {
		if al.ID != 0 && al.Title != "" {
			out = append(out, al.public())
		}
	}
	return out, nil
}

// FindAlbum finds a release on Deezer by its artist and title, matched
// the way cross-service matching matches titles. It's
// provider.ErrNotFound if nothing matches.
func (d *Deezer) FindAlbum(ctx context.Context, artist, title string) (DeezerAlbum, error) {
	want, variants := match.Title(title)
	if want == "" || artist == "" {
		return DeezerAlbum{}, fmt.Errorf("deezer: a release with no title or artist: %w", provider.ErrNotFound)
	}
	var found struct {
		Data []dzAlbum `json:"data"`
	}
	q := fmt.Sprintf("artist:%q album:%q", artist, title)
	if err := d.get(ctx, "/search/album", url.Values{"q": {q}, "limit": {"10"}}, &found); err != nil {
		return DeezerAlbum{}, err
	}
	who := match.Simplify(artist)
	for _, al := range found.Data {
		// "Bleeds (Deluxe)" is "Bleeds", but "Bleeds (Live)" isn't.
		got, vs := match.Title(al.Title)
		if got == want && slices.Equal(vs, variants) && match.Simplify(al.Artist.Name) == who {
			return al.public(), nil
		}
	}
	return DeezerAlbum{}, fmt.Errorf("deezer: release %q by %q: %w", title, artist, provider.ErrNotFound)
}

// AlbumTracks lists a release's songs on Deezer, in order.
func (d *Deezer) AlbumTracks(ctx context.Context, id int64) ([]DeezerTrack, error) {
	if id <= 0 {
		return nil, fmt.Errorf("deezer: album %d: %w", id, provider.ErrNotFound)
	}
	var found struct {
		Data []dzTrack `json:"data"`
	}
	if err := d.get(ctx, "/album/"+strconv.FormatInt(id, 10)+"/tracks", url.Values{"limit": {"200"}}, &found); err != nil {
		return nil, err
	}
	out := make([]DeezerTrack, 0, len(found.Data))
	for _, t := range found.Data {
		if t.ID != 0 && t.Title != "" {
			out = append(out, t.public())
		}
	}
	return out, nil
}

// FreshRelease is a release that came out lately, as ListenBrainz lists
// them.
type FreshRelease struct {
	Title string
	// Artist is how the release credits its artists; ArtistMBIDs, who
	// they are.
	Artist      string
	ArtistMBIDs []string
	// MBID is its release group, Release the release, and CoverRelease
	// the release the Cover Art Archive has a cover for, if any.
	MBID, Release, CoverRelease string
	// Date is when it came out, YYYY-MM-DD.
	Date string
	// PrimaryType is "Album", "Single", "EP"...; SecondaryType refines
	// it ("Compilation", "Live").
	PrimaryType, SecondaryType string
}

// maxFresh bounds reading ListenBrainz's fresh releases: about 12 MB for
// 90 days.
const maxFresh = 64 << 20

// FreshReleases lists what came out in the last days (90 at most), on
// all of ListenBrainz: one big request, to be filtered by artist after.
func (l *ListenBrainz) FreshReleases(ctx context.Context, days int) ([]FreshRelease, error) {
	params := url.Values{"days": {strconv.Itoa(max(1, min(days, 90)))}, "past": {"true"}, "future": {"false"}, "sort": {"release_date"}}
	var out []FreshRelease
	err := l.c.getUpTo(ctx, l.base+"/1/explore/fresh-releases/", params, maxFresh, func(status int, body io.Reader) error {
		if status != http.StatusOK {
			return fmt.Errorf("listenbrainz: fresh releases: HTTP %d: %w", status, provider.ErrUnavailable)
		}
		var r struct {
			Payload struct {
				Releases []struct {
					Artist        string   `json:"artist_credit_name"`
					ArtistMBIDs   []string `json:"artist_mbids"`
					Title         string   `json:"release_name"`
					MBID          string   `json:"release_group_mbid"`
					Release       string   `json:"release_mbid"`
					CAAID         *int64   `json:"caa_id"`
					CAARelease    string   `json:"caa_release_mbid"`
					Date          string   `json:"release_date"`
					PrimaryType   string   `json:"release_group_primary_type"`
					SecondaryType string   `json:"release_group_secondary_type"`
				} `json:"releases"`
			} `json:"payload"`
		}
		if err := json.NewDecoder(body).Decode(&r); err != nil {
			return fmt.Errorf("listenbrainz: fresh releases: %w: %w", provider.ErrUnavailable, err)
		}
		out = make([]FreshRelease, 0, len(r.Payload.Releases))
		for _, x := range r.Payload.Releases {
			if x.Title == "" || x.Artist == "" {
				continue
			}
			f := FreshRelease{
				Title: x.Title, Artist: x.Artist, ArtistMBIDs: x.ArtistMBIDs, MBID: x.MBID, Release: x.Release,
				Date: x.Date, PrimaryType: x.PrimaryType, SecondaryType: x.SecondaryType,
			}
			if x.CAAID != nil {
				f.CoverRelease = x.CAARelease
			}
			out = append(out, f)
		}
		return nil
	})
	return out, err
}
