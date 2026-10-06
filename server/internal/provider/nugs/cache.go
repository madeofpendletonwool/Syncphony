// SPDX-License-Identifier: AGPL-3.0-only

package nugs

import (
	"sync"
	"time"
)

// ttlCache is a small map whose entries expire. When full it drops expired
// entries, then arbitrary ones: it holds catalog data that's cheap to
// fetch again, so which goes doesn't matter much.
type ttlCache[V any] struct {
	now func() time.Time
	ttl time.Duration
	max int

	mu sync.Mutex
	m  map[string]ttlEntry[V]
}

type ttlEntry[V any] struct {
	v  V
	at time.Time
}

func newTTLCache[V any](ttl time.Duration, maxEntries int, now func() time.Time) *ttlCache[V] {
	return &ttlCache[V]{now: now, ttl: ttl, max: maxEntries, m: map[string]ttlEntry[V]{}}
}

func (c *ttlCache[V]) get(k string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || c.now().Sub(e.at) >= c.ttl {
		var zero V
		return zero, false
	}
	return e.v, true
}

func (c *ttlCache[V]) put(k string, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.m) >= c.max {
		for k, e := range c.m {
			if now.Sub(e.at) >= c.ttl {
				delete(c.m, k)
			}
		}
		for k := range c.m {
			if len(c.m) < c.max {
				break
			}
			delete(c.m, k)
		}
	}
	c.m[k] = ttlEntry[V]{v: v, at: now}
}

// showCache holds show details by ID. A show's setlist doesn't change once
// it's posted, so an hour is plenty.
type showCache = ttlCache[*show]

func newShowCache(now func() time.Time) *showCache {
	return newTTLCache[*show](time.Hour, 4096, now)
}

// searchCache holds search results by query, long enough to page through.
type searchCache = ttlCache[*results]

func newSearchCache(now func() time.Time) *searchCache {
	return newTTLCache[*results](5*time.Minute, 256, now)
}
