// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

// newLRCLIB is a stand-in LRCLIB that only knows "Left Channel".
func newLRCLIB(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/get" || r.URL.Query().Get("track_name") != "Left Channel" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"name":"TrackNotFound","message":"Failed to find specified track"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"trackName":"Left Channel","artistName":"Null Island","instrumental":false,` +
			`"plainLyrics":"Left\nRight","syncedLyrics":"[00:01.00] Left\n[00:02.50] Right"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLyricsAPI(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	link := linkFake(t, alice)

	// The fake provider has synced lyrics for every track.
	var ly httpapi.Lyrics
	alice.want(http.StatusOK, "GET", "/links/"+link+"/tracks/t01/lyrics", nil).decode(t, &ly)
	if ly.Source != "fake" || !ly.Synced || ly.Instrumental || len(ly.Lines) < 2 || ly.Lines[0].AtMs != 0 ||
		ly.Lines[1].AtMs != 5000 || ly.Lines[0].Text == "" || ly.Plain == "" {
		t.Fatalf("lyrics: %+v", ly)
	}
	if r := alice.do("GET", "/links/"+link+"/tracks/nope/lyrics", nil); r.status != http.StatusNotFound {
		t.Fatalf("missing track: %d %s", r.status, r.body)
	}
	// Someone else's link is invisible.
	if r := bob.do("GET", "/links/"+link+"/tracks/t01/lyrics", nil); r.status != http.StatusNotFound {
		t.Fatalf("bob reading alice's link: %d", r.status)
	}

	// Everyone in the room reads along, through alice's link.
	room := e.room(t, me(t, alice).Id)
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", "/rooms/"+room.ID+"/queue", addReq(link, "t01", "t02")).decode(t, &snap)
	items := map[string]string{}
	for _, it := range snap.Items {
		items[it.Track.Title] = it.Id
	}
	bob.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/queue/"+items["Reference Tone"]+"/lyrics", nil).decode(t, &ly)
	if ly.Source != "fake" || !ly.Synced {
		t.Fatalf("room lyrics: %+v", ly)
	}
	other := e.room(t, me(t, alice).Id)
	if r := bob.do("GET", "/rooms/"+other.ID+"/queue/"+items["Reference Tone"]+"/lyrics", nil); r.status != http.StatusNotFound {
		t.Fatalf("lyrics through the wrong room: %d", r.status)
	}

	// With alice's link gone, LRCLIB still knows the song from its metadata.
	alice.want(http.StatusNoContent, "DELETE", "/links/"+link, nil)
	bob.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/queue/"+items["Left Channel"]+"/lyrics", nil).decode(t, &ly)
	want := []httpapi.LyricLine{{AtMs: 1000, Text: "Left"}, {AtMs: 2500, Text: "Right"}}
	if ly.Source != "lrclib" || !ly.Synced || len(ly.Lines) != 2 || ly.Lines[0] != want[0] || ly.Lines[1] != want[1] || ly.Plain != "Left\nRight" {
		t.Fatalf("LRCLIB lyrics: %+v", ly)
	}
}
