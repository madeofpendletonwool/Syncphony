// SPDX-License-Identifier: AGPL-3.0-only

package palette_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/artwork"
	"github.com/madeofpendletonwool/syncphony/server/internal/palette"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// links opens sessions whose artwork is a solid color, or an SVG.
type links struct{ calls int }

func (l *links) Open(context.Context, string) (provider.Session, error) { return session{l}, nil }

type session struct{ l *links }

func (s session) Artwork(_ context.Context, ref provider.ArtworkRef, _ int) (io.ReadCloser, string, error) {
	s.l.calls++
	if ref == "svg" {
		return io.NopCloser(bytes.NewReader([]byte("<svg/>"))), "image/svg+xml", nil
	}
	img := image.NewRGBA(image.Rect(0, 0, 300, 300))
	for i := range img.Pix {
		img.Pix[i] = [4]uint8{200, 30, 60, 255}[i%4]
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return io.NopCloser(&b), "image/png", nil
}

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
func (session) Close() error { return nil }

func setup(t *testing.T) (*store.Store, *palette.Service, *links, func(trackID string, art provider.ArtworkRef) (store.QueueItem, provider.Track)) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	u, err := db.CreateUser(t.Context(), store.CreateUserParams{
		ID: store.NewID(), Username: "alice", DisplayName: "Alice", Color: "#000000", Role: store.RoleMember, CreatedAt: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	room, err := db.CreateRoom(t.Context(), store.CreateRoomParams{
		ID: store.NewID(), Name: "Den", OwnerID: u.ID, FairnessMode: store.FairnessRoundRobin, Settings: "{}", CreatedAt: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	l := &links{}
	svc := palette.New(db, artwork.New(l, nil))
	add := func(trackID string, art provider.ArtworkRef) (store.QueueItem, provider.Track) {
		tr := provider.Track{Ref: provider.TrackRef{Provider: "x", LinkID: "l1", ID: trackID}, Title: trackID, Artwork: art}
		meta, _ := json.Marshal(tr)
		it, err := db.AddQueueItem(t.Context(), store.AddQueueItemParams{
			ID: store.NewID(), RoomID: room.ID, AddedBy: u.ID, Provider: "x", TrackID: trackID,
			Metadata: string(meta), LanePosition: 1024, Now: store.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return it, tr
	}
	return db, svc, l, add
}

func TestEnqueueSavesPalette(t *testing.T) {
	db, svc, _, add := setup(t)
	it, tr := add("t1", "art")
	other, _ := add("t1", "art") // the same song again
	go svc.Run(t.Context())
	svc.Enqueue(tr)

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := db.GetQueueItem(t.Context(), it.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Palette.Valid {
			var p palette.Palette
			if err := json.Unmarshal([]byte(got.Palette.String), &p); err != nil || p.Accent == nil || p.Vibrant.C < 0.1 {
				t.Fatalf("palette: %s, %v", got.Palette.String, err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never computed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got, _ := db.GetQueueItem(t.Context(), other.ID); !got.Palette.Valid {
		t.Error("the same song queued again has no palette")
	}
}

func TestForItem(t *testing.T) {
	db, svc, l, add := setup(t)
	it, tr := add("t1", "art")
	p, err := svc.ForItem(t.Context(), it, tr)
	if err != nil || p.Accent == nil {
		t.Fatalf("ForItem = %+v, %v", p, err)
	}
	// Saved: the next ask doesn't load the art.
	it, _ = db.GetQueueItem(t.Context(), it.ID)
	if again, err := svc.ForItem(t.Context(), it, tr); err != nil || again.Dominant != p.Dominant || *again.Accent != *p.Accent || l.calls != 1 {
		t.Errorf("again: %+v, %v, %d artwork loads", again, err, l.calls)
	}

	// SVGs and missing art have none.
	svg, svgTrack := add("t2", "svg")
	if _, err := svc.ForItem(t.Context(), svg, svgTrack); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("SVG: %v", err)
	}
	none, noneTrack := add("t3", "")
	if _, err := svc.ForItem(t.Context(), none, noneTrack); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("no art: %v", err)
	}
}
