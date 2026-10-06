// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Displays are TVs, projectors and spare tablets showing one room's big
// screen. One shows a short code; a signed-in member types it in to pair
// the display with their room. The display then has its own token, good
// for reading that room and nothing else, until it's unpaired.

const (
	// pairingTTL is how long a display's code can be typed in.
	pairingTTL = 10 * time.Minute
	// maxPairings bounds codes waiting to be typed in, server-wide.
	maxPairings = 256
	// pairingCodeLen characters from pairingAlphabet: about 30 bits.
	pairingCodeLen = 6
	// Easy to read across a room: no 0/O, 1/I/L.
	pairingAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	// displayTTL slides like a session's, touched at most hourly.
	displayTTL        = 180 * 24 * time.Hour
	displayTouchEvery = time.Hour
	maxDisplayName    = 40
)

// Display errors.
var (
	// ErrPairingInvalid: no display is showing that code.
	ErrPairingInvalid = errors.New("no display is showing that code")
	// ErrPairingExpired: the display's code timed out; it shows a new one.
	ErrPairingExpired = errors.New("pairing code expired")
	// ErrTooManyPairings: too many displays waiting to be paired.
	ErrTooManyPairings = errors.New("too many displays are waiting to pair; try again in a few minutes")
)

// pairing is a display waiting for its code to be typed in.
type pairing struct {
	code    string
	expires time.Time
	// Set once paired, until the display collects them.
	token   string
	display *store.Display
}

type pairings struct {
	mu       sync.Mutex
	bySecret map[string]*pairing
	byCode   map[string]string // code -> secret
}

func (p *pairings) sweep(now time.Time) {
	for secret, pr := range p.bySecret {
		if !now.Before(pr.expires) {
			delete(p.bySecret, secret)
			delete(p.byCode, pr.code)
		}
	}
}

func newPairingCode() string {
	b := make([]byte, pairingCodeLen)
	for i := range b {
		b[i] = pairingAlphabet[randIntn(len(pairingAlphabet))]
	}
	return string(b)
}

func randIntn(n int) int {
	// Rejection sampling keeps it uniform.
	limit := 256 - 256%n
	var b [1]byte
	for {
		_, _ = rand.Read(b[:])
		if int(b[0]) < limit {
			return int(b[0]) % n
		}
	}
}

// NormalizePairingCode uppercases a typed code and drops spaces and dashes.
func NormalizePairingCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, strings.ToUpper(strings.TrimSpace(code)))
}

// DisplayPairing is a display's pairing as it waits.
type DisplayPairing struct {
	// Secret is the display's handle on the pairing, kept in a cookie.
	Secret  string
	Code    string
	Expires time.Time
	// Begun is when the pairing started, by the server's clock.
	Begun time.Time
	// Token and Display are set once it's paired. The token is handed out once.
	Token   string
	Display *store.Display
}

// BeginDisplayPairing makes a code for a display to show.
func (s *Service) BeginDisplayPairing() (DisplayPairing, error) {
	now := s.now()
	p := s.pairings
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweep(now)
	if len(p.bySecret) >= maxPairings {
		return DisplayPairing{}, ErrTooManyPairings
	}
	code := newPairingCode()
	for p.byCode[code] != "" {
		code = newPairingCode()
	}
	secret, _ := newToken()
	pr := &pairing{code: code, expires: now.Add(pairingTTL)}
	p.bySecret[secret] = pr
	p.byCode[code] = secret
	return DisplayPairing{Secret: secret, Code: code, Expires: pr.expires, Begun: now}, nil
}

// PollDisplayPairing reports on a display's pairing. Once it's paired, the
// result carries the display's token, once; the pairing is then done.
func (s *Service) PollDisplayPairing(secret string) (DisplayPairing, error) {
	now := s.now()
	p := s.pairings
	p.mu.Lock()
	defer p.mu.Unlock()
	pr := p.bySecret[secret]
	if pr == nil {
		return DisplayPairing{}, ErrPairingExpired
	}
	out := DisplayPairing{Secret: secret, Code: pr.code, Expires: pr.expires, Token: pr.token, Display: pr.display}
	if pr.token != "" || !now.Before(pr.expires) {
		delete(p.bySecret, secret)
		delete(p.byCode, pr.code)
	}
	if pr.token == "" && !now.Before(pr.expires) {
		return DisplayPairing{}, ErrPairingExpired
	}
	return out, nil
}

// PairDisplay pairs the display showing code with a room, on u's say-so.
// The caller checks the room exists and u may use it. Wrong codes count
// against u, so codes can't be guessed.
func (s *Service) PairDisplay(ctx context.Context, u store.User, roomID, code, name string) (store.Display, error) {
	if d, ok := s.pairFails.blocked(u.ID); ok {
		return store.Display{}, &RateLimitError{RetryAfter: d}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "TV"
	}
	if utf8.RuneCountInString(name) > maxDisplayName {
		return store.Display{}, invalid("name", "must be at most %d characters", maxDisplayName)
	}
	code = NormalizePairingCode(code)
	now := s.now()
	p := s.pairings
	p.mu.Lock()
	defer p.mu.Unlock()
	pr := p.bySecret[p.byCode[code]]
	if pr == nil || pr.token != "" || !now.Before(pr.expires) {
		s.pairFails.fail(u.ID)
		return store.Display{}, ErrPairingInvalid
	}
	token, hash := newToken()
	d, err := s.db.CreateDisplay(ctx, store.CreateDisplayParams{
		ID: store.NewID(), TokenHash: hash, RoomID: roomID, Name: name, PairedBy: sql.NullString{String: u.ID, Valid: true},
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(displayTTL),
	})
	if err != nil {
		return store.Display{}, err
	}
	pr.token, pr.display = token, &d
	// Give the display a moment to collect it, even if the code was about to lapse.
	if grace := now.Add(time.Minute); pr.expires.Before(grace) {
		pr.expires = grace
	}
	return d, nil
}

// DisplaySession is a paired display, signed in.
type DisplaySession struct {
	Display store.Display
	// Token is set when the display's expiry was extended; resend the cookie.
	Token string
}

// AuthenticateDisplay returns the display for token, extending it if it
// hasn't been seen for a while.
func (s *Service) AuthenticateDisplay(ctx context.Context, token string) (*DisplaySession, error) {
	if token == "" {
		return nil, ErrUnauthenticated
	}
	now := s.now()
	d, err := s.db.GetDisplayByToken(ctx, store.GetDisplayByTokenParams{TokenHash: hashToken(token), Now: now})
	if store.IsNotFound(err) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	out := &DisplaySession{Display: d}
	if now.Sub(d.LastSeenAt) >= displayTouchEvery {
		out.Display.LastSeenAt, out.Display.ExpiresAt = now, now.Add(displayTTL)
		if err := s.db.TouchDisplay(ctx, store.TouchDisplayParams{LastSeenAt: now, ExpiresAt: out.Display.ExpiresAt, ID: d.ID}); err != nil {
			return nil, err
		}
		out.Token = token
	}
	return out, nil
}

// CheckDisplay reports whether token is still a paired display, without
// extending it.
func (s *Service) CheckDisplay(ctx context.Context, token string) error {
	_, err := s.db.GetDisplayByToken(ctx, store.GetDisplayByTokenParams{TokenHash: hashToken(token), Now: s.now()})
	if store.IsNotFound(err) {
		return ErrUnauthenticated
	}
	return err
}

// UnpairDisplayToken unpairs the display with token, from the display itself.
func (s *Service) UnpairDisplayToken(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.db.DeleteDisplayByToken(ctx, hashToken(token))
}

// Displays lists a room's paired displays.
func (s *Service) Displays(ctx context.Context, roomID string) ([]store.Display, error) {
	return s.db.ListDisplays(ctx, store.ListDisplaysParams{RoomID: roomID, Now: s.now()})
}

// Display returns one display. ErrNotFound if there's none.
func (s *Service) Display(ctx context.Context, id string) (store.Display, error) {
	d, err := s.db.GetDisplay(ctx, id)
	if store.IsNotFound(err) {
		return store.Display{}, ErrNotFound
	}
	return d, err
}

// UnpairDisplay removes a display. The caller checks u may.
func (s *Service) UnpairDisplay(ctx context.Context, id string) error {
	return s.db.DeleteDisplay(ctx, id)
}

// SweepDisplays deletes displays that haven't been seen in displayTTL.
func (s *Service) SweepDisplays(ctx context.Context) error {
	return s.db.DeleteExpiredDisplays(ctx, s.now())
}
