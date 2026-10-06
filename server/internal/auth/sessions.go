// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"encoding/base64"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// SessionID is a session's public ID: its token hash, base64url. The hash
// can't be turned back into the token, so the ID can't sign anyone in.
func SessionID(tokenHash []byte) string { return base64.RawURLEncoding.EncodeToString(tokenHash) }

// ID is the session's public ID.
func (s *Session) ID() string { return SessionID(s.hash) }

// Sessions lists u's signed-in sessions, most recently used first.
func (s *Service) Sessions(ctx context.Context, u store.User) ([]store.Session, error) {
	return s.db.ListSessions(ctx, store.ListSessionsParams{UserID: u.ID, Now: s.now()})
}

// RevokeSession signs out one of u's sessions.
func (s *Service) RevokeSession(ctx context.Context, u store.User, id string) error {
	hash, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return ErrNotFound
	}
	n, err := s.db.DeleteUserSession(ctx, store.DeleteUserSessionParams{TokenHash: hash, UserID: u.ID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeOtherSessions signs out every session of keep's user but keep.
func (s *Service) RevokeOtherSessions(ctx context.Context, keep *Session) error {
	return s.db.DeleteOtherSessions(ctx, store.DeleteOtherSessionsParams{UserID: keep.User.ID, TokenHash: keep.hash})
}
