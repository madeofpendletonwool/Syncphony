// SPDX-License-Identifier: AGPL-3.0-only

package streaming

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	golibrespot "github.com/devgianlu/go-librespot"
	collectionpb "github.com/devgianlu/go-librespot/proto/spotify/collection/v2"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	playlist4pb "github.com/devgianlu/go-librespot/proto/spotify/playlist4"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify"
)

func TestLibraryPlaylists(t *testing.T) {
	meta := func(name string, length int32, attrs *playlist4pb.ListAttributes) *playlist4pb.MetaItem {
		if attrs == nil {
			attrs = &playlist4pb.ListAttributes{}
		}
		attrs.Name = proto.String(name)
		return &playlist4pb.MetaItem{Attributes: attrs, Length: proto.Int32(length), OwnerUsername: proto.String("alice")}
	}
	entries := []struct {
		uri  string
		meta *playlist4pb.MetaItem
	}{
		{"spotify:playlist:aaaaaaaaaaaaaaaaaaaaaa", meta("Mix", 50, &playlist4pb.ListAttributes{PictureSize: []*playlist4pb.PictureSize{
			{TargetName: proto.String("default"), Url: proto.String("https://mosaic.scdn.co/300/mix")},
			{TargetName: proto.String("large"), Url: proto.String("https://mosaic.scdn.co/640/mix")},
		}})},
		{"spotify:start-group:f00:Road%20trips", &playlist4pb.MetaItem{}},
		{"spotify:playlist:bbbbbbbbbbbbbbbbbbbbbb", meta("Uploaded", 7, &playlist4pb.ListAttributes{Picture: []byte{0xab, 0x67}})},
		{"spotify:playlist:cccccccccccccccccccccc", meta("", 3, nil)},
		{"spotify:playlist:dddddddddddddddddddddd", meta("Gone", 3, &playlist4pb.ListAttributes{DeletedByOwner: proto.Bool(true)})},
		{"spotify:end-group:f00", &playlist4pb.MetaItem{}},
		{"spotify:playlist:eeeeeeeeeeeeeeeeeeeeee", meta("Mosaic", 12, nil)},
	}
	var items []*playlist4pb.Item
	var metas []*playlist4pb.MetaItem
	for _, e := range entries {
		items = append(items, &playlist4pb.Item{Uri: proto.String(e.uri)})
		metas = append(metas, e.meta)
	}
	got := libraryPlaylists(items, metas)
	want := []spotify.LibraryPlaylist{
		{ID: "aaaaaaaaaaaaaaaaaaaaaa", Name: "Mix", Owner: "alice", TrackCount: 50, Images: []spotify.Image{
			{URL: "https://mosaic.scdn.co/300/mix", Width: 300}, {URL: "https://mosaic.scdn.co/640/mix", Width: 640},
		}},
		{ID: "bbbbbbbbbbbbbbbbbbbbbb", Name: "Uploaded", Owner: "alice", TrackCount: 7, Images: []spotify.Image{{URL: imageCDN + "ab67"}}},
		{ID: "eeeeeeeeeeeeeeeeeeeeee", Name: "Mosaic", Owner: "alice", TrackCount: 12},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("libraryPlaylists:\n got %+v\nwant %+v", got, want)
	}
}

func TestLibraryTrack(t *testing.T) {
	gid := bytes.Repeat([]byte{1}, 16)
	m := &metadatapb.Track{
		Name:     proto.String("Sine Wave"),
		Duration: proto.Int32(181500),
		Explicit: proto.Bool(true),
		Artist:   []*metadatapb.Artist{{Gid: gid, Name: proto.String("The Sines")}, {Name: proto.String("")}},
		Album: &metadatapb.Album{Gid: gid, Name: proto.String("Sine Language"), CoverGroup: &metadatapb.ImageGroup{Image: []*metadatapb.Image{
			{FileId: []byte{0xab}, Size: metadatapb.Image_SMALL.Enum()},
			{FileId: []byte{0xcd}, Size: metadatapb.Image_LARGE.Enum(), Width: proto.Int32(650)},
			{Size: metadatapb.Image_DEFAULT.Enum()},
		}}},
	}
	id := golibrespot.GidToBase62(gid)
	got := libraryTrack("trackSine0000000000000", m)
	want := spotify.LibraryTrack{
		ID:         "trackSine0000000000000",
		Name:       "Sine Wave",
		Artists:    []provider.ArtistCredit{{ID: id, Name: "The Sines"}},
		AlbumID:    id,
		AlbumName:  "Sine Language",
		AlbumCover: []spotify.Image{{URL: imageCDN + "ab", Width: 64}, {URL: imageCDN + "cd", Width: 650}},
		Duration:   181500 * time.Millisecond,
		Explicit:   true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("libraryTrack:\n got %+v\nwant %+v", got, want)
	}
	// A track without album metadata still converts.
	if got := libraryTrack("x", &metadatapb.Track{Name: proto.String("Bare")}); got.AlbumID != "" || got.AlbumCover != nil {
		t.Errorf("bare track: %+v", got)
	}
}

func TestStatusError(t *testing.T) {
	for _, c := range []struct {
		status int
		want   error
	}{
		{404, provider.ErrNotFound},
		{403, provider.ErrNotFound},
		{429, provider.ErrRateLimited},
		{503, provider.ErrUnavailable},
	} {
		if err := statusError("x", c.status, ""); !errors.Is(err, c.want) {
			t.Errorf("status %d: %v, want %v", c.status, err, c.want)
		}
	}
	var rl *provider.RateLimitError
	if err := statusError("x", 429, "7"); !errors.As(err, &rl) || rl.RetryAfter != 7*time.Second {
		t.Errorf("429 with Retry-After: %v", err)
	}
	if err := statusError("x", 400, ""); errors.Is(err, provider.ErrNotFound) || errors.Is(err, provider.ErrUnavailable) {
		t.Errorf("400: %v", err)
	}
}

func TestOrderPlaylists(t *testing.T) {
	pl := func(id string, n int) spotify.LibraryPlaylist {
		return spotify.LibraryPlaylist{ID: id, Name: id, TrackCount: n}
	}
	// The library lists b and dj as empty; b isn't.
	lib := []spotify.LibraryPlaylist{pl("a", 5), pl("b", 0), pl("c", 5), pl("d", 5), pl("dj", 0)}
	recent := []played{
		{URI: "spotify:playlist:mix", Time: 600},
		{URI: "spotify:playlist:likedpl", Time: 500},
		{URI: "spotify:album:x", Time: 400},
		{URI: "spotify:playlist:c", Time: 300},
		{URI: "spotify:playlist:gone", Time: 250},
		{URI: "spotify:playlist:a", Time: 100},
		{URI: "spotify:playlist:c", Time: 50},
	}
	unsaved := unsavedPlaylists(lib, recent)
	if want := []string{"mix", "likedpl", "gone"}; !reflect.DeepEqual(unsaved, want) {
		t.Errorf("unsavedPlaylists: %v, want %v", unsaved, want)
	}
	found := map[string]lookedUp{
		"b":       {playlist: pl("b", 50)},
		"dj":      {playlist: pl("dj", 0)},
		"mix":     {playlist: pl("mix", 30)},
		"likedpl": {playlist: pl("likedpl", 9), likedSongs: true},
		// gone couldn't be read.
	}
	all, likedAs := mergePlaylists(lib, unsaved, found)
	if likedAs != "likedpl" {
		t.Errorf("likedAs %q", likedAs)
	}
	all = append(all, pl(spotify.LikedSongsID, 9))
	var got []string
	for _, p := range orderPlaylists(all, recent, likedAs) {
		got = append(got, fmt.Sprintf("%s:%d", p.ID, p.TrackCount))
	}
	// Played, newest first (Liked Songs as likedpl), then never played in
	// library order. dj is empty and gone unreadable, so both are left out.
	if want := []string{"mix:30", "liked:9", "c:5", "a:5", "b:50", "d:5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order: %v, want %v", got, want)
	}

	// Nothing played: Liked Songs first, then the library's order.
	got = nil
	for _, p := range orderPlaylists([]spotify.LibraryPlaylist{pl("a", 1), pl(spotify.LikedSongsID, 1), pl("b", 1)}, nil, "") {
		got = append(got, p.ID)
	}
	if want := []string{spotify.LikedSongsID, "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("nothing played: %v, want %v", got, want)
	}
	// Liked Songs played from an older app, as the collection.
	got = nil
	collection := []played{{URI: "spotify:playlist:a", Time: 10}, {URI: "spotify:user:alice:collection", Time: 5}, {URI: "spotify:playlist:b", Time: 1}}
	for _, p := range orderPlaylists([]spotify.LibraryPlaylist{pl("a", 1), pl(spotify.LikedSongsID, 1), pl("b", 1)}, collection, "") {
		got = append(got, p.ID)
	}
	if want := []string{"a", spotify.LikedSongsID, "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("collection played: %v, want %v", got, want)
	}
}

func TestLikedSongsPaging(t *testing.T) {
	// A PageResponse: items, next_page_token, and a sync token to skip.
	item := func(uri string, added int64, removed bool) []byte {
		b, err := proto.Marshal(&collectionpb.CollectionItem{Uri: uri, AddedAt: added, IsRemoved: removed})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var resp []byte
	for _, it := range [][]byte{
		item("spotify:album:aaaaaaaaaaaaaaaaaaaaaa", 900, false),
		item("spotify:track:old0000000000000000000", 100, false),
		item("spotify:track:new0000000000000000000", 300, false),
		item("spotify:track:gone000000000000000000", 400, true),
		item("spotify:track:mid0000000000000000000", 200, false),
	} {
		resp = protowire.AppendTag(resp, 1, protowire.BytesType)
		resp = protowire.AppendBytes(resp, it)
	}
	resp = protowire.AppendTag(resp, 2, protowire.BytesType)
	resp = protowire.AppendString(resp, "next-token")
	resp = protowire.AppendTag(resp, 3, protowire.BytesType)
	resp = protowire.AppendString(resp, "sync")
	resp = protowire.AppendTag(resp, 9, protowire.VarintType)
	resp = protowire.AppendVarint(resp, 7)

	items, next, err := parsePageResponse(resp)
	if err != nil || len(items) != 5 || next != "next-token" {
		t.Fatalf("parsePageResponse: %d items, next %q, %v", len(items), next, err)
	}
	want := []string{"spotify:track:new0000000000000000000", "spotify:track:mid0000000000000000000", "spotify:track:old0000000000000000000"}
	if got := likedURIs(items); !reflect.DeepEqual(got, want) {
		t.Errorf("likedURIs: %v, want %v", got, want)
	}
	if _, _, err := parsePageResponse([]byte{0x0a, 0x05, 0x01}); err == nil {
		t.Error("truncated response parsed")
	}

	// The request round-trips through the same wire format.
	req := pageRequest("alice", "collection", "tok", 2000)
	fields := map[protowire.Number]any{}
	for len(req) > 0 {
		num, typ, n := protowire.ConsumeTag(req)
		req = req[n:]
		if typ == protowire.BytesType {
			v, m := protowire.ConsumeString(req)
			fields[num], req = v, req[m:]
		} else {
			v, m := protowire.ConsumeVarint(req)
			fields[num], req = v, req[m:]
		}
	}
	if !reflect.DeepEqual(fields, map[protowire.Number]any{1: "alice", 2: "collection", 3: "tok", 4: uint64(2000)}) {
		t.Errorf("pageRequest: %v", fields)
	}
}
