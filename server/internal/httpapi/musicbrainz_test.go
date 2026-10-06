// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

const (
	refToneRecording = "cc000000-0000-4000-8000-0000000000f1"
	refToneArtist    = "0b000000-0000-4000-8000-0000000000f1"
	refToneRelease   = "aa000000-0000-4000-8000-0000000000f1"
	coverWidth       = 500
)

// newMusicBrainz stands in for MusicBrainz, which knows the fake
// provider's "Reference Tone" by its ISRC, and the Cover Art Archive.
func newMusicBrainz(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws/2/isrc/XXFAK0000001":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"isrc":"XXFAK0000001","recordings":[{"id":"` + refToneRecording + `","title":"Reference Tone","length":20000,` +
				`"artist-credit":[{"name":"The Test Patterns","artist":{"id":"` + refToneArtist + `","name":"The Test Patterns"}}],` +
				`"releases":[{"id":"` + refToneRelease + `","title":"Calibration","status":"Official","date":"2019",` +
				`"release-group":{"id":"bb000000-0000-4000-8000-0000000000f1","primary-type":"Album","secondary-types":[]}}]}]}`))
		case "/ws/2/recording/" + refToneRecording:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"title":"Reference Tone","first-release-date":"2019-03-01","relations":[` +
				`{"type":"producer","target-type":"artist","direction":"backward","attributes":[],"artist":{"id":"p1","name":"Dee Bee"}},` +
				`{"type":"instrument","target-type":"artist","direction":"backward","attributes":["theremin"],"artist":{"id":"p2","name":"Wave Form"}},` +
				`{"type":"performance","target-type":"work","direction":"forward","attributes":["cover"],"work":{"title":"Tone Poem","relations":[` +
				`{"type":"composer","target-type":"artist","direction":"backward","attributes":[],"artist":{"id":"w1","name":"Hertz"}}]}},` +
				`{"type":"samples material","target-type":"recording","direction":"backward","attributes":[],"recording":{"id":"r2","title":"Dial Tone",` +
				`"artist-credit":[{"name":"The Operators","joinphrase":"","artist":{"id":"a2","name":"The Operators"}}]}}]}`))
		case "/ws/2/release/" + refToneRelease:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"title":"Calibration","date":"2019","label-info":[{"label":{"name":"Test Card Records"}}],` +
				`"release-group":{"primary-type":"Album","first-release-date":"2019"}}`))
		case "/ws/2/artist/" + refToneArtist:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + refToneArtist + `","name":"The Test Patterns","type":"Group","area":{"name":"Bristol"},` +
				`"life-span":{"begin":"2009"},"relations":[{"type":"wikidata","url":{"resource":"https://www.wikidata.org/wiki/Q42424242"}}]}`))
		case "/w/api.php":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"entities":{"Q42424242":{"sitelinks":{"enwiki":{"title":"The Test Patterns"}}}}}`))
		case "/api/rest_v1/page/summary/The_Test_Patterns":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"type":"standard","extract":"The Test Patterns are a band that only plays sine waves.",` +
				`"content_urls":{"desktop":{"page":"https://en.wikipedia.org/wiki/The_Test_Patterns"}}}`))
		case "/release/" + refToneRelease + "/front-500":
			var b bytes.Buffer
			_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, coverWidth, coverWidth)))
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(b.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestQueueArtworkFromCoverArtArchive(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	link := linkFake(t, alice)
	room := e.room(t, me(t, alice).Id)
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", "/rooms/"+room.ID+"/queue", addReq(link, "t01")).decode(t, &snap)
	item := snap.Items[0].Id

	// Queuing it started the match in the background.
	ref := provider.TrackRef{Provider: "fake", ID: "t01"}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ids, ok, _ := e.mb.Lookup(t.Context(), ref); ok {
			if ids.Release != refToneRelease || ids.Method != "isrc" {
				t.Fatalf("IDs: %+v", ids)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never matched on MusicBrainz")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The fake's own artwork is an SVG, which is never too small, and has
	// no palette: the browser works it out from the image.
	art := bob.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/queue/"+item+"/artwork?size=400", nil)
	if ct := art.header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("own artwork: %s", ct)
	}
	if r := bob.do("GET", "/rooms/"+room.ID+"/queue/"+item+"/palette", nil); r.status != http.StatusNotFound {
		t.Fatalf("SVG palette: %d %s", r.status, r.body)
	}

	// With alice's link gone, the room still sees the cover.
	alice.want(http.StatusNoContent, "DELETE", "/links/"+link, nil)
	art = bob.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/queue/"+item+"/artwork?size=400", nil)
	cfg, err := png.DecodeConfig(bytes.NewReader(art.body))
	if err != nil || art.header.Get("Content-Type") != "image/png" || cfg.Width != coverWidth ||
		art.header.Get("Content-Security-Policy") == "" {
		t.Fatalf("cover art archive: %v %v %+v", art.header, err, cfg)
	}
	// The cover's colors: black, so no accent and a dark dominant color.
	var pal httpapi.Palette
	bob.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/queue/"+item+"/palette", nil).decode(t, &pal)
	if pal.Accent != nil || pal.Dominant.L > 0.05 || pal.Light.L != 0.92 {
		t.Fatalf("palette: %+v", pal)
	}
	// It's saved with the song, so the queue carries it.
	var q httpapi.QueueSnapshot
	bob.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/queue", nil).decode(t, &q)
	if p := q.Items[0].Palette; p == nil || *p != pal {
		t.Fatalf("queued palette: %+v", p)
	}

	// Without a cover in that size, still nothing.
	if r := bob.do("GET", "/rooms/"+room.ID+"/queue/"+item+"/artwork?size=100", nil); r.status != http.StatusNotFound {
		t.Fatalf("no small cover: %d", r.status)
	}
}

func TestLinerNotes(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	link := linkFake(t, alice)
	room := e.room(t, me(t, alice).Id)
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", "/rooms/"+room.ID+"/queue", addReq(link, "t01")).decode(t, &snap)

	var n httpapi.LinerNotes
	alice.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/queue/"+snap.Items[0].Id+"/liner-notes", nil).decode(t, &n)
	if n.RecordingMbid != refToneRecording || n.Year == nil || *n.Year != 2019 {
		t.Fatalf("notes: %+v", n)
	}
	if n.Release == nil || n.Release.Title != "Calibration" || len(n.Release.Labels) != 1 || n.Release.Labels[0] != "Test Card Records" {
		t.Fatalf("release: %+v", n.Release)
	}
	if a := n.Artist; a == nil || a.About == nil || *a.About != "Group from Bristol" || a.Bio == nil ||
		!strings.Contains(*a.Bio, "sine waves") || a.BioUrl == nil {
		t.Fatalf("artist: %+v", n.Artist)
	}
	roles := []string{}
	for _, c := range n.Credits {
		roles = append(roles, c.Role+": "+strings.Join(c.Names, ", "))
	}
	if got := strings.Join(roles, "; "); got != "Written by: Hertz; Produced by: Dee Bee; Theremin: Wave Form" {
		t.Fatalf("credits: %s", got)
	}
	facts := []string{}
	for _, f := range n.Facts {
		facts = append(facts, string(f.Kind)+": "+f.Text)
	}
	want := "cover: A cover of “Tone Poem”, written by Hertz; sampled_by: Sampled in “Dial Tone” by The Operators; " +
		"origin: The Test Patterns formed in Bristol in 2009"
	if got := strings.Join(facts, "; "); got != want {
		t.Fatalf("facts: %s", got)
	}

	// A song MusicBrainz doesn't know.
	alice.want(http.StatusOK, "POST", "/rooms/"+room.ID+"/queue", addReq(link, "t02")).decode(t, &snap)
	var unknown string
	for _, it := range snap.Items {
		if it.Track.TrackId == "t02" {
			unknown = it.Id
		}
	}
	if r := alice.do("GET", "/rooms/"+room.ID+"/queue/"+unknown+"/liner-notes", nil); r.status != http.StatusNotFound {
		t.Fatalf("unknown song: %d %s", r.status, r.body)
	}
}
