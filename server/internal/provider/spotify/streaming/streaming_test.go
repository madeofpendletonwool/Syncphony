// SPDX-License-Identifier: AGPL-3.0-only

package streaming

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/devgianlu/go-librespot/ap"
	"github.com/devgianlu/go-librespot/login5"
	pb "github.com/devgianlu/go-librespot/proto/spotify"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify"
)

func audioFile(f metadatapb.AudioFile_Format) *metadatapb.AudioFile {
	return &metadatapb.AudioFile{FileId: []byte(f.String()), Format: f.Enum()}
}

func TestPickFile(t *testing.T) {
	all := []*metadatapb.AudioFile{
		audioFile(metadatapb.AudioFile_AAC_24),
		audioFile(metadatapb.AudioFile_OGG_VORBIS_160),
		audioFile(metadatapb.AudioFile_OGG_VORBIS_320),
		audioFile(metadatapb.AudioFile_OGG_VORBIS_96),
		audioFile(metadatapb.AudioFile_MP3_320),
	}
	for _, tc := range []struct {
		max  int
		want metadatapb.AudioFile_Format
	}{
		{0, metadatapb.AudioFile_OGG_VORBIS_320},
		{320, metadatapb.AudioFile_OGG_VORBIS_320},
		{256, metadatapb.AudioFile_OGG_VORBIS_160},
		{128, metadatapb.AudioFile_OGG_VORBIS_96},
		{64, metadatapb.AudioFile_OGG_VORBIS_96}, // nothing fits: the smallest
	} {
		if got := pickFile(all, tc.max); got.GetFormat() != tc.want {
			t.Errorf("max %d: got %s, want %s", tc.max, got.GetFormat(), tc.want)
		}
	}
	if got := pickFile([]*metadatapb.AudioFile{audioFile(metadatapb.AudioFile_MP3_320)}, 0); got != nil {
		t.Errorf("no Ogg: got %s", got.GetFormat())
	}
}

func TestLoginError(t *testing.T) {
	apErr := func(code pb.ErrorCode) error {
		return &ap.AccesspointLoginError{Message: &pb.APLoginFailed{ErrorCode: code.Enum()}}
	}
	for _, tc := range []struct {
		err  error
		want error
	}{
		{apErr(pb.ErrorCode_BadCredentials), provider.ErrAuthExpired},
		{apErr(pb.ErrorCode_PremiumAccountRequired), provider.ErrAuthExpired},
		{apErr(pb.ErrorCode_TryAnotherAP), provider.ErrUnavailable},
		{&login5.LoginError{}, provider.ErrAuthExpired},
		{io.ErrUnexpectedEOF, provider.ErrUnavailable},
	} {
		if got := loginError(tc.err); !errors.Is(got, tc.want) {
			t.Errorf("%v: got %v, want %v", tc.err, got, tc.want)
		}
	}
}

// TestLive streams a real track. It needs a streaming login saved as JSON
// with "username" and "stored_credentials" (base64) in the file named by
// SPOTIFY_LIVE_STATE, like the MAD-697 spike's state.json. Set
// SPOTIFY_LIVE_REFUSED to a track ID Spotify refuses, to check that too.
func TestLive(t *testing.T) {
	path := os.Getenv("SPOTIFY_LIVE_STATE")
	if path == "" {
		t.Skip("SPOTIFY_LIVE_STATE not set")
	}
	b, err := os.ReadFile(path) //nolint:gosec // a test fixture the developer names
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Username          string `json:"username"`
		StoredCredentials []byte `json:"stored_credentials"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	login := spotify.Login{Username: st.Username, Stored: st.StoredCredentials}
	be := New(nil)
	defer be.Close()

	const track = "7qiZfU4dY1lWllzX7mPBI3" // Ed Sheeran, Shape of You
	if err := be.Check(t.Context(), login, track); err != nil {
		t.Fatalf("Check: %v", err)
	}
	f, err := be.Open(t.Context(), login, track, spotify.AudioOpts{MaxBitrate: 160})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	if f.Size() < 1<<20 || f.ContentType() != "audio/ogg" {
		t.Fatalf("file: %d bytes of %s", f.Size(), f.ContentType())
	}
	read := func(off, n int64) []byte {
		t.Helper()
		r, err := f.ReadRange(t.Context(), off, n)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		got, err := io.ReadAll(r)
		if err != nil || int64(len(got)) != n {
			t.Fatalf("ReadRange(%d, %d): %d bytes, %v", off, n, len(got), err)
		}
		return got
	}
	if head := read(0, 4); !bytes.Equal(head, []byte("OggS")) {
		t.Errorf("the file starts with %q, want an Ogg page", head)
	}
	// Ranges decrypt the same from any offset.
	mid := f.Size() / 2
	if whole, part := read(mid-100, 300), read(mid, 100); !bytes.Equal(whole[100:200], part) {
		t.Error("a range read from the middle doesn't match")
	}
	t.Logf("streamed %s: %d bytes", track, f.Size())

	if refused := os.Getenv("SPOTIFY_LIVE_REFUSED"); refused != "" {
		if err := be.Check(t.Context(), login, refused); !errors.Is(err, provider.ErrNotPlayable) {
			t.Errorf("Check(%s): %v, want ErrNotPlayable", refused, err)
		}
	}
}
