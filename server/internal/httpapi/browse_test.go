// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
)

func search(t *testing.T, c *client, q string) httpapi.SearchResults {
	t.Helper()
	var res httpapi.SearchResults
	c.want(http.StatusOK, "GET", "/search?q="+url.QueryEscape(q), nil).decode(t, &res)
	return res
}

func TestSearchAndBrowse(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")

	if res := search(t, alice, "null"); len(res.Groups) != 0 {
		t.Fatalf("no links, but groups: %+v", res.Groups)
	}
	if r := alice.do("GET", "/search?q=%20", nil); r.code() != "invalid_input" {
		t.Fatalf("blank query: %d %s", r.status, r.body)
	}

	var l httpapi.ServiceLink
	alice.want(http.StatusCreated, "POST", "/links", httpapi.CreateLinkRequest{Provider: "fake", Fields: demoFields}).decode(t, &l)

	res := search(t, alice, "null")
	if res.Query != "null" || len(res.Groups) != 1 {
		t.Fatalf("results: %+v", res)
	}
	g := res.Groups[0]
	if g.LinkId != l.Id || g.Provider != "fake" || g.AccountLabel != l.AccountLabel || g.Error != nil {
		t.Fatalf("group: %+v", g)
	}
	if len(g.Tracks) != 6 || len(g.Albums) != 2 || len(g.Artists) != 1 || g.Artists[0].Name != "Null Island" {
		t.Fatalf("null island: %d tracks, %d albums, %+v", len(g.Tracks), len(g.Albums), g.Artists)
	}
	tr := g.Tracks[0]
	if tr.LinkId != l.Id || tr.Provider != "fake" || tr.TrackId == "" || tr.Album == nil || tr.Album.Id == nil ||
		len(tr.Artists) != 1 || tr.Artists[0].Id == nil || tr.DurationMs <= 0 || tr.Artwork == nil {
		t.Fatalf("track: %+v", tr)
	}
	if res := search(t, alice, "null"); len(res.Groups[0].Tracks) != 6 {
		t.Fatal("search isn't repeatable")
	}
	var limited httpapi.SearchResults
	alice.want(http.StatusOK, "GET", "/search?q=null&limit=2", nil).decode(t, &limited)
	if len(limited.Groups[0].Tracks) != 2 {
		t.Fatalf("limit: %d tracks", len(limited.Groups[0].Tracks))
	}

	// Album: its tracks, in order, queueable as they are.
	var album httpapi.AlbumDetail
	alice.want(http.StatusOK, "GET", "/links/"+l.Id+"/albums/"+*tr.Album.Id, nil).decode(t, &album)
	if album.LinkId != l.Id || album.Provider != "fake" || album.Album.Title != tr.Album.Title || len(album.Tracks) != 3 ||
		album.Album.Year == nil || album.Tracks[0].LinkId != l.Id {
		t.Fatalf("album: %+v", album)
	}

	// Artist: their albums.
	var artist httpapi.ArtistDetail
	alice.want(http.StatusOK, "GET", "/links/"+l.Id+"/artists/"+*tr.Artists[0].Id, nil).decode(t, &artist)
	if artist.Artist.Name != "Null Island" || len(artist.Albums) != 2 {
		t.Fatalf("artist: %+v", artist)
	}

	// Artwork: an image, privately cacheable, with scripts locked down.
	art := alice.want(http.StatusOK, "GET", "/links/"+l.Id+"/artwork?size=300&ref="+url.QueryEscape(*album.Album.Artwork), nil)
	if !strings.HasPrefix(art.header.Get("Content-Type"), "image/") || !strings.Contains(art.header.Get("Cache-Control"), "private") ||
		!strings.Contains(art.header.Get("Content-Security-Policy"), "sandbox") || len(art.body) == 0 {
		t.Fatalf("artwork: %v", art.header)
	}
	if r := alice.do("GET", "/links/"+l.Id+"/albums/nope", nil); r.status != http.StatusNotFound {
		t.Fatalf("missing album: %d", r.status)
	}

	// Someone else's link is invisible.
	for _, path := range []string{"/albums/" + *tr.Album.Id, "/artists/" + *tr.Artists[0].Id, "/artwork?ref=" + url.QueryEscape(*tr.Artwork)} {
		if r := bob.do("GET", "/links/"+l.Id+path, nil); r.status != http.StatusNotFound {
			t.Fatalf("bob browsing alice's link %s: %d", path, r.status)
		}
	}
	if res := search(t, bob, "null"); len(res.Groups) != 0 {
		t.Fatalf("bob searches alice's link: %+v", res.Groups)
	}

	// A search result goes straight into the queue.
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Den"}).decode(t, &room)
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", "/rooms/"+room.Id+"/queue", httpapi.AddToQueueRequest{
		Items: []httpapi.TrackToQueue{{LinkId: tr.LinkId, TrackId: tr.TrackId}},
	}).decode(t, &snap)
	if len(snap.Items) != 1 || snap.Items[0].Track.Title != tr.Title {
		t.Fatalf("queued: %+v", snap.Items)
	}

	// Everyone in the room sees the song's artwork, through alice's link.
	item := snap.Items[0].Id
	art = bob.want(http.StatusOK, "GET", "/rooms/"+room.Id+"/queue/"+item+"/artwork?size=200", nil)
	if !strings.HasPrefix(art.header.Get("Content-Type"), "image/") || len(art.body) == 0 {
		t.Fatalf("room artwork: %v", art.header)
	}
	var other httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Attic"}).decode(t, &other)
	if r := bob.do("GET", "/rooms/"+other.Id+"/queue/"+item+"/artwork", nil); r.status != http.StatusNotFound {
		t.Fatalf("artwork through the wrong room: %d", r.status)
	}
	alice.want(http.StatusNoContent, "DELETE", "/links/"+l.Id, nil)
	if r := bob.do("GET", "/rooms/"+room.Id+"/queue/"+item+"/artwork", nil); r.status != http.StatusNotFound {
		t.Fatalf("artwork after unlinking: %d", r.status)
	}
}

func TestSearchReportsFailingLinks(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	alice.want(http.StatusCreated, "POST", "/links", httpapi.CreateLinkRequest{Provider: "fake", Fields: demoFields})

	e.fake.Fail(provider.ErrUnavailable)
	res := search(t, alice, "tone")
	if g := res.Groups[0]; g.Error == nil || g.Error.Code != "service_unavailable" || len(g.Tracks) != 0 {
		t.Fatalf("outage: %+v", g)
	}
	e.fake.Fail(nil)

	e.fake.Revoke(fake.Username)
	res = search(t, alice, "tone")
	if g := res.Groups[0]; g.Error == nil || g.Error.Code != "needs_relink" {
		t.Fatalf("revoked: %+v", g)
	}
}
