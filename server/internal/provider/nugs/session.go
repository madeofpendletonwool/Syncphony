// SPDX-License-Identifier: AGPL-3.0-only

package nugs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/artcache"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

type session struct {
	p    *Provider
	link provider.Link
	auth *auth
}

var (
	_ provider.Session     = (*session)(nil)
	_ provider.Streamer    = (*session)(nil)
	_ provider.PlayChecker = (*session)(nil)
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 100
	// maxArtistShows is how many of an artist's shows Artist lists, newest
	// first. Some have thousands.
	maxArtistShows = 100
	// parallelShows bounds the show lookups one search makes at once.
	parallelShows = 6
)

func (s *session) Search(ctx context.Context, q provider.SearchQuery) (provider.SearchPage, error) {
	kinds := q.Kinds
	if len(kinds) == 0 {
		kinds = s.p.Info().Capabilities.Search
	}
	for _, k := range kinds {
		if !s.p.Info().Capabilities.CanSearch(k) {
			return provider.SearchPage{}, fmt.Errorf("nugs: search %q: %w", k, provider.ErrUnsupported)
		}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	limit = min(limit, maxSearchLimit)
	offset := 0
	if q.Cursor != "" {
		n, err := strconv.Atoi(q.Cursor)
		if err != nil || n < 0 {
			return provider.SearchPage{}, fmt.Errorf("nugs: bad search cursor %q", q.Cursor)
		}
		offset = n
	}
	token, err := s.auth.token(ctx)
	if err != nil {
		return provider.SearchPage{}, err
	}
	res, err := s.p.search(ctx, token, q.Text)
	if err != nil {
		return provider.SearchPage{}, err
	}
	var page provider.SearchPage
	more := false
	for _, k := range kinds {
		var m bool
		switch k {
		case provider.KindTrack:
			var hits []hit
			hits, m = pageOf(res.tracks, offset, limit)
			if page.Tracks, err = s.resolve(ctx, token, hits); err != nil {
				return provider.SearchPage{}, err
			}
		case provider.KindAlbum:
			page.Albums, m = pageOf(res.albums, offset, limit)
		case provider.KindArtist:
			page.Artists, m = pageOf(res.artists, offset, limit)
		}
		more = more || m
	}
	if more {
		page.Next = strconv.Itoa(offset + limit)
	}
	return page, nil
}

// resolve looks up the shows of search hits, for their tracks' details.
// A hit whose show or track can't be found is left out.
func (s *session) resolve(ctx context.Context, token string, hits []hit) ([]provider.Track, error) {
	var (
		mu       sync.Mutex
		shows    = map[string]*show{}
		firstErr error
		wg       sync.WaitGroup
		sem      = make(chan struct{}, parallelShows)
	)
	ids := map[string]bool{}
	for _, h := range hits {
		ids[h.show] = true
	}
	for id := range ids {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			sh, err := s.p.show(ctx, token, id)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				shows[id] = sh
			case firstErr == nil && !errors.Is(err, provider.ErrNotFound):
				firstErr = err
			}
		})
	}
	wg.Wait()
	var out []provider.Track
	for _, h := range hits {
		sh := shows[h.show]
		if sh == nil {
			continue
		}
		if t, ok := findTrack(sh, h.track); ok {
			out = append(out, s.track(sh, t))
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// show returns a show's details, from the cache if they're there.
func (p *Provider) show(ctx context.Context, token, id string) (*show, error) {
	if sh, ok := p.shows.get(id); ok {
		return sh, nil
	}
	sh, err := p.fetchShow(ctx, token, id)
	if err != nil {
		return nil, err
	}
	p.shows.put(id, sh)
	return sh, nil
}

// trackID joins a show ID and a track ID into a track's provider ID.
func trackID(show, track string) string { return show + "." + track }

// splitTrackID splits a track's provider ID into its show and track IDs.
func splitTrackID(id string) (show, track string, ok bool) {
	show, track, ok = strings.Cut(id, ".")
	return show, track, ok && numericID.MatchString(show) && numericID.MatchString(track)
}

func findTrack(sh *show, id string) (showTrack, bool) {
	for _, t := range sh.setlist() {
		if t.TrackID.id() == id {
			return t, true
		}
	}
	return showTrack{}, false
}

func (s *session) track(sh *show, t showTrack) provider.Track {
	al := toAlbum(sh)
	return provider.Track{
		Ref:      provider.TrackRef{Provider: ID, LinkID: s.link.ID, ID: trackID(al.ID, t.TrackID.id())},
		Title:    strings.TrimSpace(t.SongTitle),
		Artists:  al.Artists,
		Album:    provider.AlbumCredit{ID: al.ID, Title: al.Title},
		Duration: time.Duration(t.Seconds.int()) * time.Second,
		Artwork:  al.Artwork,
	}
}

func (s *session) Track(ctx context.Context, id string) (provider.Track, error) {
	sh, t, err := s.lookup(ctx, id)
	if err != nil {
		return provider.Track{}, err
	}
	return s.track(sh, t), nil
}

// lookup finds a track and its show.
func (s *session) lookup(ctx context.Context, id string) (*show, showTrack, error) {
	showID, track, ok := splitTrackID(id)
	if !ok {
		return nil, showTrack{}, fmt.Errorf("nugs: track %q: %w", id, provider.ErrNotFound)
	}
	token, err := s.auth.token(ctx)
	if err != nil {
		return nil, showTrack{}, err
	}
	sh, err := s.p.show(ctx, token, showID)
	if err != nil {
		return nil, showTrack{}, err
	}
	t, ok := findTrack(sh, track)
	if !ok {
		return nil, showTrack{}, fmt.Errorf("nugs: track %q: %w", id, provider.ErrNotFound)
	}
	return sh, t, nil
}

func (s *session) Album(ctx context.Context, id string) (provider.Album, []provider.Track, error) {
	token, err := s.auth.token(ctx)
	if err != nil {
		return provider.Album{}, nil, err
	}
	sh, err := s.p.show(ctx, token, id)
	if err != nil {
		return provider.Album{}, nil, err
	}
	var ts []provider.Track
	for _, t := range sh.setlist() {
		if t.TrackID.id() != "" {
			ts = append(ts, s.track(sh, t))
		}
	}
	return toAlbum(sh), ts, nil
}

// Artist returns an artist and their most recent shows.
func (s *session) Artist(ctx context.Context, id string) (provider.Artist, []provider.Album, error) {
	if !numericID.MatchString(id) {
		return provider.Artist{}, nil, fmt.Errorf("nugs: artist %q: %w", id, provider.ErrNotFound)
	}
	token, err := s.auth.token(ctx)
	if err != nil {
		return provider.Artist{}, nil, err
	}
	var r containersAll
	err = s.p.legacy(ctx, token, "catalog.containersAll", url.Values{
		"artistList":  {id},
		"startOffset": {"1"},
		"limit":       {strconv.Itoa(maxArtistShows)},
		"availType":   {"1"},
	}, &r)
	if err != nil {
		return provider.Artist{}, nil, err
	}
	if len(r.Containers) == 0 {
		return provider.Artist{}, nil, fmt.Errorf("nugs: artist %s: %w", id, provider.ErrNotFound)
	}
	ar := provider.Artist{ID: id, Name: strings.TrimSpace(r.Containers[0].ArtistName)}
	albums := make([]provider.Album, 0, len(r.Containers))
	for i := range r.Containers {
		if al := toAlbum(&r.Containers[i]); al.ID != "" {
			albums = append(albums, al)
		}
	}
	return ar, albums, nil
}

// artPath matches the image paths the catalog returns. Checking them keeps
// artwork requests on the image host.
var artPath = regexp.MustCompile(`^/[A-Za-z0-9_][A-Za-z0-9_./-]*$`)

func artRef(img *image) provider.ArtworkRef {
	if img == nil || !artPath.MatchString(img.URL) || strings.Contains(img.URL, "..") {
		return ""
	}
	return provider.ArtworkRef(img.URL)
}

// Artwork fetches show art. It's the same for everyone, so the cache is
// shared; nugs.net has one size of each image, so size is ignored.
func (s *session) Artwork(ctx context.Context, ref provider.ArtworkRef, _ int) (io.ReadCloser, string, error) {
	path := string(ref)
	if !artPath.MatchString(path) || strings.Contains(path, "..") {
		return nil, "", fmt.Errorf("nugs: artwork %q: %w", path, provider.ErrNotFound)
	}
	if img, ok := s.p.art.Get(path); ok {
		return io.NopCloser(bytes.NewReader(img.Data)), img.ContentType, nil
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := newRequest(ctx, s.p.ep.Images+path, "")
	if err != nil {
		return nil, "", err
	}
	resp, err := s.p.do(req, "artwork", false)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if mt, _, _ := mime.ParseMediaType(ct); !strings.HasPrefix(mt, "image/") {
		return nil, "", fmt.Errorf("nugs artwork: unexpected content type %q: %w", ct, provider.ErrUnavailable)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtwork+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, "", ctxErr
		}
		return nil, "", fmt.Errorf("nugs artwork: %w: %w", provider.ErrUnavailable, err)
	}
	if len(data) > maxArtwork {
		return nil, "", fmt.Errorf("nugs artwork: image over %d bytes", maxArtwork)
	}
	s.p.art.Put(path, artcache.Image{Data: data, ContentType: ct})
	return io.NopCloser(bytes.NewReader(data)), ct, nil
}

// CheckPlayable refuses tracks on shows that are sold but not streamed,
// and every track when the account's subscription has lapsed.
func (s *session) CheckPlayable(ctx context.Context, trackID string) error {
	sh, _, err := s.lookup(ctx, trackID)
	if err != nil {
		return err
	}
	if !sh.streamable() {
		return fmt.Errorf("nugs: show %s isn't in the subscription catalog: %w", sh.ContainerID, provider.ErrNotPlayable)
	}
	sub, err := s.auth.subscription(ctx)
	if err != nil {
		return err
	}
	if !sub.active(s.p.now()) {
		return errNoSubscription
	}
	return nil
}

func (s *session) Close() error { return nil }
