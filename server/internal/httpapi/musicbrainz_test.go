// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

const (
	refToneRelease = "aa000000-0000-4000-8000-0000000000f1"
	coverWidth     = 500
)

// newMusicBrainz stands in for MusicBrainz, which knows the fake
// provider's "Reference Tone" by its ISRC, and the Cover Art Archive.
func newMusicBrainz(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws/2/isrc/XXFAK0000001":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"isrc":"XXFAK0000001","recordings":[{"id":"cc000000-0000-4000-8000-0000000000f1","title":"Reference Tone","length":20000,` +
				`"artist-credit":[{"name":"The Test Patterns","artist":{"id":"0b000000-0000-4000-8000-0000000000f1","name":"The Test Patterns"}}],` +
				`"releases":[{"id":"` + refToneRelease + `","title":"Calibration","status":"Official","date":"2019",` +
				`"release-group":{"id":"bb000000-0000-4000-8000-0000000000f1","primary-type":"Album","secondary-types":[]}}]}]}`))
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

	// The fake's own artwork is an SVG, which is never too small.
	art := bob.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/queue/"+item+"/artwork?size=400", nil)
	if ct := art.header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("own artwork: %s", ct)
	}

	// With alice's link gone, the room still sees the cover.
	alice.want(http.StatusNoContent, "DELETE", "/links/"+link, nil)
	art = bob.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/queue/"+item+"/artwork?size=400", nil)
	cfg, err := png.DecodeConfig(bytes.NewReader(art.body))
	if err != nil || art.header.Get("Content-Type") != "image/png" || cfg.Width != coverWidth ||
		art.header.Get("Content-Security-Policy") == "" {
		t.Fatalf("cover art archive: %v %v %+v", art.header, err, cfg)
	}
	// Without a cover in that size, still nothing.
	if r := bob.do("GET", "/rooms/"+room.ID+"/queue/"+item+"/artwork?size=100", nil); r.status != http.StatusNotFound {
		t.Fatalf("no small cover: %d", r.status)
	}
}
