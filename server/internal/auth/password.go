// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// PasswordCost sets argon2id's cost. Raising it makes existing hashes get
// rehashed at the next sign-in.
type PasswordCost struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
}

// DefaultPasswordCost is about 50ms and 64 MiB per hash.
var DefaultPasswordCost = PasswordCost{MemoryKiB: 64 * 1024, Time: 2, Threads: 1}

const (
	saltLen = 16
	keyLen  = 32
)

// hashPassword returns a PHC-format argon2id hash.
func (p PasswordCost) hashPassword(pw string) string {
	salt := make([]byte, saltLen)
	_, _ = rand.Read(salt) // never fails
	key := argon2.IDKey([]byte(pw), salt, p.Time, p.MemoryKiB, p.Threads, keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.MemoryKiB, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// verifyPassword checks pw against a hash from hashPassword. needsRehash is
// set when the hash used a different cost than p.
func (p PasswordCost) verifyPassword(hash, pw string) (ok, needsRehash bool, err error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, false, errors.New("auth: unknown password hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, false, fmt.Errorf("auth: unsupported argon2 version %q", parts[2])
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, false, fmt.Errorf("auth: bad argon2 parameters %q", parts[3])
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, false, err
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(pw), salt, time, memory, threads, uint32(len(want))) //nolint:gosec // len is a few dozen bytes
	needsRehash = memory != p.MemoryKiB || time != p.Time || threads != p.Threads
	return subtle.ConstantTimeCompare(got, want) == 1, needsRehash, nil
}
