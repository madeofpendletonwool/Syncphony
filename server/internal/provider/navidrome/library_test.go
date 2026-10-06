// SPDX-License-Identifier: AGPL-3.0-only

package navidrome_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/navidrome"
)

// library answers the playlist and collection methods. starredSongs is
// whether the account has starred any songs.
func library(srv *server, starredSongs bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch strings.TrimPrefix(r.URL.Path, "/rest/") {
		case "getStarred2":
			songs := ""
			if starredSongs {
				songs = `,"song":[{"id":"s7","title":"Starred One","coverArt":"al-al7"},{"id":"s8","title":"Starred Two"}]`
			}
			ok(w, `"starred2":{"artist":[{"id":"ar1","name":"The Square Roots"}],"album":[{"id":"al1","name":"Low Pass","artist":"The Square Roots","songCount":9,"coverArt":"al-al1"}]`+songs+`}`)
		case "getPlaylists":
			ok(w, `"playlists":{"playlist":[{"id":"9f1c","name":"Road Trip","owner":"bob","songCount":2,"coverArt":"pl-9f1c"}]}`)
		case "getPlaylist":
			if q.Get("id") != "9f1c" {
				subsonicError(w, 70, "playlist not found")
				return
			}
			ok(w, `"playlist":{"id":"9f1c","name":"Road Trip","entry":[{"id":"s1","title":"Rolloff"},{"id":"s2","title":"Highshelf"}]}`)
		case "getAlbumList2":
			if q.Get("size") != "2" {
				srv.t.Errorf("getAlbumList2 size = %q, want 2", q.Get("size"))
			}
			switch q.Get("type") {
			case "newest":
				ok(w, `"albumList2":{"album":[{"id":"al-new","name":"newest"}]}`)
			case "frequent":
				ok(w, `"albumList2":{"album":[{"id":"al-freq","name":"frequent"}]}`)
			case "recent":
				ok(w, `"albumList2":{"album":[{"id":"al-rec","name":"recent"}]}`)
			default:
				subsonicError(w, 10, "unknown list type")
			}
		default:
			srv.route(w, r)
		}
	}
}

func TestPlaylists(t *testing.T) {
	srv := newServer(t)
	srv.fail = library(srv, true)
	pl := open(t, navidrome.New(navidrome.Options{}), srv.URL).(provider.PlaylistLister)
	ctx := t.Context()

	page, err := pl.Playlists(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Next != "" {
		t.Fatalf("Playlists = %+v, want starred songs and one playlist", page)
	}
	if got := page.Items[0]; got.ID != navidrome.StarredID || got.TrackCount != 2 || got.Artwork != "al-al7" || got.Owner != username {
		t.Errorf("first playlist = %+v, want starred songs", got)
	}
	if got := page.Items[1]; got.ID != "9f1c" || got.Name != "Road Trip" || got.Owner != "bob" || got.TrackCount != 2 || got.Artwork != "pl-9f1c" {
		t.Errorf("second playlist = %+v", got)
	}

	tracks, err := pl.PlaylistTracks(ctx, "9f1c", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks.Items) != 2 || tracks.Items[1].Ref.ID != "s2" || tracks.Items[1].Ref.LinkID != "link-1" || tracks.Next != "" {
		t.Errorf("PlaylistTracks = %+v", tracks)
	}
	starred, err := pl.PlaylistTracks(ctx, navidrome.StarredID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(starred.Items) != 2 || starred.Items[0].Title != "Starred One" {
		t.Errorf("starred songs = %+v", starred)
	}
	if _, err := pl.PlaylistTracks(ctx, "nope", ""); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("missing playlist: got %v, want ErrNotFound", err)
	}
	if _, err := pl.PlaylistTracks(ctx, "9f1c", "100"); err == nil {
		t.Error("a cursor should be refused: there's only one page")
	}
}

func TestPlaylistsNoStarredSongs(t *testing.T) {
	srv := newServer(t)
	srv.fail = library(srv, false)
	pl := open(t, navidrome.New(navidrome.Options{}), srv.URL).(provider.PlaylistLister)
	page, err := pl.Playlists(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "9f1c" {
		t.Errorf("Playlists = %+v, want only the real playlist", page.Items)
	}
}

func TestCollection(t *testing.T) {
	srv := newServer(t)
	srv.fail = library(srv, true)
	c := open(t, navidrome.New(navidrome.Options{}), srv.URL).(provider.Collection)
	ctx := t.Context()

	saved, err := c.Saved(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Albums) != 1 || saved.Albums[0].Title != "Low Pass" || saved.Albums[0].TrackCount != 9 {
		t.Errorf("saved albums = %+v", saved.Albums)
	}
	if len(saved.Artists) != 1 || saved.Artists[0].Name != "The Square Roots" || saved.Artists[0].Artwork != "ar-ar1" {
		t.Errorf("saved artists = %+v", saved.Artists)
	}

	for _, kind := range []provider.AlbumListKind{provider.AlbumsNewest, provider.AlbumsFrequent, provider.AlbumsRecent} {
		albums, err := c.AlbumList(ctx, kind, 2)
		if err != nil {
			t.Fatalf("AlbumList(%s): %v", kind, err)
		}
		if len(albums) != 1 || albums[0].Title != string(kind) {
			t.Errorf("AlbumList(%s) = %+v", kind, albums)
		}
	}
	if _, err := c.AlbumList(ctx, "highest", 2); !errors.Is(err, provider.ErrUnsupported) {
		t.Errorf("AlbumList(highest): got %v, want ErrUnsupported", err)
	}
}
