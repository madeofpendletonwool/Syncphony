// SPDX-License-Identifier: AGPL-3.0-only

package playlists_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/playlists"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func play(prov, trackID, isrc, reason string) rooms.Played {
	meta, _ := json.Marshal(provider.Track{Title: trackID, ISRC: isrc})
	return rooms.Played{
		Item:      store.QueueItem{Provider: prov, TrackID: trackID, Metadata: string(meta), LinkID: sql.NullString{String: "link", Valid: true}},
		EndReason: reason,
	}
}

func ids(ps []rooms.Played) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Item.TrackID)
	}
	return strings.Join(out, " ")
}

func TestNightSongs(t *testing.T) {
	unlinked := play("navidrome", "gone", "", store.EndFinished)
	unlinked.Item.LinkID = sql.NullString{}
	plays := []rooms.Played{
		play("navidrome", "a", "ISRC1", store.EndFinished),
		play("navidrome", "b", "", store.EndSkipped),
		play("navidrome", "c", "", store.EndError),
		play("spotify", "a-sp", "ISRC1", store.EndFinished), // the same song, on another service
		play("navidrome", "d", "", store.EndFinished),
		play("navidrome", "d", "", store.EndFinished),
		unlinked,
	}
	for _, tc := range []struct {
		skipped, repeats bool
		want             string
	}{
		{false, false, "a d"},
		{true, false, "a b d"},
		{false, true, "a a-sp d d"},
		{true, true, "a b a-sp d d"},
	} {
		if got := ids(playlists.NightSongs(plays, tc.skipped, tc.repeats)); got != tc.want {
			t.Errorf("skipped %v, repeats %v: %q, want %q", tc.skipped, tc.repeats, got, tc.want)
		}
	}
}
