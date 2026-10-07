// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"database/sql"
	"net/url"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Reset link lifetimes. There's no email: the admin sends the link however
// they like, so it should outlive a slow reply but not linger.
const (
	DefaultResetTTL = 24 * time.Hour
	maxResetTTL     = 7 * 24 * time.Hour
)

// ResetLink is a one-time link that lets a user back in.
type ResetLink struct {
	store.ResetLink
	// URL is set only when the link is created: only the code's hash is
	// stored, so it can't be shown again.
	URL string
}

// ResetURL is the link for a reset code.
func (s *Service) ResetURL(code string) string {
	return s.baseURL + "/reset/" + url.PathEscape(code)
}

// CreateResetLink makes a reset link for userID, replacing any they had.
// by must be an admin; nil means the server's operator (the CLI).
func (s *Service) CreateResetLink(ctx context.Context, by *store.User, userID string, ttl time.Duration) (ResetLink, error) {
	if by != nil && by.Role != store.RoleAdmin {
		return ResetLink{}, ErrForbidden
	}
	if ttl < time.Hour || ttl > maxResetTTL {
		return ResetLink{}, invalid("expiresInHours", "must be between 1 and 168")
	}
	u, err := s.db.GetUser(ctx, userID)
	if store.IsNotFound(err) || (err == nil && u.RemovedAt.Valid) {
		return ResetLink{}, ErrNotFound
	}
	if err != nil {
		return ResetLink{}, err
	}
	if _, guest, err := s.GuestOf(ctx, u.ID); err != nil {
		return ResetLink{}, err
	} else if guest {
		return ResetLink{}, invalid("user", "guests join with a room's QR code and have nothing to reset")
	}
	code := newCode()
	now := s.now()
	params := store.CreateResetLinkParams{
		ID: store.NewID(), CodeHash: hashToken(code), UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	if by != nil {
		params.CreatedBy = sql.NullString{String: by.ID, Valid: true}
	}
	var link store.ResetLink
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		if err := q.DeleteExpiredResetLinks(ctx, now); err != nil {
			return err
		}
		if _, err := q.DeleteUserResetLinks(ctx, u.ID); err != nil {
			return err
		}
		link, err = q.CreateResetLink(ctx, params)
		return err
	})
	return ResetLink{ResetLink: link, URL: s.ResetURL(code)}, err
}

// CreateResetLinkFor makes a reset link for the user with username, for
// the operator's CLI.
func (s *Service) CreateResetLinkFor(ctx context.Context, username string, ttl time.Duration) (store.User, ResetLink, error) {
	u, err := s.db.GetUserByUsername(ctx, normalizeUsername(username))
	if store.IsNotFound(err) {
		return u, ResetLink{}, ErrNotFound
	}
	if err != nil {
		return u, ResetLink{}, err
	}
	link, err := s.CreateResetLink(ctx, nil, u.ID, ttl)
	return u, link, err
}

// ResetLinks lists unused, unexpired reset links (admins only).
func (s *Service) ResetLinks(ctx context.Context, by store.User) ([]store.ResetLink, error) {
	if by.Role != store.RoleAdmin {
		return nil, ErrForbidden
	}
	return s.db.ListResetLinks(ctx, s.now())
}

// RevokeResetLink cancels userID's reset link (admins only).
func (s *Service) RevokeResetLink(ctx context.Context, by store.User, userID string) error {
	if by.Role != store.RoleAdmin {
		return ErrForbidden
	}
	n, err := s.db.DeleteUserResetLinks(ctx, userID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CheckResetLink returns the user a usable reset code is for, or
// ErrResetLinkInvalid. Misses count against ip's rate limit.
func (s *Service) CheckResetLink(ctx context.Context, ip, code string) (store.User, store.ResetLink, error) {
	if d, ok := s.byIP.blocked(ip); ok {
		return store.User{}, store.ResetLink{}, &RateLimitError{RetryAfter: d}
	}
	link, err := s.db.GetResetLink(ctx, store.GetResetLinkParams{CodeHash: hashToken(code), Now: s.now()})
	if store.IsNotFound(err) {
		s.byIP.fail(ip)
		return store.User{}, link, ErrResetLinkInvalid
	}
	if err != nil {
		return store.User{}, link, err
	}
	u, err := s.db.GetUser(ctx, link.UserID)
	return u, link, err
}

// recover uses up link and signs its user in afresh: every other session
// ends. set saves the new credential in the same transaction.
func (s *Service) recover(ctx context.Context, link store.ResetLink, u store.User, userAgent string, set func(q *store.Queries) error) (*Session, error) {
	var sess *Session
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		n, err := q.UseResetLink(ctx, store.UseResetLinkParams{ID: link.ID, Now: s.now()})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrResetLinkInvalid
		}
		if err := set(q); err != nil {
			return err
		}
		if err := q.DeleteUserSessions(ctx, u.ID); err != nil {
			return err
		}
		sess, err = s.newSession(ctx, q, u, userAgent)
		return err
	})
	if err != nil {
		return nil, err
	}
	// They're back in: forget their failed sign-ins.
	s.byUser.reset(u.Username)
	return sess, nil
}

// ResetPasswordParams uses a reset link to set a password.
type ResetPasswordParams struct {
	Code, Password string
	IP, UserAgent  string
}

// ResetPassword sets a new password with a reset link and signs in.
func (s *Service) ResetPassword(ctx context.Context, p ResetPasswordParams) (*Session, error) {
	if err := validatePassword("newPassword", p.Password); err != nil {
		return nil, err
	}
	u, link, err := s.CheckResetLink(ctx, p.IP, p.Code)
	if err != nil {
		return nil, err
	}
	hash := s.cost.hashPassword(p.Password)
	return s.recover(ctx, link, u, p.UserAgent, func(q *store.Queries) error {
		return q.SetPassword(ctx, store.SetPasswordParams{UserID: u.ID, Hash: hash, UpdatedAt: s.now()})
	})
}

// BeginResetPasskey starts enrolling a passkey with a reset link.
func (s *Service) BeginResetPasskey(ctx context.Context, ip, code string) (*Ceremony, error) {
	u, link, err := s.CheckResetLink(ctx, ip, code)
	if err != nil {
		return nil, err
	}
	wu, err := s.loadWAUser(ctx, s.db.Queries, u)
	if err != nil {
		return nil, err
	}
	creation, session, err := s.webauthn.BeginRegistration(wu, registrationOptions(wu.creds)...)
	if err != nil {
		return nil, err
	}
	return s.startCeremony(&ceremony{kind: ceremonyReset, session: *session, userID: u.ID, resetID: link.ID}, creation)
}

// FinishResetPasskey saves the passkey, uses up the link, and signs in.
func (s *Service) FinishResetPasskey(ctx context.Context, p FinishParams) (*Session, error) {
	cer, err := s.cers.take(p.CeremonyID, ceremonyReset)
	if err != nil {
		return nil, err
	}
	u, err := s.db.GetUser(ctx, cer.userID)
	if err != nil {
		return nil, err
	}
	wu, err := s.loadWAUser(ctx, s.db.Queries, u)
	if err != nil {
		return nil, err
	}
	cred, err := s.verifyRegistration(wu, cer.session, p.Credential)
	if err != nil {
		return nil, err
	}
	return s.recover(ctx, store.ResetLink{ID: cer.resetID}, u, p.UserAgent, func(q *store.Queries) error {
		_, err := s.savePasskey(ctx, q, u.ID, cred, p.Name, p.UserAgent)
		return err
	})
}
