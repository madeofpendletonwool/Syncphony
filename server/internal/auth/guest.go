// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Guests (MAD-720) join one room by scanning its guest pass: a QR code on
// a member's phone or the big screen. A guest is a user with no password,
// passkey, services or settings, tied to that room until the pass expires
// or a host removes them. The pass is a link carrying the pass's ID and a
// server signature, so it can be shown again (on another phone, after the
// TV restarts) without the server keeping the link itself.

const (
	// MinGuestPass is the shortest a pass may last.
	MinGuestPass = 15 * time.Minute
	// MaxGuestPass is the longest a pass may last.
	MaxGuestPass = 24 * time.Hour
	// maxGuestsPerPass caps the guests one pass lets in, so a leaked code
	// can't fill the server with accounts.
	maxGuestsPerPass = 100
	// maxGuestName is shorter than a member's display name: it's typed on
	// a phone at a party, and shown on a big screen.
	maxGuestName = 32
	guestKeyName = "guest-pass"
)

// Guest errors.
var (
	// ErrGuestPassInvalid: a wrong, expired or revoked pass.
	ErrGuestPassInvalid = errors.New("that guest pass doesn't work any more; ask for a new one")
	// ErrPassFull: the pass has let in as many guests as it may.
	ErrPassFull = errors.New("this guest pass is full; ask for a new one")
)

// Guest is a guest's tie to their room.
type Guest = store.Guest

// guestKey returns the key guest passes are signed with, making it on
// first use.
func (s *Service) guestKey(ctx context.Context) ([]byte, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if s.passKey != nil {
		return s.passKey, nil
	}
	key, err := s.db.GetServerKey(ctx, guestKeyName)
	if store.IsNotFound(err) {
		if err := s.db.CreateServerKey(ctx, store.CreateServerKeyParams{Name: guestKeyName, Key: []byte(rand.Text() + rand.Text()), CreatedAt: s.now()}); err != nil {
			return nil, err
		}
		key, err = s.db.GetServerKey(ctx, guestKeyName)
	}
	if err != nil {
		return nil, err
	}
	s.passKey = key
	return key, nil
}

// passSignature signs a pass's ID and expiry.
func passSignature(key []byte, p store.GuestPass) string {
	m := hmac.New(sha256.New, key)
	fmt.Fprintf(m, "syncphony guest pass\x00%s\x00%d", p.ID, p.ExpiresAt.Unix())
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// GuestPassToken is the signed token in a pass's link.
func (s *Service) GuestPassToken(ctx context.Context, p store.GuestPass) (string, error) {
	key, err := s.guestKey(ctx)
	if err != nil {
		return "", err
	}
	return p.ID + "." + passSignature(key, p), nil
}

// GuestPassURL is the link a pass's QR code shows.
func (s *Service) GuestPassURL(ctx context.Context, p store.GuestPass) (string, error) {
	token, err := s.GuestPassToken(ctx, p)
	if err != nil {
		return "", err
	}
	return s.baseURL + "/join/" + url.PathEscape(token), nil
}

// CreateGuestPass starts a new guest pass for a room, replacing any it
// had, on by's say-so. The caller checks the room exists and allows guests.
func (s *Service) CreateGuestPass(ctx context.Context, by store.User, roomID string, expires time.Time) (store.GuestPass, error) {
	now := s.now()
	if d := expires.Sub(now); d < MinGuestPass || d > MaxGuestPass {
		return store.GuestPass{}, invalid("expiresAt", "must be 15 minutes to 24 hours from now")
	}
	var p store.GuestPass
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		if err := q.RevokeGuestPasses(ctx, store.RevokeGuestPassesParams{Now: sql.NullTime{Time: now, Valid: true}, RoomID: roomID}); err != nil {
			return err
		}
		var err error
		p, err = q.CreateGuestPass(ctx, store.CreateGuestPassParams{
			ID: store.NewID(), RoomID: roomID, CreatedBy: sql.NullString{String: by.ID, Valid: true},
			CreatedAt: now, ExpiresAt: expires.UTC().Truncate(time.Second),
		})
		return err
	})
	return p, err
}

// CurrentGuestPass returns a room's pass that's still good, or ErrNotFound.
func (s *Service) CurrentGuestPass(ctx context.Context, roomID string) (store.GuestPass, error) {
	p, err := s.db.CurrentGuestPass(ctx, store.CurrentGuestPassParams{RoomID: roomID, Now: s.now()})
	if store.IsNotFound(err) {
		return p, ErrNotFound
	}
	return p, err
}

// RevokeGuestPasses stops a room's passes letting anyone else in.
func (s *Service) RevokeGuestPasses(ctx context.Context, roomID string) error {
	return s.db.RevokeGuestPasses(ctx, store.RevokeGuestPassesParams{Now: sql.NullTime{Time: s.now(), Valid: true}, RoomID: roomID})
}

// CheckGuestPass returns the pass a token is for, if it's still good.
// Misses count against ip's rate limit, so passes can't be guessed.
func (s *Service) CheckGuestPass(ctx context.Context, ip, token string) (store.GuestPass, error) {
	if d, ok := s.byIP.blocked(ip); ok {
		return store.GuestPass{}, &RateLimitError{RetryAfter: d}
	}
	fail := func() (store.GuestPass, error) {
		s.byIP.fail(ip)
		return store.GuestPass{}, ErrGuestPassInvalid
	}
	id, sig, ok := strings.Cut(token, ".")
	if !ok || id == "" || sig == "" {
		return fail()
	}
	p, err := s.db.GetGuestPass(ctx, id)
	if store.IsNotFound(err) {
		return fail()
	} else if err != nil {
		return store.GuestPass{}, err
	}
	key, err := s.guestKey(ctx)
	if err != nil {
		return store.GuestPass{}, err
	}
	if !hmac.Equal([]byte(sig), []byte(passSignature(key, p))) || p.RevokedAt.Valid || !s.now().Before(p.ExpiresAt) {
		return fail()
	}
	return p, nil
}

// JoinAsGuest makes a guest for a pass the caller checked (CheckGuestPass,
// and that the room still allows guests), and signs them in until the pass
// expires.
func (s *Service) JoinAsGuest(ctx context.Context, p store.GuestPass, displayName, userAgent string) (*Session, error) {
	name := strings.TrimSpace(displayName)
	if l := utf8.RuneCountInString(name); l < 1 || l > maxGuestName {
		return nil, invalid("displayName", "must be 1 to %d characters", maxGuestName)
	}
	var sess *Session
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		n, err := q.CountPassGuests(ctx, sql.NullString{String: p.ID, Valid: true})
		if err != nil {
			return err
		}
		if n >= maxGuestsPerPass {
			return ErrPassFull
		}
		users, err := q.ListUsers(ctx)
		if err != nil {
			return err
		}
		now := s.now()
		u, err := q.CreateUser(ctx, store.CreateUserParams{
			// Guests can't sign in by name; this only has to be unique.
			ID: store.NewID(), Username: "guest-" + strings.ToLower(rand.Text()[:12]), DisplayName: name,
			Color: pickColor(users), Role: store.RoleMember, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		g, err := q.CreateGuest(ctx, store.CreateGuestParams{
			UserID: u.ID, RoomID: p.RoomID, PassID: sql.NullString{String: p.ID, Valid: true}, CreatedAt: now, ExpiresAt: p.ExpiresAt,
		})
		if err != nil {
			return err
		}
		token, hash := newToken()
		if _, err := q.CreateSession(ctx, store.CreateSessionParams{
			TokenHash: hash, UserID: u.ID, UserAgent: truncate(userAgent, 256), CreatedAt: now, LastSeenAt: now, ExpiresAt: g.ExpiresAt,
		}); err != nil {
			return err
		}
		sess = &Session{User: u, Guest: &g, Token: token, Expires: g.ExpiresAt, hash: hash}
		return nil
	})
	return sess, err
}

// GuestOf returns userID's guest row, if they're a guest (live or not).
func (s *Service) GuestOf(ctx context.Context, userID string) (store.Guest, bool, error) {
	g, err := s.db.GetGuest(ctx, userID)
	if store.IsNotFound(err) {
		return g, false, nil
	}
	return g, err == nil, err
}

// AllGuests returns every guest, live or not, by user ID.
func (s *Service) AllGuests(ctx context.Context) (map[string]store.Guest, error) {
	gs, err := s.db.ListAllGuests(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]store.Guest, len(gs))
	for _, g := range gs {
		out[g.UserID] = g
	}
	return out, nil
}

// RoomGuests returns a room's live guests, newest first.
func (s *Service) RoomGuests(ctx context.Context, roomID string) ([]store.ListRoomGuestsRow, error) {
	return s.db.ListRoomGuests(ctx, store.ListRoomGuestsParams{RoomID: roomID, Now: s.now()})
}

// EndGuest signs a guest out for good. Their user row stays, so the room's
// history still shows their name; the caller takes their songs out of the
// queue. ErrNotFound if userID isn't a guest of roomID.
func (s *Service) EndGuest(ctx context.Context, roomID, userID string) error {
	return s.db.Tx(ctx, func(q *store.Queries) error {
		g, err := q.GetGuest(ctx, userID)
		if store.IsNotFound(err) || (err == nil && g.RoomID != roomID) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if err := q.EndGuest(ctx, store.EndGuestParams{Now: sql.NullTime{Time: s.now(), Valid: true}, UserID: userID}); err != nil {
			return err
		}
		return q.DeleteUserSessions(ctx, userID)
	})
}

// ExpiredGuests returns guests whose time is up but who haven't been
// ended yet. Call EndGuest on each.
func (s *Service) ExpiredGuests(ctx context.Context) ([]store.Guest, error) {
	return s.db.ListExpiredGuests(ctx, s.now())
}

// SweepGuestPasses deletes passes that expired a day ago and that no live
// guest joined with.
func (s *Service) SweepGuestPasses(ctx context.Context) error {
	return s.db.DeleteOldGuestPasses(ctx, s.now().Add(-24*time.Hour))
}
