// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

// roomDevices fetches a room's devices, by ID.
func roomDevices(t *testing.T, c *client, roomID string) map[string]httpapi.RoomDevice {
	t.Helper()
	var ds []httpapi.RoomDevice
	c.want(http.StatusOK, "GET", "/rooms/"+roomID+"/devices", nil).decode(t, &ds)
	out := map[string]httpapi.RoomDevice{}
	for _, d := range ds {
		out[d.Id] = d
	}
	return out
}

func mustUnmarshal(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

// awaitOnline waits until a paired display's socket is counted.
func awaitOnline(t *testing.T, c *client, roomID, displayID string, online bool) {
	t.Helper()
	for i := 0; displayByName(t, c, roomID, displayID).Online != online; i++ {
		if i > 50 {
			t.Fatalf("display %s never went online=%v", displayID, online)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRoomDevicesList(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	carol := e.member(alice, "carol")
	room := e.room(t, me(t, bob).Id)
	tv, d := pairTVAudio(t, e, bob, room.ID, true)

	b := bob.mustDial(room.ID, "device=bob-mac&kind=computer&name=Mac")
	b.expect("hello", nil)
	// Its own coming, first.
	b.await("devices.updated", nil)
	tvSock := tv.mustDial(room.ID, "")
	tvSock.expect("hello", nil)
	awaitOnline(t, bob, room.ID, d.Id, true)
	c := carol.mustDial(room.ID, "device=carol-phone&kind=phone&name=iPhone")
	c.expect("hello", nil)
	// Carol coming tells the room.
	var changed httpapi.DevicesChanged
	b.await("devices.updated", &changed)
	if changed.RoomId != room.ID {
		t.Fatalf("devices.updated: %+v", changed)
	}

	// Bob owns the room and paired the screen: he may hand the speaker to
	// it and to his own computer, but not to carol's phone. He may stop it.
	got := roomDevices(t, bob, room.ID)
	if len(got) != 3 {
		t.Fatalf("bob sees %d devices: %+v", len(got), got)
	}
	if s := got[d.Id]; s.Kind != httpapi.RoomDeviceKindScreen || !s.Online || !s.CanSpeak || !s.CanStop || s.DisplayId == nil || s.UserId != nil {
		t.Fatalf("screen, as bob: %+v", s)
	}
	if m := got["bob-mac"]; m.Kind != httpapi.RoomDeviceKindComputer || m.Name != "Mac" || !m.CanSpeak || !m.CanStop || m.UserId == nil || *m.UserId != me(t, bob).Id {
		t.Fatalf("bob's mac, as bob: %+v", m)
	}
	if p := got["carol-phone"]; p.Kind != httpapi.RoomDeviceKindPhone || p.CanSpeak || !p.CanStop {
		t.Fatalf("carol's phone, as bob: %+v", p)
	}

	// Carol may do nothing to the screen or bob's computer, and her phone
	// only becomes the speaker from itself.
	got = roomDevices(t, carol, room.ID)
	if s := got[d.Id]; s.CanSpeak || s.CanStop {
		t.Fatalf("screen, as carol: %+v", s)
	}
	if m := got["bob-mac"]; m.CanSpeak || m.CanStop {
		t.Fatalf("bob's mac, as carol: %+v", m)
	}
	if p := got["carol-phone"]; p.CanSpeak || !p.CanStop {
		t.Fatalf("carol's phone, as carol: %+v", p)
	}

	// A browser that says nothing about itself counts as a phone; a big
	// screen opened while signed in is a screen.
	big := carol.mustDial(room.ID, "device=carol-tv&display=1")
	big.expect("hello", nil)
	b.await("devices.updated", nil)
	old := alice.mustDial(room.ID, "device=alice-old")
	old.expect("hello", nil)
	b.await("devices.updated", nil)
	got = roomDevices(t, carol, room.ID)
	if s := got["carol-tv"]; s.Kind != httpapi.RoomDeviceKindScreen || !s.CanSpeak {
		t.Fatalf("carol's big screen: %+v", s)
	}
	if o := got["alice-old"]; o.Kind != httpapi.RoomDeviceKindPhone {
		t.Fatalf("a browser that didn't say: %+v", o)
	}

	// Leaving: gone from the list. A paired screen stays, offline.
	_ = c.c.CloseNow()
	b.await("devices.updated", nil)
	_ = tvSock.c.CloseNow()
	awaitOnline(t, bob, room.ID, d.Id, false)
	got = roomDevices(t, bob, room.ID)
	if _, ok := got["carol-phone"]; ok {
		t.Fatalf("carol's phone still listed: %+v", got)
	}
	if s := got[d.Id]; s.Online || s.CanSpeak {
		t.Fatalf("offline screen: %+v", s)
	}
}

func TestMakeDeviceSpeaker(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	carol := e.member(alice, "carol")
	room := e.room(t, me(t, bob).Id)
	tv, d := pairTVAudio(t, e, bob, room.ID, true)
	quiet, qd := pairTVAudio(t, e, bob, room.ID, false)
	path := func(id string) string { return "/rooms/" + room.ID + "/devices/" + id + "/speaker" }

	tvSock := tv.mustDial(room.ID, "")
	tvSock.expect("hello", nil)
	quietSock := quiet.mustDial(room.ID, "")
	quietSock.expect("hello", nil)
	awaitOnline(t, bob, room.ID, d.Id, true)
	awaitOnline(t, bob, room.ID, qd.Id, true)
	b := bob.mustDial(room.ID, "device=bob-mac&kind=computer&name=Mac")
	b.expect("hello", nil)
	bp := bob.mustDial(room.ID, "device=bob-phone&kind=phone&name=iPhone")
	bp.expect("hello", nil)
	c := carol.mustDial(room.ID, "device=carol-mac&kind=computer&name=Mac")
	c.expect("hello", nil)

	// The screen hears it on its own socket.
	bob.want(http.StatusAccepted, "PUT", path(d.Id), nil)
	var cmd httpapi.DeviceCommand
	tvSock.await("device.command", &cmd)
	if cmd.Action != httpapi.Speak || cmd.DeviceId != d.Id || cmd.RoomId != room.ID {
		t.Fatalf("screen got %+v", cmd)
	}
	// Carol can't manage it; nor play through a screen with its audio off.
	if r := carol.do("PUT", path(d.Id), nil); r.status != http.StatusForbidden {
		t.Fatalf("carol making the screen the speaker: %d %s", r.status, r.body)
	}
	if r := bob.do("PUT", path(qd.Id), nil); r.status != http.StatusForbidden {
		t.Fatalf("a screen without audio: %d %s", r.status, r.body)
	}

	// Bob's computer hears it on his sockets, with its device ID.
	bob.want(http.StatusAccepted, "PUT", path("bob-mac"), nil)
	b.await("device.command", &cmd)
	if cmd.Action != httpapi.Speak || cmd.DeviceId != "bob-mac" {
		t.Fatalf("bob's mac got %+v", cmd)
	}
	// Not someone else's computer, not a phone, not a browser that's gone.
	if r := bob.do("PUT", path("carol-mac"), nil); r.status != http.StatusForbidden {
		t.Fatalf("bob making carol's mac the speaker: %d %s", r.status, r.body)
	}
	if r := bob.do("PUT", path("bob-phone"), nil); r.status != http.StatusConflict || r.code() != "device_is_phone" {
		t.Fatalf("bob's phone: %d %s", r.status, r.body)
	}
	if r := bob.do("PUT", path("nowhere"), nil); r.status != http.StatusConflict || r.code() != "device_offline" {
		t.Fatalf("an unknown device: %d %s", r.status, r.body)
	}
	_ = tvSock.c.CloseNow()
	awaitOnline(t, bob, room.ID, d.Id, false)
	if r := bob.do("PUT", path(d.Id), nil); r.status != http.StatusConflict || r.code() != "display_offline" {
		t.Fatalf("an offline screen: %d %s", r.status, r.body)
	}

	// Only the owner may be the speaker: carol can't hand it to her own computer.
	owner := httpapi.PermissionLevelOwner
	bob.want(http.StatusOK, "PATCH", "/rooms/"+room.ID, httpapi.UpdateRoomRequest{Permissions: &httpapi.RoomPermissionsChange{Speaker: &owner}})
	if r := carol.do("PUT", path("carol-mac"), nil); r.status != http.StatusForbidden {
		t.Fatalf("carol, with the speaker owner-only: %d %s", r.status, r.body)
	}
}

func TestStopDeviceSpeaker(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	carol := e.member(alice, "carol")
	room := e.room(t, me(t, bob).Id)
	base := "/rooms/" + room.ID
	tv, d := pairTVAudio(t, e, bob, room.ID, true)
	tvSock := tv.mustDial(room.ID, "")
	tvSock.expect("hello", nil)
	path := func(id string) string { return base + "/devices/" + id + "/speaker" }

	// Nothing to stop yet.
	if r := bob.do("DELETE", path(d.Id), nil); r.status != http.StatusConflict || r.code() != "not_player" {
		t.Fatalf("stopping a screen that isn't the speaker: %d %s", r.status, r.body)
	}

	tv.want(http.StatusOK, "PUT", base+"/player", httpapi.ClaimPlayerRequest{DeviceId: d.Id, Name: "TV"})
	if r := carol.do("DELETE", path(d.Id), nil); r.status != http.StatusForbidden {
		t.Fatalf("carol stopping the screen: %d %s", r.status, r.body)
	}
	var np httpapi.NowPlaying
	bob.want(http.StatusOK, "DELETE", path(d.Id), nil).decode(t, &np)
	if np.Player != nil {
		t.Fatalf("still a speaker: %+v", np.Player)
	}
	// The screen hears it's stopped before it hears it lost the room, so
	// it doesn't claim the room straight back.
	speaking := false
	for {
		ev := tvSock.next()
		if ev.Type == "nowplaying.updated" {
			var got httpapi.NowPlaying
			mustUnmarshal(t, ev.Data, &got)
			if got.Player != nil {
				speaking = true
			} else if speaking {
				t.Fatal("nowplaying without a speaker came before the stop")
			}
			continue
		}
		if ev.Type == "device.command" {
			var cmd httpapi.DeviceCommand
			mustUnmarshal(t, ev.Data, &cmd)
			if cmd.Action != httpapi.Stop || cmd.DeviceId != d.Id {
				t.Fatalf("screen got %+v", cmd)
			}
			break
		}
	}

	// Carol's own computer: she may stop it, so may the owner; alice is an admin.
	c := carol.mustDial(room.ID, "device=carol-mac&kind=computer")
	c.expect("hello", nil)
	for _, who := range []*client{carol, bob, alice} {
		carol.want(http.StatusOK, "PUT", base+"/player", httpapi.ClaimPlayerRequest{DeviceId: "carol-mac", Name: "Carol's Mac"})
		who.want(http.StatusOK, "DELETE", path("carol-mac"), nil)
	}
	var cmd httpapi.DeviceCommand
	c.await("device.command", &cmd)
	if cmd.Action != httpapi.Stop || cmd.DeviceId != "carol-mac" {
		t.Fatalf("carol's mac got %+v", cmd)
	}
	// But not someone else in the room.
	dave := e.member(alice, "dave")
	carol.want(http.StatusOK, "PUT", base+"/player", httpapi.ClaimPlayerRequest{DeviceId: "carol-mac", Name: "Carol's Mac"})
	if r := dave.do("DELETE", path("carol-mac"), nil); r.status != http.StatusForbidden {
		t.Fatalf("dave stopping carol's mac: %d %s", r.status, r.body)
	}
}
