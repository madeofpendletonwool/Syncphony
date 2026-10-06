// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func TestHistoryAndStatsAPI(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	alice := e.admin()
	bob := e.member(alice, "bob")
	var room httpapi.Room
	alice.want(http.StatusCreated, "POST", "/rooms", httpapi.CreateRoomRequest{Name: "Living room"}).decode(t, &room)
	base := "/rooms/" + room.Id
	alice.want(http.StatusOK, "POST", base+"/queue", addReq(linkFake(t, alice), "t01", "t02", "t03"))
	var snap httpapi.QueueSnapshot
	bob.want(http.StatusOK, "POST", base+"/queue", addReq(linkFake(t, bob), "t04")).decode(t, &snap)

	// Play everything: three songs one evening, the last the next day.
	night := time.Date(2026, 9, 1, 20, 0, 0, 0, time.UTC)
	starts := []time.Time{night, night.Add(5 * time.Minute), night.Add(10 * time.Minute), night.Add(26 * time.Hour)}
	reasons := []string{store.EndFinished, store.EndSkipped, store.EndFinished, store.EndFinished}
	for i, id := range snap.UpNext {
		if _, err := e.db.StartPlay(ctx, store.StartPlayParams{ID: store.NewID(), RoomID: room.Id, QueueItemID: id, StartedAt: starts[i]}); err != nil {
			t.Fatal(err)
		}
		if err := e.db.EndOpenPlays(ctx, store.EndOpenPlaysParams{
			EndedAt: sql.NullTime{Time: starts[i].Add(time.Minute), Valid: true}, EndReason: sql.NullString{String: reasons[i], Valid: true}, RoomID: room.Id,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// History pages back with before, and filters by person.
	var page []httpapi.PlayedItem
	bob.want(http.StatusOK, "GET", base+"/history?limit=3", nil).decode(t, &page)
	if len(page) != 3 || !page[0].StartedAt.Equal(starts[3]) {
		t.Fatalf("first page: %+v", page)
	}
	bob.want(http.StatusOK, "GET", base+"/history?limit=3&before="+url.QueryEscape(page[2].StartedAt.Format(time.RFC3339Nano)), nil).decode(t, &page)
	if len(page) != 1 || !page[0].StartedAt.Equal(starts[0]) {
		t.Fatalf("second page: %+v", page)
	}
	bobID := me(t, bob).Id
	bob.want(http.StatusOK, "GET", base+"/history?userId="+bobID, nil).decode(t, &page)
	if len(page) != 1 || page[0].Item.AddedBy != bobID {
		t.Fatalf("bob's history: %+v", page)
	}

	// Two sessions: the evening, and the next day.
	var sessions []httpapi.ListeningSession
	bob.want(http.StatusOK, "GET", base+"/sessions", nil).decode(t, &sessions)
	if len(sessions) != 2 || sessions[0].Plays != 1 || sessions[1].Plays != 3 || !sessions[1].StartedAt.Equal(night) {
		t.Fatalf("sessions: %+v", sessions)
	}

	// All-time stats (each play counts up to its song's length), then the evening's recap.
	var st httpapi.RoomStats
	bob.want(http.StatusOK, "GET", base+"/stats", nil).decode(t, &st)
	if st.Plays != 4 || st.Skipped != 1 || st.ListeningMs != (20+25+30+35)*1000 || len(st.TopTracks) != 3 || len(st.People) != 2 || st.First == nil {
		t.Fatalf("stats: %+v", st)
	}
	q := fmt.Sprintf("?from=%s&to=%s", url.QueryEscape(sessions[1].StartedAt.Format(time.RFC3339Nano)),
		url.QueryEscape(sessions[1].EndedAt.Add(time.Millisecond).Format(time.RFC3339Nano)))
	bob.want(http.StatusOK, "GET", base+"/stats"+q, nil).decode(t, &st)
	if st.Plays != 3 || st.Last == nil || !st.Last.StartedAt.Equal(starts[2]) {
		t.Fatalf("recap: %+v", st)
	}
	if r := bob.do("GET", "/rooms/nope/stats", nil); r.status != http.StatusNotFound {
		t.Errorf("stats of a missing room: %d", r.status)
	}

	// Queueing a song again: bob can't borrow from alice's link, until the
	// room lets him.
	if room.Matching != (httpapi.RoomMatching{Fallback: true, Borrow: false}) {
		t.Fatalf("default matching: %+v", room.Matching)
	}
	alices := st.First.Item.Id
	again := httpapi.AddToQueueRequest{Items: []httpapi.TrackToQueue{{FromItemId: &alices}}}
	if r := bob.do("POST", base+"/queue", again); r.status != http.StatusForbidden || r.code() != "cant_borrow" {
		t.Fatalf("borrowing: %d %s", r.status, r.body)
	}
	alice.want(http.StatusOK, "PATCH", base, httpapi.UpdateRoomRequest{Matching: &httpapi.RoomMatching{Fallback: true, Borrow: true}}).decode(t, &room)
	if !room.Matching.Borrow {
		t.Fatalf("matching: %+v", room.Matching)
	}
	bob.want(http.StatusOK, "POST", base+"/queue", again).decode(t, &snap)
	n := 0
	for _, it := range snap.Items {
		if it.AddedBy == bobID && it.Track.TrackId == st.First.Item.Track.TrackId && it.State == "queued" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("bob's lane: %+v", snap.Items)
	}
}
