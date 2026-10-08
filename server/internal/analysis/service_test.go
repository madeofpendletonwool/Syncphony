// SPDX-License-Identifier: AGPL-3.0-only

package analysis

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// links opens sessions that stream a 120 BPM drum pattern, or, for the
// link "nostream", sessions with no audio at all.
type links struct {
	mu      sync.Mutex
	opened  []string
	streams []string
}

func (l *links) Open(_ context.Context, linkID string) (provider.Session, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.opened = append(l.opened, linkID)
	if linkID == "nostream" {
		return session{}, nil
	}
	return streamer{session{}, l}, nil
}

type session struct{}

func (session) Search(context.Context, provider.SearchQuery) (provider.SearchPage, error) {
	return provider.SearchPage{}, nil
}

func (session) Track(context.Context, string) (provider.Track, error) { return provider.Track{}, nil }

func (session) Album(context.Context, string) (provider.Album, []provider.Track, error) {
	return provider.Album{}, nil, nil
}

func (session) Artist(context.Context, string) (provider.Artist, []provider.Album, error) {
	return provider.Artist{}, nil, nil
}

func (session) Artwork(context.Context, provider.ArtworkRef, int) (io.ReadCloser, string, error) {
	return nil, "", provider.ErrNotFound
}
func (session) Close() error { return nil }

type streamer struct {
	session
	l *links
}

func (s streamer) Stream(_ context.Context, trackID string, _ provider.StreamOpts) (*provider.AudioStream, error) {
	s.l.mu.Lock()
	s.l.streams = append(s.l.streams, trackID)
	s.l.mu.Unlock()
	if trackID == "gone" {
		return nil, provider.ErrNotFound
	}
	pcm, _ := song(120, 24, 99, 0.25)
	return &provider.AudioStream{Body: io.NopCloser(bytes.NewReader(pcm)), ContentType: "audio/x-f32le", Length: -1, Size: -1}, nil
}

// raw "decodes" files that already hold the samples.
type raw struct{}

func (raw) Decode(_ context.Context, path string) (io.ReadCloser, error) {
	return os.Open(path) //nolint:gosec // the service's own temporary file
}

func newService(t *testing.T, dec Decoder) (*Service, *links, *store.Store) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	l := &links{}
	return New(db, l, dec), l, db
}

func item(link, track string) store.QueueItem {
	return store.QueueItem{ID: store.NewID(), Provider: "fake", LinkID: sql.NullString{String: link, Valid: true}, TrackID: track}
}

func TestForItemAnalysesOnce(t *testing.T) {
	svc, l, db := newService(t, raw{})
	m, err := svc.ForItem(t.Context(), item("link1", "t1"), 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(m.BPM-120) > 1 || len(m.BeatsMs) < 80 || m.Version != Version {
		t.Fatalf("map: %v BPM, %d beats, v%d", m.BPM, len(m.BeatsMs), m.Version)
	}
	// Again, from another item and a fresh service: it's kept.
	again, err := New(db, l, raw{}).ForItem(t.Context(), item("link1", "t1"), 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.streams) != 1 || again.BPM != m.BPM || len(again.BandsEnv) != len(m.BandsEnv) {
		t.Errorf("streamed %v; kept map %v BPM, %d band bytes", l.streams, again.BPM, len(again.BandsEnv))
	}
}

func TestForItemUnavailable(t *testing.T) {
	svc, l, _ := newService(t, raw{})
	for _, it := range []store.QueueItem{item("nostream", "t1"), item("link1", "gone")} {
		for range 2 {
			if _, err := svc.ForItem(t.Context(), it, time.Minute); !errors.Is(err, ErrUnavailable) {
				t.Errorf("%s/%s: %v", it.LinkID.String, it.TrackID, err)
			}
		}
	}
	// Misses are kept: each song was tried once.
	if len(l.opened) != 2 {
		t.Errorf("opened %v", l.opened)
	}
	// A DJ mix is too long to bother with, and no link means no audio.
	if _, err := svc.ForItem(t.Context(), item("link1", "mix"), 2*time.Hour); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a two-hour mix: %v", err)
	}
	if _, err := svc.ForItem(t.Context(), store.QueueItem{Provider: "fake", TrackID: "t9"}, time.Minute); !errors.Is(err, ErrUnavailable) {
		t.Errorf("no link: %v", err)
	}
}

func TestForItemVia(t *testing.T) {
	svc, l, _ := newService(t, raw{})
	it := item("nostream", "t1")
	it.ViaProvider = sql.NullString{String: "other", Valid: true}
	it.ViaLinkID = sql.NullString{String: "link2", Valid: true}
	it.ViaTrackID = sql.NullString{String: "t2", Valid: true}
	if _, err := svc.ForItem(t.Context(), it, time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(l.opened) != 1 || l.opened[0] != "link2" || l.streams[0] != "t2" {
		t.Errorf("opened %v, streamed %v", l.opened, l.streams)
	}
}

func TestOldVersionIsRedone(t *testing.T) {
	svc, l, db := newService(t, raw{})
	if err := db.PutBeatMap(t.Context(), store.PutBeatMapParams{
		Provider: "fake", TrackID: "t1", Version: Version - 1, Found: true, Map: `{"v":0,"bpm":99}`, AnalyzedAt: store.Now(), UsedAt: store.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	m, err := svc.ForItem(t.Context(), item("link1", "t1"), time.Minute)
	if err != nil || math.Abs(m.BPM-120) > 1 || len(l.streams) != 1 {
		t.Errorf("%v BPM, %v, streamed %v", m.BPM, err, l.streams)
	}
}

func TestEnqueueAnalysesInBackground(t *testing.T) {
	svc, _, db := newService(t, raw{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go svc.Run(ctx)
	svc.Enqueue(provider.Track{Ref: provider.TrackRef{Provider: "fake", LinkID: "link1", ID: "t1"}, Duration: time.Minute})
	deadline := time.Now().Add(10 * time.Second)
	for {
		row, err := db.GetBeatMap(t.Context(), store.GetBeatMapParams{Provider: "fake", TrackID: "t1"})
		if err == nil && row.Found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("not analysed: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSweep(t *testing.T) {
	svc, _, db := newService(t, raw{})
	old := store.Now().Add(-200 * 24 * time.Hour)
	for _, r := range []store.PutBeatMapParams{
		{TrackID: "unused", Found: true, AnalyzedAt: old, UsedAt: old},
		{TrackID: "used", Found: true, AnalyzedAt: old, UsedAt: store.Now()},
		{TrackID: "oldmiss", Found: false, AnalyzedAt: store.Now().Add(-8 * 24 * time.Hour), UsedAt: store.Now()},
		{TrackID: "newmiss", Found: false, AnalyzedAt: store.Now(), UsedAt: store.Now()},
	} {
		r.Provider, r.Version, r.Map = "fake", Version, "{}"
		if err := db.PutBeatMap(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	for track, want := range map[string]bool{"unused": false, "used": true, "oldmiss": false, "newmiss": true} {
		_, err := db.GetBeatMap(t.Context(), store.GetBeatMapParams{Provider: "fake", TrackID: track})
		if got := err == nil; got != want {
			t.Errorf("%s kept: %v", track, got)
		}
	}
}

func TestFFmpegDecode(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	// A 44.1 kHz stereo WAV, as a provider might send.
	pcm, _ := song(128, 16, 99, 0.3)
	var mono []float32
	for i := 0; i+4 <= len(pcm); i += 4 {
		mono = append(mono, math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:])))
	}
	path := filepath.Join(t.TempDir(), "song.wav")
	if err := os.WriteFile(path, wav44(mono), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := FFmpeg{}.Decode(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m, err := Analyze(r)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(m.BPM-128) > 1 {
		t.Errorf("decoded with ffmpeg: %v BPM", m.BPM)
	}
	// A file ffmpeg can't read fails when read.
	bad := filepath.Join(t.TempDir(), "bad.mp3")
	_ = os.WriteFile(bad, []byte("not audio"), 0o600)
	r, err = FFmpeg{}.Decode(t.Context(), bad)
	if err == nil {
		_, err = Analyze(r)
		r.Close()
	}
	if err == nil {
		t.Error("decoding garbage didn't fail")
	}
}

// wav44 resamples 22.05 kHz mono to 44.1 kHz stereo 16-bit PCM in a WAV.
func wav44(mono []float32) []byte {
	var data bytes.Buffer
	for _, v := range mono {
		s := int16(max(-1, min(1, v)) * 32767)
		for range 4 { // two samples × two channels
			_ = binary.Write(&data, binary.LittleEndian, s)
		}
	}
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + data.Len())) //nolint:gosec // a test file, far under 4 GB
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1))
	w(uint16(2))
	w(uint32(44100))
	w(uint32(44100 * 4))
	w(uint16(4))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(data.Len())) //nolint:gosec // as above
	b.Write(data.Bytes())
	return b.Bytes()
}
