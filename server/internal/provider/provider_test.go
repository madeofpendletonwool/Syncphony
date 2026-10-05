// SPDX-License-Identifier: AGPL-3.0-only

package provider_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
)

func TestRegistry(t *testing.T) {
	a := fake.New(fake.Options{ID: "a"})
	b := fake.New(fake.Options{ID: "b"})
	r, err := provider.NewRegistry(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := r.Get("b"); !ok || p != b {
		t.Errorf("Get(b) = %v, %v", p, ok)
	}
	if _, ok := r.Get("c"); ok {
		t.Error("Get(c) found a provider")
	}
	if all := r.All(); len(all) != 2 || all[0] != a || all[1] != b {
		t.Errorf("All() = %v, want [a b]", all)
	}
	if err := r.Register(fake.New(fake.Options{ID: "a"})); err == nil {
		t.Error("registering a duplicate ID succeeded")
	}
	var zero provider.Registry
	if err := zero.Register(a); err != nil {
		t.Errorf("zero Registry: %v", err)
	}
}

func TestValidate(t *testing.T) {
	for _, id := range []string{"Navidrome", "9lives", "has space", strings.Repeat("a", 33)} {
		if err := provider.Validate(fake.New(fake.Options{ID: id})); err == nil {
			t.Errorf("ID %q accepted", id)
		}
	}
	if err := provider.Validate(fake.New(fake.Options{ID: "you-tube-music2"})); err != nil {
		t.Error(err)
	}
	if err := provider.Validate(fake.New(fake.Options{Playback: "carrier-pigeon"})); err == nil {
		t.Error("unknown playback mode accepted")
	}
}

func TestErrors(t *testing.T) {
	err := fmt.Errorf("spotify: search: %w", &provider.RateLimitError{RetryAfter: 2 * time.Second})
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Error("RateLimitError is not ErrRateLimited")
	}
	if d, ok := provider.RetryAfter(err); !ok || d != 2*time.Second {
		t.Errorf("RetryAfter = %v, %v", d, ok)
	}
	if _, ok := provider.RetryAfter(provider.ErrRateLimited); ok {
		t.Error("RetryAfter found a delay on the bare sentinel")
	}
}

func TestContentRange(t *testing.T) {
	for _, tc := range []struct {
		a    provider.AudioStream
		want string
	}{
		{provider.AudioStream{Offset: 0, Length: 100, Size: 100}, ""},
		{provider.AudioStream{Offset: 0, Length: -1, Size: -1}, ""},
		{provider.AudioStream{Offset: 10, Length: 90, Size: 100}, "bytes 10-99/100"},
		{provider.AudioStream{Offset: 0, Length: 50, Size: 100}, "bytes 0-49/100"},
		{provider.AudioStream{Offset: 10, Length: 5, Size: -1}, "bytes 10-14/*"},
	} {
		if got := tc.a.ContentRange(); got != tc.want {
			t.Errorf("%+v: ContentRange() = %q, want %q", tc.a, got, tc.want)
		}
	}
}
