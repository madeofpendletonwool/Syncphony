// SPDX-License-Identifier: AGPL-3.0-only

package musicbrainz_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

func TestFindArtist(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.route(`/ws/2/artist?artist:"the square roots"`, "artist-search.json")
	// The best scored result has another name; the one with this name,
	// spelled any way, is the answer.
	id, err := e.svc.FindArtist(t.Context(), "the square roots")
	if err != nil || id != artistID {
		t.Fatalf("FindArtist = %q, %v, want %s", id, err, artistID)
	}
	if _, err := e.svc.FindArtist(t.Context(), "Nobody"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("unknown artist: %v, want not found", err)
	}
}

func TestArtistTags(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.route("/ws/2/artist/"+artistID, "artist-tags.json")
	tags, err := e.svc.ArtistTags(t.Context(), artistID)
	if err != nil {
		t.Fatal(err)
	}
	want := []musicbrainz.Tag{
		{Name: "art rock", Count: 9, Genre: true},
		{Name: "alternative rock", Count: 4, Genre: true},
		{Name: "british", Count: 2},
	}
	if !slices.Equal(tags, want) {
		t.Errorf("tags = %+v, want %+v", tags, want)
	}
	if got := e.mb.last().URL.Query().Get("inc"); got != "genres tags" && got != "genres+tags" {
		t.Errorf("inc = %q", got)
	}
	if _, err := e.svc.ArtistTags(t.Context(), "nope"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("unknown artist: %v, want not found", err)
	}
}

func TestFirstReleased(t *testing.T) {
	e := newEnv(t, -1)
	e.mb.route("/ws/2/recording/"+recordingID, "recording-first.json")
	date, err := e.svc.FirstReleased(t.Context(), recordingID)
	if err != nil || date != "2019-03" {
		t.Errorf("FirstReleased = %q, %v", date, err)
	}
	if _, err := e.svc.FirstReleased(t.Context(), "nope"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("unknown recording: %v, want not found", err)
	}
}
