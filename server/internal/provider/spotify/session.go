// SPDX-License-Identifier: AGPL-3.0-only

package spotify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

type session struct {
	p     *Provider
	link  provider.Link
	creds creds
}

var (
	_ provider.Session      = (*session)(nil)
	_ provider.Streamer     = (*session)(nil)
	_ provider.PlayChecker  = (*session)(nil)
	_ provider.DevicePairer = linker{}
)

const (
	// searchLimit is the most results per kind Spotify returns to a
	// development-mode app (since February 2026), and our default.
	searchLimit = 10
	// maxSearchOffset is the furthest Spotify pages into search results.
	maxSearchOffset = 1000
	// Pages fetched when listing an album's tracks or an artist's albums.
	albumTracksPage  = 50
	maxAlbumTracks   = 1000
	artistAlbumsPage = 10 // the endpoint's maximum
	maxArtistAlbums  = 50
	// maxArtwork is the largest image we'll fetch.
	maxArtwork = 8 << 20
)

// idPattern matches Spotify IDs. Anything else can't exist, so it's
// ErrNotFound without asking Spotify (which would answer 400).
var idPattern = regexp.MustCompile(`^[0-9A-Za-z]{22}$`)

func checkID(kind, id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("spotify: %s %q: %w", kind, id, provider.ErrNotFound)
	}
	return nil
}

func (s *session) Search(ctx context.Context, q provider.SearchQuery) (provider.SearchPage, error) {
	kinds := q.Kinds
	if len(kinds) == 0 {
		kinds = s.p.Info().Capabilities.Search
	}
	var types []string
	for _, k := range kinds {
		switch k {
		case provider.KindTrack, provider.KindAlbum, provider.KindArtist:
			types = append(types, string(k))
		default:
			return provider.SearchPage{}, fmt.Errorf("spotify: search %q: %w", k, provider.ErrUnsupported)
		}
	}
	limit := searchLimit
	if q.Limit > 0 {
		limit = min(q.Limit, searchLimit)
	}
	offset := 0
	if q.Cursor != "" {
		n, err := strconv.Atoi(q.Cursor)
		if err != nil || n < 0 || n > maxSearchOffset {
			return provider.SearchPage{}, fmt.Errorf("spotify: bad search cursor %q", q.Cursor)
		}
		offset = n
	}
	var page provider.SearchPage
	if strings.TrimSpace(q.Text) == "" {
		return page, nil
	}
	params := url.Values{
		"q":      {q.Text},
		"type":   {strings.Join(types, ",")},
		"limit":  {strconv.Itoa(limit)},
		"offset": {strconv.Itoa(offset)},
	}
	var r searchResponse
	if err := s.get(ctx, "/search", params, &r); err != nil {
		return provider.SearchPage{}, err
	}
	more := false
	if r.Tracks != nil {
		more = more || r.Tracks.Next != nil
		for _, t := range r.Tracks.Items {
			if playable(t) {
				page.Tracks = append(page.Tracks, s.track(t, nil))
			}
		}
	}
	if r.Albums != nil {
		more = more || r.Albums.Next != nil
		for _, a := range r.Albums.Items {
			if a.ID != "" {
				page.Albums = append(page.Albums, s.album(a))
			}
		}
	}
	if r.Artists != nil {
		more = more || r.Artists.Next != nil
		for _, a := range r.Artists.Items {
			if a.ID != "" {
				page.Artists = append(page.Artists, provider.Artist{ID: a.ID, Name: a.Name, Artwork: s.artworkRef(a.Images)})
			}
		}
	}
	if more && offset+limit <= maxSearchOffset {
		page.Next = strconv.Itoa(offset + limit)
	}
	return page, nil
}

// playable reports whether a track object is one we can queue: not a
// local file (those exist only on the owner's device) or an episode.
func playable(t track) bool {
	return t.ID != "" && !t.IsLocal && (t.Type == "" || t.Type == "track")
}

func (s *session) Track(ctx context.Context, id string) (provider.Track, error) {
	if err := checkID("track", id); err != nil {
		return provider.Track{}, err
	}
	var t track
	if err := s.get(ctx, "/tracks/"+id, nil, &t); err != nil {
		return provider.Track{}, err
	}
	return s.track(t, nil), nil
}

func (s *session) Album(ctx context.Context, id string) (provider.Album, []provider.Track, error) {
	if err := checkID("album", id); err != nil {
		return provider.Album{}, nil, err
	}
	var a album
	if err := s.get(ctx, "/albums/"+id, nil, &a); err != nil {
		return provider.Album{}, nil, err
	}
	var items []track
	more := false
	if a.Tracks != nil {
		items, more = a.Tracks.Items, a.Tracks.Next != nil
	}
	// Long albums (box sets) come in pages.
	for more && len(items) < maxAlbumTracks {
		var p paging[track]
		q := url.Values{"limit": {strconv.Itoa(albumTracksPage)}, "offset": {strconv.Itoa(len(items))}}
		if err := s.get(ctx, "/albums/"+id+"/tracks", q, &p); err != nil {
			return provider.Album{}, nil, err
		}
		if len(p.Items) == 0 {
			break
		}
		items, more = append(items, p.Items...), p.Next != nil
	}
	tracks := make([]provider.Track, 0, len(items))
	for _, t := range items {
		if playable(t) {
			tracks = append(tracks, s.track(t, &a))
		}
	}
	return s.album(a), tracks, nil
}

func (s *session) Artist(ctx context.Context, id string) (provider.Artist, []provider.Album, error) {
	if err := checkID("artist", id); err != nil {
		return provider.Artist{}, nil, err
	}
	var a artist
	if err := s.get(ctx, "/artists/"+id, nil, &a); err != nil {
		return provider.Artist{}, nil, err
	}
	var albums []provider.Album
	for len(albums) < maxArtistAlbums {
		var p paging[album]
		q := url.Values{
			"include_groups": {"album,single,compilation"},
			"limit":          {strconv.Itoa(artistAlbumsPage)},
			"offset":         {strconv.Itoa(len(albums))},
		}
		if err := s.get(ctx, "/artists/"+id+"/albums", q, &p); err != nil {
			return provider.Artist{}, nil, err
		}
		for _, al := range p.Items {
			albums = append(albums, s.album(al))
		}
		if p.Next == nil || len(p.Items) == 0 {
			break
		}
	}
	return provider.Artist{ID: a.ID, Name: a.Name, Artwork: s.artworkRef(a.Images)}, albums, nil
}

// track converts a track object. on is the album it came from, for the
// tracks of GET /albums/{id}, which don't carry their album.
func (s *session) track(t track, on *album) provider.Track {
	al := t.Album
	if on != nil {
		al = on
	}
	out := provider.Track{
		Ref:      provider.TrackRef{Provider: ID, LinkID: s.link.ID, ID: t.ID},
		Title:    t.Name,
		Artists:  credits(t.Artists),
		Duration: time.Duration(t.DurationMS) * time.Millisecond,
		Explicit: t.Explicit,
	}
	if al != nil {
		out.Album = provider.AlbumCredit{ID: al.ID, Title: al.Name}
		out.Artwork = s.artworkRef(al.Images)
	}
	return out
}

func (s *session) album(a album) provider.Album {
	year, _ := strconv.Atoi(a.ReleaseDate[:min(4, len(a.ReleaseDate))])
	return provider.Album{
		ID:         a.ID,
		Title:      a.Name,
		Artists:    credits(a.Artists),
		Year:       year,
		TrackCount: a.TotalTracks,
		Artwork:    s.artworkRef(a.Images),
	}
}

func credits(list []artistCredit) []provider.ArtistCredit {
	var out []provider.ArtistCredit
	for _, c := range list {
		if c.Name != "" {
			out = append(out, provider.ArtistCredit{ID: c.ID, Name: c.Name})
		}
	}
	return out
}

// --- Artwork --------------------------------------------------------------------

// An artwork ref lists an image's sizes, largest first, as
// "width:image,width:image". image is the ID of an image on the image CDN
// (i.scdn.co), or the URL of one on another Spotify image host. Width is 0 when Spotify doesn't say.

// imageID matches IDs of images on the image CDN.
var imageID = regexp.MustCompile(`^[0-9A-Za-z]{1,100}$`)

// imageHosts are the other hosts artwork is fetched from. Refs come back
// from browsers, so they're checked before fetching.
func imageHost(host string) bool {
	return host == "mosaic.scdn.co" || host == "i.scdn.co" || strings.HasSuffix(host, ".spotifycdn.com")
}

type sized struct {
	width int
	url   string
}

func (s *session) artworkRef(images []Image) provider.ArtworkRef {
	images = slices.Clone(images)
	slices.SortStableFunc(images, func(a, b Image) int { return b.Width - a.Width })
	var parts []string
	for _, im := range images {
		var target string
		if id, ok := strings.CutPrefix(im.URL, s.p.imageURL+"/"); ok && imageID.MatchString(id) {
			target = id
		} else if u, err := url.Parse(im.URL); err == nil && u.Scheme == "https" && imageHost(u.Hostname()) && !strings.Contains(im.URL, ",") {
			target = im.URL
		} else {
			continue
		}
		parts = append(parts, strconv.Itoa(max(im.Width, 0))+":"+target)
	}
	return provider.ArtworkRef(strings.Join(parts, ","))
}

func (s *session) parseArtwork(ref provider.ArtworkRef) ([]sized, error) {
	var out []sized
	for part := range strings.SplitSeq(string(ref), ",") {
		w, target, ok := strings.Cut(part, ":")
		width, err := strconv.Atoi(w)
		if !ok || err != nil || width < 0 {
			return nil, fmt.Errorf("spotify: malformed artwork ref: %w", provider.ErrNotFound)
		}
		if imageID.MatchString(target) {
			out = append(out, sized{width, s.p.imageURL + "/" + target})
			continue
		}
		u, err := url.Parse(target)
		if err != nil || u.Scheme != "https" || !imageHost(u.Hostname()) {
			return nil, fmt.Errorf("spotify: artwork ref names an image elsewhere: %w", provider.ErrNotFound)
		}
		out = append(out, sized{width, target})
	}
	return out, nil
}

// Artwork fetches the smallest image at least size pixels wide, or the
// largest if none is. Spotify's images are public, so no token is sent.
func (s *session) Artwork(ctx context.Context, ref provider.ArtworkRef, size int) (io.ReadCloser, string, error) {
	if ref == "" {
		return nil, "", fmt.Errorf("spotify: empty artwork ref: %w", provider.ErrNotFound)
	}
	images, err := s.parseArtwork(ref)
	if err != nil {
		return nil, "", err
	}
	pick := images[0]
	if size > 0 {
		for _, im := range images {
			if im.width >= size {
				pick = im
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pick.url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := s.p.client.Do(req)
	if err != nil {
		return nil, "", transportError(ctx, "artwork", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", responseError(resp, "artwork")
	}
	ct := resp.Header.Get("Content-Type")
	if mt, _, _ := mime.ParseMediaType(ct); !strings.HasPrefix(mt, "image/") {
		return nil, "", fmt.Errorf("spotify artwork: unexpected content type %q: %w", ct, provider.ErrUnavailable)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtwork+1))
	if err != nil {
		return nil, "", transportError(ctx, "artwork", err)
	}
	if len(data) > maxArtwork {
		return nil, "", fmt.Errorf("spotify artwork: image over %d bytes", maxArtwork)
	}
	return io.NopCloser(bytes.NewReader(data)), ct, nil
}

func (s *session) Close() error { return nil }
