// SPDX-License-Identifier: AGPL-3.0-only

package provider

import (
	"fmt"
	"sync"
)

// Registry holds the providers the server knows about. main builds it at
// startup; there is no global registry, so tests can build their own.
type Registry struct {
	mu    sync.RWMutex
	byID  map[string]Provider
	order []Provider
}

// NewRegistry returns a registry holding ps, or the first registration error.
func NewRegistry(ps ...Provider) (*Registry, error) {
	r := &Registry{byID: map[string]Provider{}}
	for _, p := range ps {
		if err := r.Register(p); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Register adds p. It fails if p is invalid or its ID is taken.
func (r *Registry) Register(p Provider) error {
	if err := Validate(p); err != nil {
		return err
	}
	id := p.Info().ID
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byID == nil {
		r.byID = map[string]Provider{}
	}
	if _, ok := r.byID[id]; ok {
		return fmt.Errorf("provider %s: already registered", id)
	}
	r.byID[id] = p
	r.order = append(r.order, p)
	return nil
}

// Get returns the provider with the given ID.
func (r *Registry) Get(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byID[id]
	return p, ok
}

// All returns the providers in registration order.
func (r *Registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Provider(nil), r.order...)
}
