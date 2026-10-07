// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/artcache"
	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/avatar"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// --- Conversions --------------------------------------------------------------

// toUser converts a user; g is their guest row if they're a guest.
func toUser(u store.User, g *store.Guest) User {
	out := User{Id: u.ID, Username: u.Username, DisplayName: u.DisplayName, Color: u.Color, Role: Role(u.Role), CreatedAt: u.CreatedAt}
	if u.Avatar.Valid {
		out.Avatar = &u.Avatar.String
	}
	if g != nil {
		out.Guest = &UserGuest{RoomId: g.RoomID, ExpiresAt: g.ExpiresAt, Ended: g.EndedAt.Valid}
	}
	if u.RemovedAt.Valid {
		out.Removed = ptr(true)
	} else if u.DisabledAt.Valid {
		out.Disabled = ptr(true)
	}
	return out
}

// apiUser converts one user, looking up whether they're a guest.
func (s *Server) apiUser(ctx context.Context, u store.User) (User, error) {
	g, ok, err := s.Auth.GuestOf(ctx, u.ID)
	if err != nil || !ok {
		return toUser(u, nil), err
	}
	return toUser(u, &g), nil
}

// apiUsers converts users, marking guests.
func (s *Server) apiUsers(ctx context.Context, users []store.User) ([]User, error) {
	guests, err := s.Auth.AllGuests(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]User, len(users))
	for i, u := range users {
		var g *store.Guest
		if row, ok := guests[u.ID]; ok {
			g = &row
		}
		out[i] = toUser(u, g)
	}
	return out, nil
}

func (s *Server) toMe(ctx context.Context, u store.User) (Me, error) {
	hasPassword, passkeys, err := s.Auth.Credentials(ctx, u)
	if err != nil {
		return Me{}, err
	}
	pu, err := s.apiUser(ctx, u)
	if err != nil {
		return Me{}, err
	}
	return Me{
		Id: pu.Id, Username: pu.Username, DisplayName: pu.DisplayName, Avatar: pu.Avatar, Color: pu.Color, Role: pu.Role, CreatedAt: pu.CreatedAt,
		Guest: pu.Guest, HasPassword: hasPassword, PasskeyCount: passkeys,
	}, nil
}

// signedIn is the response for a new session: the user, and the cookie.
func (s *Server) signedIn(ctx context.Context, sess *auth.Session) (SignedInJSONResponse, error) {
	me, err := s.toMe(ctx, sess.User)
	if err != nil {
		return SignedInJSONResponse{}, err
	}
	return SignedInJSONResponse{Body: me, Headers: SignedInResponseHeaders{SetCookie: ptr(s.cookie(sess).String())}}, nil
}

func toPasskey(p store.CredentialsPasskey) Passkey {
	return Passkey{
		Id:         base64.RawURLEncoding.EncodeToString(p.ID),
		Name:       p.Name,
		CreatedAt:  p.CreatedAt,
		LastUsedAt: timePtr(p.LastUsedAt.Time, p.LastUsedAt.Valid),
	}
}

func (s *Server) toInvite(inv store.Invite) Invite {
	out := Invite{
		Code: inv.Code, Url: s.Auth.InviteURL(inv.Code), Role: Role(inv.Role),
		CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt, UsedAt: timePtr(inv.UsedAt.Time, inv.UsedAt.Valid),
	}
	if inv.CreatedBy.Valid {
		out.CreatedBy = &inv.CreatedBy.String
	}
	if inv.UsedBy.Valid {
		out.UsedBy = &inv.UsedBy.String
	}
	return out
}

func (s *Server) finishParams(ctx context.Context, body FinishCeremony) (auth.FinishParams, error) {
	cred, err := json.Marshal(body.Credential)
	if err != nil {
		return auth.FinishParams{}, err
	}
	req := requestFrom(ctx)
	p := auth.FinishParams{CeremonyID: body.CeremonyId, Credential: cred, IP: req.ip, UserAgent: req.userAgent}
	if body.Name != nil {
		p.Name = *body.Name
	}
	return p, nil
}

// --- Signup and sign-in -------------------------------------------------------

// GetInvite checks an invite before signup.
func (s *Server) GetInvite(ctx context.Context, req GetInviteRequestObject) (GetInviteResponseObject, error) {
	inv, err := s.Auth.CheckInvite(ctx, requestFrom(ctx).ip, req.Code)
	if err != nil {
		return nil, err
	}
	return GetInvite200JSONResponse{Role: Role(inv.Role), ExpiresAt: inv.ExpiresAt}, nil
}

// Signup creates an account with a password.
func (s *Server) Signup(ctx context.Context, req SignupRequestObject) (SignupResponseObject, error) {
	r := requestFrom(ctx)
	sess, err := s.Auth.Signup(ctx, auth.SignupParams{
		Invite: req.Body.Invite, Username: req.Body.Username, DisplayName: req.Body.DisplayName, Password: req.Body.Password,
		IP: r.ip, UserAgent: r.userAgent,
	})
	if err != nil {
		return nil, err
	}
	resp, err := s.signedIn(ctx, sess)
	return Signup201JSONResponse{resp}, err
}

// BeginPasskeySignup starts a passkey signup.
func (s *Server) BeginPasskeySignup(ctx context.Context, req BeginPasskeySignupRequestObject) (BeginPasskeySignupResponseObject, error) {
	c, err := s.Auth.BeginPasskeySignup(ctx, requestFrom(ctx).ip, req.Body.Invite, req.Body.Username, req.Body.DisplayName)
	if err != nil {
		return nil, err
	}
	return BeginPasskeySignup200JSONResponse{CeremonyJSONResponse{CeremonyId: c.ID, Options: c.Options}}, nil
}

// FinishPasskeySignup creates the account.
func (s *Server) FinishPasskeySignup(ctx context.Context, req FinishPasskeySignupRequestObject) (FinishPasskeySignupResponseObject, error) {
	p, err := s.finishParams(ctx, *req.Body)
	if err != nil {
		return nil, err
	}
	sess, err := s.Auth.FinishPasskeySignup(ctx, p)
	if err != nil {
		return nil, err
	}
	resp, err := s.signedIn(ctx, sess)
	return FinishPasskeySignup201JSONResponse{resp}, err
}

// Login signs in with a password.
func (s *Server) Login(ctx context.Context, req LoginRequestObject) (LoginResponseObject, error) {
	r := requestFrom(ctx)
	sess, err := s.Auth.Login(ctx, auth.LoginParams{Username: req.Body.Username, Password: req.Body.Password, IP: r.ip, UserAgent: r.userAgent})
	if err != nil {
		return nil, err
	}
	resp, err := s.signedIn(ctx, sess)
	return Login200JSONResponse{resp}, err
}

// BeginPasskeyLogin starts a passkey sign-in.
func (s *Server) BeginPasskeyLogin(ctx context.Context, _ BeginPasskeyLoginRequestObject) (BeginPasskeyLoginResponseObject, error) {
	c, err := s.Auth.BeginPasskeyLogin(requestFrom(ctx).ip)
	if err != nil {
		return nil, err
	}
	return BeginPasskeyLogin200JSONResponse{CeremonyJSONResponse{CeremonyId: c.ID, Options: c.Options}}, nil
}

// FinishPasskeyLogin completes a passkey sign-in.
func (s *Server) FinishPasskeyLogin(ctx context.Context, req FinishPasskeyLoginRequestObject) (FinishPasskeyLoginResponseObject, error) {
	p, err := s.finishParams(ctx, *req.Body)
	if err != nil {
		return nil, err
	}
	sess, err := s.Auth.FinishPasskeyLogin(ctx, p)
	if err != nil {
		return nil, err
	}
	resp, err := s.signedIn(ctx, sess)
	return FinishPasskeyLogin200JSONResponse{resp}, err
}

// Logout ends the current session.
func (s *Server) Logout(ctx context.Context, _ LogoutRequestObject) (LogoutResponseObject, error) {
	if err := s.Auth.Logout(ctx, requestFrom(ctx).token); err != nil {
		return nil, err
	}
	return Logout204Response{Headers: Logout204ResponseHeaders{SetCookie: ptr(s.clearCookie().String())}}, nil
}

// --- The signed-in user -------------------------------------------------------

// GetMe returns the signed-in user.
func (s *Server) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	me, err := s.toMe(ctx, sessionFrom(ctx).User)
	return GetMe200JSONResponse(me), err
}

// UpdateMe changes the profile.
func (s *Server) UpdateMe(ctx context.Context, req UpdateMeRequestObject) (UpdateMeResponseObject, error) {
	u, err := s.Auth.UpdateProfile(ctx, sessionFrom(ctx).User, auth.ProfileUpdate{
		DisplayName: req.Body.DisplayName, Avatar: req.Body.Avatar, Color: req.Body.Color,
	})
	if err != nil {
		return nil, err
	}
	me, err := s.toMe(ctx, u)
	return UpdateMe200JSONResponse(me), err
}

// UploadAvatar sets an uploaded profile picture.
func (s *Server) UploadAvatar(ctx context.Context, req UploadAvatarRequestObject) (UploadAvatarResponseObject, error) {
	data, err := io.ReadAll(io.LimitReader(req.Body, avatar.MaxUpload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > avatar.MaxUpload {
		return nil, &auth.InvalidInputError{Field: "avatar", Message: "must be 10 MB or smaller"}
	}
	u, err := s.Auth.UploadAvatar(ctx, sessionFrom(ctx).User, data)
	if err != nil {
		return nil, err
	}
	me, err := s.toMe(ctx, u)
	return UploadAvatar200JSONResponse(me), err
}

// GetUserAvatar serves an uploaded profile picture. Its URL changes with
// the picture, so browsers may keep it.
func (s *Server) GetUserAvatar(ctx context.Context, req GetUserAvatarRequestObject) (GetUserAvatarResponseObject, error) {
	a, err := s.Auth.Avatar(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return imageResponse(artcache.Image{Data: a.Data, ContentType: a.ContentType}), nil
}

// SetPassword sets or changes the password.
func (s *Server) SetPassword(ctx context.Context, req SetPasswordRequestObject) (SetPasswordResponseObject, error) {
	signOut := req.Body.SignOutOtherSessions == nil || *req.Body.SignOutOtherSessions
	if err := s.Auth.SetPassword(ctx, sessionFrom(ctx), requestFrom(ctx).ip, req.Body.CurrentPassword, req.Body.NewPassword, signOut); err != nil {
		return nil, err
	}
	return SetPassword204Response{}, nil
}

// DeletePassword removes the password.
func (s *Server) DeletePassword(ctx context.Context, _ DeletePasswordRequestObject) (DeletePasswordResponseObject, error) {
	if err := s.Auth.DeletePassword(ctx, sessionFrom(ctx).User); err != nil {
		return nil, err
	}
	return DeletePassword204Response{}, nil
}

// ListPasskeys lists the user's passkeys.
func (s *Server) ListPasskeys(ctx context.Context, _ ListPasskeysRequestObject) (ListPasskeysResponseObject, error) {
	pks, err := s.Auth.Passkeys(ctx, sessionFrom(ctx).User)
	if err != nil {
		return nil, err
	}
	out := make(ListPasskeys200JSONResponse, len(pks))
	for i, p := range pks {
		out[i] = toPasskey(p)
	}
	return out, nil
}

// BeginAddPasskey starts registering a passkey.
func (s *Server) BeginAddPasskey(ctx context.Context, _ BeginAddPasskeyRequestObject) (BeginAddPasskeyResponseObject, error) {
	c, err := s.Auth.BeginAddPasskey(ctx, sessionFrom(ctx).User)
	if err != nil {
		return nil, err
	}
	return BeginAddPasskey200JSONResponse{CeremonyJSONResponse{CeremonyId: c.ID, Options: c.Options}}, nil
}

// FinishAddPasskey saves a new passkey.
func (s *Server) FinishAddPasskey(ctx context.Context, req FinishAddPasskeyRequestObject) (FinishAddPasskeyResponseObject, error) {
	p, err := s.finishParams(ctx, *req.Body)
	if err != nil {
		return nil, err
	}
	pk, err := s.Auth.FinishAddPasskey(ctx, sessionFrom(ctx).User, p)
	if err != nil {
		return nil, err
	}
	return FinishAddPasskey201JSONResponse(toPasskey(pk)), nil
}

// RenamePasskey renames a passkey.
func (s *Server) RenamePasskey(ctx context.Context, req RenamePasskeyRequestObject) (RenamePasskeyResponseObject, error) {
	id, err := auth.PasskeyID(req.Id)
	if err != nil {
		return nil, err
	}
	if err := s.Auth.RenamePasskey(ctx, sessionFrom(ctx).User, id, req.Body.Name); err != nil {
		return nil, err
	}
	return RenamePasskey204Response{}, nil
}

// DeletePasskey removes a passkey.
func (s *Server) DeletePasskey(ctx context.Context, req DeletePasskeyRequestObject) (DeletePasskeyResponseObject, error) {
	id, err := auth.PasskeyID(req.Id)
	if err != nil {
		return nil, err
	}
	if err := s.Auth.DeletePasskey(ctx, sessionFrom(ctx).User, id); err != nil {
		return nil, err
	}
	return DeletePasskey204Response{}, nil
}

// --- Signed-in devices -------------------------------------------------------

// ListMySessions lists the devices the user is signed in on.
func (s *Server) ListMySessions(ctx context.Context, _ ListMySessionsRequestObject) (ListMySessionsResponseObject, error) {
	sess := sessionFrom(ctx)
	rows, err := s.Auth.Sessions(ctx, sess.User)
	if err != nil {
		return nil, err
	}
	current := sess.ID()
	out := make(ListMySessions200JSONResponse, len(rows))
	for i, r := range rows {
		id := auth.SessionID(r.TokenHash)
		out[i] = SignedInSession{
			Id: id, UserAgent: r.UserAgent, CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, Current: id == current,
		}
	}
	return out, nil
}

// RevokeMySession signs out one device.
func (s *Server) RevokeMySession(ctx context.Context, req RevokeMySessionRequestObject) (RevokeMySessionResponseObject, error) {
	if err := s.Auth.RevokeSession(ctx, sessionFrom(ctx).User, req.Id); err != nil {
		return nil, err
	}
	return RevokeMySession204Response{}, nil
}

// RevokeOtherSessions signs out every device but this one.
func (s *Server) RevokeOtherSessions(ctx context.Context, _ RevokeOtherSessionsRequestObject) (RevokeOtherSessionsResponseObject, error) {
	if err := s.Auth.RevokeOtherSessions(ctx, sessionFrom(ctx)); err != nil {
		return nil, err
	}
	return RevokeOtherSessions204Response{}, nil
}

// --- People and invites -------------------------------------------------------

// ListUsers lists everyone.
func (s *Server) ListUsers(ctx context.Context, _ ListUsersRequestObject) (ListUsersResponseObject, error) {
	users, err := s.Auth.Users(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.apiUsers(ctx, users)
	// Guests see names, not the usernames members sign in with.
	if sess, _ := ctx.Value(ctxSession).(*auth.Session); sess != nil && sess.Guest != nil {
		for i := range out {
			if out[i].Id != sess.User.ID {
				out[i].Username = ""
			}
		}
	}
	return ListUsers200JSONResponse(out), err
}

// ListInvites lists invites (admins).
func (s *Server) ListInvites(ctx context.Context, _ ListInvitesRequestObject) (ListInvitesResponseObject, error) {
	invs, err := s.Auth.Invites(ctx, sessionFrom(ctx).User)
	if err != nil {
		return nil, err
	}
	out := make(ListInvites200JSONResponse, len(invs))
	for i, inv := range invs {
		out[i] = s.toInvite(inv)
	}
	return out, nil
}

// CreateInvite makes an invite (admins).
func (s *Server) CreateInvite(ctx context.Context, req CreateInviteRequestObject) (CreateInviteResponseObject, error) {
	st, err := s.Admin.Settings(ctx)
	if err != nil {
		return nil, err
	}
	role, ttl := store.RoleMember, st.InviteExpiry()
	if req.Body.Role != nil {
		role = string(*req.Body.Role)
	}
	if req.Body.ExpiresInHours != nil {
		ttl = time.Duration(*req.Body.ExpiresInHours) * time.Hour
	}
	inv, err := s.Auth.CreateInvite(ctx, sessionFrom(ctx).User, role, ttl)
	if err != nil {
		return nil, err
	}
	return CreateInvite201JSONResponse(s.toInvite(inv)), nil
}

// DeleteInvite revokes an invite (admins).
func (s *Server) DeleteInvite(ctx context.Context, req DeleteInviteRequestObject) (DeleteInviteResponseObject, error) {
	if err := s.Auth.DeleteInvite(ctx, sessionFrom(ctx).User, req.Code); err != nil {
		return nil, err
	}
	return DeleteInvite204Response{}, nil
}
