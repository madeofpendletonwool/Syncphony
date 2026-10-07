// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

var cheap = PasswordCost{MemoryKiB: 64, Time: 1, Threads: 1}

func TestPasswordHash(t *testing.T) {
	h := cheap.hashPassword("correct horse")
	if !strings.HasPrefix(h, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("hash %q", h)
	}
	if h == cheap.hashPassword("correct horse") {
		t.Fatal("hashes aren't salted")
	}
	ok, rehash, err := cheap.verifyPassword(h, "correct horse")
	if err != nil || !ok || rehash {
		t.Fatalf("verify right password: %v %v %v", ok, rehash, err)
	}
	if ok, _, _ := cheap.verifyPassword(h, "Correct horse"); ok {
		t.Fatal("wrong password verified")
	}
	stronger := PasswordCost{MemoryKiB: 128, Time: 1, Threads: 1}
	if ok, rehash, _ := stronger.verifyPassword(h, "correct horse"); !ok || !rehash {
		t.Fatalf("after raising the cost: ok %v, rehash %v", ok, rehash)
	}
	if _, _, err := cheap.verifyPassword("$2a$10$bcrypt", "x"); err == nil {
		t.Fatal("accepted a non-argon2 hash")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := newLimiter(3, time.Minute, func() time.Time { return now })
	for range 3 {
		if _, blocked := l.blocked("k"); blocked {
			t.Fatal("blocked too early")
		}
		l.fail("k")
	}
	d, blocked := l.blocked("k")
	if !blocked || d != time.Minute {
		t.Fatalf("after 3 failures: blocked %v for %v", blocked, d)
	}
	if _, blocked := l.blocked("other"); blocked {
		t.Fatal("keys aren't independent")
	}
	now = now.Add(time.Minute)
	if _, blocked := l.blocked("k"); blocked {
		t.Fatal("still blocked after the window")
	}
	l.fail("k")
	l.reset("k")
	if len(l.byKey) != 0 {
		t.Fatal("reset didn't forget the key")
	}
}

func TestValidation(t *testing.T) {
	for _, u := range []string{"al", "alice", "a.b-c_d", strings.Repeat("a", 32)} {
		if err := validateUsername(u); err != nil {
			t.Errorf("username %q rejected: %v", u, err)
		}
	}
	for _, u := range []string{"a", "Alice", "al ice", "al@ice", strings.Repeat("a", 33)} {
		if validateUsername(u) == nil {
			t.Errorf("username %q accepted", u)
		}
	}
	for _, a := range []string{"", "https://example.com/a.png", "/api/avatars/1", "icon:headphones", "icon:disc-3"} {
		if err := validateAvatar(a); err != nil {
			t.Errorf("avatar %q rejected: %v", a, err)
		}
	}
	for _, a := range []string{"javascript:alert(1)", "data:image/png;base64,AA", "//evil.com/a.png", "a.png", "icon:", "icon:Bad Name", "icon:../x"} {
		if validateAvatar(a) == nil {
			t.Errorf("avatar %q accepted", a)
		}
	}
	if validatePassword("p", "short") == nil || validatePassword("p", strings.Repeat("x", 257)) == nil {
		t.Error("bad password lengths accepted")
	}
}

func TestPickColor(t *testing.T) {
	var users []store.User
	seen := map[string]bool{}
	for range palette {
		c := pickColor(users)
		if seen[c] {
			t.Fatalf("color %s handed out twice before the palette ran out", c)
		}
		seen[c] = true
		users = append(users, store.User{Color: c})
	}
	if c := pickColor(users); c != palette[0] {
		t.Fatalf("after a full round got %s, want %s", c, palette[0])
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("héllo", 2); got != "h" {
		t.Fatalf("truncate split a rune: %q", got)
	}
}
