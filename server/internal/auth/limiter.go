// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"sync"
	"time"
)

// limiter counts failures per key in a fixed window. Once a key reaches max
// failures, it is blocked until its window ends.
type limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	byKey map[string]*bucket
}

type bucket struct {
	failures int
	until    time.Time
}

func newLimiter(maxFailures int, window time.Duration, now func() time.Time) *limiter {
	return &limiter{max: maxFailures, window: window, now: now, byKey: map[string]*bucket{}}
}

// blocked reports whether key is over its limit, and for how much longer.
func (l *limiter) blocked(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.live(key)
	if b == nil || b.failures < l.max {
		return 0, false
	}
	return b.until.Sub(l.now()), true
}

// fail records a failure for key.
func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.live(key)
	if b == nil {
		if len(l.byKey) > 10_000 {
			l.sweep()
		}
		b = &bucket{until: l.now().Add(l.window)}
		l.byKey[key] = b
	}
	b.failures++
}

// reset forgets key's failures, e.g. after a successful sign-in.
func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.byKey, key)
}

// live returns key's bucket if its window hasn't ended. The caller holds mu.
func (l *limiter) live(key string) *bucket {
	b := l.byKey[key]
	if b != nil && !l.now().Before(b.until) {
		delete(l.byKey, key)
		return nil
	}
	return b
}

func (l *limiter) sweep() {
	now := l.now()
	for k, b := range l.byKey {
		if !now.Before(b.until) {
			delete(l.byKey, k)
		}
	}
}
