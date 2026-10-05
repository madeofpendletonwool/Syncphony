// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
)

func TestRoomsAPI(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")

	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: " Living room "}).decode(t, &room)
	if room.Name != "Living room" || room.OwnerId != me(t, alice).Id || room.FairnessMode != "round_robin" || room.Controls != "everyone" {
		t.Fatalf("created: %+v", room)
	}
	var list []httpapi.Room
	bob.want(http.StatusOK, "GET", "/rooms", nil).decode(t, &list)
	if len(list) != 1 || list[0].Id != room.Id {
		t.Fatalf("list: %+v", list)
	}
	owner := httpapi.RoomControls("owner")
	fifo := httpapi.FairnessMode("fifo")
	alice.want(http.StatusOK, "PATCH", "/rooms/"+room.Id, httpapi.UpdateRoomRequest{Controls: &owner, FairnessMode: &fifo}).decode(t, &room)
	if room.Controls != "owner" || room.FairnessMode != "fifo" || room.Name != "Living room" {
		t.Fatalf("updated: %+v", room)
	}
	bob.want(http.StatusOK, "GET", "/rooms/"+room.Id, nil).decode(t, &room)

	for _, tc := range []struct {
		name         string
		c            *client
		method, path string
		body         any
		status       int
		code         string
	}{
		{"bob changes alice's room", bob, "PATCH", "/rooms/" + room.Id, httpapi.UpdateRoomRequest{Name: ptr("Mine")}, http.StatusForbidden, "forbidden"},
		{"blank name", alice, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "  "}, http.StatusBadRequest, "invalid_input"},
		{"missing room", bob, "GET", "/rooms/nope", nil, http.StatusNotFound, "not_found"},
		{"bob claims in an owner-controlled room", bob, "PUT", "/rooms/" + room.Id + "/player", httpapi.ClaimPlayerRequest{DeviceId: "tablet", Name: "Tablet"}, http.StatusForbidden, "forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r := tc.c.do(tc.method, tc.path, tc.body); r.status != tc.status || r.code() != tc.code {
				t.Errorf("got %d %s", r.status, r.body)
			}
		})
	}
}

func TestPlaybackAPI(t *testing.T) {
	e := newEnv(t)
	alice := e.admin()
	bob := e.member(alice, "bob")
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	base := "/rooms/" + room.Id
	aliceLink, bobLink := linkFake(t, alice), linkFake(t, bob)

	sock := bob.mustDial(room.Id, "")
	sock.expect("hello", nil)
	sock.expect("queue.updated", nil)
	var np httpapi.NowPlaying
	sock.expect("nowplaying.updated", &np)
	if np.State != httpapi.PlaybackStateIdle || np.Item != nil {
		t.Fatalf("hello now playing: %+v", np)
	}

	if r := bob.do("POST", base+"/playback", httpapi.PlaybackCommand{Action: "play"}); r.status != http.StatusConflict || r.code() != "no_player" {
		t.Fatalf("play with no speaker: %d %s", r.status, r.body)
	}
	alice.want(http.StatusOK, "POST", base+"/queue", addReq(aliceLink, "t01"))
	bob.want(http.StatusOK, "POST", base+"/queue", addReq(bobLink, "t04"))

	// Alice's phone becomes the speaker and gets her song to load.
	alice.want(http.StatusOK, "PUT", base+"/player", httpapi.ClaimPlayerRequest{DeviceId: "phone", Name: "Alice's phone"}).decode(t, &np)
	if np.State != httpapi.PlaybackStateLoading || np.Item == nil || np.Item.Track.Title != "Reference Tone" || np.Player == nil || np.Player.Name != "Alice's phone" ||
		np.Driver == nil || *np.Driver != httpapi.NowPlayingDriverStream || np.Next == nil || np.Next.Track.Title != "SMPTE" {
		t.Fatalf("claim: %+v", np)
	}
	// Bob's socket sees it start (after updates to what's next as songs arrived).
	for {
		var pushed httpapi.NowPlaying
		sock.await("nowplaying.updated", &pushed)
		if pushed.State == httpapi.PlaybackStateLoading {
			if pushed.Item == nil || pushed.Item.Id != np.Item.Id {
				t.Fatalf("pushed: %+v", pushed)
			}
			break
		}
	}
	item := np.Item.Id

	// The speaker streams the song, from an offset.
	r := alice.do("GET", base+"/stream/"+item+"?accept=audio/wav,audio/mpeg", nil, "Range", "bytes=100-199")
	if r.status != http.StatusPartialContent || r.header.Get("Content-Type") != "audio/wav" || r.header.Get("Accept-Ranges") != "bytes" ||
		!strings.HasPrefix(r.header.Get("Content-Range"), "bytes 100-199/") || len(r.body) != 100 || r.header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("ranged stream: %d %v (%d bytes)", r.status, r.header, len(r.body))
	}
	if r := alice.do("GET", base+"/stream/"+item, nil); r.status != http.StatusOK || r.header.Get("Content-Range") != "" || !strings.HasPrefix(string(r.body), "RIFF") {
		t.Fatalf("whole stream: %d %v", r.status, r.header)
	}
	if r := alice.do("GET", base+"/stream/"+item, nil, "Range", "bytes=99999999-"); r.status != http.StatusRequestedRangeNotSatisfiable || r.code() != "range_not_satisfiable" {
		t.Errorf("range past the end: %d %s", r.status, r.body)
	}
	if r := alice.do("GET", base+"/stream/"+item+"?accept=audio/mpeg", nil); r.status != http.StatusUnsupportedMediaType || r.code() != "unsupported_format" {
		t.Errorf("no transcoder: %d %s", r.status, r.body)
	}
	if r := e.client().do("GET", base+"/stream/"+item, nil); r.status != http.StatusUnauthorized {
		t.Errorf("signed out: %d", r.status)
	}

	report := func(c *client, event httpapi.PlayerReportEvent, ms int64) httpapi.NowPlaying {
		t.Helper()
		var np httpapi.NowPlaying
		c.want(http.StatusOK, "POST", base+"/player/report", httpapi.PlayerReport{DeviceId: "phone", ItemId: item, Event: event, PositionMs: ms}).decode(t, &np)
		return np
	}
	if np = report(alice, httpapi.PlayerReportEventPlaying, 0); np.State != httpapi.PlaybackStatePlaying {
		t.Fatalf("after playing: %+v", np)
	}
	if r := bob.do("POST", base+"/player/report", httpapi.PlayerReport{DeviceId: "phone", ItemId: item, Event: "ended"}); r.status != http.StatusConflict || r.code() != "not_player" {
		t.Errorf("bob reporting: %d %s", r.status, r.body)
	}

	// Bob pauses and seeks; everyone sees it.
	e.advance(3 * time.Second)
	bob.want(http.StatusOK, "POST", base+"/playback", httpapi.PlaybackCommand{Action: "pause"}).decode(t, &np)
	if np.State != httpapi.PlaybackStatePaused || np.PositionMs != 3000 {
		t.Fatalf("pause: %+v", np)
	}
	bob.want(http.StatusOK, "POST", base+"/playback", httpapi.PlaybackCommand{Action: "seek", PositionMs: ptr[int64](12000)}).decode(t, &np)
	if np.PositionMs != 12000 {
		t.Fatalf("seek: %+v", np)
	}
	var got httpapi.NowPlaying
	bob.want(http.StatusOK, "GET", base+"/playback", nil).decode(t, &got)
	if got.Revision != np.Revision || got.PositionMs != 12000 {
		t.Fatalf("GET playback: %+v", got)
	}
	if r := bob.do("POST", base+"/playback", httpapi.PlaybackCommand{Action: "rewind"}); r.status != http.StatusBadRequest {
		t.Errorf("bad action: %d %s", r.status, r.body)
	}

	// The song fails on the speaker: skipped with a notice to the room.
	np = report(alice, httpapi.PlayerReportEventError, 12000)
	if np.Item == nil || np.Item.Track.Title != "SMPTE" {
		t.Fatalf("after error: %+v", np)
	}
	var notice httpapi.PlaybackNotice
	sock.await("playback.notice", &notice)
	if notice.ItemId == nil || *notice.ItemId != item || !strings.Contains(notice.Message, "Reference Tone") {
		t.Fatalf("notice: %+v", notice)
	}
	// It already played, so it can't be streamed any more.
	if r := alice.do("GET", base+"/stream/"+item, nil); r.status != http.StatusConflict || r.code() != "not_streamable" {
		t.Errorf("stream an old song: %d %s", r.status, r.body)
	}

	// Releasing the speaker pauses the room.
	if r := bob.do("DELETE", base+"/player?deviceId=phone", nil); r.status != http.StatusForbidden {
		t.Errorf("bob releasing alice's phone: %d %s", r.status, r.body)
	}
	var released httpapi.NowPlaying
	alice.want(http.StatusOK, "DELETE", base+"/player?deviceId=phone", nil).decode(t, &released)
	if np = released; np.Player != nil || np.State != httpapi.PlaybackStatePaused {
		t.Fatalf("release: %+v", np)
	}
}
