// SPDX-License-Identifier: AGPL-3.0-only

// Package vault encrypts linked-service credentials at rest.
//
// It uses envelope encryption. Each record gets a fresh random data key
// that encrypts the credentials with AES-256-GCM. The data key is in turn
// encrypted ("wrapped") by the master key, and stored next to the
// ciphertext. Rotating the master key only re-wraps the small data keys.
//
// Both layers bind the record's link ID and user ID as associated data, so
// a ciphertext copied into another row fails to decrypt.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// KeySize is the master key size: 32 bytes, for AES-256.
const KeySize = 32

// Sealed record layout (version 1):
//
//	version(1) | keyID(8) | dekNonce(12) | wrappedDEK(32+16) | dataNonce(12) | ciphertext+tag
const (
	version1   = 1
	idLen      = 8
	nonceLen   = 12
	tagLen     = 16
	dekLen     = 32
	wrappedLen = dekLen + tagLen
	headerLen  = 1 + idLen + nonceLen + wrappedLen + nonceLen
)

var (
	// ErrUnknownKey means the record was sealed with a master key the vault
	// doesn't have (e.g. the key changed without a rotation).
	ErrUnknownKey = errors.New("vault: sealed with an unknown master key")
	// ErrCorrupt means the record is malformed, tampered with, or belongs to
	// another row.
	ErrCorrupt = errors.New("vault: record is corrupt or doesn't belong to this link")
)

// Key is a master key.
type Key struct {
	id  [idLen]byte
	aes cipher.AEAD
}

// NewKey makes a key from 32 bytes.
func NewKey(b []byte) (*Key, error) {
	if len(b) != KeySize {
		return nil, fmt.Errorf("vault: key is %d bytes, want %d", len(b), KeySize)
	}
	aead, err := newGCM(b)
	if err != nil {
		return nil, err
	}
	k := &Key{aes: aead}
	// The key ID is a MAC, not a plain hash, so it says nothing about the key.
	mac := hmac.New(sha256.New, b)
	mac.Write([]byte("syncphony vault key id"))
	copy(k.id[:], mac.Sum(nil))
	return k, nil
}

// ParseKey decodes a base64 key, as printed by GenerateKey.
func ParseKey(s string) (*Key, error) {
	s = strings.TrimSpace(s)
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		if b, err = base64.RawURLEncoding.DecodeString(s); err != nil {
			return nil, errors.New("vault: key isn't valid base64")
		}
	}
	return NewKey(b)
}

// GenerateKey returns a new random key, base64-encoded.
func GenerateKey() string {
	b := make([]byte, KeySize)
	_, _ = rand.Read(b) // never fails
	return base64.StdEncoding.EncodeToString(b)
}

// ID identifies the key in sealed records and logs.
func (k *Key) ID() string { return fmt.Sprintf("%x", k.id) }

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Vault seals and opens records. It seals with the current key and opens
// with any of its keys.
type Vault struct {
	current *Key
	keys    map[[idLen]byte]*Key
}

// New returns a vault that seals with current. old keys can still open
// records, until they're rotated.
func New(current *Key, old ...*Key) *Vault {
	v := &Vault{current: current, keys: map[[idLen]byte]*Key{current.id: current}}
	for _, k := range old {
		v.keys[k.id] = k
	}
	return v
}

// CurrentKeyID is the ID of the sealing key.
func (v *Vault) CurrentKeyID() string { return v.current.ID() }

// aad binds a record to its row. The wrapped data key is also bound to the
// master key's ID.
func aad(linkID, userID string, keyID []byte) []byte {
	return []byte("syncphony-vault-v1\x00" + linkID + "\x00" + userID + "\x00" + string(keyID))
}

// Seal encrypts plaintext for the link row (linkID, userID).
func (v *Vault) Seal(linkID, userID string, plaintext []byte) ([]byte, error) {
	dek := make([]byte, dekLen)
	_, _ = rand.Read(dek)
	data, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	out := v.wrap(make([]byte, 0, headerLen+len(plaintext)+tagLen), linkID, userID, dek)
	out = appendNonce(out)
	return data.Seal(out, out[len(out)-nonceLen:], plaintext, aad(linkID, userID, nil)), nil
}

// wrap appends version | keyID | dekNonce | wrappedDEK, using the current key.
func (v *Vault) wrap(out []byte, linkID, userID string, dek []byte) []byte {
	k := v.current
	out = append(out, version1)
	out = append(out, k.id[:]...)
	out = appendNonce(out)
	return k.aes.Seal(out, out[len(out)-nonceLen:], dek, aad(linkID, userID, k.id[:]))
}

func appendNonce(b []byte) []byte {
	n := make([]byte, nonceLen)
	_, _ = rand.Read(n)
	return append(b, n...)
}

// parts splits a sealed record.
type parts struct {
	keyID      []byte
	dekNonce   []byte
	wrapped    []byte
	dataNonce  []byte
	ciphertext []byte
}

func split(sealed []byte) (parts, error) {
	if len(sealed) < headerLen+tagLen || sealed[0] != version1 {
		return parts{}, ErrCorrupt
	}
	p := parts{}
	rest := sealed[1:]
	p.keyID, rest = rest[:idLen], rest[idLen:]
	p.dekNonce, rest = rest[:nonceLen], rest[nonceLen:]
	p.wrapped, rest = rest[:wrappedLen], rest[wrappedLen:]
	p.dataNonce, p.ciphertext = rest[:nonceLen], rest[nonceLen:]
	return p, nil
}

// unwrap returns the record's data key.
func (v *Vault) unwrap(p parts, linkID, userID string) ([]byte, error) {
	k, ok := v.keys[[idLen]byte(p.keyID)]
	if !ok {
		return nil, ErrUnknownKey
	}
	dek, err := k.aes.Open(nil, p.dekNonce, p.wrapped, aad(linkID, userID, p.keyID))
	if err != nil {
		return nil, ErrCorrupt
	}
	return dek, nil
}

// Open decrypts a record sealed for (linkID, userID).
func (v *Vault) Open(linkID, userID string, sealed []byte) ([]byte, error) {
	p, err := split(sealed)
	if err != nil {
		return nil, err
	}
	dek, err := v.unwrap(p, linkID, userID)
	if err != nil {
		return nil, err
	}
	defer clear(dek)
	data, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	plain, err := data.Open(nil, p.dataNonce, p.ciphertext, aad(linkID, userID, nil))
	if err != nil {
		return nil, ErrCorrupt
	}
	return plain, nil
}

// NeedsRewrap reports whether a record is sealed with an older key.
func (v *Vault) NeedsRewrap(sealed []byte) bool {
	p, err := split(sealed)
	return err == nil && [idLen]byte(p.keyID) != v.current.id
}

// Rewrap re-wraps a record's data key with the current master key. The
// encrypted credentials are carried over untouched, after checking they
// still decrypt.
func (v *Vault) Rewrap(linkID, userID string, sealed []byte) ([]byte, error) {
	plain, err := v.Open(linkID, userID, sealed)
	if err != nil {
		return nil, err
	}
	clear(plain)
	p, _ := split(sealed)
	dek, err := v.unwrap(p, linkID, userID)
	if err != nil {
		return nil, err
	}
	defer clear(dek)
	out := v.wrap(make([]byte, 0, len(sealed)), linkID, userID, dek)
	out = append(out, p.dataNonce...)
	return append(out, p.ciphertext...), nil
}
