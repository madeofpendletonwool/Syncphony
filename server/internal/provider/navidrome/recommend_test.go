// SPDX-License-Identifier: AGPL-3.0-only

package navidrome_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/navidrome"
)

// songsJSON is a song list under key, with songs s1 (the seed) and s2..sn.
func songsJSON(key string, ids ...string) string {
	var songs []string
	for _, id := range ids {
		songs = append(songs, fmt.Sprintf(`{"id":%q,"title":"Song %s","artist":"Band","artistId":"ar9","album":"LP","albumId":"al9","duration":180}`, id, id))
	}
	return fmt.Sprintf(`%q:{"song":[%s]}`, key, strings.Join(songs, ","))
}

func TestRecommendations(t *testing.T) {
	srv := newServer(t)
	srv.fail = func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch strings.TrimPrefix(r.URL.Path, "/rest/") {
		case "getSimilarSongs":
			if q.Get("id") == "s1" {
				// Navidrome's list can include the seed itself.
				ok(w, songsJSON("similarSongs", "s1", "s2", "s3", "s4"))
				return
			}
			ok(w, `"similarSongs":{}`) // what Navidrome says for an unknown ID
		case "getSimilarSongs2":
			ok(w, songsJSON("similarSongs2", "s5", "s6"))
		case "getTopSongs":
			if q.Get("artist") != "The Square Roots" {
				ok(w, `"topSongs":{}`)
				return
			}
			ok(w, songsJSON("topSongs", "s7"))
		case "getRandomSongs":
			ok(w, songsJSON("randomSongs", "s8", "s9", "s10"))
		default:
			srv.route(w, r)
		}
	}
	r := open(t, navidrome.New(navidrome.Options{}), srv.URL).(provider.Recommender)
	ctx := t.Context()
	ids := func(ts []provider.Track, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tr := range ts {
			if tr.Ref.Provider != navidrome.ID || tr.Ref.LinkID != "link-1" {
				t.Errorf("track %s has ref %+v", tr.Ref.ID, tr.Ref)
			}
			out = append(out, tr.Ref.ID)
		}
		return strings.Join(out, ",")
	}

	if got := ids(r.SimilarToTrack(ctx, "s1", 2)); got != "s2,s3" {
		t.Errorf("SimilarToTrack = %s, want s2,s3 (no seed, limited)", got)
	}
	if got := srv.last("/rest/getSimilarSongs").Get("count"); got != "3" {
		t.Errorf("getSimilarSongs count = %s, want 3 (one spare for the seed)", got)
	}
	if got := ids(r.SimilarToArtist(ctx, "ar1", 10)); got != "s5,s6" {
		t.Errorf("SimilarToArtist = %s", got)
	}
	if got := ids(r.TopTracks(ctx, "The Square Roots", 10)); got != "s7" {
		t.Errorf("TopTracks = %s", got)
	}
	if got := ids(r.TopTracks(ctx, "Nobody", 10)); got != "" {
		t.Errorf("TopTracks(unknown) = %s, want none", got)
	}
	if got := ids(r.RandomTracks(ctx, 2)); got != "s8,s9" {
		t.Errorf("RandomTracks = %s", got)
	}
	if got := srv.last("/rest/getRandomSongs").Get("size"); got != "2" {
		t.Errorf("getRandomSongs size = %s, want 2", got)
	}
	if _, err := r.SimilarToTrack(ctx, "nope", 2); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("SimilarToTrack(unknown) = %v, want ErrNotFound", err)
	}
}
