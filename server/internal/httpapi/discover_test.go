// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// discoverGraph knows Hum, who's like Failure and Shiner.
type discoverGraph struct{}

func (discoverGraph) CachedArtist(_ context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, bool, error) {
	songs := func(name string, id int64) []musicgraph.Song {
		var out []musicgraph.Song
		for i := range 3 {
			out = append(out, musicgraph.Song{SongRef: musicgraph.SongRef{
				Title: fmt.Sprintf("%s %d", name, i+1), Artist: musicgraph.ArtistRef{Name: name}, DeezerID: id + int64(i),
			}, Score: 1})
		}
		return out
	}
	switch a.Name {
	case "Hum":
		return musicgraph.Artist{Ref: a, Similar: []musicgraph.Similar{
			{Artist: musicgraph.ArtistRef{Name: "Failure"}, Score: 0.8}, {Artist: musicgraph.ArtistRef{Name: "Shiner"}, Score: 0.5},
		}, Top: songs("Hum", 10)}, true, nil
	case "Failure", "Shiner":
		return musicgraph.Artist{Ref: a, Top: songs(a.Name, int64(len(a.Name))*100)}, true, nil
	}
	return musicgraph.Artist{}, true, provider.ErrNotFound
}

func (g discoverGraph) Artist(ctx context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, error) {
	out, _, err := g.CachedArtist(ctx, a)
	return out, err
}

func (discoverGraph) WarmArtist(...musicgraph.ArtistRef) {}

func TestDiscover(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	e.api.Discover.Graph = discoverGraph{}
	audio := bytes.Repeat([]byte("mp3!"), 100)
	var dzURL string
	dz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/track/800":
			fmt.Fprintf(w, `{"id":800,"title":"Failure 1","preview":"%s/preview.mp3?exp=1","artist":{"name":"Failure"}}`, dzURL)
		case "/preview.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(audio))
		default:
			fmt.Fprint(w, `{"error":{"code":800,"message":"no data"}}`)
		}
	}))
	t.Cleanup(dz.Close)
	dzURL = dz.URL
	e.api.Deezer = musicgraph.NewDeezer(musicgraph.DeezerOptions{BaseURL: dz.URL, Interval: -1})

	var set httpapi.DiscoverySet
	alice.want(http.StatusCreated, "POST", "/me/discover/sets", httpapi.CreateDiscoverySetRequest{
		Name: "90s alt", Artists: &[]httpapi.DiscoverySeed{{Name: "Hum"}},
	}).decode(t, &set)
	if len(set.Artists) != 1 || set.Explore != 40 || set.Mode != httpapi.DiscoveryModeBoth {
		t.Fatalf("set: %+v", set)
	}
	base := "/me/discover/sets/" + set.Id
	alice.want(http.StatusOK, "POST", base+"/artists", httpapi.DiscoverySeed{Name: "Nobody Knows"}).decode(t, &set)
	alice.want(http.StatusOK, "DELETE", base+"/artists/"+url.PathEscape(set.Artists[1].Key), nil).decode(t, &set)
	if len(set.Artists) != 1 {
		t.Fatalf("artists: %+v", set.Artists)
	}
	mode := httpapi.DiscoveryModeDeep
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateDiscoverySetRequest{Mode: &mode, Explore: ptr(70)}).decode(t, &set)
	if set.Mode != httpapi.DiscoveryModeDeep || set.Explore != 70 {
		t.Fatalf("updated: %+v", set)
	}
	if r := alice.do("PATCH", base, httpapi.UpdateDiscoverySetRequest{Explore: ptr(101)}); r.status != http.StatusBadRequest {
		t.Errorf("explore 101: %d %s", r.status, r.body)
	}

	// The feed: Failure's songs lead, each saying why.
	var feed httpapi.DiscoveryFeed
	alice.want(http.StatusOK, "GET", base+"/feed", nil).decode(t, &feed)
	if len(feed.Items) == 0 || feed.Items[0].Artist != "Failure" || len(feed.Items[0].Because) != 1 || feed.Items[0].Because[0] != "Hum" || feed.NoGraph {
		t.Fatalf("feed: %+v", feed)
	}
	first := feed.Items[0]
	alice.want(http.StatusNoContent, "POST", "/me/discover/feedback", httpapi.DiscoveryFeedbackRequest{
		Kind: httpapi.DiscoveryFeedbackRequestKindDown, SetId: &set.Id, Title: first.Title, Artist: first.Artist,
	})
	alice.want(http.StatusOK, "GET", base+"/feed", nil).decode(t, &feed)
	for _, it := range feed.Items {
		if it.Key == first.Key {
			t.Fatal("still there after a thumbs-down")
		}
	}
	var block httpapi.DiscoveryBlock
	alice.want(http.StatusOK, "POST", "/me/discover/blocks", httpapi.BlockDiscoveryArtistRequest{Name: "Shiner"}).decode(t, &block)
	alice.want(http.StatusOK, "GET", base+"/feed", nil).decode(t, &feed)
	for _, it := range feed.Items {
		if it.Artist == "Shiner" {
			t.Fatal("a blocked artist")
		}
	}
	alice.want(http.StatusNoContent, "DELETE", "/me/discover/blocks/"+url.PathEscape(block.Key), nil)

	// The preview plays through the server, a range at a time.
	r := alice.do("GET", "/me/discover/preview/800", nil, "Range", "bytes=100-")
	if r.status != http.StatusPartialContent || len(r.body) != len(audio)-100 || r.header.Get("Content-Type") != "audio/mpeg" {
		t.Errorf("preview: %d, %d bytes, %s", r.status, len(r.body), r.header.Get("Content-Type"))
	}
	if r := alice.do("GET", "/me/discover/preview/801", nil); r.status != http.StatusNotFound {
		t.Errorf("a song with no preview: %d", r.status)
	}

	var found []httpapi.DiscoveryArtist
	alice.want(http.StatusOK, "GET", "/me/discover/artists?q=Duster", nil).decode(t, &found)
	if len(found) != 1 || found[0].Name != "Duster" {
		t.Errorf("artist search: %+v", found)
	}
	var lib httpapi.DiscoveryLibrary
	alice.want(http.StatusOK, "GET", "/me/discover/library", nil).decode(t, &lib)

	// The map, of the set and of any artist, and an artist's songs.
	var m httpapi.DiscoveryMap
	alice.want(http.StatusOK, "GET", "/me/discover/map?setId="+set.Id, nil).decode(t, &m)
	if m.Set == nil || len(m.Nodes) != 3 || !m.Nodes[0].Seed || m.Nodes[1].Name != "Failure" || len(m.Edges) != 2 {
		t.Fatalf("set map: %+v", m)
	}
	m = httpapi.DiscoveryMap{}
	alice.want(http.StatusOK, "GET", "/me/discover/map?artist=Hum&depth=1&limit=10", nil).decode(t, &m)
	if m.Set != nil || len(m.Nodes) != 3 || !m.Nodes[0].Center {
		t.Fatalf("artist map: %+v", m)
	}
	if r := alice.do("GET", "/me/discover/map", nil); r.status != http.StatusBadRequest {
		t.Errorf("a map of nothing: %d", r.status)
	}
	var songs httpapi.DiscoverySongs
	alice.want(http.StatusOK, "GET", "/me/discover/songs?artist=Shiner", nil).decode(t, &songs)
	if len(songs.Items) != 3 || songs.Items[0].DeezerId == nil {
		t.Errorf("songs: %+v", songs)
	}

	// Bob can't see alice's set, or its feed.
	var bobs []httpapi.DiscoverySetSummary
	bob.want(http.StatusOK, "GET", "/me/discover/sets", nil).decode(t, &bobs)
	if len(bobs) != 0 {
		t.Errorf("bob's sets: %+v", bobs)
	}
	for _, path := range []string{base, base + "/feed", "/me/discover/map?setId=" + set.Id} {
		if r := bob.do("GET", path, nil); r.status != http.StatusNotFound {
			t.Errorf("bob GET %s: %d", path, r.status)
		}
	}
	alice.want(http.StatusNoContent, "DELETE", base, nil)
	alice.want(http.StatusNotFound, "GET", base, nil)
}

func TestGuestsHaveNoDiscovery(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	e.advance(time.Since(e.clock()))
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	alice.want(http.StatusOK, "PATCH", "/rooms/"+room.Id, httpapi.UpdateRoomRequest{Guests: &httpapi.RoomGuests{Allowed: true, MaxSongs: 2}})
	var pass httpapi.GuestPass
	alice.want(http.StatusCreated, "POST", "/rooms/"+room.Id+"/guest-pass", httpapi.CreateGuestPassRequest{ExpiresAt: e.clock().Add(time.Hour)}).decode(t, &pass)
	guest, _ := e.joinAsGuest(passToken(t, pass), "Gus")
	for _, path := range []string{"/me/discover/sets", "/me/discover/library", "/me/discover/preview/1"} {
		if r := guest.do("GET", path, nil); r.status != http.StatusForbidden {
			t.Errorf("guest GET %s: %d %s", path, r.status, r.body)
		}
	}
}
