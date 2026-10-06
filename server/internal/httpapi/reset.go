// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

func toResetLink(l auth.ResetLink) ResetLink {
	out := ResetLink{Id: l.ID, UserId: l.UserID, CreatedAt: l.CreatedAt, ExpiresAt: l.ExpiresAt}
	if l.URL != "" {
		out.Url = &l.URL
	}
	if l.CreatedBy.Valid {
		out.CreatedBy = &l.CreatedBy.String
	}
	return out
}

// --- Admins -------------------------------------------------------------------

// CreateResetLink makes a one-time reset link for a user (admins).
func (s *Server) CreateResetLink(ctx context.Context, req CreateResetLinkRequestObject) (CreateResetLinkResponseObject, error) {
	by, err := member(ctx)
	if err != nil {
		return nil, err
	}
	ttl := auth.DefaultResetTTL
	if req.Body != nil && req.Body.ExpiresInHours != nil {
		ttl = time.Duration(*req.Body.ExpiresInHours) * time.Hour
	}
	link, err := s.Auth.CreateResetLink(ctx, &by, req.Id, ttl)
	if err != nil {
		return nil, err
	}
	return CreateResetLink201JSONResponse(toResetLink(link)), nil
}

// RevokeResetLink cancels a user's reset link (admins).
func (s *Server) RevokeResetLink(ctx context.Context, req RevokeResetLinkRequestObject) (RevokeResetLinkResponseObject, error) {
	if err := s.Auth.RevokeResetLink(ctx, sessionFrom(ctx).User, req.Id); err != nil {
		return nil, err
	}
	return RevokeResetLink204Response{}, nil
}

// ListResetLinks lists reset links waiting to be used (admins).
func (s *Server) ListResetLinks(ctx context.Context, _ ListResetLinksRequestObject) (ListResetLinksResponseObject, error) {
	links, err := s.Auth.ResetLinks(ctx, sessionFrom(ctx).User)
	if err != nil {
		return nil, err
	}
	out := make(ListResetLinks200JSONResponse, len(links))
	for i, l := range links {
		out[i] = toResetLink(auth.ResetLink{ResetLink: l})
	}
	return out, nil
}

// --- The person locked out -----------------------------------------------------

// GetResetLink checks a reset link before it's used.
func (s *Server) GetResetLink(ctx context.Context, req GetResetLinkRequestObject) (GetResetLinkResponseObject, error) {
	u, link, err := s.Auth.CheckResetLink(ctx, requestFrom(ctx).ip, req.Code)
	if err != nil {
		return nil, err
	}
	return GetResetLink200JSONResponse(toResetLinkInfo(u, link)), nil
}

func toResetLinkInfo(u store.User, link store.ResetLink) ResetLinkInfo {
	out := ResetLinkInfo{Username: u.Username, DisplayName: u.DisplayName, Color: u.Color, ExpiresAt: link.ExpiresAt}
	if u.Avatar.Valid {
		out.Avatar = &u.Avatar.String
	}
	return out
}

// ResetPassword sets a new password with a reset link.
func (s *Server) ResetPassword(ctx context.Context, req ResetPasswordRequestObject) (ResetPasswordResponseObject, error) {
	r := requestFrom(ctx)
	sess, err := s.Auth.ResetPassword(ctx, auth.ResetPasswordParams{
		Code: req.Code, Password: req.Body.NewPassword, IP: r.ip, UserAgent: r.userAgent,
	})
	if err != nil {
		return nil, err
	}
	resp, err := s.signedIn(ctx, sess)
	return ResetPassword200JSONResponse{resp}, err
}

// BeginResetPasskey starts adding a passkey with a reset link.
func (s *Server) BeginResetPasskey(ctx context.Context, req BeginResetPasskeyRequestObject) (BeginResetPasskeyResponseObject, error) {
	c, err := s.Auth.BeginResetPasskey(ctx, requestFrom(ctx).ip, req.Code)
	if err != nil {
		return nil, err
	}
	return BeginResetPasskey200JSONResponse{CeremonyJSONResponse{CeremonyId: c.ID, Options: c.Options}}, nil
}

// FinishResetPasskey saves the passkey and signs in.
func (s *Server) FinishResetPasskey(ctx context.Context, req FinishResetPasskeyRequestObject) (FinishResetPasskeyResponseObject, error) {
	p, err := s.finishParams(ctx, *req.Body)
	if err != nil {
		return nil, err
	}
	sess, err := s.Auth.FinishResetPasskey(ctx, p)
	if err != nil {
		return nil, err
	}
	resp, err := s.signedIn(ctx, sess)
	return FinishResetPasskey200JSONResponse{resp}, err
}
