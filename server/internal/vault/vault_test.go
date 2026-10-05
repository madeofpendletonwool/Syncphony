// SPDX-License-Identifier: AGPL-3.0-only

package vault

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func key(t *testing.T) *Key {
	t.Helper()
	k, err := ParseKey(GenerateKey())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealOpen(t *testing.T) {
	v := New(key(t))
	secret := []byte(`{"token":"hunter2"}`)
	sealed, err := v.Seal("link-1", "user-1", secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("hunter2")) {
		t.Fatal("plaintext visible in the sealed record")
	}
	got, err := v.Open("link-1", "user-1", sealed)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("Open = %q, %v", got, err)
	}
	again, _ := v.Seal("link-1", "user-1", secret)
	if bytes.Equal(again, sealed) {
		t.Fatal("sealing twice gave identical records")
	}
}

func TestBoundToRow(t *testing.T) {
	v := New(key(t))
	sealed, _ := v.Seal("link-1", "user-1", []byte("x"))
	for _, row := range [][2]string{{"link-2", "user-1"}, {"link-1", "user-2"}, {"link-1user-1", ""}} {
		if _, err := v.Open(row[0], row[1], sealed); !errors.Is(err, ErrCorrupt) {
			t.Errorf("opened as %v: %v", row, err)
		}
	}
}

func TestTamper(t *testing.T) {
	v := New(key(t))
	sealed, _ := v.Seal("l", "u", []byte("credentials"))
	for i := range sealed {
		bad := bytes.Clone(sealed)
		bad[i] ^= 1
		if _, err := v.Open("l", "u", bad); err == nil {
			t.Fatalf("flipping byte %d went unnoticed", i)
		}
	}
	if _, err := v.Open("l", "u", sealed[:10]); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncated: %v", err)
	}
}

func TestUnknownKey(t *testing.T) {
	sealed, _ := New(key(t)).Seal("l", "u", []byte("x"))
	if _, err := New(key(t)).Open("l", "u", sealed); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("got %v, want ErrUnknownKey", err)
	}
}

func TestRotate(t *testing.T) {
	oldKey, newKey := key(t), key(t)
	sealed, _ := New(oldKey).Seal("l", "u", []byte("credentials"))

	v := New(newKey, oldKey)
	if !v.NeedsRewrap(sealed) {
		t.Fatal("old record doesn't need rewrapping")
	}
	if got, err := v.Open("l", "u", sealed); err != nil || string(got) != "credentials" {
		t.Fatalf("old record with the old key as fallback: %q, %v", got, err)
	}
	rewrapped, err := v.Rewrap("l", "u", sealed)
	if err != nil {
		t.Fatal(err)
	}
	if v.NeedsRewrap(rewrapped) {
		t.Fatal("rewrapped record still needs rewrapping")
	}
	// Only the key wrapping changed; the encrypted credentials are the same bytes.
	if !bytes.Equal(rewrapped[headerLen-nonceLen:], sealed[headerLen-nonceLen:]) {
		t.Fatal("rewrap re-encrypted the data")
	}
	if got, err := New(newKey).Open("l", "u", rewrapped); err != nil || string(got) != "credentials" {
		t.Fatalf("rewrapped record with only the new key: %q, %v", got, err)
	}
	if _, err := New(oldKey).Open("l", "u", rewrapped); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("rewrapped record opened with the old key: %v", err)
	}
}

func TestParseKey(t *testing.T) {
	for _, s := range []string{"", "not base64!", "c2hvcnQ="} {
		if _, err := ParseKey(s); err == nil {
			t.Errorf("ParseKey(%q) succeeded", s)
		}
	}
	k1, _ := ParseKey("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	k2, _ := ParseKey("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if k1 == nil || k2 == nil || k1.ID() != k2.ID() {
		t.Fatal("padded and unpadded encodings of a key differ")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/vault.key"
	v1, created, err := Load(KeySource{}, file)
	if err != nil || !created {
		t.Fatalf("first run: created %v, %v", created, err)
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode: %v, %v", fi.Mode(), err)
	}
	v2, created, err := Load(KeySource{}, file)
	if err != nil || created || v2.CurrentKeyID() != v1.CurrentKeyID() {
		t.Fatalf("second run: created %v, same key %v, %v", created, v2.CurrentKeyID() == v1.CurrentKeyID(), err)
	}
	k := GenerateKey()
	v3, _, err := Load(KeySource{Key: k, OldKeys: []string{GenerateKey()}}, file)
	if err != nil || v3.CurrentKeyID() == v1.CurrentKeyID() || len(v3.keys) != 2 {
		t.Fatalf("explicit key: %v", err)
	}
	if _, _, err := Load(KeySource{KeyFile: dir + "/missing"}, file); err == nil {
		t.Fatal("missing explicit key file accepted")
	}
	if _, _, err := Load(KeySource{Key: "short"}, file); err == nil {
		t.Fatal("bad key accepted")
	}
}
