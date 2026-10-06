// SPDX-License-Identifier: AGPL-3.0-only

package musicbrainz

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// minSearchScore is the lowest MusicBrainz search score worth checking.
const minSearchScore = 80

// How a track was resolved.
const (
	MethodMBID   = "mbid"
	MethodISRC   = "isrc"
	MethodSearch = "search"
)

// IDs are a track's MusicBrainz IDs.
type IDs struct {
	Recording    string
	Release      string
	ReleaseGroup string
	Artist       string
	// Method says how they were found: MethodMBID, MethodISRC or MethodSearch.
	Method string
}

// resolve finds t on MusicBrainz: by the recording MBID the provider gave,
// then by ISRC, then by searching. It returns errNotFound if nothing fits.
func (c *client) resolve(ctx context.Context, t provider.Track) (IDs, error) {
	if t.MBID != "" {
		r, err := c.recording(ctx, t.MBID)
		switch {
		case err == nil:
			return ids(r, t, MethodMBID), nil
		case !errors.Is(err, errNotFound):
			return IDs{}, err
		}
	}
	if t.ISRC != "" {
		rs, err := c.isrc(ctx, t.ISRC)
		switch {
		case err == nil:
			// An ISRC is one recording, but MusicBrainz sometimes has it on
			// several (a mistake, or a duplicate). Take the one that fits.
			want := t
			want.ISRC = ""
			if r, ok := best(want, rs, 0); ok {
				return ids(r, t, MethodISRC), nil
			}
			if len(rs) == 1 {
				return ids(rs[0], t, MethodISRC), nil
			}
		case !errors.Is(err, errNotFound):
			return IDs{}, err
		}
	}
	if t.Title == "" || len(t.Artists) == 0 {
		return IDs{}, errNotFound
	}
	rs, err := c.search(ctx, t.Title, t.Artists[0].Name)
	if err != nil {
		return IDs{}, err
	}
	if r, ok := best(t, rs, minSearchScore); ok {
		return ids(r, t, MethodSearch), nil
	}
	return IDs{}, errNotFound
}

// best returns the recording that matches want best, by the same rules
// cross-service matching uses. Search results under minScore are skipped.
func best(want provider.Track, rs []recording, minScore int) (recording, bool) {
	var top recording
	topScore := 0.0
	for _, r := range rs {
		if r.Score < minScore {
			continue
		}
		if s, ok := match.Score(want, asTrack(r)); ok && s > topScore {
			top, topScore = r, s
		}
	}
	return top, topScore > 0
}

func asTrack(r recording) provider.Track {
	t := provider.Track{Title: r.Title, Duration: time.Duration(r.Length) * time.Millisecond}
	for _, ac := range r.ArtistCredit {
		t.Artists = append(t.Artists, provider.ArtistCredit{Name: cmp.Or(ac.Name, ac.Artist.Name)})
	}
	return t
}

func ids(r recording, t provider.Track, method string) IDs {
	out := IDs{Recording: r.ID, Method: method}
	if len(r.ArtistCredit) > 0 {
		out.Artist = r.ArtistCredit[0].Artist.ID
	}
	if rel, ok := pickRelease(r.Releases, t.Album.Title); ok {
		out.Release, out.ReleaseGroup = rel.ID, rel.ReleaseGroup.ID
	}
	return out
}

// pickRelease chooses the release a track most likely came from: the one
// named like its album, else an official album, else the earliest.
func pickRelease(rs []release, album string) (release, bool) {
	if len(rs) == 0 {
		return release{}, false
	}
	album = normalize(album)
	rank := func(r release) int {
		n := 0
		if album != "" && normalize(r.Title) == album {
			n += 8
		}
		if r.Status == "Official" {
			n += 4
		}
		if r.ReleaseGroup.PrimaryType == "Album" || r.ReleaseGroup.PrimaryType == "Single" || r.ReleaseGroup.PrimaryType == "EP" {
			n += 2
		}
		// Compilations, live albums and the like.
		if len(r.ReleaseGroup.SecondaryTypes) == 0 {
			n++
		}
		return n
	}
	return slices.MinFunc(rs, func(a, b release) int {
		if c := cmp.Compare(rank(b), rank(a)); c != 0 {
			return c
		}
		// Earliest first; undated last.
		switch {
		case a.Date == "" && b.Date != "":
			return 1
		case b.Date == "" && a.Date != "":
			return -1
		}
		return cmp.Compare(a.Date, b.Date)
	}), true
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
