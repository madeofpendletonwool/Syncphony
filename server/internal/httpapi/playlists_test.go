// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

func TestSavedPlaylists(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	aliceID, bobID := me(t, alice).Id, me(t, bob).Id
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	base := "/rooms/" + room.Id
	aliceLink, bobLink := linkFake(t, alice), linkFake(t, bob)
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", base+"/queue", addReq(aliceLink, "t01", "t02"))
	bob.want(http.StatusOK, "POST", base+"/queue", addReq(bobLink, "t03")).decode(t, &snap)

	// Tonight: alice's, bob's, alice's.
	night := e.clock().Add(-time.Hour)
	for i, id := range snap.UpNext {
		e.play(room.Id, id, night.Add(time.Duration(i)*5*time.Minute))
	}
	from, to := night, night.Add(time.Hour)

	// Save the night, shared with the room.
	var saved httpapi.SavedPlaylist
	alice.want(http.StatusCreated, "POST", base+"/playlists", httpapi.SaveNightRequest{
		Name: "Living room · Friday", From: from, To: to, Share: ptr(true),
	}).decode(t, &saved)
	if len(saved.Songs) != 3 || saved.Night == nil || saved.RoomId == nil || *saved.RoomId != room.Id {
		t.Fatalf("saved night: %+v", saved)
	}
	if got := []string{saved.Songs[0].Track.TrackId, saved.Songs[1].Track.TrackId, saved.Songs[2].Track.TrackId}; got[0] != "t01" || got[1] != "t03" || got[2] != "t02" {
		t.Errorf("order %v", got)
	}
	if saved.Songs[1].AddedBy == nil || *saved.Songs[1].AddedBy != bobID {
		t.Errorf("bob's song kept who brought it: %+v", saved.Songs[1])
	}
	// A night with nothing in it.
	if r := alice.do("POST", base+"/playlists", httpapi.SaveNightRequest{Name: "Empty", From: to, To: to.Add(time.Hour)}); r.status != http.StatusConflict || r.code() != "nothing_played" {
		t.Errorf("saving an empty night: %d %s", r.status, r.body)
	}

	// A playlist of alice's own, from search and from the room.
	var mine httpapi.SavedPlaylist
	alice.want(http.StatusCreated, "POST", "/playlists", httpapi.CreatePlaylistRequest{Name: "  Keepers "}).decode(t, &mine)
	if mine.Name != "Keepers" || mine.RoomId != nil {
		t.Fatalf("created: %+v", mine)
	}
	pl := "/playlists/" + mine.Id
	alice.want(http.StatusOK, "POST", pl+"/songs", httpapi.AddPlaylistSongsRequest{Items: []httpapi.PlaylistSongToAdd{
		{LinkId: ptr(aliceLink), TrackId: ptr("t04")}, {ItemId: ptr(snap.UpNext[1])},
	}}).decode(t, &mine)
	if len(mine.Songs) != 2 || mine.Songs[1].Track.TrackId != "t03" {
		t.Fatalf("songs: %+v", mine.Songs)
	}
	alice.want(http.StatusOK, "PUT", pl+"/songs/"+mine.Songs[1].Id+"/position", httpapi.MovePlaylistSongRequest{Position: 0}).decode(t, &mine)
	if mine.Songs[0].Track.TrackId != "t03" || mine.Songs[1].Track.TrackId != "t04" {
		t.Fatalf("after moving: %+v", mine.Songs)
	}
	alice.want(http.StatusOK, "DELETE", pl+"/songs/"+mine.Songs[0].Id, nil).decode(t, &mine)
	if len(mine.Songs) != 1 || mine.Songs[0].Track.TrackId != "t04" {
		t.Fatalf("after removing: %+v", mine.Songs)
	}

	// Bob sees the shared night, not alice's own; and can't change either.
	var list []httpapi.SavedPlaylistSummary
	bob.want(http.StatusOK, "GET", "/playlists", nil).decode(t, &list)
	if len(list) != 1 || list[0].Id != saved.Id || list[0].SongCount != 3 || len(list[0].Covers) == 0 {
		t.Fatalf("bob's list: %+v", list)
	}
	alice.want(http.StatusOK, "GET", "/playlists", nil).decode(t, &list)
	if len(list) != 2 {
		t.Fatalf("alice's list: %+v", list)
	}
	bob.want(http.StatusNotFound, "GET", pl, nil)
	bob.want(http.StatusForbidden, "PATCH", "/playlists/"+saved.Id, httpapi.UpdatePlaylistRequest{Name: ptr("Mine now")})

	// Queueing from it: bob can't borrow alice's service here, but can
	// queue his own song from her playlist.
	if r := bob.do("POST", base+"/queue", httpapi.AddToQueueRequest{Items: []httpapi.TrackToQueue{{FromPlaylistSongId: &saved.Songs[0].Id}}}); r.status != http.StatusForbidden || r.code() != "cant_borrow" {
		t.Errorf("borrowing: %d %s", r.status, r.body)
	}
	bob.want(http.StatusOK, "POST", base+"/queue", httpapi.AddToQueueRequest{Items: []httpapi.TrackToQueue{{FromPlaylistSongId: &saved.Songs[1].Id}}})
	// Nor from a playlist he can't see.
	if r := bob.do("POST", base+"/queue", httpapi.AddToQueueRequest{Items: []httpapi.TrackToQueue{{FromPlaylistSongId: &mine.Songs[0].Id}}}); r.status != http.StatusNotFound {
		t.Errorf("queueing from a hidden playlist: %d %s", r.status, r.body)
	}

	// Unsharing hides it from bob.
	alice.want(http.StatusOK, "PATCH", "/playlists/"+saved.Id, httpapi.UpdatePlaylistRequest{RoomId: ptr("")})
	bob.want(http.StatusNotFound, "GET", "/playlists/"+saved.Id, nil)

	// The night's recap.
	q := url.Values{"from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)}}
	var rc httpapi.Recap
	alice.want(http.StatusOK, "GET", base+"/recap?"+q.Encode(), nil).decode(t, &rc)
	if rc.Stats.Plays != 3 || rc.TopAdder == nil || rc.TopAdder.UserId != aliceID || rc.TopAdder.Count != 2 {
		t.Errorf("recap: %+v", rc)
	}
	if len(rc.Playlists) != 1 || rc.Playlists[0].Id != saved.Id {
		t.Errorf("recap playlists: %+v", rc.Playlists)
	}
	// Bob doesn't see alice's now-private playlist in it.
	bob.want(http.StatusOK, "GET", base+"/recap?"+q.Encode(), nil).decode(t, &rc)
	if len(rc.Playlists) != 0 {
		t.Errorf("bob's recap playlists: %+v", rc.Playlists)
	}

	alice.want(http.StatusNoContent, "DELETE", pl, nil)
	alice.want(http.StatusNotFound, "GET", pl, nil)
}
