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
		Items: []httpapi.TrackToQueue{{LinkId: &tr.LinkId, TrackId: &tr.TrackId}},
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

func TestSearchPlaylists(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	var l httpapi.ServiceLink
	alice.want(http.StatusCreated, "POST", "/links", httpapi.CreateLinkRequest{Provider: "fake", Fields: demoFields}).decode(t, &l)

	names := func(g httpapi.SearchGroup) []string {
		var out []string
		for _, p := range g.Playlists {
			out = append(out, p.Name)
		}
		return out
	}

	// The link's own playlists, by name.
	g := search(t, alice, "fav").Groups[0]
	if got := names(g); len(got) != 1 || got[0] != "Fake Favourites" {
		t.Fatalf("own playlists for fav: %v", got)
	}
	if p := g.Playlists[0]; p.Id != "p1" || p.TrackCount == nil || *p.TrackCount != 6 {
		t.Fatalf("playlist: %+v", p)
	}
	if got := names(search(t, alice, "null").Groups[0]); len(got) != 0 {
		t.Fatalf("public playlists without asking: %v", got)
	}

	// Public ones when asked, each playlist once.
	var res httpapi.SearchResults
	alice.want(http.StatusOK, "GET", "/search?publicPlaylists=true&q="+url.QueryEscape("null"), nil).decode(t, &res)
	if got := names(res.Groups[0]); len(got) != 1 || got[0] != "Null Island Radio" {
		t.Fatalf("public playlists for null: %v", got)
	}
	alice.want(http.StatusOK, "GET", "/search?publicPlaylists=true&q="+url.QueryEscape("fake"), nil).decode(t, &res)
	if got := names(res.Groups[0]); len(got) != 1 || got[0] != "Fake Favourites" {
		t.Fatalf("own and public playlists for fake: %v", got)
	}

	// A public playlist opens like your own.
	var page httpapi.PlaylistTracks
	alice.want(http.StatusOK, "GET", "/links/"+l.Id+"/playlists/p3/tracks", nil).decode(t, &page)
	if len(page.Tracks) != 5 || page.Next == nil {
		t.Fatalf("public playlist's first page: %d tracks, next %v", len(page.Tracks), page.Next)
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

func TestPlaylists(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	var l httpapi.ServiceLink
	alice.want(http.StatusCreated, "POST", "/links", httpapi.CreateLinkRequest{Provider: "fake", Fields: demoFields}).decode(t, &l)

	var list httpapi.PlaylistList
	alice.want(http.StatusOK, "GET", "/links/"+l.Id+"/playlists", nil).decode(t, &list)
	if list.LinkId != l.Id || list.Provider != "fake" || len(list.Playlists) != 2 || list.Next != nil {
		t.Fatalf("playlists: %+v", list)
	}
	p := list.Playlists[1]
	if p.Name != "Everything" || p.TrackCount == nil || p.Owner == nil || p.Artwork == nil {
		t.Fatalf("playlist: %+v", p)
	}

	// Every page, until there's no next.
	var tracks []httpapi.TrackResult
	path := "/links/" + l.Id + "/playlists/" + p.Id + "/tracks"
	for cursor := ""; ; {
		var page httpapi.PlaylistTracks
		q := ""
		if cursor != "" {
			q = "?cursor=" + url.QueryEscape(cursor)
		}
		alice.want(http.StatusOK, "GET", path+q, nil).decode(t, &page)
		if page.LinkId != l.Id || page.Provider != "fake" {
			t.Fatalf("page: %+v", page)
		}
		tracks = append(tracks, page.Tracks...)
		if page.Next == nil {
			break
		}
		if *page.Next == cursor || len(tracks) > 100 {
			t.Fatalf("cursor %q doesn't advance", cursor)
		}
		cursor = *page.Next
	}
	if len(tracks) != *p.TrackCount || tracks[0].LinkId != l.Id || tracks[0].TrackId == "" {
		t.Fatalf("%d tracks of %d: %+v", len(tracks), *p.TrackCount, tracks[0])
	}

	if r := alice.do("GET", "/links/"+l.Id+"/playlists/nope/tracks", nil); r.status != http.StatusNotFound {
		t.Fatalf("missing playlist: %d", r.status)
	}
	for _, path := range []string{"/playlists", "/playlists/" + p.Id + "/tracks"} {
		if r := bob.do("GET", "/links/"+l.Id+path, nil); r.status != http.StatusNotFound {
			t.Fatalf("bob browsing alice's link %s: %d", path, r.status)
		}
	}
}

func TestCollection(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	var l httpapi.ServiceLink
	alice.want(http.StatusCreated, "POST", "/links", httpapi.CreateLinkRequest{Provider: "fake", Fields: demoFields}).decode(t, &l)

	var c httpapi.LinkCollection
	alice.want(http.StatusOK, "GET", "/links/"+l.Id+"/collection", nil).decode(t, &c)
	if c.LinkId != l.Id || c.Provider != "fake" || len(c.SavedAlbums) != 3 || len(c.SavedArtists) != 1 {
		t.Fatalf("collection: %+v", c)
	}
	if len(c.RecentlyAdded) != 6 || c.RecentlyAdded[0].Title != "Overtones" || len(c.MostPlayed) != 6 || len(c.RecentlyPlayed) != 3 {
		t.Fatalf("album lists: %d added, %d most played, %d recently played", len(c.RecentlyAdded), len(c.MostPlayed), len(c.RecentlyPlayed))
	}

	if r := bob.do("GET", "/links/"+l.Id+"/collection", nil); r.status != http.StatusNotFound {
		t.Fatalf("bob browsing alice's link: %d", r.status)
	}
	e.fake.Fail(provider.ErrUnavailable)
	if r := alice.do("GET", "/links/"+l.Id+"/collection", nil); r.status != http.StatusBadGateway {
		t.Fatalf("outage: %d", r.status)
	}
}

func TestSuggestions(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	var l httpapi.ServiceLink
	alice.want(http.StatusCreated, "POST", "/links", httpapi.CreateLinkRequest{Provider: "fake", Fields: demoFields}).decode(t, &l)
	tr := search(t, alice, "Reference Tone").Groups[0].Tracks[0]

	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Den"}).decode(t, &room)
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", "/rooms/"+room.Id+"/queue", httpapi.AddToQueueRequest{
		Items: []httpapi.TrackToQueue{{LinkId: &tr.LinkId, TrackId: &tr.TrackId}},
	}).decode(t, &snap)
	path := "/rooms/" + room.Id + "/queue/" + snap.Items[0].Id + "/similar"

	var similar httpapi.TrackList
	alice.want(http.StatusOK, "GET", path+"?limit=3", nil).decode(t, &similar)
	if len(similar.Tracks) == 0 || len(similar.Tracks) > 3 {
		t.Fatalf("similar: %d tracks", len(similar.Tracks))
	}
	for _, s := range similar.Tracks {
		if s.Title == tr.Title || s.LinkId != l.Id {
			t.Errorf("similar track: %+v", s)
		}
	}
	// Bob has no services of his own, so nothing to suggest from.
	bob.want(http.StatusOK, "GET", path, nil).decode(t, &similar)
	if len(similar.Tracks) != 0 {
		t.Fatalf("bob's similar: %+v", similar.Tracks)
	}
	if r := alice.do("GET", "/rooms/"+room.Id+"/queue/nope/similar", nil); r.status != http.StatusNotFound {
		t.Fatalf("missing item: %d", r.status)
	}

	var random httpapi.TrackList
	alice.want(http.StatusOK, "GET", "/random-tracks?limit=2", nil).decode(t, &random)
	if len(random.Tracks) != 2 || random.Tracks[0].LinkId != l.Id {
		t.Fatalf("random: %+v", random.Tracks)
	}
	bob.want(http.StatusOK, "GET", "/random-tracks", nil).decode(t, &random)
	if len(random.Tracks) != 0 {
		t.Fatalf("bob's random: %+v", random.Tracks)
	}
}
