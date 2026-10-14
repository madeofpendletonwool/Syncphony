// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

func TestRoomScreens(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	base := "/rooms/" + room.Id

	// Big screens default to the stage, with visuals for songs with no words.
	// Chat floats by.
	if !sameScreens(room.Screens, httpapi.RoomScreens{Look: httpapi.Auto, Scene: "", Intensity: 1.5, Chat: ptr(httpapi.RoomScreensChatFloating)}) {
		t.Fatalf("default screens: %+v", room.Screens)
	}
	want := httpapi.RoomScreens{Look: httpapi.Visualizer, Scene: "fluid", Intensity: 1.75, Chat: ptr(httpapi.RoomScreensChatLog)}
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Screens: &want}).decode(t, &room)
	if !sameScreens(room.Screens, want) {
		t.Fatalf("screens: %+v", room.Screens)
	}
	// Left out, chat stays as it was.
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Screens: &httpapi.RoomScreens{Look: httpapi.Visualizer, Scene: "fluid", Intensity: 1.75}}).decode(t, &room)
	if !sameScreens(room.Screens, want) {
		t.Fatalf("screens without chat: %+v", room.Screens)
	}
	if r := alice.do("PATCH", base, httpapi.UpdateRoomRequest{Screens: &httpapi.RoomScreens{Look: httpapi.Stage, Intensity: 1, Chat: ptr(httpapi.RoomScreensChat("loud"))}}); r.status != http.StatusBadRequest {
		t.Fatalf("chat loud: %d %s", r.status, r.body)
	}
	// Only whoever manages the room changes them, and only to something sensible.
	if r := bob.do("PATCH", base, httpapi.UpdateRoomRequest{Screens: &httpapi.RoomScreens{Look: httpapi.Stage, Intensity: 1}}); r.status != http.StatusForbidden {
		t.Fatalf("bob changed the screens: %d", r.status)
	}
	if r := alice.do("PATCH", base, httpapi.UpdateRoomRequest{Screens: &httpapi.RoomScreens{Look: httpapi.Stage, Intensity: 9}}); r.status != http.StatusBadRequest {
		t.Fatalf("intensity 9: %d %s", r.status, r.body)
	}
	if r := alice.do("PATCH", base, httpapi.UpdateRoomRequest{Screens: &httpapi.RoomScreens{Look: "disco", Intensity: 1}}); r.status != http.StatusBadRequest {
		t.Fatalf("look disco: %d %s", r.status, r.body)
	}
	alice.want(http.StatusOK, "GET", base, nil).decode(t, &room)
	if !sameScreens(room.Screens, want) {
		t.Fatalf("screens after bad changes: %+v", room.Screens)
	}
}

func sameScreens(a, b httpapi.RoomScreens) bool {
	return a.Look == b.Look && a.Scene == b.Scene && a.Intensity == b.Intensity && (a.Chat == nil) == (b.Chat == nil) && (a.Chat == nil || *a.Chat == *b.Chat)
}
