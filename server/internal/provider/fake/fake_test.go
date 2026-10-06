// SPDX-License-Identifier: AGPL-3.0-only

package fake_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/providertest"
)

func TestConformance(t *testing.T) {
	for _, playback := range []provider.PlaybackMode{provider.PlaybackStream, provider.PlaybackRemote} {
		for _, method := range []provider.LinkMethod{provider.LinkCredentials, provider.LinkOAuth2, provider.LinkDevice, "oauth2+pair"} {
			t.Run(string(playback)+"/"+string(method), func(t *testing.T) {
				opts := fake.Options{Playback: playback, Link: method}
				if method == "oauth2+pair" {
					opts.Link, opts.Pair = provider.LinkOAuth2, true
				}
				p := fake.New(opts)
				bad := provider.LinkInput{Fields: map[string]string{"username": fake.Username, "password": "wrong"}}
				switch {
				case opts.Pair:
					// The right code, but no pairing.
					bad = provider.LinkInput{Code: fake.Code, OAuthSecret: "x"}
				case method == provider.LinkOAuth2:
					bad = provider.LinkInput{Code: "denied", OAuthSecret: "x"}
				case method == provider.LinkDevice:
					bad = provider.LinkInput{Paired: "never-paired"}
				}
				providertest.Run(t, providertest.Harness{
					Provider: p,
					Link:     func(t *testing.T) provider.Link { return link(t, p) },
					Query:    "zero",
					BadInput: &bad,
					Expire:   func(*testing.T, provider.Link) { p.Revoke(fake.Username) },
				})
			})
		}
	}
}

// link links the demo account through p's linker, whichever method it uses.
func link(t *testing.T, p *fake.Provider) provider.Link {
	t.Helper()
	l := p.Linker()
	in := provider.LinkInput{Fields: map[string]string{"username": fake.Username, "password": fake.Password}}
	if dp, ok := l.(provider.DevicePairer); ok {
		pairing, err := dp.BeginPairing(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dp.PollPairing(t.Context(), pairing.Secret); !errors.Is(err, provider.ErrPending) {
			t.Fatalf("first poll: %v, want ErrPending", err)
		}
		if in.Paired, err = dp.PollPairing(t.Context(), pairing.Secret); err != nil {
			t.Fatal(err)
		}
	}
	if l.Method() == provider.LinkOAuth2 {
		start, err := l.BeginOAuth(t.Context(), provider.OAuthRequest{State: "s", RedirectURL: "https://example.com/cb"})
		if err != nil {
			t.Fatal(err)
		}
		in.Code, in.OAuthSecret = fake.Code, start.Secret
	}
	creds, account, err := l.Complete(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return provider.Link{ID: "link-1", Account: account, Credentials: creds}
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestTokenRefreshCallsSink(t *testing.T) {
	clk := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	p := fake.New(fake.Options{Link: provider.LinkOAuth2, Now: clk.Now, TokenTTL: time.Minute})
	l := link(t, p)
	var saved []provider.Credentials
	l.Sink = func(_ context.Context, c provider.Credentials) error {
		saved = append(saved, c)
		return nil
	}
	sess, err := p.Open(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Track(t.Context(), "t01"); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 0 {
		t.Fatalf("refreshed before expiry: %d saves", len(saved))
	}
	clk.Add(2 * time.Minute)
	if _, err := sess.Track(t.Context(), "t01"); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || string(saved[0]) == string(l.Credentials) {
		t.Fatalf("after expiry: %d saves, want 1 with new credentials", len(saved))
	}

	// The rotated credentials open a working session.
	l.Credentials = saved[0]
	sess2, err := p.Open(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess2.Track(t.Context(), "t01"); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 {
		t.Fatalf("fresh credentials refreshed again: %d saves", len(saved))
	}
}

func TestFail(t *testing.T) {
	p := fake.New(fake.Options{})
	sess, err := p.Open(t.Context(), link(t, p))
	if err != nil {
		t.Fatal(err)
	}
	p.Fail(&provider.RateLimitError{RetryAfter: 3 * time.Second})
	_, err = sess.Track(t.Context(), "t01")
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Fatalf("got %v, want ErrRateLimited", err)
	}
	if d, ok := provider.RetryAfter(err); !ok || d != 3*time.Second {
		t.Fatalf("RetryAfter = %v, %v", d, ok)
	}
	p.Fail(nil)
	if _, err := sess.Track(t.Context(), "t01"); err != nil {
		t.Fatal(err)
	}
}

func TestRemotePositionAdvances(t *testing.T) {
	clk := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	p := fake.New(fake.Options{Playback: provider.PlaybackRemote, Now: clk.Now})
	sess, err := p.Open(t.Context(), link(t, p))
	if err != nil {
		t.Fatal(err)
	}
	r := sess.(provider.Remote)
	if err := r.Play(t.Context(), "t01", 0); err != nil {
		t.Fatal(err)
	}
	clk.Add(3 * time.Second)
	st, err := r.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.Position != 3*time.Second || !st.Playing {
		t.Fatalf("after 3s: %+v", st)
	}
	clk.Add(time.Hour)
	if st, _ = r.State(t.Context()); st.Playing || st.Position != 20*time.Second {
		t.Fatalf("past the end: %+v, want stopped at 20s", st)
	}
}

func TestStreamIsWAV(t *testing.T) {
	p := fake.New(fake.Options{})
	sess, err := p.Open(t.Context(), link(t, p))
	if err != nil {
		t.Fatal(err)
	}
	a, err := sess.(provider.Streamer).Stream(t.Context(), "t01", provider.StreamOpts{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Body.Close()
	head := make([]byte, 12)
	if _, err := io.ReadFull(a.Body, head); err != nil {
		t.Fatal(err)
	}
	if string(head[:4]) != "RIFF" || string(head[8:]) != "WAVE" {
		t.Fatalf("header %q", head)
	}
	// 20s at 8 kHz, 8-bit mono, plus the 44-byte header.
	if a.Size != 44+20*8000 {
		t.Fatalf("Size = %d", a.Size)
	}
}
