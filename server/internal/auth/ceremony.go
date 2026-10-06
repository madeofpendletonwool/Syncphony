// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// ceremonyTTL is how long a WebAuthn ceremony may take.
const ceremonyTTL = 5 * time.Minute

type ceremonyKind int

const (
	ceremonySignup ceremonyKind = iota + 1
	ceremonyLogin
	ceremonyAddPasskey
	ceremonyReset
)

// ceremony is server-side state between a WebAuthn begin and finish call.
type ceremony struct {
	kind    ceremonyKind
	session webauthn.SessionData
	expires time.Time

	// userID is the user registering (signup: the ID they will get).
	userID string
	// signup holds the account to create when a signup ceremony finishes.
	signup *signupInput
	// resetID is the reset link a reset ceremony uses up.
	resetID string
}

// ceremonies is an in-memory store. Ceremonies are short-lived and a
// restart only means the user retries, so they aren't persisted.
type ceremonies struct {
	now func() time.Time

	mu   sync.Mutex
	byID map[string]*ceremony
}

func (c *ceremonies) put(cer *ceremony) string {
	id := rand.Text()
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, v := range c.byID {
		if now.After(v.expires) {
			delete(c.byID, k)
		}
	}
	cer.expires = now.Add(ceremonyTTL)
	c.byID[id] = cer
	return id
}

// take removes and returns the ceremony, which must be of the given kind.
// Each ceremony can be finished once.
func (c *ceremonies) take(id string, kind ceremonyKind) (*ceremony, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cer, ok := c.byID[id]
	if !ok || cer.kind != kind {
		return nil, ErrCeremonyExpired
	}
	delete(c.byID, id)
	if c.now().After(cer.expires) {
		return nil, ErrCeremonyExpired
	}
	return cer, nil
}
