// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/links"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// searchTimeout bounds each link's part of a search, so one slow service
// doesn't hold up everyone else's results.
const searchTimeout = 8 * time.Second

// searchKinds are what the search screen shows. Playlists are browsed, not
// searched, for now.
var searchKinds = []provider.EntityKind{provider.KindTrack, provider.KindAlbum, provider.KindArtist}

// Search searches every one of the caller's links at once.
func (s *Server) Search(ctx context.Context, req SearchRequestObject) (SearchResponseObject, error) {
	text := strings.TrimSpace(req.Params.Q)
	if text == "" {
		return nil, &links.InvalidInputError{Field: "q", Message: "search for something"}
	}
	limit := 20
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	userID := sessionFrom(ctx).User.ID
	ls, err := s.Links.List(ctx, userID)
	if err != nil {
		return nil, err
	}

	groups := make([]SearchGroup, len(ls))
	var wg sync.WaitGroup
	for i, l := range ls {
		groups[i] = SearchGroup{
			LinkId: l.ID, Provider: l.Provider, AccountLabel: l.AccountLabel,
			Tracks: []TrackResult{}, Albums: []AlbumResult{}, Artists: []ArtistResult{},
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, searchTimeout)
			defer cancel()
			if err := s.searchLink(ctx, l.ID, text, limit, &groups[i]); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					err = provider.ErrUnavailable
				}
				groups[i].Error = ptr(toAPIError(err))
			}
		})
	}
	wg.Wait()
	return Search200JSONResponse{Query: text, Groups: groups}, nil
}

func (s *Server) searchLink(ctx context.Context, linkID, text string, limit int, g *SearchGroup) error {
	sess, err := s.Links.Open(ctx, linkID)
	if err != nil {
		return err
	}
	defer sess.Close()
	p, err := s.Links.Provider(g.Provider)
	if err != nil {
		return err
	}
	var kinds []provider.EntityKind
	for _, k := range searchKinds {
		if p.Info().Capabilities.CanSearch(k) {
			kinds = append(kinds, k)
		}
	}
	page, err := sess.Search(ctx, provider.SearchQuery{Text: text, Kinds: kinds, Limit: limit})
	if err != nil {
		return err
	}
	for _, t := range page.Tracks {
		g.Tracks = append(g.Tracks, toTrackResult(t))
	}
	for _, a := range page.Albums {
		g.Albums = append(g.Albums, toAlbumResult(a))
	}
	for _, a := range page.Artists {
		g.Artists = append(g.Artists, toArtistResult(a))
	}
	return nil
}

// GetAlbum returns an album and its tracks through one of the caller's links.
func (s *Server) GetAlbum(ctx context.Context, req GetAlbumRequestObject) (GetAlbumResponseObject, error) {
	l, sess, err := s.openOwn(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	a, ts, err := sess.Album(ctx, req.AlbumId)
	if err != nil {
		return nil, err
	}
	out := AlbumDetail{LinkId: l.ID, Provider: l.Provider, Album: toAlbumResult(a), Tracks: make([]TrackResult, len(ts))}
	for i, t := range ts {
		out.Tracks[i] = toTrackResult(t)
	}
	return GetAlbum200JSONResponse(out), nil
}

// GetArtist returns an artist and their albums through one of the caller's links.
func (s *Server) GetArtist(ctx context.Context, req GetArtistRequestObject) (GetArtistResponseObject, error) {
	l, sess, err := s.openOwn(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	a, albums, err := sess.Artist(ctx, req.ArtistId)
	if err != nil {
		return nil, err
	}
	out := ArtistDetail{
		LinkId: l.ID, Provider: l.Provider,
		Artist: toArtistResult(a), Albums: make([]AlbumResult, len(albums)),
	}
	for i, al := range albums {
		out.Albums[i] = toAlbumResult(al)
	}
	return GetArtist200JSONResponse(out), nil
}

// openOwn opens one of the caller's links.
func (s *Server) openOwn(ctx context.Context, linkID string) (store.ServiceLink, provider.Session, error) {
	l, err := s.Links.Get(ctx, sessionFrom(ctx).User.ID, linkID)
	if err != nil {
		return l, nil, err
	}
	sess, err := s.Links.Open(ctx, l.ID)
	return l, sess, err
}

// GetLinkArtwork proxies an image through one of the caller's links.
func (s *Server) GetLinkArtwork(ctx context.Context, req GetLinkArtworkRequestObject) (GetLinkArtworkResponseObject, error) {
	_, sess, err := s.openOwn(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	size := 0
	if req.Params.Size != nil {
		size = *req.Params.Size
	}
	body, ct, err := sess.Artwork(ctx, provider.ArtworkRef(req.Params.Ref), size)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(ct, "image/") {
		body.Close()
		slog.Warn("artwork isn't an image", "link", req.Id, "content_type", ct)
		return nil, provider.ErrNotFound
	}
	return artworkResponse{body, ct}, nil
}

// artworkResponse writes an image. Browsers may keep it: refs are stable,
// and the response is only for the link's owner.
type artworkResponse struct {
	body io.ReadCloser
	ct   string
}

func (r artworkResponse) VisitGetLinkArtworkResponse(w http.ResponseWriter) error {
	defer r.body.Close()
	h := w.Header()
	h.Set("Content-Type", r.ct)
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("X-Content-Type-Options", "nosniff")
	// SVGs can carry script; never let one run if opened directly.
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, r.body); err != nil {
		slog.Debug("artwork ended early", "err", err)
	}
	return nil
}

func toTrackResult(t provider.Track) TrackResult {
	out := TrackResult{
		LinkId: t.Ref.LinkID, Provider: t.Ref.Provider, TrackId: t.Ref.ID, Title: t.Title,
		Artists: toArtistCredits(t.Artists), DurationMs: t.Duration.Milliseconds(), Explicit: t.Explicit,
	}
	if t.Album.Title != "" {
		out.Album = &AlbumCredit{Title: t.Album.Title}
		if t.Album.ID != "" {
			out.Album.Id = ptr(t.Album.ID)
		}
	}
	if t.Artwork != "" {
		out.Artwork = ptr(string(t.Artwork))
	}
	return out
}

func toAlbumResult(a provider.Album) AlbumResult {
	out := AlbumResult{Id: a.ID, Title: a.Title, Artists: toArtistCredits(a.Artists)}
	if a.Year > 0 {
		out.Year = ptr(a.Year)
	}
	if a.TrackCount > 0 {
		out.TrackCount = ptr(a.TrackCount)
	}
	if a.Artwork != "" {
		out.Artwork = ptr(string(a.Artwork))
	}
	return out
}

func toArtistResult(a provider.Artist) ArtistResult {
	out := ArtistResult{Id: a.ID, Name: a.Name}
	if a.Artwork != "" {
		out.Artwork = ptr(string(a.Artwork))
	}
	return out
}

func toArtistCredits(as []provider.ArtistCredit) []ArtistCredit {
	out := make([]ArtistCredit, len(as))
	for i, a := range as {
		out[i] = ArtistCredit{Name: a.Name}
		if a.ID != "" {
			out[i].Id = ptr(a.ID)
		}
	}
	return out
}

// toAPIError is the body writeError would send for err, for errors
// reported inside a successful response.
func toAPIError(err error) Error {
	for _, e := range errorCodes {
		if errors.Is(err, e.err) {
			return Error{Code: e.code, Message: e.err.Error()}
		}
	}
	slog.Error("search failed", "err", err)
	return Error{Code: "internal", Message: "something went wrong"}
}
