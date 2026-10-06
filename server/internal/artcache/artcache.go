// SPDX-License-Identifier: AGPL-3.0-only

// Package artcache is an in-memory LRU cache of images, bounded by total
// bytes, with entries that expire.
package artcache

import (
	"container/list"
	"sync"
	"time"
)

// Image is a cached image. Empty Data can record that there is none.
type Image struct {
	Data        []byte
	ContentType string
}

type entry[K comparable] struct {
	key     K
	img     Image
	expires time.Time
}

// Cache is an LRU cache of images. A nil *Cache caches nothing.
type Cache[K comparable] struct {
	max int64
	ttl time.Duration
	now func() time.Time

	mu    sync.Mutex
	size  int64
	order *list.List // front is most recently used
	byKey map[K]*list.Element
}

// New returns a cache holding up to maxBytes of images, each for ttl. It
// returns nil, which caches nothing, if maxBytes isn't positive.
func New[K comparable](maxBytes int64, ttl time.Duration, now func() time.Time) *Cache[K] {
	if maxBytes <= 0 {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &Cache[K]{max: maxBytes, ttl: ttl, now: now, order: list.New(), byKey: map[K]*list.Element{}}
}

// Get returns the image for k, if cached and fresh.
func (c *Cache[K]) Get(k K) (Image, bool) {
	if c == nil {
		return Image{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[k]
	if !ok {
		return Image{}, false
	}
	e := el.Value.(*entry[K])
	if c.now().After(e.expires) {
		c.remove(el)
		return Image{}, false
	}
	c.order.MoveToFront(el)
	return e.img, true
}

// Put caches img under k, evicting the least recently used to make room.
func (c *Cache[K]) Put(k K, img Image) {
	if c == nil || int64(len(img.Data)) > c.max {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[k]; ok {
		c.remove(el)
	}
	c.byKey[k] = c.order.PushFront(&entry[K]{key: k, img: img, expires: c.now().Add(c.ttl)})
	c.size += int64(len(img.Data))
	for c.size > c.max {
		c.remove(c.order.Back())
	}
}

// remove drops an entry. The caller holds mu.
func (c *Cache[K]) remove(el *list.Element) {
	e := c.order.Remove(el).(*entry[K])
	delete(c.byKey, e.key)
	c.size -= int64(len(e.img.Data))
}
