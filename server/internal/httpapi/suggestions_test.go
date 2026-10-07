// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

func TestSuggestionsAPI(t *testing.T) {
	e := newEnv(t)
	e.api.Suggest.Rand = func(int) int { return 0 } // the top of each list
	alice := e.admin()
	bob := e.member(alice, "bob")
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	base := "/rooms/" + room.Id
	aliceLink := linkFake(t, alice)
	var snap httpapi.QueueSnapshot
	alice.want(http.StatusOK, "POST", base+"/queue", addReq(aliceLink, "t01")).decode(t, &snap)
	aliceID := me(t, alice).Id

	// Your vibe is the default: like what you've queued, not including it.
	var sg httpapi.Suggestions
	alice.want(http.StatusOK, "GET", base+"/suggestions?limit=3", nil).decode(t, &sg)
	if sg.Scope != httpapi.SuggestionsScopeMine || len(sg.Items) != 3 {
		t.Fatalf("alice's vibe: %+v", sg)
	}
	first := sg.Items[0]
	if first.Track.TrackId != "t02" || first.Track.LinkId != aliceLink {
		t.Errorf("first suggestion: %+v", first.Track)
	}
	b := first.Because
	if b.ItemId != snap.UpNext[0] || b.Title != "Reference Tone" || b.Artist == nil || *b.Artist != "The Test Patterns" || b.UserId != aliceID {
		t.Errorf("because: %+v", b)
	}

	// Bob has nothing of his own in the room, and no services to add from.
	bob.want(http.StatusOK, "GET", base+"/suggestions", nil).decode(t, &sg)
	if len(sg.Items) != 0 {
		t.Errorf("bob's vibe, with nothing queued: %+v", sg.Items)
	}
	bob.want(http.StatusOK, "GET", base+"/suggestions?scope=group", nil).decode(t, &sg)
	if len(sg.Items) != 0 {
		t.Errorf("group vibe for bob, with no services: %+v", sg.Items)
	}
	// Once he links one, the group's vibe comes from it.
	bobLink := linkFake(t, bob)
	bob.want(http.StatusOK, "GET", base+"/suggestions?scope=group&refresh=true", nil).decode(t, &sg)
	if sg.Scope != httpapi.SuggestionsScopeGroup || len(sg.Items) == 0 || sg.Items[0].Track.LinkId != bobLink || sg.Items[0].Because.UserId != aliceID {
		t.Errorf("group vibe for bob: %+v", sg)
	}

	// The vibe can follow what's queued instead of what's played through.
	alice.want(http.StatusOK, "GET", base+"/suggestions?source=queue", nil).decode(t, &sg)
	if sg.Scope != httpapi.SuggestionsScopeMine || len(sg.Items) == 0 {
		t.Errorf("vibe from the queue: %+v", sg)
	}

	if r := alice.do("GET", base+"/suggestions?scope=theirs", nil); r.status != http.StatusBadRequest {
		t.Errorf("unknown scope: %d %s", r.status, r.body)
	}
	if r := alice.do("GET", base+"/suggestions?source=now", nil); r.status != http.StatusBadRequest {
		t.Errorf("unknown source: %d %s", r.status, r.body)
	}
	if r := alice.do("GET", "/rooms/nope/suggestions", nil); r.status != http.StatusNotFound {
		t.Errorf("missing room: %d", r.status)
	}
}
