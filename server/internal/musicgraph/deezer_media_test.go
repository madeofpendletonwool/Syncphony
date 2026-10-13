// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

const coverID = "0123456789abcdef0123456789abcdef"

func TestDeezerTopSongsKeepTheirDeezerIDs(t *testing.T) {
	e := newEnv(t)
	e.http.routes["/deezer/artist/399/top"] = `{"data":[
		{"id":138547415,"title":"Creep","rank":978547,"preview":"https://cdnt-preview.dzcdn.net/x.mp3","artist":{"id":399,"name":"Radiohead"},
		 "album":{"id":1,"title":"Pablo Honey","cover_medium":"https://e-cdns-images.dzcdn.net/images/cover/` + coverID + `/250x250-000000-80-0-0.jpg"}}],"total":1}`
	e.http.routes["/deezer/search/artist?Radiohead"] = `{"data":[{"id":399,"name":"Radiohead","nb_fan":4101295,"md5_image":"` + coverID + `"}],"total":1}`
	a, err := e.svc.Artist(t.Context(), radiohead)
	if err != nil {
		t.Fatal(err)
	}
	if a.Fans != 4101295 || a.Picture != coverID {
		t.Errorf("fans %d, picture %q", a.Fans, a.Picture)
	}
	for _, so := range a.Top {
		if so.Title != "Creep" {
			continue
		}
		if so.DeezerID != 138547415 || so.Album != "Pablo Honey" || so.Cover != coverID || so.Rank < 0.97 {
			t.Errorf("Creep: %+v", so)
		}
		return
	}
	t.Fatalf("no Creep in %+v", a.Top)
}

func TestDeezerFindTrack(t *testing.T) {
	e := newEnv(t)
	e.http.routes[`/deezer/search?artist:"Hum" track:"Stars"`] = `{"data":[
		{"id":9,"title":"Stars (Live)","artist":{"name":"Hum"}},
		{"id":10,"title":"Stars","rank":400000,"preview":"https://cdnt-preview.dzcdn.net/y.mp3","artist":{"name":"Hum"},"album":{"title":"You'd Prefer an Astronaut","md5_image":"` + coverID + `"}}]}`
	d := musicgraph.NewDeezer(musicgraph.DeezerOptions{BaseURL: e.http.URL + "/deezer", Interval: -1})
	got, err := d.FindTrack(t.Context(), musicgraph.SongRef{Title: "Stars", Artist: musicgraph.ArtistRef{Name: "Hum"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 10 || got.Album != "You'd Prefer an Astronaut" || got.Cover != coverID || !got.HasPreview {
		t.Errorf("found %+v", got)
	}
	// Neither search finds it.
	if _, err := d.FindTrack(t.Context(), musicgraph.SongRef{Title: "Nope", Artist: musicgraph.ArtistRef{Name: "Hum"}}); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("a song Deezer doesn't have: %v", err)
	}
}

// previewServer is Deezer's API and CDN: each /track/1 gives a new signed
// preview URL, and only the newest one plays.
type previewServer struct {
	*httptest.Server
	issued atomic.Int64
}

var mp3 = bytes.Repeat([]byte("ID3mp3"), 200)

func newPreviewServer(t *testing.T) *previewServer {
	p := &previewServer{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/track/1":
			n := p.issued.Add(1)
			fmt.Fprintf(w, `{"id":1,"title":"Stars","preview":"%s/preview.mp3?hdnea=exp=%d","artist":{"name":"Hum"}}`, p.URL, n)
		case "/track/2":
			fmt.Fprint(w, `{"id":2,"title":"Silence","preview":"","artist":{"name":"Hum"}}`)
		case "/track/3":
			fmt.Fprint(w, `{"id":3,"title":"Elsewhere","preview":"https://evil.example.com/x.mp3","artist":{"name":"Hum"}}`)
		case "/preview.mp3":
			if r.URL.Query().Get("hdnea") != fmt.Sprintf("exp=%d", p.issued.Load()) {
				http.Error(w, "expired", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(mp3))
		case "/images/cover/" + coverID + "/250x250-000000-80-0-0.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpeg"))
		default:
			fmt.Fprint(w, `{"error":{"type":"DataException","message":"no data","code":800}}`)
		}
	}))
	t.Cleanup(p.Close)
	return p
}

func TestDeezerPreviewIsFetchedFresh(t *testing.T) {
	p := newPreviewServer(t)
	d := musicgraph.NewDeezer(musicgraph.DeezerOptions{BaseURL: p.URL, ImageURL: p.URL + "/images", Interval: -1})
	for range 2 {
		a, err := d.OpenPreview(t.Context(), 1, nil)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(a.Body)
		a.Body.Close()
		if !bytes.Equal(body, mp3) || a.ContentType != "audio/mpeg" || a.ContentRange() != "" {
			t.Fatalf("preview: %d bytes, %s", len(body), a.ContentType)
		}
	}
	if p.issued.Load() != 2 {
		t.Errorf("asked for a preview URL %d times, want every time", p.issued.Load())
	}
	// A range, for seeking.
	a, err := d.OpenPreview(t.Context(), 1, &provider.ByteRange{Start: 100, End: -1})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(a.Body)
	a.Body.Close()
	if a.Offset != 100 || int(a.Length) != len(mp3)-100 || len(body) != len(mp3)-100 || !strings.HasPrefix(a.ContentRange(), "bytes 100-") {
		t.Errorf("range: offset %d, length %d, %s", a.Offset, a.Length, a.ContentRange())
	}
	if _, err := d.OpenPreview(t.Context(), 2, nil); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("no preview: %v", err)
	}
	if _, err := d.OpenPreview(t.Context(), 3, nil); !errors.Is(err, provider.ErrUnavailable) {
		t.Errorf("a preview somewhere else: %v", err)
	}

	img, ct, err := d.Image(t.Context(), musicgraph.ImageCover, coverID, 200)
	if err != nil || string(img) != "jpeg" || ct != "image/jpeg" {
		t.Errorf("image: %q %s %v", img, ct, err)
	}
	if _, _, err := d.Image(t.Context(), musicgraph.ImageCover, "../../etc/passwd", 200); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("a bad image ID: %v", err)
	}
}
