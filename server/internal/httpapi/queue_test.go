// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

// linkFake links the fake provider for c and returns the link ID.
func linkFake(t *testing.T, c *client) string {
	t.Helper()
	var l httpapi.ServiceLink
	c.want(http.StatusCreated, "POST", "/links", httpapi.CreateLinkRequest{Provider: "fake", Fields: demoFields}).decode(t, &l)
	return l.Id
}

func addReq(linkID string, trackIDs ...string) httpapi.AddToQueueRequest {
	var req httpapi.AddToQueueRequest
	for _, id := range trackIDs {
		req.Items = append(req.Items, httpapi.TrackToQueue{LinkId: ptr(linkID), TrackId: ptr(id)})
	}
	return req
}

// titles returns the up-next order as track titles.
func titles(s httpapi.QueueSnapshot) []string {
	byID := map[string]string{}
	for _, it := range s.Items {
		byID[it.Id] = it.Track.Title
	}
	out := []string{}
	for _, id := range s.UpNext {
		out = append(out, byID[id])
	}
	return out
}

func TestQueueAPI(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	room := e.room(t, me(t, alice).Id)
	aliceLink, bobLink := linkFake(t, alice), linkFake(t, bob)
	path := "/rooms/" + room.ID + "/queue"

	sock := bob.mustDial(room.ID, "")
	sock.expect("hello", nil)
	sock.expect("queue.updated", nil)
	sock.expect("nowplaying.updated", nil)

	// t01-t03 are "Reference Tone", "Left Channel", "Right Channel"; t04 is "SMPTE".
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", path, addReq(aliceLink, "t01", "t02", "t03")).decode(t, &snap)
	bob.want(http.StatusOK, "POST", path, addReq(bobLink, "t04")).decode(t, &snap)
	if got := titles(snap); len(got) != 4 || got[0] != "Reference Tone" || got[1] != "SMPTE" || got[2] != "Left Channel" {
		t.Fatalf("up next: %v", got)
	}
	if snap.Version != 2 {
		t.Errorf("version %d, want 2", snap.Version)
	}
	// Queued songs keep their album and artist IDs, for browsing to them.
	if tr := snap.Items[0].Track; tr.AlbumId == nil || *tr.AlbumId == "" || tr.ArtistIds == nil || len(*tr.ArtistIds) != len(tr.Artists) || (*tr.ArtistIds)[0] == "" {
		t.Errorf("queued track IDs: %+v", tr)
	}
	// Bob's socket sees each change, with the order.
	var pushed httpapi.QueueSnapshot
	sock.await("queue.updated", &pushed)
	if ev := sock.await("queue.updated", &pushed); ev.Version != 2 || len(pushed.UpNext) != 4 {
		t.Fatalf("pushed: version %d, %+v", ev.Version, pushed)
	}

	var got httpapi.QueueSnapshot
	bob.want(http.StatusOK, "GET", path, nil).decode(t, &got)
	if got.Version != 2 || len(got.UpNext) != 4 {
		t.Fatalf("GET queue: %+v", got)
	}

	// Alice moves "Right Channel" to the front of her lane.
	right := snap.UpNext[3]
	alice.want(http.StatusOK, "PATCH", path+"/"+right, httpapi.MoveQueueItemRequest{Position: 0}).decode(t, &snap)
	if got := titles(snap); got[0] != "Right Channel" || got[1] != "SMPTE" {
		t.Fatalf("after move: %v", got)
	}

	smpte := snap.UpNext[1]
	for _, tc := range []struct {
		name         string
		c            *client
		method, path string
		body         any
		status       int
		code         string
	}{
		{"bob reorders alice's song", bob, "PATCH", path + "/" + right, httpapi.MoveQueueItemRequest{Position: 1}, http.StatusForbidden, "forbidden"},
		{"bob removes alice's song", bob, "DELETE", path + "/" + right, nil, http.StatusForbidden, "forbidden"},
		{"missing item", bob, "DELETE", path + "/nope", nil, http.StatusNotFound, "not_found"},
		{"missing room", bob, "GET", "/rooms/nope/queue", nil, http.StatusNotFound, "not_found"},
		{"adding nothing", bob, "POST", path, httpapi.AddToQueueRequest{}, http.StatusBadRequest, "invalid_input"},
		{"someone else's link", bob, "POST", path, addReq(aliceLink, "t01"), http.StatusNotFound, "not_found"},
		{"a track that doesn't exist", bob, "POST", path, addReq(bobLink, "nope"), http.StatusNotFound, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r := tc.c.do(tc.method, tc.path, tc.body); r.status != tc.status || r.code() != tc.code {
				t.Errorf("got %d %s", r.status, r.body)
			}
		})
	}
	if r := e.client().do("GET", path, nil); r.status != http.StatusUnauthorized {
		t.Errorf("signed out: %d", r.status)
	}

	// The room owner can remove anyone's song.
	alice.want(http.StatusOK, "DELETE", path+"/"+smpte, nil).decode(t, &snap)
	if got := titles(snap); len(got) != 3 || got[0] != "Right Channel" {
		t.Fatalf("after removing SMPTE: %v", got)
	}
	if r := alice.do("DELETE", path+"/"+smpte, nil); r.status != http.StatusConflict || r.code() != "not_queued" {
		t.Errorf("removing twice: %d %s", r.status, r.body)
	}

	// Undo: bob can't put back what alice removed; alice can.
	if r := bob.do("POST", path+"/restore", httpapi.RestoreQueueItemsRequest{ItemIds: []string{smpte}}); r.status != http.StatusForbidden {
		t.Errorf("bob restoring: %d %s", r.status, r.body)
	}
	alice.want(http.StatusOK, "POST", path+"/restore", httpapi.RestoreQueueItemsRequest{ItemIds: []string{smpte}}).decode(t, &snap)
	if got := titles(snap); len(got) != 4 || got[1] != "SMPTE" {
		t.Fatalf("after restoring SMPTE: %v", got)
	}
	if r := alice.do("POST", path+"/restore", httpapi.RestoreQueueItemsRequest{ItemIds: []string{smpte}}); r.status != http.StatusConflict || r.code() != "undo_expired" {
		t.Errorf("restoring a waiting song: %d %s", r.status, r.body)
	}

	// Clearing your lane, and undoing it.
	var cleared httpapi.ClearedLane
	alice.want(http.StatusOK, "DELETE", "/rooms/"+room.ID+"/lane", nil).decode(t, &cleared)
	if got := titles(cleared.Queue); len(cleared.Removed) != 3 || len(got) != 1 || got[0] != "SMPTE" {
		t.Fatalf("after clearing: %v, removed %v", got, cleared.Removed)
	}
	alice.want(http.StatusOK, "POST", path+"/restore", httpapi.RestoreQueueItemsRequest{ItemIds: cleared.Removed}).decode(t, &snap)
	if got := titles(snap); len(got) != 4 || got[0] != "Right Channel" {
		t.Fatalf("after undoing the clear: %v", got)
	}
}
