// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

func TestSavedArtists(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	link := linkFake(t, alice)
	room := e.room(t, me(t, alice).Id)
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", "/rooms/"+room.ID+"/queue", addReq(link, "t01")).decode(t, &snap)
	item := snap.Items[0].Id

	// From the room's song: the room and who queued it come along.
	var a httpapi.SavedArtist
	alice.want(http.StatusCreated, "POST", "/me/saved-artists", httpapi.SaveArtistRequest{ItemId: &item}).decode(t, &a)
	if a.Name != "The Test Patterns" || a.RoomId == nil || *a.RoomId != room.ID || a.QueuedBy == nil || *a.QueuedBy != me(t, alice).Id {
		t.Errorf("saved from a song: %+v", a)
	}
	if len(a.Refs) != 1 || a.Refs[0].LinkId != link || a.Refs[0].Id != "ar1" {
		t.Errorf("refs: %+v", a.Refs)
	}

	// The same artist by another spelling, from elsewhere, is the same one,
	// and remembers the new link first.
	var again httpapi.SavedArtist
	alice.want(http.StatusOK, "POST", "/me/saved-artists",
		httpapi.SaveArtistRequest{Name: ptr("the test-patterns"), LinkId: ptr("other"), ArtistId: ptr("x9")}).decode(t, &again)
	if again.Id != a.Id || len(again.Refs) != 2 || again.Refs[0].LinkId != "other" {
		t.Errorf("saved again: %+v", again)
	}
	alice.want(http.StatusCreated, "POST", "/me/saved-artists", httpapi.SaveArtistRequest{Name: ptr("Null Island")})

	var list []httpapi.SavedArtist
	alice.want(http.StatusOK, "GET", "/me/saved-artists", nil).decode(t, &list)
	if len(list) != 2 || list[0].Name != "Null Island" {
		t.Errorf("list: %+v", list)
	}
	// They're Alice's alone.
	bob.want(http.StatusOK, "GET", "/me/saved-artists", nil).decode(t, &list)
	if len(list) != 0 {
		t.Errorf("bob sees %+v", list)
	}
	bob.want(http.StatusNotFound, "DELETE", "/me/saved-artists/"+a.Id, nil)

	alice.want(http.StatusBadRequest, "POST", "/me/saved-artists", httpapi.SaveArtistRequest{Name: ptr("  ")})
	alice.want(http.StatusBadRequest, "POST", "/me/saved-artists", httpapi.SaveArtistRequest{Name: ptr("!!!")})
	alice.want(http.StatusBadRequest, "POST", "/me/saved-artists", httpapi.SaveArtistRequest{ItemId: &item, ArtistIndex: ptr(3)})
	alice.want(http.StatusBadRequest, "POST", "/me/saved-artists", httpapi.SaveArtistRequest{ItemId: ptr("nope")})

	alice.want(http.StatusNoContent, "DELETE", "/me/saved-artists/"+a.Id, nil)
	alice.want(http.StatusNotFound, "DELETE", "/me/saved-artists/"+a.Id, nil)
	alice.want(http.StatusOK, "GET", "/me/saved-artists", nil).decode(t, &list)
	if len(list) != 1 {
		t.Errorf("after unsaving: %+v", list)
	}
}
