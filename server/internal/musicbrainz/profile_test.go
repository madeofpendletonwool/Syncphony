// SPDX-License-Identifier: AGPL-3.0-only

package musicbrainz_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

func TestProfile(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.route("/ws/2/artist/"+artistID, "artist-profile.json")
	e.mb.route("/ws/2/release-group", "release-groups.json")
	p, err := e.svc.Profile(t.Context(), artistID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "The Square Roots" || p.BeginArea != "Leeds" || p.WikidataID != "Q42" || !p.Ended {
		t.Errorf("artist: %+v", p.Artist)
	}
	if !slices.Equal(p.Genres, []string{"art rock", "alternative rock"}) {
		t.Errorf("genres = %v", p.Genres)
	}
	var members []string
	for _, m := range p.Members {
		members = append(members, fmt.Sprintf("%s %s–%s %v [%s]", m.Name, m.Begin, m.End, m.Ended, strings.Join(m.Roles, ",")))
	}
	// Current members first; someone who left and came back once, for
	// their whole time; only members, of this band.
	want := []string{
		"Bo 1996– false [drums (drum set)]",
		"Ann Root 1994–2012 true [guitar,lead vocals]",
		"Cy Former 1994–1995 true [bass guitar]",
	}
	if !slices.Equal(members, want) {
		t.Errorf("members:\n got %q\nwant %q", members, want)
	}
	var groups []string
	for _, rg := range p.ReleaseGroups {
		groups = append(groups, rg.Title+"="+rg.Kind())
	}
	if want := []string{"Rolloff=single", "Low Pass=album", "Live at Leeds=live", "Roots: The Best Of=compilation"}; !slices.Equal(groups, want) {
		t.Errorf("release groups = %v, want %v", groups, want)
	}
	if got := e.mb.last().URL.Query().Get("artist"); got != artistID {
		t.Errorf("release groups browsed for %q", got)
	}

	if _, err := e.svc.Profile(t.Context(), "nope"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("unknown artist: %v, want not found", err)
	}
}

func TestProfileWithoutReleaseGroups(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.route("/ws/2/artist/"+artistID, "artist-profile.json")
	p, err := e.svc.Profile(t.Context(), artistID)
	if err != nil || p.Name == "" || len(p.ReleaseGroups) != 0 {
		t.Fatalf("Profile = %+v, %v", p, err)
	}
}

func TestFindAlbum(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.route(`/ws/2/release-group?releasegroup:"Low Pass" AND artist:"The Square Roots"`, "release-group-search.json")
	e.mb.route("/ws/2/release-group/rg1", "release-group.json")
	e.mb.route("/ws/2/release", "releases-labels.json")
	// The edition doesn't count, and the best scored result is someone
	// else's.
	a, err := e.svc.FindAlbum(t.Context(), "The Square Roots", "Low Pass (Deluxe Edition)")
	if err != nil {
		t.Fatal(err)
	}
	if a.MBID != "rg1" || a.Kind() != "album" || a.FirstReleased != "1998-03-02" {
		t.Errorf("album: %+v", a.ReleaseGroup)
	}
	if a.WikipediaURL != "https://en.wikipedia.org/wiki/Low_Pass_(album)" {
		t.Errorf("wikipedia = %q", a.WikipediaURL)
	}
	if !slices.Equal(a.Genres, []string{"art rock", "trip hop"}) {
		t.Errorf("genres = %v", a.Genres)
	}
	// The first edition's labels first; reissues' after, to three.
	if want := []string{"Rootstock", "Square One", "Reissue Records"}; !slices.Equal(a.Labels, want) {
		t.Errorf("labels = %v, want %v", a.Labels, want)
	}
	if got := e.mb.last().URL.Query().Get("release-group"); got != "rg1" {
		t.Errorf("releases browsed for %q", got)
	}

	if _, err := e.svc.FindAlbum(t.Context(), "The Square Roots", "Nothing Like It"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("unknown album: %v, want not found", err)
	}
}

func TestKind(t *testing.T) {
	for _, c := range []struct {
		primary   string
		secondary []string
		want      string
	}{
		{"Album", nil, "album"},
		{"Single", nil, "single"},
		{"EP", nil, "ep"},
		{"Album", []string{"Compilation"}, "compilation"},
		{"Album", []string{"Live"}, "live"},
		{"Album", []string{"Soundtrack"}, "album"},
		{"", nil, "album"},
	} {
		if got := musicbrainz.Kind(c.primary, c.secondary); got != c.want {
			t.Errorf("Kind(%q, %v) = %q, want %q", c.primary, c.secondary, got, c.want)
		}
	}
}
