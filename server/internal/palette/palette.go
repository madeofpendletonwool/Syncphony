// SPDX-License-Identifier: AGPL-3.0-only

package palette

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/madeofpendletonwool/syncphony/server/internal/artwork"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

const (
	// artSize is the artwork width palettes are taken from.
	artSize = 300
	// computeTimeout bounds loading the art and extracting a palette.
	computeTimeout = 30 * time.Second
	queueSize      = 256
)

// Service works out queued songs' palettes and saves them on the queue
// items. Call Run to compute them in the background as songs are queued.
type Service struct {
	db  *store.Store
	art *artwork.Service

	jobs    chan provider.Track
	mu      sync.Mutex
	pending map[string]bool
	group   singleflight.Group
}

// New returns a Service.
func New(db *store.Store, art *artwork.Service) *Service {
	return &Service{db: db, art: art, jobs: make(chan provider.Track, queueSize), pending: map[string]bool{}}
}

func key(ref provider.TrackRef) string { return ref.Provider + "\x00" + ref.ID }

// Enqueue asks for newly queued songs' palettes to be computed.
func (s *Service) Enqueue(ts ...provider.Track) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range ts {
		k := key(t.Ref)
		if t.Ref.ID == "" || s.pending[k] {
			continue
		}
		select {
		case s.jobs <- t:
			s.pending[k] = true
		default:
			slog.Debug("palette: queue full, dropping a track", "provider", t.Ref.Provider, "track", t.Ref.ID)
		}
	}
}

// Run computes queued palettes, one at a time, until ctx is done.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-s.jobs:
			if _, err := s.compute(ctx, t, ""); err != nil && ctx.Err() == nil {
				slog.Debug("palette", "provider", t.Ref.Provider, "track", t.Ref.ID, "err", err)
			}
			s.mu.Lock()
			delete(s.pending, key(t.Ref))
			s.mu.Unlock()
		}
	}
}

// ForItem returns a queue item's palette, computing and saving it if it
// hasn't been. It's provider.ErrNotFound if the song has no artwork the
// server can read.
func (s *Service) ForItem(ctx context.Context, it store.QueueItem, t provider.Track) (Palette, error) {
	if it.Palette.Valid {
		var p Palette
		if err := json.Unmarshal([]byte(it.Palette.String), &p); err == nil {
			return p, nil
		}
	}
	return s.compute(ctx, t, it.ID)
}

// compute extracts t's palette from its artwork and saves it on the
// queue's items for t, and on itemID.
func (s *Service) compute(ctx context.Context, t provider.Track, itemID string) (Palette, error) {
	v, err, _ := s.group.Do(key(t.Ref)+"\x00"+itemID, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), computeTimeout)
		defer cancel()
		img, err := s.art.ForTrack(ctx, t, artSize)
		if err != nil {
			return Palette{}, err
		}
		decoded, _, err := image.Decode(bytes.NewReader(img.Data))
		if err != nil {
			// An SVG, say: the browser can still tint from it.
			return Palette{}, fmt.Errorf("palette: decoding %s artwork: %w: %w", img.ContentType, provider.ErrNotFound, err)
		}
		p := Extract(decoded)
		b, err := json.Marshal(p)
		if err != nil {
			return Palette{}, err
		}
		if err := s.db.SetTrackPalette(ctx, store.SetTrackPaletteParams{
			Palette: sql.NullString{String: string(b), Valid: true}, Provider: t.Ref.Provider, TrackID: t.Ref.ID, ItemID: itemID,
		}); err != nil {
			return Palette{}, err
		}
		return p, nil
	})
	if err != nil {
		return Palette{}, err
	}
	return v.(Palette), nil
}
