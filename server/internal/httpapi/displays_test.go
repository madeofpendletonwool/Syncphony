// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

// pairTV pairs a fresh client as a display of roomID, by way of someone in the room.
func pairTV(t *testing.T, e *env, by *client, roomID string) *client {
	t.Helper()
	tv, _ := pairTVAudio(t, e, by, roomID, false)
	return tv
}

// pairTVAudio pairs a display, with its audio on or off.
func pairTVAudio(t *testing.T, e *env, by *client, roomID string, audio bool) (*client, httpapi.Display) {
	t.Helper()
	tv := e.client()
	var p httpapi.DisplayPairing
	tv.want(http.StatusCreated, "POST", "/display/pairing", nil).decode(t, &p)
	var d httpapi.Display
	// Typed in as people would: lowercase, with a space.
	by.want(http.StatusCreated, "POST", "/rooms/"+roomID+"/displays", httpapi.PairDisplayRequest{
		Code: strings.ToLower(p.Code[:3] + " " + p.Code[3:]), Name: ptr("Living room TV"), Audio: ptr(audio),
	}).decode(t, &d)
	if d.Audio != audio {
		t.Fatalf("paired display's audio: %+v", d)
	}
	var st httpapi.DisplayPairingStatus
	tv.want(http.StatusOK, "GET", "/display/pairing", nil).decode(t, &st)
	if st.Status != httpapi.Paired || st.RoomId == nil || *st.RoomId != roomID {
		t.Fatalf("pairing status: %+v", st)
	}
	return tv, d
}

func TestDisplayPairing(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	carol := e.member(alice, "carol")
	room := e.room(t, me(t, alice).Id)
	other := e.room(t, me(t, alice).Id)
	item := e.queue(t, room, me(t, bob).Id, "Concert Pitch")

	// Waiting for a code to be typed in.
	tv := e.client()
	if r := tv.do("GET", "/display", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("unpaired display: %d", r.status)
	}
	var p httpapi.DisplayPairing
	tv.want(http.StatusCreated, "POST", "/display/pairing", nil).decode(t, &p)
	if len(p.Code) != 6 {
		t.Fatalf("code: %q", p.Code)
	}
	var st httpapi.DisplayPairingStatus
	tv.want(http.StatusOK, "GET", "/display/pairing", nil).decode(t, &st)
	if st.Status != httpapi.Waiting || st.Code != p.Code {
		t.Fatalf("waiting: %+v", st)
	}
	if r := bob.do("POST", "/rooms/"+room.ID+"/displays", httpapi.PairDisplayRequest{Code: "ZZZZZZ"}); r.status != http.StatusNotFound || r.code() != "pairing_invalid" {
		t.Fatalf("wrong code: %d %s", r.status, r.body)
	}
	// Codes time out.
	e.advance(11 * time.Minute)
	if r := tv.do("GET", "/display/pairing", nil); r.status != http.StatusGone || r.code() != "pairing_expired" {
		t.Fatalf("expired: %d %s", r.status, r.body)
	}

	tv = pairTV(t, e, bob, room.ID)
	var dm httpapi.DisplayMe
	tv.want(http.StatusOK, "GET", "/display", nil).decode(t, &dm)
	if dm.Room.Id != room.ID || dm.Display.Name != "Living room TV" || dm.Display.PairedBy == nil || *dm.Display.PairedBy != me(t, bob).Id {
		t.Fatalf("display: %+v", dm)
	}
	// The code was used up.
	if r := tv.do("GET", "/display/pairing", nil); r.status != http.StatusGone {
		t.Fatalf("pairing after paired: %d", r.status)
	}

	// It reads its own room...
	for _, path := range []string{"/rooms/" + room.ID, "/rooms/" + room.ID + "/queue", "/rooms/" + room.ID + "/playback", "/users"} {
		tv.want(http.StatusOK, "GET", path, nil)
	}
	// (The test song has no artwork or lyrics, but the display may ask.)
	for _, path := range []string{"/artwork", "/lyrics", "/liner-notes"} {
		if r := tv.do("GET", "/rooms/"+room.ID+"/queue/"+item.ID+path, nil); r.status != http.StatusNotFound {
			t.Fatalf("GET %s: %d %s", path, r.status, r.body)
		}
	}
	// ...and nothing else.
	if r := tv.do("GET", "/rooms/"+other.ID+"/queue", nil); r.status != http.StatusForbidden {
		t.Fatalf("other room: %d", r.status)
	}
	for _, path := range []string{"/rooms", "/me", "/links", "/rooms/" + room.ID + "/history"} {
		if r := tv.do("GET", path, nil); r.status != http.StatusUnauthorized {
			t.Fatalf("GET %s: %d", path, r.status)
		}
	}
	if r := tv.do("POST", "/rooms/"+room.ID+"/reactions", httpapi.ReactionRequest{Emoji: "🔥"}); r.status != http.StatusUnauthorized {
		t.Fatalf("display reacting: %d", r.status)
	}

	var ds []httpapi.Display
	carol.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/displays", nil).decode(t, &ds)
	if len(ds) != 1 || ds[0].Id != dm.Display.Id {
		t.Fatalf("displays: %+v", ds)
	}
	// Only the owner, whoever paired it, or an admin may unpair it.
	if r := carol.do("DELETE", "/rooms/"+room.ID+"/displays/"+ds[0].Id, nil); r.status != http.StatusForbidden {
		t.Fatalf("carol unpairing: %d", r.status)
	}
	if r := bob.do("DELETE", "/rooms/"+other.ID+"/displays/"+ds[0].Id, nil); r.status != http.StatusNotFound {
		t.Fatalf("unpairing through another room: %d", r.status)
	}
	bob.want(http.StatusNoContent, "DELETE", "/rooms/"+room.ID+"/displays/"+ds[0].Id, nil)
	if r := tv.do("GET", "/rooms/"+room.ID+"/queue", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("unpaired display reading: %d", r.status)
	}

	// A display can unpair itself.
	tv = pairTV(t, e, alice, room.ID)
	tv.want(http.StatusNoContent, "DELETE", "/display", nil)
	if r := tv.do("GET", "/display", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("after leaving: %d", r.status)
	}
}

func TestDisplaySocketAndReactions(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	aliceMe := me(t, alice)
	room := e.room(t, aliceMe.Id)
	other := e.room(t, aliceMe.Id)
	tv := pairTV(t, e, alice, room.ID)

	a := alice.mustDial(room.ID, "")
	a.expect("hello", nil)

	// The display watches without being in the room.
	d := tv.mustDial(room.ID, "")
	var hello httpapi.RoomHello
	d.expect("hello", &hello)
	if hello.You != "" || len(hello.Members) != 1 || hello.ServerTime.IsZero() {
		t.Fatalf("display hello: %+v", hello)
	}
	d.expect("queue.updated", nil)
	d.expect("nowplaying.updated", nil)
	if _, status, err := tv.dialRoom(other.ID, ""); err == nil || status != http.StatusForbidden {
		t.Fatalf("display dialing another room: %d %v", status, err)
	}
	// So does a signed-in user's big screen.
	big := alice.mustDial(room.ID, "display=1")
	big.expect("hello", nil)

	// Reactions reach everyone.
	bob := e.member(alice, "bob")
	bobMe := me(t, bob)
	if r := bob.do("POST", "/rooms/"+room.ID+"/reactions", map[string]string{"emoji": "💩"}); r.status != http.StatusBadRequest {
		t.Fatalf("unlisted emoji: %d", r.status)
	}
	var sent httpapi.Reaction
	bob.want(http.StatusAccepted, "POST", "/rooms/"+room.ID+"/reactions", httpapi.ReactionRequest{Emoji: "🔥"}).decode(t, &sent)
	var got httpapi.Reaction
	d.await("reaction.sent", &got)
	if got.Id != sent.Id || got.UserId != bobMe.Id || got.Emoji != "🔥" {
		t.Fatalf("reaction: %+v", got)
	}
	a.await("reaction.sent", nil)

	// A burst is fine; a flood isn't.
	limited := false
	for range 20 {
		if r := bob.do("POST", "/rooms/"+room.ID+"/reactions", httpapi.ReactionRequest{Emoji: "🎉"}); r.status == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("reactions weren't rate limited")
	}

	// Unpairing ends the display's socket.
	var ds []httpapi.Display
	alice.want(http.StatusOK, "GET", "/rooms/"+room.ID+"/displays", nil).decode(t, &ds)
	alice.want(http.StatusNoContent, "DELETE", "/rooms/"+room.ID+"/displays/"+ds[0].Id, nil)
	if code := d.closeStatus(); code != websocket.StatusCode(4001) {
		t.Fatalf("display socket closed with %d", code)
	}
}

func TestDisplaySpeaker(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	carol := e.member(alice, "carol")
	var room, other httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Kitchen"}).decode(t, &other)
	base := "/rooms/" + room.Id
	bob.want(http.StatusOK, "POST", base+"/queue", addReq(linkFake(t, bob), "t01"))

	// Without audio, a display only watches.
	tv, d := pairTVAudio(t, e, bob, room.Id, false)
	claim := httpapi.ClaimPlayerRequest{DeviceId: "bobs-phone", Name: "Not the TV"}
	if r := tv.do("PUT", base+"/player", claim); r.status != http.StatusForbidden {
		t.Fatalf("claiming without audio: %d %s", r.status, r.body)
	}

	// Only the owner, whoever paired it, or an admin may turn audio on.
	if r := carol.do("PATCH", base+"/displays/"+d.Id, httpapi.UpdateDisplayRequest{Audio: true}); r.status != http.StatusForbidden {
		t.Fatalf("carol turning audio on: %d %s", r.status, r.body)
	}
	bob.want(http.StatusOK, "PATCH", base+"/displays/"+d.Id, httpapi.UpdateDisplayRequest{Audio: true}).decode(t, &d)
	if !d.Audio {
		t.Fatalf("after turning audio on: %+v", d)
	}
	var dm httpapi.DisplayMe
	tv.want(http.StatusOK, "GET", "/display", nil).decode(t, &dm)
	if !dm.Display.Audio {
		t.Fatalf("display sees its audio off: %+v", dm.Display)
	}

	// It becomes the speaker as itself, for whoever paired it, whatever device it claims to be.
	var np httpapi.NowPlaying
	tv.want(http.StatusOK, "PUT", base+"/player", claim).decode(t, &np)
	if np.Player == nil || np.Player.DeviceId != d.Id || np.Player.Name != "Living room TV" || np.Player.UserId != me(t, bob).Id ||
		np.State != httpapi.PlaybackStateLoading || np.Item == nil {
		t.Fatalf("display claim: %+v", np)
	}
	item := np.Item.Id
	if r := tv.do("GET", base+"/stream/"+item+"?accept=audio/wav", nil); r.status != http.StatusOK || !strings.HasPrefix(string(r.body), "RIFF") {
		t.Fatalf("display streaming: %d %v", r.status, r.header)
	}
	if r := tv.do("GET", "/rooms/"+other.Id+"/stream/"+item, nil); r.status != http.StatusForbidden {
		t.Fatalf("display streaming from another room: %d", r.status)
	}
	tv.want(http.StatusOK, "POST", base+"/player/report", httpapi.PlayerReport{
		DeviceId: "anything", ItemId: item, Event: httpapi.PlayerReportEventPlaying,
	}).decode(t, &np)
	if np.State != httpapi.PlaybackStatePlaying {
		t.Fatalf("after the display's report: %+v", np)
	}
	// It has a speaker's buttons: play, pause and skip, as bob. Nothing else.
	tv.want(http.StatusOK, "POST", base+"/playback", httpapi.PlaybackCommand{Action: httpapi.Pause}).decode(t, &np)
	if np.State != httpapi.PlaybackStatePaused {
		t.Fatalf("after the display paused: %+v", np)
	}
	tv.want(http.StatusOK, "POST", base+"/playback", httpapi.PlaybackCommand{Action: httpapi.Play}).decode(t, &np)
	if np.State != httpapi.PlaybackStatePlaying {
		t.Fatalf("after the display played: %+v", np)
	}
	if r := tv.do("POST", base+"/playback", httpapi.PlaybackCommand{Action: httpapi.Seek, PositionMs: ptr(int64(0))}); r.status != http.StatusForbidden {
		t.Fatalf("display seeking: %d %s", r.status, r.body)
	}
	if r := tv.do("POST", "/rooms/"+other.Id+"/playback", httpapi.PlaybackCommand{Action: httpapi.Pause}); r.status != http.StatusForbidden {
		t.Fatalf("display pausing another room: %d %s", r.status, r.body)
	}
	// Bob's own phone isn't the TV.
	if r := bob.do("POST", base+"/player/report", httpapi.PlayerReport{DeviceId: "bobs-phone", ItemId: item, Event: "ended"}); r.status != http.StatusConflict {
		t.Fatalf("bob reporting as his phone: %d %s", r.status, r.body)
	}

	// Turning audio off stops it, and the room pauses.
	alice.want(http.StatusOK, "PATCH", base+"/displays/"+d.Id, httpapi.UpdateDisplayRequest{Audio: false})
	var off httpapi.NowPlaying
	alice.want(http.StatusOK, "GET", base+"/playback", nil).decode(t, &off)
	if off.Player != nil || off.State != httpapi.PlaybackStatePaused {
		t.Fatalf("after audio off: %+v", off)
	}
	if r := tv.do("POST", base+"/player/report", httpapi.PlayerReport{DeviceId: d.Id, ItemId: item, Event: "progress"}); r.status != http.StatusForbidden {
		t.Fatalf("reporting with audio off: %d %s", r.status, r.body)
	}
	if r := tv.do("GET", base+"/stream/"+item, nil); r.status != http.StatusForbidden {
		t.Fatalf("streaming with audio off: %d", r.status)
	}
	if r := tv.do("POST", base+"/playback", httpapi.PlaybackCommand{Action: httpapi.Play}); r.status != http.StatusForbidden {
		t.Fatalf("playing with audio off: %d %s", r.status, r.body)
	}

	// A display can let go itself; unpairing one that's the speaker stops it too.
	bob.want(http.StatusOK, "PATCH", base+"/displays/"+d.Id, httpapi.UpdateDisplayRequest{Audio: true})
	tv.want(http.StatusOK, "PUT", base+"/player", claim)
	var released httpapi.NowPlaying
	tv.want(http.StatusOK, "DELETE", base+"/player?deviceId=x", nil).decode(t, &released)
	if released.Player != nil {
		t.Fatalf("display releasing: %+v", released)
	}
	tv.want(http.StatusOK, "PUT", base+"/player", claim)
	bob.want(http.StatusNoContent, "DELETE", base+"/displays/"+d.Id, nil)
	var unpaired httpapi.NowPlaying
	alice.want(http.StatusOK, "GET", base+"/playback", nil).decode(t, &unpaired)
	if unpaired.Player != nil {
		t.Fatalf("after unpairing the speaker: %+v", unpaired)
	}
}
