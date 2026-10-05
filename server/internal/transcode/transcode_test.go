// SPDX-License-Identifier: AGPL-3.0-only

package transcode_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/transcode"
)

// streamer opens a fake session, whose tracks are audio/wav.
func streamer(t *testing.T) provider.Streamer {
	t.Helper()
	p := fake.New(fake.Options{})
	creds, _, err := p.Linker().Complete(t.Context(), provider.LinkInput{Fields: map[string]string{"username": fake.Username, "password": fake.Password}})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := p.Open(t.Context(), provider.Link{ID: "l", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	return sess.(provider.Streamer)
}

// recorder is a Transcoder that records what it was asked to do.
type recorder struct {
	src    *provider.AudioStream
	format transcode.Format
	kbps   int
}

func (r *recorder) Transcode(_ context.Context, src *provider.AudioStream, f transcode.Format, kbps int) (*provider.AudioStream, error) {
	r.src, r.format, r.kbps = src, f, kbps
	return &provider.AudioStream{Body: src.Body, ContentType: f.ContentType, Length: -1, Size: -1}, nil
}

func TestAccepts(t *testing.T) {
	for _, tc := range []struct {
		accept []string
		ct     string
		want   bool
	}{
		{nil, "audio/ogg", true},
		{[]string{"audio/mpeg"}, "audio/mpeg", true},
		{[]string{"audio/mpeg"}, "Audio/MPEG; charset=binary", true},
		{[]string{"audio/mpeg", "audio/aac"}, "audio/ogg", false},
		{[]string{"audio/*"}, "audio/flac", true},
		{[]string{"audio/*"}, "video/mp4", false},
		{[]string{"*/*"}, "audio/flac", true},
	} {
		if got := transcode.Accepts(tc.accept, tc.ct); got != tc.want {
			t.Errorf("Accepts(%v, %q) = %v", tc.accept, tc.ct, got)
		}
	}
}

func TestStreamPassesThroughAcceptedFormats(t *testing.T) {
	var r recorder
	a, err := transcode.Stream(t.Context(), streamer(t), "t01", provider.StreamOpts{Accept: []string{"audio/wav"}}, &r)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Body.Close()
	if r.src != nil || !a.Seekable || a.ContentType != "audio/wav" {
		t.Fatalf("transcoded an accepted format: %+v", a)
	}
}

func TestStreamTranscodesFromTheStart(t *testing.T) {
	var r recorder
	opts := provider.StreamOpts{
		Accept:     []string{"audio/aac"},
		Range:      &provider.ByteRange{Start: 1000, End: -1},
		MaxBitrate: 128,
	}
	a, err := transcode.Stream(t.Context(), streamer(t), "t01", opts, &r)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Body.Close()
	if r.format != transcode.AAC || r.kbps != 128 {
		t.Errorf("transcoded to %v at %d kbps, want AAC at 128", r.format, r.kbps)
	}
	if r.src.Offset != 0 || r.src.Length != r.src.Size {
		t.Errorf("transcoder got a partial file: offset %d, length %d of %d", r.src.Offset, r.src.Length, r.src.Size)
	}
	if a.Seekable || a.Length != -1 {
		t.Errorf("transcoded stream claims seekable %v, length %d", a.Seekable, a.Length)
	}
}

func TestStreamWithoutTranscoder(t *testing.T) {
	_, err := transcode.Stream(t.Context(), streamer(t), "t01", provider.StreamOpts{Accept: []string{"audio/mpeg"}}, nil)
	if !errors.Is(err, transcode.ErrNoTranscoder) {
		t.Fatalf("got %v, want ErrNoTranscoder", err)
	}
	_, err = transcode.Stream(t.Context(), streamer(t), "t01", provider.StreamOpts{Accept: []string{"audio/flac"}}, &recorder{})
	if !errors.Is(err, transcode.ErrNoTranscoder) {
		t.Fatalf("no acceptable target: got %v, want ErrNoTranscoder", err)
	}
}

func TestFFmpeg(t *testing.T) {
	ff := transcode.FFmpeg{}
	if !ff.Available() {
		t.Skip("ffmpeg not installed")
	}
	a, err := transcode.Stream(t.Context(), streamer(t), "t01", provider.StreamOpts{Accept: []string{"audio/mpeg"}}, ff)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Body.Close()
	b, err := io.ReadAll(a.Body)
	if err != nil {
		t.Fatal(err)
	}
	if a.ContentType != "audio/mpeg" || len(b) < 1000 {
		t.Fatalf("got %s, %d bytes", a.ContentType, len(b))
	}
	// An MP3 starts with an ID3 tag or an MPEG frame sync.
	if !bytes.HasPrefix(b, []byte("ID3")) && (b[0] != 0xFF || b[1]&0xE0 != 0xE0) {
		t.Fatalf("not MP3: % x", b[:4])
	}
}

func TestFFmpegCloseEarly(t *testing.T) {
	ff := transcode.FFmpeg{}
	if !ff.Available() {
		t.Skip("ffmpeg not installed")
	}
	a, err := transcode.Stream(t.Context(), streamer(t), "t01", provider.StreamOpts{Accept: []string{"audio/mpeg"}}, ff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(a.Body, make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	if err := a.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFFmpegReportsFailure(t *testing.T) {
	ff := transcode.FFmpeg{}
	if !ff.Available() {
		t.Skip("ffmpeg not installed")
	}
	src := &provider.AudioStream{Body: io.NopCloser(bytes.NewReader([]byte("not audio"))), ContentType: "audio/ogg"}
	a, err := ff.Transcode(t.Context(), src, transcode.MP3, 128)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Body.Close()
	if _, err := io.ReadAll(a.Body); err == nil {
		t.Fatal("transcoding garbage succeeded")
	}
}
