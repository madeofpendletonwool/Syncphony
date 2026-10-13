// SPDX-License-Identifier: AGPL-3.0-only

package musicbrainz_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

func TestRecentReleaseGroups(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.route(`/ws/2/release-group?arid:"`+artistID+`" AND firstreleasedate:[2026-07-01 TO 2026-10-05]`, "recent-release-groups.json")
	rgs, err := e.svc.RecentReleaseGroups(t.Context(), artistID, "2026-07-01", "2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, rg := range rgs {
		got = append(got, rg.Title+" "+rg.FirstReleased+" "+rg.Kind())
	}
	if want := []string{"Highpass 2026-09-12 album", "Live at Leeds Again 2026-08 live"}; !slices.Equal(got, want) {
		t.Errorf("release groups = %q, want %q", got, want)
	}
	// Nothing lately is nothing, not an error.
	rgs, err = e.svc.RecentReleaseGroups(t.Context(), artistID, "2020-01-01", "2020-03-01")
	if err != nil || len(rgs) != 0 {
		t.Errorf("nothing lately: %v, %v", rgs, err)
	}
	if _, err := e.svc.RecentReleaseGroups(t.Context(), "", "2026-07-01", "2026-10-05"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("no MBID: %v", err)
	}
}

func TestReleaseArtOfAGroup(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.cover("/release-group/"+lowPassRG+"/front-250", 250)
	img, err := e.svc.ReleaseArt(t.Context(), "", lowPassRG, 200)
	if err != nil {
		t.Fatal(err)
	}
	if img.ContentType != "image/png" || len(img.Data) == 0 {
		t.Errorf("image: %s, %d bytes", img.ContentType, len(img.Data))
	}
	// Only the group was asked for: there's no release to ask about.
	for _, r := range e.mb.requests {
		if r.URL.Path == "/release//front-250" {
			t.Error("asked for a release with no MBID")
		}
	}
	if _, err := e.svc.ReleaseArt(t.Context(), "", "", 250); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("no release: %v", err)
	}
}
