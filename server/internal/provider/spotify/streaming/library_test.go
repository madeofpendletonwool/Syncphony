// SPDX-License-Identifier: AGPL-3.0-only

package streaming

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"

	golibrespot "github.com/devgianlu/go-librespot"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	playlist4pb "github.com/devgianlu/go-librespot/proto/spotify/playlist4"
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
