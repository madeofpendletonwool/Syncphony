// SPDX-License-Identifier: AGPL-3.0-only

// Package artwork picks the best cover for a queued song: its own
// service's, or the Cover Art Archive's when that's missing or too small.
package artwork

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // artwork formats whose size we can read
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"strings"

	_ "golang.org/x/image/webp" // and WebP, which image doesn't read itself

	"github.com/madeofpendletonwool/syncphony/server/internal/artcache"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

const (
	// maxArtwork is the largest image we'll read.
	maxArtwork = 8 << 20
	// defaultWidth is the width wanted when the caller doesn't say.
	defaultWidth = 600
)

// Links opens sessions for links. It's links.Service.
type Links interface {
	Open(ctx context.Context, linkID string) (provider.Session, error)
}

// Service loads queued songs' artwork.
type Service struct {
	links Links
	// mb, if set, supplies covers from the Cover Art Archive.
	mb *musicbrainz.Service
}

// New returns a Service. mb may be nil.
func New(links Links, mb *musicbrainz.Service) *Service {
	return &Service{links: links, mb: mb}
}

// ForTrack returns t's artwork, about px wide (0 for a default). It loads
// it through t.Ref.LinkID; when that's missing, or narrower than px, the
// Cover Art Archive's is used if it's bigger.
func (s *Service) ForTrack(ctx context.Context, t provider.Track, px int) (artcache.Image, error) {
	own, ownErr := s.own(ctx, t, px)
	if s.mb == nil || (ownErr == nil && !SmallerThan(own, px)) {
		return own, ownErr
	}
	caa, err := s.mb.CoverArt(ctx, t, px)
	switch {
	case err != nil && ownErr != nil:
		// The song's own error says more.
		return own, ownErr
	case err != nil:
		if !errors.Is(err, provider.ErrNotFound) {
			slog.Debug("cover art archive", "err", err)
		}
		return own, nil
	case ownErr == nil && Width(caa) <= Width(own):
		return own, nil
	}
	return caa, nil
}

// ForRecording returns the cover of a MusicBrainz recording nobody
// queued, about px wide: the other half of a sample, say.
func (s *Service) ForRecording(ctx context.Context, mbid string, px int) (artcache.Image, error) {
	if s.mb == nil || mbid == "" {
		return artcache.Image{}, provider.ErrNotFound
	}
	return s.mb.RecordingArt(ctx, mbid, px)
}

// own loads t's artwork from its own service.
func (s *Service) own(ctx context.Context, t provider.Track, px int) (artcache.Image, error) {
	if t.Artwork == "" || t.Ref.LinkID == "" {
		return artcache.Image{}, provider.ErrNotFound
	}
	sess, err := s.links.Open(ctx, t.Ref.LinkID)
	if err != nil {
		return artcache.Image{}, err
	}
	defer sess.Close()
	body, ct, err := sess.Artwork(ctx, t.Artwork, px)
	if err != nil {
		return artcache.Image{}, err
	}
	defer body.Close()
	if !strings.HasPrefix(ct, "image/") {
		slog.Warn("artwork isn't an image", "content_type", ct)
		return artcache.Image{}, provider.ErrNotFound
	}
	data, err := io.ReadAll(io.LimitReader(body, maxArtwork+1))
	if err != nil {
		return artcache.Image{}, fmt.Errorf("reading artwork: %w: %w", provider.ErrUnavailable, err)
	}
	if len(data) > maxArtwork {
		return artcache.Image{}, fmt.Errorf("artwork over %d bytes: %w", maxArtwork, provider.ErrUnavailable)
	}
	return artcache.Image{Data: data, ContentType: ct}, nil
}

// SmallerThan reports whether img is narrower than px (or the default
// width). Images of unknown format, like SVGs, aren't.
func SmallerThan(img artcache.Image, px int) bool {
	if px <= 0 {
		px = defaultWidth
	}
	w := Width(img)
	return w > 0 && w < px
}

// Width is an image's width in pixels, or 0 if it can't be read.
func Width(img artcache.Image) int {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil {
		return 0
	}
	return cfg.Width
}
