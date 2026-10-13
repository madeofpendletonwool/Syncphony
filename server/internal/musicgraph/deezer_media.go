// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// What discovery (ADR 0017) asks Deezer for besides music knowledge: a
// song's ID, a fresh 30-second preview, artists by name, and images. The
// server fetches all of it, so the browser never talks to Deezer.

// maxImage is the largest image fetched from Deezer's CDN.
const maxImage = 4 << 20

// DeezerTrack is a song on Deezer.
type DeezerTrack struct {
	ID     int64
	Title  string
	Artist string
	// Album is its album's title, Cover the album cover's image ID.
	Album string
	Cover string
	// Rank is how popular it is across all music, from 0 to 1.
	Rank float64
	// HasPreview is whether Deezer has a 30-second preview of it.
	HasPreview bool
	ISRC       string
}

func (t dzTrack) public() DeezerTrack {
	return DeezerTrack{
		ID: t.ID, Title: t.Title, Artist: t.Artist.Name, Album: t.Album.Title, Cover: t.cover(),
		Rank: min(t.Rank/deezerTopRank, 1), HasPreview: t.Preview != "", ISRC: t.ISRC,
	}
}

// DeezerArtist is an artist on Deezer.
type DeezerArtist struct {
	ID      int64
	Name    string
	Fans    int64
	Picture string
}

// FindTrack finds a song on Deezer by its artist and title: an exact
// search first (artist:"x" track:"y"), then a loose one. It's
// provider.ErrNotFound if nothing matches.
func (d *Deezer) FindTrack(ctx context.Context, s SongRef) (DeezerTrack, error) {
	if s.Title == "" || s.Artist.Name == "" {
		return DeezerTrack{}, fmt.Errorf("deezer: a song with no title or artist: %w", provider.ErrNotFound)
	}
	q := fmt.Sprintf("artist:%q track:%q", s.Artist.Name, s.Title)
	t, err := d.searchFor(ctx, s, q)
	if isNotFound(err) {
		t, err = d.searchFor(ctx, s, s.Artist.Name+" "+s.Title)
	}
	if err != nil {
		return DeezerTrack{}, err
	}
	return t.public(), nil
}

// TrackByID returns a song on Deezer by its ID.
func (d *Deezer) TrackByID(ctx context.Context, id int64) (DeezerTrack, error) {
	t, err := d.track(ctx, id)
	return t.public(), err
}

func (d *Deezer) track(ctx context.Context, id int64) (dzTrack, error) {
	if id <= 0 {
		return dzTrack{}, fmt.Errorf("deezer: track %d: %w", id, provider.ErrNotFound)
	}
	var t dzTrack
	if err := d.get(ctx, "/track/"+strconv.FormatInt(id, 10), nil, &t); err != nil {
		return dzTrack{}, err
	}
	if t.ID == 0 {
		return dzTrack{}, fmt.Errorf("deezer: track %d: %w", id, provider.ErrNotFound)
	}
	return t, nil
}

// SearchArtists finds artists on Deezer by name, best known first among
// equally good matches, as Deezer orders them.
func (d *Deezer) SearchArtists(ctx context.Context, q string, limit int) ([]DeezerArtist, error) {
	var found struct {
		Data []dzArtist `json:"data"`
	}
	if err := d.get(ctx, "/search/artist", url.Values{"q": {q}, "limit": {strconv.Itoa(max(1, min(limit, 50)))}}, &found); err != nil {
		return nil, err
	}
	out := make([]DeezerArtist, 0, len(found.Data))
	for _, a := range found.Data {
		out = append(out, DeezerArtist{ID: a.ID, Name: a.Name, Fans: a.Fans, Picture: cmp.Or(a.MD5Image, imageID(a.Picture))})
	}
	return out, nil
}

// OpenPreview streams a song's 30-second preview. Its URL is asked for
// fresh every time, since Deezer signs them to expire; it's never kept.
// r, if set, asks for part of it. It's provider.ErrNotFound if Deezer has
// no preview of the song.
func (d *Deezer) OpenPreview(ctx context.Context, id int64, r *provider.ByteRange) (*provider.AudioStream, error) {
	t, err := d.track(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Preview == "" {
		return nil, fmt.Errorf("deezer: track %d has no preview: %w", id, provider.ErrNotFound)
	}
	u, err := url.Parse(t.Preview)
	if err != nil || !d.mediaHost(u) {
		return nil, fmt.Errorf("deezer: track %d: preview from an unexpected host: %w", id, provider.ErrUnavailable)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", d.c.userAgent)
	if r != nil {
		if r.End >= 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.Start, r.End))
		} else {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", r.Start))
		}
	}
	resp, err := d.c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL is signed; keep it out of logs
		}
		return nil, fmt.Errorf("deezer preview: %w: %w", provider.ErrUnavailable, err)
	}
	a := &provider.AudioStream{
		Body: resp.Body, ContentType: "audio/mpeg", Length: resp.ContentLength, Size: resp.ContentLength,
		Seekable: resp.Header.Get("Accept-Ranges") == "bytes",
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusPartialContent:
		var start, end, size int64
		if _, err := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &size); err == nil {
			a.Offset, a.Length, a.Size, a.Seekable = start, end-start+1, size, true
		}
	case http.StatusRequestedRangeNotSatisfiable:
		resp.Body.Close()
		return nil, fmt.Errorf("deezer preview: %w", provider.ErrRange)
	default:
		resp.Body.Close()
		return nil, fmt.Errorf("deezer preview: HTTP %d: %w", resp.StatusCode, provider.ErrUnavailable)
	}
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "audio/") {
		a.ContentType = ct
	}
	return a, nil
}

// mediaHost reports whether u is somewhere Deezer serves media from: its
// CDN, or (for tests) the API's own host.
func (d *Deezer) mediaHost(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	if base, err := url.Parse(d.base); err == nil && u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host) {
		return true
	}
	return u.Scheme == "https" && (strings.HasSuffix(host, ".dzcdn.net") || strings.HasSuffix(host, ".deezer.com"))
}

// Image kinds on Deezer's CDN.
const (
	ImageCover  = "cover"
	ImageArtist = "artist"
)

// imageSizes are the sizes Deezer's CDN serves.
var imageSizes = []int{56, 120, 250, 500, 1000}

// Image fetches a cover or artist picture from Deezer's CDN by its image
// ID, at about size pixels wide.
func (d *Deezer) Image(ctx context.Context, kind, id string, size int) ([]byte, string, error) {
	if (kind != ImageCover && kind != ImageArtist) || !isImageID(id) {
		return nil, "", fmt.Errorf("deezer image %s/%s: %w", kind, id, provider.ErrNotFound)
	}
	px := imageSizes[len(imageSizes)-1]
	for _, s := range imageSizes {
		if s >= size {
			px = s
			break
		}
	}
	u := fmt.Sprintf("%s/%s/%s/%dx%d-000000-80-0-0.jpg", d.images, kind, id, px, px)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", d.c.userAgent)
	resp, err := d.c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("deezer image: %w: %w", provider.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, "", fmt.Errorf("deezer image %s/%s: %w", kind, id, provider.ErrNotFound)
	case resp.StatusCode != http.StatusOK:
		return nil, "", fmt.Errorf("deezer image: HTTP %d: %w", resp.StatusCode, provider.ErrUnavailable)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		return nil, "", fmt.Errorf("deezer image: not an image (%s): %w", ct, provider.ErrUnavailable)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImage))
	if err != nil {
		return nil, "", fmt.Errorf("deezer image: %w: %w", provider.ErrUnavailable, err)
	}
	return body, ct, nil
}

// isImageID reports whether id looks like a Deezer image ID: 32 hex digits.
func isImageID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
