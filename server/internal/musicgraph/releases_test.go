// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph_test

import (
	"errors"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

func TestDeezerArtistAlbums(t *testing.T) {
	e := newEnv(t)
	e.http.routes["/deezer/search/artist?Big Thief"] = `{"data":[{"id":7,"name":"Big Thief","nb_fan":73053},{"id":8,"name":"Big Chief","nb_fan":486}]}`
	e.http.routes["/deezer/artist/7/albums"] = `{"data":[
		{"id":804726201,"title":"Double Infinity","release_date":"2025-09-05","record_type":"album","md5_image":"` + coverID + `"},
		{"id":223922482,"title":"Live at The Bunker Studio","release_date":"2021-05-11","record_type":"ep","cover_medium":"https://e-cdns-images.dzcdn.net/images/cover/` + coverID + `/250x250-000000-80-0-0.jpg"},
		{"id":0,"title":"no ID"}]}`
	d := musicgraph.NewDeezer(musicgraph.DeezerOptions{BaseURL: e.http.URL + "/deezer", Interval: -1})
	got, err := d.ArtistAlbums(t.Context(), "Big Thief")
	if err != nil {
		t.Fatal(err)
	}
	want := []musicgraph.DeezerAlbum{
		{ID: 804726201, Title: "Double Infinity", Date: "2025-09-05", Type: "album", Cover: coverID},
		{ID: 223922482, Title: "Live at The Bunker Studio", Date: "2021-05-11", Type: "ep", Cover: coverID},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("albums:\n got %+v\nwant %+v", got, want)
	}
	if _, err := d.ArtistAlbums(t.Context(), "Nobody"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("an unknown artist: %v", err)
	}
}

func TestDeezerFindAlbumAndItsTracks(t *testing.T) {
	e := newEnv(t)
	e.http.routes[`/deezer/search/album?artist:"Wednesday" album:"Bleeds"`] = `{"data":[
		{"id":1,"title":"Bleeds (Live)","artist":{"name":"Wednesday"}},
		{"id":2,"title":"Bleeds (Deluxe)","record_type":"album","release_date":"2025-09-19","md5_image":"` + coverID + `","artist":{"name":"Wednesday"}}]}`
	e.http.routes["/deezer/album/2/tracks"] = `{"data":[
		{"id":21,"title":"Reality TV Argument Bleeds","isrc":"US0000000001","preview":"https://cdnt-preview.dzcdn.net/a.mp3","artist":{"name":"Wednesday"}},
		{"id":22,"title":"Elderberry Wine","artist":{"name":"Wednesday"}}]}`
	d := musicgraph.NewDeezer(musicgraph.DeezerOptions{BaseURL: e.http.URL + "/deezer", Interval: -1})
	al, err := d.FindAlbum(t.Context(), "Wednesday", "Bleeds")
	if err != nil {
		t.Fatal(err)
	}
	if al.ID != 2 || al.Cover != coverID || al.Date != "2025-09-19" {
		t.Errorf("album: %+v", al)
	}
	tracks, err := d.AlbumTracks(t.Context(), al.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 || tracks[0].ID != 21 || !tracks[0].HasPreview || tracks[0].ISRC != "US0000000001" || tracks[1].HasPreview {
		t.Errorf("tracks: %+v", tracks)
	}
	if _, err := d.FindAlbum(t.Context(), "Wednesday", "Twin Plagues"); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("an album Deezer doesn't have: %v", err)
	}
}

func TestListenBrainzFreshReleases(t *testing.T) {
	e := newEnv(t)
	e.http.routes["/lb/1/explore/fresh-releases/"] = `{"payload":{"releases":[
		{"artist_credit_name":"Wednesday","artist_mbids":["aa"],"release_name":"Bleeds","release_group_mbid":"rg1","release_mbid":"r1",
		 "caa_id":123,"caa_release_mbid":"r1","release_date":"2026-09-19","release_group_primary_type":"Album","release_group_secondary_type":null},
		{"artist_credit_name":"MJ Lenderman","artist_mbids":["bb"],"release_name":"Live at the Grey Eagle","release_group_mbid":"rg2","release_mbid":"r2",
		 "caa_id":null,"caa_release_mbid":null,"release_date":"2026-08-01","release_group_primary_type":"Album","release_group_secondary_type":"Live"},
		{"artist_credit_name":"","release_name":"nobody's"}],"total_count":3}}`
	lb := musicgraph.NewListenBrainz(musicgraph.ListenBrainzOptions{BaseURL: e.http.URL + "/lb", LabsURL: e.http.URL + "/labs", Interval: -1})
	got, err := lb.FreshReleases(t.Context(), 365)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("releases: %+v", got)
	}
	if r := got[0]; r.Title != "Bleeds" || r.Artist != "Wednesday" || r.ArtistMBIDs[0] != "aa" || r.MBID != "rg1" || r.CoverRelease != "r1" || r.Date != "2026-09-19" || r.PrimaryType != "Album" {
		t.Errorf("Bleeds: %+v", r)
	}
	if r := got[1]; r.CoverRelease != "" || r.SecondaryType != "Live" {
		t.Errorf("a live album without a cover: %+v", r)
	}
	req := e.http.requests[len(e.http.requests)-1]
	if q := req.URL.Query(); q.Get("days") != "90" || q.Get("future") != "false" {
		t.Errorf("asked %s", req.URL)
	}
}
