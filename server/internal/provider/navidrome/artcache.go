// SPDX-License-Identifier: AGPL-3.0-only

package navidrome

import (
	"container/list"
	"sync"
	"time"
)

const (
	// artworkTTL is how long a cached image is served before it's fetched
	// again, so changed cover art shows up eventually.
	artworkTTL = 24 * time.Hour
	// maxArtwork is the largest image we'll fetch or cache.
	maxArtwork = 8 << 20
)

type artKey struct {
	// account keeps users' caches apart: Navidrome libraries can be
	// per-user, so one user's art isn't necessarily visible to another.
	account string
	ref     string
	size    int
}

type image struct {
	data        []byte
	contentType string
}

type artEntry struct {
	key     artKey
	img     image
	expires time.Time
}

// artCache is an LRU cache of images bounded by total bytes. A nil
// *artCache caches nothing.
type artCache struct {
	max int64
	ttl time.Duration
	now func() time.Time

	mu    sync.Mutex
	size  int64
	order *list.List // front is most recently used
	byKey map[artKey]*list.Element
}

func newArtCache(maxBytes int64, ttl time.Duration, now func() time.Time) *artCache {
	if maxBytes <= 0 {
		return nil
	}
	return &artCache{max: maxBytes, ttl: ttl, now: now, order: list.New(), byKey: map[artKey]*list.Element{}}
}

func (c *artCache) get(k artKey) (image, bool) {
	if c == nil {
		return image{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[k]
	if !ok {
		return image{}, false
	}
	e := el.Value.(*artEntry)
	if c.now().After(e.expires) {
		c.remove(el)
		return image{}, false
	}
	c.order.MoveToFront(el)
	return e.img, true
}

func (c *artCache) put(k artKey, img image) {
	if c == nil || int64(len(img.data)) > c.max {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[k]; ok {
		c.remove(el)
	}
	c.byKey[k] = c.order.PushFront(&artEntry{key: k, img: img, expires: c.now().Add(c.ttl)})
	c.size += int64(len(img.data))
	for c.size > c.max {
		c.remove(c.order.Back())
	}
}

// remove drops an entry. The caller holds mu.
func (c *artCache) remove(el *list.Element) {
	e := c.order.Remove(el).(*artEntry)
	delete(c.byKey, e.key)
	c.size -= int64(len(e.img.data))
}
