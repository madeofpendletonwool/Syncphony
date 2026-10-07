// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

func TestAutopilotExplore(t *testing.T) {
	e := newEnv(t)
	bob := e.member(e.admin(), "bob")
	var room httpapi.Room
	bob.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Bob's"}).decode(t, &room)
	if a := room.Autopilot; a.Explore == nil || *a.Explore != 25 || a.Adventure != httpapi.Similar {
		t.Fatalf("new room's autopilot: %+v", a)
	}
	base := "/rooms/" + room.Id
	// Explore decides; the adventure follows.
	bob.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Autopilot: &httpapi.RoomAutopilot{
		On: true, Adventure: httpapi.Similar, Explore: ptr(80),
	}}).decode(t, &room)
	if a := room.Autopilot; *a.Explore != 80 || a.Adventure != httpapi.Discovery {
		t.Errorf("explore 80: %+v", a)
	}
	// An older client sends only the adventure.
	bob.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Autopilot: &httpapi.RoomAutopilot{
		On: true, Adventure: httpapi.Similar,
	}}).decode(t, &room)
	if a := room.Autopilot; *a.Explore != 25 {
		t.Errorf("adventure only: %+v", a)
	}
	if r := bob.do("PATCH", base, httpapi.UpdateRoomRequest{Autopilot: &httpapi.RoomAutopilot{On: true, Adventure: httpapi.Similar, Explore: ptr(101)}}); r.status != http.StatusBadRequest {
		t.Errorf("explore 101: %d %s", r.status, r.body)
	}
}
