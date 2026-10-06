// SPDX-License-Identifier: AGPL-3.0-only

// Package auth implements accounts: invite-only signup, passkey and password
// sign-in, sessions, invites, and profiles.
//
// The first run has no users. Bootstrap then creates a one-time admin
// invite, logged as a setup link; whoever signs up with it becomes the
// admin. After that, admins create invites for everyone else.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Session lifetime. Sessions slide: each use (at most once per
// sessionTouchEvery) pushes the expiry out to sessionTTL from then.
const (
	sessionTTL        = 30 * 24 * time.Hour
	sessionTouchEvery = time.Hour
	// bootstrapTTL is how long the first-run admin invite lasts. A restart
	// while there are still no users makes a new one.
	bootstrapTTL = 24 * time.Hour
	// DefaultInviteTTL is used when an admin doesn't pick an expiry.
	DefaultInviteTTL = 7 * 24 * time.Hour
)

// Config configures a Service.
type Config struct {
	// BaseURL is the public URL of the web app, e.g.
	// "https://syncphony.example.com". It sets the passkey relying party
	// (its hostname) and the links in invites.
	BaseURL string
	// Now is the clock. Defaults to store.Now.
	Now func() time.Time
	// PasswordCost defaults to DefaultPasswordCost. Tests lower it.
	PasswordCost *PasswordCost
}

// Service implements accounts on top of the store.
type Service struct {
	db       *store.Store
	baseURL  string
	now      func() time.Time
	webauthn *webauthnRP
	cers     *ceremonies
	cost     PasswordCost
	// dummyHash is checked when a username doesn't exist, so a failed
	// sign-in takes the same time either way.
	dummyHash string

	// Failed attempts, by client IP and by username.
	byIP   *limiter
	byUser *limiter

	// Displays waiting to pair, and wrong pairing codes by user.
	pairings  *pairings
	pairFails *limiter
}

// New returns a Service.
func New(db *store.Store, cfg Config) (*Service, error) {
	if cfg.Now == nil {
		cfg.Now = store.Now
	}
	rp, err := newWebAuthn(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	cost := DefaultPasswordCost
	if cfg.PasswordCost != nil {
		cost = *cfg.PasswordCost
	}
	return &Service{
		cost:      cost,
		dummyHash: cost.hashPassword(rand.Text()),
		db:        db,
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		now:       cfg.Now,
		webauthn:  rp,
		cers:      &ceremonies{now: cfg.Now, byID: map[string]*ceremony{}},
		byIP:      newLimiter(30, 15*time.Minute, cfg.Now),
		byUser:    newLimiter(10, 15*time.Minute, cfg.Now),
		pairings:  &pairings{bySecret: map[string]*pairing{}, byCode: map[string]string{}},
		pairFails: newLimiter(10, 15*time.Minute, cfg.Now),
	}, nil
}

// Bootstrap prepares first-run setup. If there are no users yet, it
// replaces any unused setup invite with a fresh admin invite and returns its
// link. Otherwise it returns "".
func (s *Service) Bootstrap(ctx context.Context) (string, error) {
	var link string
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		n, err := q.CountUsers(ctx)
		if err != nil || n > 0 {
			return err
		}
		if err := q.DeleteUnusedBootstrapInvites(ctx); err != nil {
			return err
		}
		now := s.now()
		inv, err := q.CreateInvite(ctx, store.CreateInviteParams{
			Code: newCode(), Role: store.RoleAdmin, CreatedAt: now, ExpiresAt: now.Add(bootstrapTTL),
		})
		if err != nil {
			return err
		}
		link = s.InviteURL(inv.Code)
		return nil
	})
	return link, err
}

// InviteURL is the signup link for an invite code.
func (s *Service) InviteURL(code string) string {
	return s.baseURL + "/invite/" + url.PathEscape(code)
}

// --- Sessions ----------------------------------------------------------------

// Session is a signed-in user's session.
type Session struct {
	User store.User
	// Token is set only when the session is new or was extended; the caller
	// should then (re)send the cookie.
	Token string
	// Expires is when the session lapses unless used again.
	Expires time.Time
	hash    []byte
}

// newSession signs u in. The caller must reset rate limits as needed.
func (s *Service) newSession(ctx context.Context, q *store.Queries, u store.User, userAgent string) (*Session, error) {
	token, hash := newToken()
	now := s.now()
	sess, err := q.CreateSession(ctx, store.CreateSessionParams{
		TokenHash: hash, UserID: u.ID, UserAgent: truncate(userAgent, 256), CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(sessionTTL),
	})
	if err != nil {
		return nil, err
	}
	return &Session{User: u, Token: token, Expires: sess.ExpiresAt, hash: hash}, nil
}

// Authenticate returns the session for token, extending it if it hasn't
// been used for a while (then Session.Token is set).
func (s *Service) Authenticate(ctx context.Context, token string) (*Session, error) {
	if token == "" {
		return nil, ErrUnauthenticated
	}
	hash := hashToken(token)
	now := s.now()
	sess, err := s.db.GetSession(ctx, store.GetSessionParams{TokenHash: hash, Now: now})
	if store.IsNotFound(err) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	u, err := s.db.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	out := &Session{User: u, Expires: sess.ExpiresAt, hash: hash}
	if now.Sub(sess.LastSeenAt) >= sessionTouchEvery {
		out.Expires = now.Add(sessionTTL)
		if err := s.db.TouchSession(ctx, store.TouchSessionParams{LastSeenAt: now, ExpiresAt: out.Expires, TokenHash: hash}); err != nil {
			return nil, err
		}
		out.Token = token
	}
	return out, nil
}

// Check reports whether token is still a live session, without extending
// it. Long-lived connections use it to notice sign-outs.
func (s *Service) Check(ctx context.Context, token string) error {
	_, err := s.db.GetSession(ctx, store.GetSessionParams{TokenHash: hashToken(token), Now: s.now()})
	if store.IsNotFound(err) {
		return ErrUnauthenticated
	}
	return err
}

// Logout ends the session for token. Unknown tokens are ignored.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.db.DeleteSession(ctx, hashToken(token))
}

// newToken returns a session token and the hash stored for it.
func newToken() (string, []byte) {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // never fails
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token)
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// newCode returns an invite code: 128 random bits, URL-safe.
func newCode() string { return strings.ToLower(rand.Text()) }

// --- Rate limiting -----------------------------------------------------------

// checkLimits fails if ip or username has too many recent failures.
func (s *Service) checkLimits(ip, username string) error {
	if d, ok := s.byIP.blocked(ip); ok {
		return &RateLimitError{RetryAfter: d}
	}
	if username != "" {
		if d, ok := s.byUser.blocked(username); ok {
			return &RateLimitError{RetryAfter: d}
		}
	}
	return nil
}

func (s *Service) recordFailure(ip, username string) {
	s.byIP.fail(ip)
	if username != "" {
		s.byUser.fail(username)
	}
}

// --- Signup ------------------------------------------------------------------

type signupInput struct {
	invite      string
	username    string
	displayName string
}

// SignupParams creates an account with a password.
type SignupParams struct {
	Invite, Username, DisplayName, Password string
	IP, UserAgent                           string
}

// Signup creates an account from an invite and signs it in.
func (s *Service) Signup(ctx context.Context, p SignupParams) (*Session, error) {
	in, err := s.checkSignup(ctx, p.IP, p.Invite, p.Username, p.DisplayName)
	if err != nil {
		return nil, err
	}
	if err := validatePassword("password", p.Password); err != nil {
		return nil, err
	}
	hash := s.cost.hashPassword(p.Password)
	var sess *Session
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		u, err := s.createUser(ctx, q, store.NewID(), in)
		if err != nil {
			return err
		}
		if err := q.SetPassword(ctx, store.SetPasswordParams{UserID: u.ID, Hash: hash, UpdatedAt: s.now()}); err != nil {
			return err
		}
		sess, err = s.newSession(ctx, q, u, p.UserAgent)
		return err
	})
	if err != nil {
		s.failSignup(err, p.IP)
		return nil, err
	}
	return sess, nil
}

// checkSignup validates signup input and the invite before any work is done.
func (s *Service) checkSignup(ctx context.Context, ip, invite, username, displayName string) (signupInput, error) {
	if err := s.checkLimits(ip, ""); err != nil {
		return signupInput{}, err
	}
	in := signupInput{invite: strings.TrimSpace(invite), username: normalizeUsername(username), displayName: strings.TrimSpace(displayName)}
	if err := validateUsername(in.username); err != nil {
		return in, err
	}
	if err := validateDisplayName(in.displayName); err != nil {
		return in, err
	}
	if _, err := s.CheckInvite(ctx, ip, in.invite); err != nil {
		return in, err
	}
	if _, err := s.db.GetUserByUsername(ctx, in.username); err == nil {
		return in, ErrUsernameTaken
	} else if !store.IsNotFound(err) {
		return in, err
	}
	return in, nil
}

func (s *Service) failSignup(err error, ip string) {
	if errors.Is(err, ErrInviteInvalid) {
		s.byIP.fail(ip)
	}
}

// createUser creates the user with the invite's role and uses up the invite.
func (s *Service) createUser(ctx context.Context, q *store.Queries, id string, in signupInput) (store.User, error) {
	now := s.now()
	inv, err := q.GetInvite(ctx, in.invite)
	if store.IsNotFound(err) || (err == nil && (inv.UsedBy.Valid || !now.Before(inv.ExpiresAt))) {
		return store.User{}, ErrInviteInvalid
	}
	if err != nil {
		return store.User{}, err
	}
	users, err := q.ListUsers(ctx)
	if err != nil {
		return store.User{}, err
	}
	u, err := q.CreateUser(ctx, store.CreateUserParams{
		ID: id, Username: in.username, DisplayName: in.displayName, Color: pickColor(users), Role: inv.Role, CreatedAt: now,
	})
	if err != nil {
		if _, lookupErr := q.GetUserByUsername(ctx, in.username); lookupErr == nil {
			return store.User{}, ErrUsernameTaken
		}
		return store.User{}, err
	}
	if _, err := q.RedeemInvite(ctx, in.invite, u.ID, now); store.IsNotFound(err) {
		return store.User{}, ErrInviteInvalid
	} else if err != nil {
		return store.User{}, err
	}
	return u, nil
}

// CheckInvite returns a usable invite, or ErrInviteInvalid. Misses count
// against ip's rate limit, so codes can't be guessed.
func (s *Service) CheckInvite(ctx context.Context, ip, code string) (store.Invite, error) {
	if d, ok := s.byIP.blocked(ip); ok {
		return store.Invite{}, &RateLimitError{RetryAfter: d}
	}
	inv, err := s.db.GetInvite(ctx, code)
	if store.IsNotFound(err) || (err == nil && (inv.UsedBy.Valid || !s.now().Before(inv.ExpiresAt))) {
		s.byIP.fail(ip)
		return store.Invite{}, ErrInviteInvalid
	}
	return inv, err
}

// --- Password sign-in --------------------------------------------------------

// LoginParams signs in with a password.
type LoginParams struct {
	Username, Password string
	IP, UserAgent      string
}

// Login checks a username and password and starts a session.
func (s *Service) Login(ctx context.Context, p LoginParams) (*Session, error) {
	username := normalizeUsername(p.Username)
	if err := s.checkLimits(p.IP, username); err != nil {
		return nil, err
	}
	u, err := s.db.GetUserByUsername(ctx, username)
	if err != nil && !store.IsNotFound(err) {
		return nil, err
	}
	hash := s.dummyHash
	if err == nil {
		switch pw, err := s.db.GetPassword(ctx, u.ID); {
		case err == nil:
			hash = pw.Hash
		case !store.IsNotFound(err):
			return nil, err
		}
	}
	ok, rehash, err := s.cost.verifyPassword(hash, p.Password)
	if err != nil {
		return nil, err
	}
	if !ok || hash == s.dummyHash {
		s.recordFailure(p.IP, username)
		return nil, ErrInvalidCredentials
	}
	s.byUser.reset(username)
	var sess *Session
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		if rehash {
			if err := q.SetPassword(ctx, store.SetPasswordParams{UserID: u.ID, Hash: s.cost.hashPassword(p.Password), UpdatedAt: s.now()}); err != nil {
				return err
			}
		}
		sess, err = s.newSession(ctx, q, u, p.UserAgent)
		return err
	})
	return sess, err
}

// SetPassword sets or changes u's password. current is required if u has
// one. Other sessions than keep are signed out.
func (s *Service) SetPassword(ctx context.Context, keep *Session, ip string, current *string, newPassword string) error {
	u := keep.User
	if err := s.checkLimits(ip, u.Username); err != nil {
		return err
	}
	if err := validatePassword("newPassword", newPassword); err != nil {
		return err
	}
	pw, err := s.db.GetPassword(ctx, u.ID)
	switch {
	case err == nil:
		if current == nil {
			return invalid("currentPassword", "required to change your password")
		}
		ok, _, err := s.cost.verifyPassword(pw.Hash, *current)
		if err != nil {
			return err
		}
		if !ok {
			s.recordFailure(ip, u.Username)
			return ErrWrongPassword
		}
	case !store.IsNotFound(err):
		return err
	}
	hash := s.cost.hashPassword(newPassword)
	return s.db.Tx(ctx, func(q *store.Queries) error {
		if err := q.SetPassword(ctx, store.SetPasswordParams{UserID: u.ID, Hash: hash, UpdatedAt: s.now()}); err != nil {
			return err
		}
		return q.DeleteOtherSessions(ctx, store.DeleteOtherSessionsParams{UserID: u.ID, TokenHash: keep.hash})
	})
}

// DeletePassword removes u's password, if u has a passkey to sign in with.
func (s *Service) DeletePassword(ctx context.Context, u store.User) error {
	return s.db.Tx(ctx, func(q *store.Queries) error {
		n, err := q.CountPasskeys(ctx, u.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrLastCredential
		}
		return q.DeletePassword(ctx, u.ID)
	})
}

// --- Profile, users, invites -------------------------------------------------

// Credentials reports how u can sign in.
func (s *Service) Credentials(ctx context.Context, u store.User) (hasPassword bool, passkeys int, err error) {
	_, err = s.db.GetPassword(ctx, u.ID)
	if err != nil && !store.IsNotFound(err) {
		return false, 0, err
	}
	hasPassword = err == nil
	n, err := s.db.CountPasskeys(ctx, u.ID)
	return hasPassword, int(n), err
}

// ProfileUpdate changes profile fields; nil leaves a field alone.
type ProfileUpdate struct {
	DisplayName *string
	// Avatar "" removes the avatar.
	Avatar *string
	Color  *string
}

// UpdateProfile applies p to u.
func (s *Service) UpdateProfile(ctx context.Context, u store.User, p ProfileUpdate) (store.User, error) {
	params := store.UpdateUserProfileParams{ID: u.ID, DisplayName: u.DisplayName, Avatar: u.Avatar, Color: u.Color}
	if p.DisplayName != nil {
		name := strings.TrimSpace(*p.DisplayName)
		if err := validateDisplayName(name); err != nil {
			return u, err
		}
		params.DisplayName = name
	}
	if p.Avatar != nil {
		a := strings.TrimSpace(*p.Avatar)
		if err := validateAvatar(a); err != nil {
			return u, err
		}
		params.Avatar = sql.NullString{String: a, Valid: a != ""}
	}
	if p.Color != nil {
		c := strings.ToLower(*p.Color)
		if !colorPattern.MatchString(c) {
			return u, invalid("color", "must look like #7c3aed")
		}
		params.Color = c
	}
	return s.db.UpdateUserProfile(ctx, params)
}

// Users lists everyone.
func (s *Service) Users(ctx context.Context) ([]store.User, error) { return s.db.ListUsers(ctx) }

// CreateInvite makes an invite (admins only).
func (s *Service) CreateInvite(ctx context.Context, by store.User, role string, ttl time.Duration) (store.Invite, error) {
	if by.Role != store.RoleAdmin {
		return store.Invite{}, ErrForbidden
	}
	if role != store.RoleAdmin && role != store.RoleMember {
		return store.Invite{}, invalid("role", "must be admin or member")
	}
	if ttl < time.Hour || ttl > 30*24*time.Hour {
		return store.Invite{}, invalid("expiresInHours", "must be between 1 and 720")
	}
	now := s.now()
	return s.db.CreateInvite(ctx, store.CreateInviteParams{
		Code: newCode(), CreatedBy: sql.NullString{String: by.ID, Valid: true}, Role: role, CreatedAt: now, ExpiresAt: now.Add(ttl),
	})
}

// Invites lists all invites (admins only).
func (s *Service) Invites(ctx context.Context, by store.User) ([]store.Invite, error) {
	if by.Role != store.RoleAdmin {
		return nil, ErrForbidden
	}
	return s.db.ListInvites(ctx)
}

// DeleteInvite revokes an invite (admins only).
func (s *Service) DeleteInvite(ctx context.Context, by store.User, code string) error {
	if by.Role != store.RoleAdmin {
		return ErrForbidden
	}
	if _, err := s.db.GetInvite(ctx, code); store.IsNotFound(err) {
		return ErrNotFound
	}
	return s.db.DeleteInvite(ctx, code)
}

// --- Validation --------------------------------------------------------------

var (
	usernamePattern = regexp.MustCompile(`^[a-z0-9._-]{2,32}$`)
	colorPattern    = regexp.MustCompile(`^#[0-9a-f]{6}$`)
)

func normalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func validateUsername(u string) error {
	if !usernamePattern.MatchString(u) {
		return invalid("username", "use 2 to 32 lowercase letters, digits, dots, dashes or underscores")
	}
	return nil
}

func validateDisplayName(n string) error {
	if l := utf8.RuneCountInString(n); l < 1 || l > 64 {
		return invalid("displayName", "must be 1 to 64 characters")
	}
	return nil
}

func validatePassword(field, pw string) error {
	if l := utf8.RuneCountInString(pw); l < 8 || len(pw) > 256 {
		return invalid(field, "must be at least 8 characters (and at most 256 bytes)")
	}
	return nil
}

func validateAvatar(a string) error {
	if a == "" {
		return nil
	}
	u, err := url.Parse(a)
	if err != nil || len(a) > 2048 {
		return invalid("avatar", "must be a valid URL")
	}
	web := u.Scheme == "https" || u.Scheme == "http"
	local := u.Scheme == "" && u.Host == "" && strings.HasPrefix(a, "/")
	if !web && !local {
		return invalid("avatar", "must be an http(s) URL or a path on this server")
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// palette is the lane colors handed out at signup, in order.
var palette = []string{
	"#7c3aed", "#0ea5e9", "#f97316", "#10b981", "#e11d48", "#eab308",
	"#6366f1", "#14b8a6", "#ec4899", "#84cc16", "#f43f5e", "#06b6d4",
}

// pickColor returns the palette color fewest users have.
func pickColor(users []store.User) string {
	counts := map[string]int{}
	for _, u := range users {
		counts[u.Color]++
	}
	return slices.MinFunc(palette, func(a, b string) int { return counts[a] - counts[b] })
}
