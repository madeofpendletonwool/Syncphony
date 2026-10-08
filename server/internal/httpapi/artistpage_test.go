// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

func trackTitles(ts []httpapi.TrackResult) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.Title)
	}
	return out
}

func TestArtistPage(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	link := linkFake(t, alice)
	base := "/links/" + link + "/artists/ar1"

	var about httpapi.ArtistAbout
	alice.want(http.StatusOK, "GET", base+"/about", nil).decode(t, &about)
	if about.Name != "The Test Patterns" || about.Bio == nil || !strings.Contains(*about.Bio, "sine waves") || about.Mbid == nil {
		t.Fatalf("about: %+v", about)
	}
	if len(about.Facts) != 1 || about.Facts[0].Text != "Formed in 2009" {
		t.Errorf("facts: %+v", about.Facts)
	}
	// The fake doesn't say what its albums are; MusicBrainz does.
	if about.AlbumKinds["al1"] != "album" || about.AlbumKinds["al2"] != "ep" {
		t.Errorf("album kinds: %v", about.AlbumKinds)
	}

	var tracks httpapi.ArtistTracks
	alice.want(http.StatusOK, "GET", base+"/tracks", nil).decode(t, &tracks)
	if got := trackTitles(tracks.Top); !slices.Equal(got, []string{"Reference Tone", "SMPTE"}) {
		t.Errorf("top: %v", got)
	}
	if len(tracks.AppearsOn) != 0 {
		t.Errorf("appears on: %v", trackTitles(tracks.AppearsOn))
	}

	var related httpapi.RelatedMusic
	alice.want(http.StatusOK, "GET", base+"/related", nil).decode(t, &related)
	if len(related.Artists) != 2 || related.Artists[0].Name != "Null Island" || related.Artists[0].Artist == nil || len(related.Tracks) != 4 {
		t.Errorf("related: %+v", related)
	}

	var shuffled []httpapi.TrackResult
	alice.want(http.StatusOK, "GET", base+"/shuffle?limit=4", nil).decode(t, &shuffled)
	if len(shuffled) != 4 {
		t.Errorf("shuffle: %v", trackTitles(shuffled))
	}

	var elsewhere []httpapi.ArtistElsewhere
	alice.want(http.StatusOK, "GET", base+"/elsewhere", nil).decode(t, &elsewhere)
	if len(elsewhere) != 0 {
		t.Errorf("elsewhere with one link: %+v", elsewhere)
	}

	var album httpapi.AlbumAbout
	alice.want(http.StatusOK, "GET", "/links/"+link+"/albums/al1/about", nil).decode(t, &album)
	if album.Kind == nil || *album.Kind != "album" || album.FirstReleased == nil || *album.FirstReleased != "2019-03-01" ||
		!slices.Equal(album.Labels, []string{"Test Card Records"}) || !slices.Equal(album.Genres, []string{"test tones"}) {
		t.Errorf("album: %+v", album)
	}
	if r := alice.do("GET", "/links/"+link+"/albums/al3/about", nil); r.status != http.StatusNotFound {
		t.Errorf("unknown album: %d %s", r.status, r.body)
	}
	// Without the music knowledge layer, there are no genre pages.
	if r := alice.do("GET", "/links/"+link+"/genre?name=test+tones", nil); r.status != http.StatusNotFound {
		t.Errorf("genre: %d %s", r.status, r.body)
	}
	if r := alice.do("GET", "/links/"+link+"/artists/nope/about", nil); r.status != http.StatusNotFound {
		t.Errorf("unknown artist: %d %s", r.status, r.body)
	}
}

func TestArtistElsewhere(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	mine := linkFake(t, alice)
	// Bob's shares his: Alice can use it too.
	theirs := linkFake(t, bob)
	bob.want(http.StatusOK, "PATCH", "/links/"+theirs, map[string]any{"shared": true})
	var found []httpapi.ArtistElsewhere
	alice.want(http.StatusOK, "GET", "/links/"+mine+"/artists/ar2/elsewhere", nil).decode(t, &found)
	if len(found) != 1 || found[0].LinkId != theirs || found[0].Artist.Name != "Null Island" {
		t.Errorf("elsewhere: %+v", found)
	}
}

func TestRoomArtistPlays(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	link := linkFake(t, alice)
	room := e.room(t, me(t, alice).Id)
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", "/rooms/"+room.ID+"/queue", addReq(link, "t01", "t04", "t07")).decode(t, &snap)
	start := e.clock()
	for i, it := range snap.Items {
		e.play(room.ID, it.Id, start.Add(time.Duration(i)*time.Minute))
	}
	var tops []httpapi.TrackCount
	alice.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/artist-plays?name="+url.QueryEscape("the test patterns"), nil).decode(t, &tops)
	var got []string
	for _, tc := range tops {
		got = append(got, tc.Item.Track.Title)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"Reference Tone", "SMPTE"}) {
		t.Errorf("plays: %v", got)
	}
}
