// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/madeofpendletonwool/syncphony/server/internal/artistpage"
	"github.com/madeofpendletonwool/syncphony/server/internal/links"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Linked names (MAD-837): an artist or album name anywhere in the app opens
// its page. When the name came from a link the viewer can't browse, these
// find it by name on one they can.

// ResolveArtist finds an artist by name on the caller's links.
func (s *Server) ResolveArtist(ctx context.Context, req ResolveArtistRequestObject) (ResolveArtistResponseObject, error) {
	name := strings.TrimSpace(req.Params.Name)
	if name == "" {
		return nil, &links.InvalidInputError{Field: "name", Message: "name an artist"}
	}
	ref, err := s.resolve(ctx, func(ctx context.Context, sess provider.Session, caps provider.Capabilities) string {
		if a := artistpage.Elsewhere(ctx, sess, caps, name); a != nil {
			return a.ID
		}
		return ""
	})
	if err != nil {
		return nil, err
	}
	return ResolveArtist200JSONResponse(ref), nil
}

// ResolveAlbum finds an album by title, and artist if given, on the
// caller's links.
func (s *Server) ResolveAlbum(ctx context.Context, req ResolveAlbumRequestObject) (ResolveAlbumResponseObject, error) {
	title := strings.TrimSpace(req.Params.Title)
	if title == "" {
		return nil, &links.InvalidInputError{Field: "title", Message: "name an album"}
	}
	artist := ""
	if req.Params.Artist != nil {
		artist = strings.TrimSpace(*req.Params.Artist)
	}
	ref, err := s.resolve(ctx, func(ctx context.Context, sess provider.Session, caps provider.Capabilities) string {
		if a := artistpage.AlbumElsewhere(ctx, sess, caps, title, artist); a != nil {
			return a.ID
		}
		return ""
	})
	if err != nil {
		return nil, err
	}
	return ResolveAlbum200JSONResponse(ref), nil
}

// resolve runs find on every link the caller may use at once, and returns
// the first match, the caller's own links before shared ones.
// provider.ErrNotFound when none has it.
func (s *Server) resolve(ctx context.Context, find func(context.Context, provider.Session, provider.Capabilities) string) (ResolvedRef, error) {
	// Usable lists the caller's own links first.
	ls, err := s.Links.Usable(ctx, sessionFrom(ctx).User.ID)
	if err != nil {
		return ResolvedRef{}, err
	}
	found := make([]string, len(ls))
	var wg sync.WaitGroup
	for i, l := range ls {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, searchTimeout)
			defer cancel()
			p, err := s.Links.Provider(l.Provider)
			if err != nil {
				return
			}
			sess, err := s.Links.Open(ctx, l.ID)
			if err != nil {
				slog.Debug("resolve: opening a link", "link", l.ID, "err", err)
				return
			}
			defer sess.Close()
			found[i] = find(ctx, sess, p.Info().Capabilities)
		})
	}
	wg.Wait()
	for i, id := range found {
		if id != "" {
			return ResolvedRef{LinkId: ls[i].ID, Id: id}, nil
		}
	}
	return ResolvedRef{}, provider.ErrNotFound
}
