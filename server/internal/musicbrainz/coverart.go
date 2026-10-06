// SPDX-License-Identifier: AGPL-3.0-only

package musicbrainz

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/artcache"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// DefaultCoverArtURL is the Cover Art Archive.
const DefaultCoverArtURL = "https://coverartarchive.org"

const (
	// artTTL is how long a cover, or knowing there's none, is cached.
	artTTL     = 24 * time.Hour
	maxArtwork = 8 << 20
	artTimeout = 20 * time.Second
)

// thumbs are the sizes the Cover Art Archive serves, besides originals,
// which can be huge.
var thumbs = []int{250, 500, 1200}

type artKey struct {
	release string
	size    int
}

// coverArt is a Cover Art Archive client with a cache.
type coverArt struct {
	base      string
	userAgent string
	http      *http.Client
	cache     *artcache.Cache[artKey]
}

// front returns the release's front cover, else its release group's.
func (c *coverArt) front(ctx context.Context, ids IDs, size int) (artcache.Image, error) {
	thumb := thumbs[len(thumbs)-1]
	for _, n := range thumbs {
		if n >= size {
			thumb = n
			break
		}
	}
	k := artKey{release: ids.Release, size: thumb}
	if img, ok := c.cache.Get(k); ok {
		if len(img.Data) == 0 {
			return artcache.Image{}, fmt.Errorf("cover art for release %s: %w", ids.Release, provider.ErrNotFound)
		}
		return img, nil
	}
	img, err := c.fetch(ctx, fmt.Sprintf("/release/%s/front-%d", ids.Release, thumb))
	if img == nil && err == nil && ids.ReleaseGroup != "" {
		img, err = c.fetch(ctx, fmt.Sprintf("/release-group/%s/front-%d", ids.ReleaseGroup, thumb))
	}
	if err != nil {
		return artcache.Image{}, err
	}
	if img == nil {
		c.cache.Put(k, artcache.Image{})
		return artcache.Image{}, fmt.Errorf("cover art for release %s: %w", ids.Release, provider.ErrNotFound)
	}
	c.cache.Put(k, *img)
	return *img, nil
}

// fetch gets an image. It's nil, with no error, if there's none.
func (c *coverArt) fetch(ctx context.Context, path string) (*artcache.Image, error) {
	ctx, cancel := context.WithTimeout(ctx, artTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	// The archive redirects to the image on archive.org.
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cover art archive: %w: %w", provider.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, nil
	default:
		return nil, fmt.Errorf("cover art archive: HTTP %d: %w", resp.StatusCode, provider.ErrUnavailable)
	}
	ct := resp.Header.Get("Content-Type")
	if mt, _, _ := mime.ParseMediaType(ct); !strings.HasPrefix(mt, "image/") {
		return nil, fmt.Errorf("cover art archive: content type %q: %w", ct, provider.ErrUnavailable)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtwork+1))
	if err != nil {
		return nil, fmt.Errorf("cover art archive: %w: %w", provider.ErrUnavailable, err)
	}
	if len(data) > maxArtwork {
		return nil, fmt.Errorf("cover art archive: image over %d bytes", maxArtwork)
	}
	return &artcache.Image{Data: data, ContentType: ct}, nil
}
