// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // artwork formats whose size we can read
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp" // and WebP, which image doesn't read itself

	"github.com/madeofpendletonwool/syncphony/server/internal/artcache"
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

// Search searches every one of the caller's links, and every shared one, at once.
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
	ls, err := s.Links.Usable(ctx, userID)
	if err != nil {
		return nil, err
	}

	groups := make([]SearchGroup, len(ls))
	var wg sync.WaitGroup
	for i, l := range ls {
		groups[i] = SearchGroup{
			LinkId: l.ID, OwnerId: l.UserID, Provider: l.Provider, AccountLabel: l.AccountLabel,
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

// GetAlbum returns an album and its tracks through a link the caller may use.
func (s *Server) GetAlbum(ctx context.Context, req GetAlbumRequestObject) (GetAlbumResponseObject, error) {
	l, sess, err := s.openUsable(ctx, req.Id)
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

// GetArtist returns an artist and their albums through a link the caller may use.
func (s *Server) GetArtist(ctx context.Context, req GetArtistRequestObject) (GetArtistResponseObject, error) {
	l, sess, err := s.openUsable(ctx, req.Id)
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

// ListPlaylists returns a page of a link's playlists.
func (s *Server) ListPlaylists(ctx context.Context, req ListPlaylistsRequestObject) (ListPlaylistsResponseObject, error) {
	l, pl, closeSess, err := s.openPlaylists(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	defer closeSess()
	page, err := pl.Playlists(ctx, deref(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out := PlaylistList{LinkId: l.ID, Provider: l.Provider, Playlists: make([]PlaylistResult, len(page.Items))}
	for i, p := range page.Items {
		out.Playlists[i] = toPlaylistResult(p)
	}
	if page.Next != "" {
		out.Next = ptr(page.Next)
	}
	return ListPlaylists200JSONResponse(out), nil
}

// GetPlaylistTracks returns a page of a playlist's tracks.
func (s *Server) GetPlaylistTracks(ctx context.Context, req GetPlaylistTracksRequestObject) (GetPlaylistTracksResponseObject, error) {
	l, pl, closeSess, err := s.openPlaylists(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	defer closeSess()
	page, err := pl.PlaylistTracks(ctx, req.PlaylistId, deref(req.Params.Cursor))
	if err != nil {
		return nil, err
	}
	out := PlaylistTracks{LinkId: l.ID, Provider: l.Provider, Tracks: make([]TrackResult, len(page.Items))}
	for i, t := range page.Items {
		out.Tracks[i] = toTrackResult(t)
	}
	if page.Next != "" {
		out.Next = ptr(page.Next)
	}
	return GetPlaylistTracks200JSONResponse(out), nil
}

// openPlaylists opens a link the caller may use, if its service has
// playlists.
func (s *Server) openPlaylists(ctx context.Context, linkID string) (store.ServiceLink, provider.PlaylistLister, func(), error) {
	l, sess, err := s.openUsable(ctx, linkID)
	if err != nil {
		return l, nil, nil, err
	}
	pl, ok := sess.(provider.PlaylistLister)
	if !ok {
		sess.Close()
		return l, nil, nil, fmt.Errorf("%s has no playlists: %w", l.Provider, provider.ErrNotFound)
	}
	return l, pl, func() { sess.Close() }, nil
}

// openUsable opens one of the caller's links, or a shared one.
func (s *Server) openUsable(ctx context.Context, linkID string) (store.ServiceLink, provider.Session, error) {
	l, err := s.Links.GetUsable(ctx, sessionFrom(ctx).User.ID, linkID)
	if err != nil {
		return l, nil, err
	}
	sess, err := s.Links.Open(ctx, l.ID)
	return l, sess, err
}

// GetLinkArtwork proxies an image through a link the caller may use.
func (s *Server) GetLinkArtwork(ctx context.Context, req GetLinkArtworkRequestObject) (GetLinkArtworkResponseObject, error) {
	_, sess, err := s.openUsable(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	a, err := artwork(ctx, sess, provider.ArtworkRef(req.Params.Ref), req.Params.Size)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// GetQueueItemArtwork loads a queued song's artwork through the link of
// whoever queued it, so the whole room sees it. When that's missing, or
// smaller than asked for, the Cover Art Archive's is used if it's bigger.
func (s *Server) GetQueueItemArtwork(ctx context.Context, req GetQueueItemArtworkRequestObject) (GetQueueItemArtworkResponseObject, error) {
	it, err := s.Queue.Item(ctx, req.RoomId, req.ItemId)
	if err != nil {
		return nil, err
	}
	// metadata is the provider.Track snapshot taken when the item was queued.
	var t provider.Track
	if err := json.Unmarshal([]byte(it.Metadata), &t); err != nil {
		return nil, provider.ErrNotFound
	}
	t.Ref = provider.TrackRef{Provider: it.Provider, LinkID: it.LinkID.String, ID: it.TrackID}
	px := 0
	if req.Params.Size != nil {
		px = *req.Params.Size
	}
	own, ownErr := s.ownArtwork(ctx, t, px)
	if s.MusicBrainz == nil || (ownErr == nil && !smallerThan(own, px)) {
		if ownErr != nil {
			return nil, ownErr
		}
		return imageResponse(own), nil
	}
	caa, err := s.MusicBrainz.CoverArt(ctx, t, px)
	switch {
	case err != nil && ownErr != nil:
		// The song's own error says more.
		return nil, ownErr
	case err != nil:
		if !errors.Is(err, provider.ErrNotFound) {
			slog.Debug("cover art archive", "err", err)
		}
		return imageResponse(own), nil
	case ownErr == nil && width(caa) <= width(own):
		return imageResponse(own), nil
	}
	return imageResponse(caa), nil
}

// ownArtwork loads a queued song's artwork from its own service.
func (s *Server) ownArtwork(ctx context.Context, t provider.Track, px int) (artcache.Image, error) {
	if t.Artwork == "" || t.Ref.LinkID == "" {
		return artcache.Image{}, provider.ErrNotFound
	}
	sess, err := s.Links.Open(ctx, t.Ref.LinkID)
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

// maxArtwork is the largest queue artwork we'll read.
const maxArtwork = 8 << 20

// defaultArtworkWidth is the width wanted when the client doesn't say.
const defaultArtworkWidth = 600

// smallerThan reports whether img is narrower than px (or the default
// width). Images of unknown format aren't.
func smallerThan(img artcache.Image, px int) bool {
	if px <= 0 {
		px = defaultArtworkWidth
	}
	w := width(img)
	return w > 0 && w < px
}

// width is an image's width in pixels, or 0 if it can't be read.
func width(img artcache.Image) int {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil {
		return 0
	}
	return cfg.Width
}

func imageResponse(img artcache.Image) artworkResponse {
	return artworkResponse{body: io.NopCloser(bytes.NewReader(img.Data)), ct: img.ContentType}
}

// artwork loads an image, refusing anything that isn't one. It takes over
// sess: the response closes it once the image is sent.
func artwork(ctx context.Context, sess provider.Session, ref provider.ArtworkRef, size *int) (artworkResponse, error) {
	px := 0
	if size != nil {
		px = *size
	}
	body, ct, err := sess.Artwork(ctx, ref, px)
	if err != nil {
		sess.Close()
		return artworkResponse{}, err
	}
	if !strings.HasPrefix(ct, "image/") {
		body.Close()
		sess.Close()
		slog.Warn("artwork isn't an image", "content_type", ct)
		return artworkResponse{}, provider.ErrNotFound
	}
	return artworkResponse{body, ct, sess}, nil
}

// artworkResponse writes an image. Browsers may keep it, privately: refs
// are stable.
type artworkResponse struct {
	body io.ReadCloser
	ct   string
	// sess, if set, is closed once the image is sent.
	sess provider.Session
}

func (r artworkResponse) VisitGetLinkArtworkResponse(w http.ResponseWriter) error { return r.write(w) }

func (r artworkResponse) VisitGetQueueItemArtworkResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r artworkResponse) write(w http.ResponseWriter) error {
	if r.sess != nil {
		defer r.sess.Close()
	}
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

func toPlaylistResult(p provider.Playlist) PlaylistResult {
	out := PlaylistResult{Id: p.ID, Name: p.Name}
	if p.Owner != "" {
		out.Owner = ptr(p.Owner)
	}
	if p.TrackCount > 0 {
		out.TrackCount = ptr(p.TrackCount)
	}
	if p.Artwork != "" {
		out.Artwork = ptr(string(p.Artwork))
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
