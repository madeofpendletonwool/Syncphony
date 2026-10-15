// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

// report sends a box's status up a paired display's socket, the way /tv
// on a box does (ADR 0016).
func (s *socket) report(data map[string]any) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(s.t.Context(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, s.c, map[string]any{"type": "box.status", "data": data}); err != nil {
		s.t.Fatalf("sending box status: %v", err)
	}
}

// displayByName finds a room's display in the Screens list.
func displayByName(t *testing.T, c *client, roomID, id string) httpapi.Display {
	t.Helper()
	var ds []httpapi.Display
	c.want(http.StatusOK, "GET", "/rooms/"+roomID+"/displays", nil).decode(t, &ds)
	for _, d := range ds {
		if d.Id == id {
			return d
		}
	}
	t.Fatalf("display %s not listed: %+v", id, ds)
	return httpapi.Display{}
}

func TestBoxStatusInTheScreensList(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	room := e.room(t, me(t, alice).Id)
	tv, d := pairTVAudio(t, e, alice, room.ID, true)

	// Paired but not connected: offline, and nothing known about a box.
	if got := displayByName(t, alice, room.ID, d.Id); got.Online || got.Box != nil {
		t.Fatalf("before connecting: %+v", got)
	}

	a := alice.mustDial(room.ID, "")
	a.expect("hello", nil)
	sock := tv.mustDial(room.ID, "")
	sock.expect("hello", nil)
	// Coming online tells the room.
	a.await("displays.updated", nil)
	if got := displayByName(t, alice, room.ID, d.Id); !got.Online {
		t.Fatalf("connected display isn't online: %+v", got)
	}

	sock.report(map[string]any{
		"version": "v0.1.0", "name": "Living room TV", "capabilities": []string{"reboot", "reload", "cec"},
		"temperatureC": 52.3, "throttled": false, "uptimeSeconds": 3600,
		"update":       map[string]any{"at": "2026-10-16T04:03:00Z", "result": "failed", "error": "apt-get update: exit status 100"},
		"somethingNew": true, // fields the server doesn't know are ignored
	})
	var changed httpapi.DisplaysChanged
	a.await("displays.updated", &changed)
	if changed.RoomId != room.ID {
		t.Fatalf("displays.updated: %+v", changed)
	}
	got := displayByName(t, alice, room.ID, d.Id)
	b := got.Box
	if b == nil || b.Version != "v0.1.0" || b.TemperatureC == nil || *b.TemperatureC != 52.3 ||
		b.UptimeSeconds == nil || *b.UptimeSeconds != 3600 || b.Update == nil || b.Update.Result != httpapi.Failed {
		t.Fatalf("box status: %+v", b)
	}
	// Older than this server expects.
	if !b.Outdated {
		t.Fatalf("v0.1.0 should be outdated against %s", httpapi.MinBoxVersion)
	}

	// Garbage and unknown messages are ignored, and don't break the socket.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_ = wsjson.Write(ctx, sock.c, map[string]any{"type": "box.status", "data": map[string]any{"version": ""}})
	_ = wsjson.Write(ctx, sock.c, map[string]any{"type": "something.else", "data": 1})
	if got := displayByName(t, alice, room.ID, d.Id); got.Box == nil || got.Box.Version != "v0.1.0" {
		t.Fatalf("a bad report replaced a good one: %+v", got.Box)
	}

	// Going away: offline, the last report kept.
	_ = sock.c.CloseNow()
	a.await("displays.updated", nil)
	if got := displayByName(t, alice, room.ID, d.Id); got.Online || got.Box == nil {
		t.Fatalf("after disconnecting: %+v", got)
	}
}

func TestReloadAndRestartReachOnlyThatDisplay(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	room := e.room(t, me(t, alice).Id)
	other := e.room(t, me(t, alice).Id)

	boxTV, boxD := pairTVAudio(t, e, alice, room.ID, true)
	plainTV, plainD := pairTVAudio(t, e, alice, room.ID, false)
	otherTV, _ := pairTVAudio(t, e, alice, other.ID, false)

	boxSock := boxTV.mustDial(room.ID, "")
	boxSock.expect("hello", nil)
	plainSock := plainTV.mustDial(room.ID, "")
	plainSock.expect("hello", nil)
	otherSock := otherTV.mustDial(other.ID, "")
	otherSock.expect("hello", nil)
	member := alice.mustDial(room.ID, "")
	member.expect("hello", nil)
	boxSock.report(map[string]any{"version": "v1.0.0", "capabilities": []string{"reboot", "reload"}})
	// Wait until the server has the report.
	for i := 0; ; i++ {
		if d := displayByName(t, alice, room.ID, boxD.Id); d.Box != nil {
			break
		}
		if i > 50 {
			t.Fatal("box report never arrived")
		}
		time.Sleep(20 * time.Millisecond)
	}

	path := func(id, action string) string { return "/rooms/" + room.ID + "/displays/" + id + "/" + action }

	// Only someone who may manage the screens: bob is in the room, but
	// isn't its owner and didn't pair them.
	for _, action := range []string{"reload", "restart"} {
		if r := bob.do("POST", path(boxD.Id, action), nil); r.status != http.StatusForbidden {
			t.Fatalf("bob %s: %d", action, r.status)
		}
	}
	// A plain screen only reloads.
	if r := alice.do("POST", path(plainD.Id, "restart"), nil); r.status != http.StatusConflict || r.code() != "not_a_box" {
		t.Fatalf("restarting a plain screen: %d %s", r.status, r.code())
	}
	// Not through another room.
	if r := alice.do("POST", "/rooms/"+other.ID+"/displays/"+boxD.Id+"/reload", nil); r.status != http.StatusNotFound {
		t.Fatalf("reload through another room: %d", r.status)
	}

	alice.want(http.StatusAccepted, "POST", path(boxD.Id, "restart"), nil)
	alice.want(http.StatusAccepted, "POST", path(plainD.Id, "reload"), nil)

	var cmd httpapi.DisplayCommand
	boxSock.await("display.command", &cmd)
	if cmd.Action != httpapi.Restart {
		t.Fatalf("box got %+v", cmd)
	}
	// The plain screen's first command is its own reload, not the box's restart.
	plainSock.await("display.command", &cmd)
	if cmd.Action != httpapi.Reload {
		t.Fatalf("plain screen got %+v", cmd)
	}

	// Nobody else saw a command: the member's socket and the other room's
	// display get the next room event without one in between.
	bob.want(http.StatusAccepted, "POST", "/rooms/"+room.ID+"/reactions", httpapi.ReactionRequest{Emoji: "🔥"})
	for {
		ev := member.next()
		if ev.Type == "display.command" {
			t.Fatal("a member's socket got a display command")
		}
		if ev.Type == "reaction.sent" {
			break
		}
	}
	alice.want(http.StatusAccepted, "POST", "/rooms/"+other.ID+"/reactions", httpapi.ReactionRequest{Emoji: "🎉"})
	for {
		ev := otherSock.next()
		if ev.Type == "display.command" {
			t.Fatal("another room's display got a command")
		}
		if ev.Type == "reaction.sent" {
			break
		}
	}

	// Offline: nothing to send to.
	_ = plainSock.c.CloseNow()
	for i := 0; ; i++ {
		if d := displayByName(t, alice, room.ID, plainD.Id); !d.Online {
			break
		}
		if i > 50 {
			t.Fatal("plain screen never went offline")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if r := alice.do("POST", path(plainD.Id, "reload"), nil); r.status != http.StatusConflict || r.code() != "display_offline" {
		t.Fatalf("reloading an offline screen: %d %s", r.status, r.code())
	}
}
