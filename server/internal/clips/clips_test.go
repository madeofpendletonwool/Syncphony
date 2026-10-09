// SPDX-License-Identifier: AGPL-3.0-only

package clips_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/clips"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/transcode"
)

// sessions opens the fake provider's sessions, whose tracks are WAV tones.
type sessions struct{ creds []byte }

func (s sessions) Open(ctx context.Context, linkID string) (provider.Session, error) {
	return fake.New(fake.Options{}).Open(ctx, provider.Link{ID: linkID, Credentials: s.creds})
}

func newSessions(t *testing.T) sessions {
	t.Helper()
	creds, _, err := fake.New(fake.Options{}).Linker().Complete(t.Context(), provider.LinkInput{Fields: map[string]string{"username": fake.Username, "password": fake.Password}})
	if err != nil {
		t.Fatal(err)
	}
	return sessions{creds}
}

func TestClips(t *testing.T) {
	ff := transcode.FFmpeg{}
	if !ff.Available() {
		t.Skip("ffmpeg not installed")
	}
	s := clips.New(newSessions(t), ff, clips.Config{})
	defer s.Close()
	song := clips.Song{LinkID: "l", TrackID: "t01", Duration: 25 * time.Second}
	ids := s.Cut("room", song, []transcode.Cut{{Start: 5 * time.Second, Length: time.Second}, {Start: 5 * time.Second, Length: 4 * time.Second}})
	if len(ids) != 2 || ids[0] == ids[1] || bytes.Contains([]byte(ids[0]), []byte("t01")) {
		t.Fatalf("ids %v", ids)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	one, err := s.Get(ctx, "room", ids[0])
	if err != nil {
		t.Fatal(err)
	}
	four, err := s.Get(ctx, "room", ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if one.ContentType != "audio/mpeg" || len(one.Data) < 1000 || len(four.Data) < 3*len(one.Data) {
		t.Errorf("1s clip: %s, %d bytes; 4s clip %d", one.ContentType, len(one.Data), len(four.Data))
	}
	if ready, failed := s.Ready(ids); !ready || failed {
		t.Errorf("ready %v, failed %v", ready, failed)
	}
	// Another room's clip, or one that never was, isn't there.
	if _, err := s.Get(ctx, "other", ids[0]); !errors.Is(err, clips.ErrNotFound) {
		t.Errorf("another room's clip: %v", err)
	}
	if _, err := s.Get(ctx, "room", "nope"); !errors.Is(err, clips.ErrNotFound) {
		t.Errorf("no such clip: %v", err)
	}
	// A song that won't stream fails its clips.
	bad := s.Cut("room", clips.Song{LinkID: "l", TrackID: "missing"}, []transcode.Cut{{Length: time.Second}})
	if _, err := s.Get(ctx, "room", bad[0]); !errors.Is(err, clips.ErrNotFound) {
		t.Errorf("a missing song's clip: %v", err)
	}
	if _, failed := s.Ready(bad); !failed {
		t.Error("a missing song's clips aren't failed")
	}
}

func TestInside(t *testing.T) {
	for _, tc := range []struct {
		in   transcode.Cut
		d    time.Duration
		want transcode.Cut
	}{
		{transcode.Cut{Start: 10 * time.Second, Length: 4 * time.Second}, time.Minute, transcode.Cut{Start: 10 * time.Second, Length: 4 * time.Second}},
		// Too near the end: moved back so it ends a second before.
		{transcode.Cut{Start: 58 * time.Second, Length: 4 * time.Second}, time.Minute, transcode.Cut{Start: 55 * time.Second, Length: 4 * time.Second}},
		// Unknown duration: left be. Too long: capped.
		{transcode.Cut{Start: time.Hour, Length: time.Hour}, 0, transcode.Cut{Start: time.Hour, Length: transcode.MaxClip}},
		{transcode.Cut{Start: -time.Second, Length: time.Second}, time.Minute, transcode.Cut{Length: time.Second}},
	} {
		if got := clips.Inside(tc.in, tc.d); got != tc.want {
			t.Errorf("Inside(%+v, %v) = %+v, want %+v", tc.in, tc.d, got, tc.want)
		}
	}
}
