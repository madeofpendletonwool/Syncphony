// SPDX-License-Identifier: AGPL-3.0-only

package httpapi_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

// authenticator is a software passkey: enough of a WebAuthn authenticator
// and browser to drive registration and sign-in through the real verifier.
type authenticator struct {
	origin     string
	key        *ecdsa.PrivateKey
	credID     []byte
	userHandle []byte
	count      uint32
}

func newAuthenticator(t *testing.T, origin string) *authenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &authenticator{origin: origin, key: key, credID: id}
}

var b64 = base64.RawURLEncoding

// field digs a string out of nested JSON objects.
func field(t *testing.T, m map[string]any, path ...string) string {
	t.Helper()
	for _, k := range path[:len(path)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			t.Fatalf("options: no object at %q in %v", k, m)
		}
		m = next
	}
	s, ok := m[path[len(path)-1]].(string)
	if !ok {
		t.Fatalf("options: no string at %v", path)
	}
	return s
}

func (a *authenticator) clientData(t *testing.T, typ, challenge string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// authData builds authenticator data. Flags: user present and verified.
func (a *authenticator) authData(rpID string, attested []byte) []byte {
	rpHash := sha256.Sum256([]byte(rpID))
	flags := byte(0x01 | 0x04)
	if attested != nil {
		flags |= 0x40
	}
	out := append(rpHash[:], flags)
	out = binary.BigEndian.AppendUint32(out, a.count)
	return append(out, attested...)
}

// create answers navigator.credentials.create.
func (a *authenticator) create(t *testing.T, options map[string]any) map[string]any {
	t.Helper()
	challenge := field(t, options, "publicKey", "challenge")
	rpID := field(t, options, "publicKey", "rp", "id")
	handle, err := b64.DecodeString(field(t, options, "publicKey", "user", "id"))
	if err != nil {
		t.Fatal(err)
	}
	a.userHandle = handle

	pub := a.key.PublicKey
	x, y := make([]byte, 32), make([]byte, 32)
	pub.X.FillBytes(x) //nolint:staticcheck // fine for a test key
	pub.Y.FillBytes(y) //nolint:staticcheck // fine for a test key
	coseKey, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	if err != nil {
		t.Fatal(err)
	}
	attested := make([]byte, 16)                                              // AAGUID: zeros
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(a.credID))) //nolint:gosec // 16 bytes
	attested = append(attested, a.credID...)
	attested = append(attested, coseKey...)

	attObj, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(rpID, attested)})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"id":    b64.EncodeToString(a.credID),
		"rawId": b64.EncodeToString(a.credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(a.clientData(t, "webauthn.create", challenge)),
			"attestationObject": b64.EncodeToString(attObj),
			"transports":        []string{"internal"},
		},
		"clientExtensionResults":  map[string]any{},
		"authenticatorAttachment": "platform",
	}
}

// get answers navigator.credentials.get.
func (a *authenticator) get(t *testing.T, options map[string]any) map[string]any {
	t.Helper()
	challenge := field(t, options, "publicKey", "challenge")
	rpID := field(t, options, "publicKey", "rpId")
	a.count++
	authData := a.authData(rpID, nil)
	cd := a.clientData(t, "webauthn.get", challenge)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, authData...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"id":    b64.EncodeToString(a.credID),
		"rawId": b64.EncodeToString(a.credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cd),
			"authenticatorData": b64.EncodeToString(authData),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(a.userHandle),
		},
		"clientExtensionResults":  map[string]any{},
		"authenticatorAttachment": "platform",
	}
}
