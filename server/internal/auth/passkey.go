// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

type webauthnRP = webauthn.WebAuthn

func newWebAuthn(baseURL string) (*webauthnRP, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("auth: base URL %q: need an absolute URL", baseURL)
	}
	return webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: "Syncphony",
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
	})
}

// waUser adapts a user and their passkeys to webauthn.User.
type waUser struct {
	id, name, displayName string
	creds                 []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte                         { return []byte(u.id) }
func (u *waUser) WebAuthnName() string                       { return u.name }
func (u *waUser) WebAuthnDisplayName() string                { return u.displayName }
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (s *Service) loadWAUser(ctx context.Context, q *store.Queries, u store.User) (*waUser, error) {
	rows, err := q.ListPasskeys(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	wu := &waUser{id: u.ID, name: u.Username, displayName: u.DisplayName}
	for _, r := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(r.Data), &c); err != nil {
			return nil, fmt.Errorf("passkey %x: %w", r.ID, err)
		}
		wu.creds = append(wu.creds, c)
	}
	return wu, nil
}

// Ceremony is a WebAuthn ceremony for the browser to complete.
type Ceremony struct {
	ID string
	// Options is the JSON for navigator.credentials.create/get
	// ({"publicKey": ...}), decoded into a map.
	Options map[string]any
}

func (s *Service) startCeremony(cer *ceremony, options any) (*Ceremony, error) {
	b, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &Ceremony{ID: s.cers.put(cer), Options: m}, nil
}

// registrationOptions ask for a discoverable credential (a passkey), so it
// can sign in without a username.
func registrationOptions(existing []webauthn.Credential) []webauthn.RegistrationOption {
	exclude := make([]protocol.CredentialDescriptor, len(existing))
	for i, c := range existing {
		exclude[i] = c.Descriptor()
	}
	return []webauthn.RegistrationOption{
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(exclude),
	}
}

// BeginPasskeySignup starts creating an account with a passkey.
func (s *Service) BeginPasskeySignup(ctx context.Context, ip, invite, username, displayName string) (*Ceremony, error) {
	in, err := s.checkSignup(ctx, ip, invite, username, displayName)
	if err != nil {
		return nil, err
	}
	wu := &waUser{id: store.NewID(), name: in.username, displayName: in.displayName}
	creation, session, err := s.webauthn.BeginRegistration(wu, registrationOptions(nil)...)
	if err != nil {
		return nil, err
	}
	return s.startCeremony(&ceremony{kind: ceremonySignup, session: *session, userID: wu.id, signup: &in}, creation)
}

// FinishParams completes a WebAuthn ceremony.
type FinishParams struct {
	CeremonyID string
	// Credential is the browser's PublicKeyCredential.toJSON(), as JSON.
	Credential []byte
	// Name labels a new passkey.
	Name          string
	IP, UserAgent string
}

// FinishPasskeySignup creates the account and signs it in.
func (s *Service) FinishPasskeySignup(ctx context.Context, p FinishParams) (*Session, error) {
	cer, err := s.cers.take(p.CeremonyID, ceremonySignup)
	if err != nil {
		return nil, err
	}
	in := *cer.signup
	wu := &waUser{id: cer.userID, name: in.username, displayName: in.displayName}
	cred, err := s.verifyRegistration(wu, cer.session, p.Credential)
	if err != nil {
		return nil, err
	}
	var sess *Session
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		u, err := s.createUser(ctx, q, cer.userID, in)
		if err != nil {
			return err
		}
		if _, err := s.savePasskey(ctx, q, u.ID, cred, p.Name, p.UserAgent); err != nil {
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

func (s *Service) verifyRegistration(wu *waUser, session webauthn.SessionData, response []byte) (*webauthn.Credential, error) {
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPasskeyFailed, err)
	}
	cred, err := s.webauthn.CreateCredential(wu, session, parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPasskeyFailed, err)
	}
	return cred, nil
}

func (s *Service) savePasskey(ctx context.Context, q *store.Queries, userID string, cred *webauthn.Credential, name, userAgent string) (store.CredentialsPasskey, error) {
	data, err := json.Marshal(cred)
	if err != nil {
		return store.CredentialsPasskey{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultPasskeyName(userAgent)
	}
	return q.CreatePasskey(ctx, store.CreatePasskeyParams{
		ID: cred.ID, UserID: userID, Name: truncate(name, 64), Data: string(data), CreatedAt: s.now(),
	})
}

// defaultPasskeyName guesses a label from the browser's user agent.
func defaultPasskeyName(ua string) string {
	for _, d := range []struct{ match, name string }{
		{"iPhone", "iPhone"},
		{"iPad", "iPad"},
		{"Android", "Android"},
		{"Macintosh", "Mac"},
		{"Windows", "Windows"},
		{"CrOS", "Chromebook"},
		{"Linux", "Linux"},
	} {
		if strings.Contains(ua, d.match) {
			return d.name
		}
	}
	return "Passkey"
}

// BeginPasskeyLogin starts a username-less passkey sign-in.
func (s *Service) BeginPasskeyLogin(ip string) (*Ceremony, error) {
	if err := s.checkLimits(ip, ""); err != nil {
		return nil, err
	}
	assertion, session, err := s.webauthn.BeginDiscoverableLogin()
	if err != nil {
		return nil, err
	}
	return s.startCeremony(&ceremony{kind: ceremonyLogin, session: *session}, assertion)
}

// FinishPasskeyLogin verifies the passkey and signs its owner in.
func (s *Service) FinishPasskeyLogin(ctx context.Context, p FinishParams) (*Session, error) {
	if err := s.checkLimits(p.IP, ""); err != nil {
		return nil, err
	}
	cer, err := s.cers.take(p.CeremonyID, ceremonyLogin)
	if err != nil {
		return nil, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(p.Credential)
	if err != nil {
		s.byIP.fail(p.IP)
		return nil, fmt.Errorf("%w: %w", ErrPasskeyFailed, err)
	}
	var owner store.User
	handler := func(_, userHandle []byte) (webauthn.User, error) {
		u, err := s.db.GetUser(ctx, string(userHandle))
		if err != nil {
			return nil, err
		}
		owner = u
		return s.loadWAUser(ctx, s.db.Queries, u)
	}
	_, cred, err := s.webauthn.ValidatePasskeyLogin(handler, cer.session, parsed)
	if err != nil {
		s.byIP.fail(p.IP)
		return nil, ErrInvalidCredentials
	}
	if cred.Authenticator.CloneWarning {
		slog.Warn("passkey sign count went backwards; possible cloned authenticator", "user", owner.Username, "credential", base64.RawURLEncoding.EncodeToString(cred.ID))
		s.byIP.fail(p.IP)
		return nil, ErrInvalidCredentials
	}
	data, err := json.Marshal(cred)
	if err != nil {
		return nil, err
	}
	var sess *Session
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		if err := q.UsePasskey(ctx, store.UsePasskeyParams{Data: string(data), LastUsedAt: sql.NullTime{Time: s.now(), Valid: true}, ID: cred.ID}); err != nil {
			return err
		}
		sess, err = s.newSession(ctx, q, owner, p.UserAgent)
		return err
	})
	return sess, err
}

// BeginAddPasskey starts registering another passkey for u.
func (s *Service) BeginAddPasskey(ctx context.Context, u store.User) (*Ceremony, error) {
	wu, err := s.loadWAUser(ctx, s.db.Queries, u)
	if err != nil {
		return nil, err
	}
	creation, session, err := s.webauthn.BeginRegistration(wu, registrationOptions(wu.creds)...)
	if err != nil {
		return nil, err
	}
	return s.startCeremony(&ceremony{kind: ceremonyAddPasskey, session: *session, userID: u.ID}, creation)
}

// FinishAddPasskey saves the new passkey.
func (s *Service) FinishAddPasskey(ctx context.Context, u store.User, p FinishParams) (store.CredentialsPasskey, error) {
	cer, err := s.cers.take(p.CeremonyID, ceremonyAddPasskey)
	if err != nil {
		return store.CredentialsPasskey{}, err
	}
	if cer.userID != u.ID {
		return store.CredentialsPasskey{}, ErrCeremonyExpired
	}
	wu, err := s.loadWAUser(ctx, s.db.Queries, u)
	if err != nil {
		return store.CredentialsPasskey{}, err
	}
	cred, err := s.verifyRegistration(wu, cer.session, p.Credential)
	if err != nil {
		return store.CredentialsPasskey{}, err
	}
	return s.savePasskey(ctx, s.db.Queries, u.ID, cred, p.Name, p.UserAgent)
}

// Passkeys lists u's passkeys.
func (s *Service) Passkeys(ctx context.Context, u store.User) ([]store.CredentialsPasskey, error) {
	return s.db.ListPasskeys(ctx, u.ID)
}

// RenamePasskey renames one of u's passkeys.
func (s *Service) RenamePasskey(ctx context.Context, u store.User, id []byte, name string) error {
	name = strings.TrimSpace(name)
	if l := len([]rune(name)); l < 1 || l > 64 {
		return invalid("name", "must be 1 to 64 characters")
	}
	if err := s.ownPasskey(ctx, s.db.Queries, u, id); err != nil {
		return err
	}
	return s.db.RenamePasskey(ctx, store.RenamePasskeyParams{Name: name, ID: id, UserID: u.ID})
}

// DeletePasskey removes one of u's passkeys, unless it's their only way to
// sign in.
func (s *Service) DeletePasskey(ctx context.Context, u store.User, id []byte) error {
	return s.db.Tx(ctx, func(q *store.Queries) error {
		if err := s.ownPasskey(ctx, q, u, id); err != nil {
			return err
		}
		n, err := q.CountPasskeys(ctx, u.ID)
		if err != nil {
			return err
		}
		if n == 1 {
			if _, err := q.GetPassword(ctx, u.ID); store.IsNotFound(err) {
				return ErrLastCredential
			} else if err != nil {
				return err
			}
		}
		return q.DeletePasskey(ctx, store.DeletePasskeyParams{ID: id, UserID: u.ID})
	})
}

func (s *Service) ownPasskey(ctx context.Context, q *store.Queries, u store.User, id []byte) error {
	pk, err := q.GetPasskey(ctx, id)
	if store.IsNotFound(err) || (err == nil && pk.UserID != u.ID) {
		return ErrNotFound
	}
	return err
}

// PasskeyID decodes a base64url passkey ID from a URL.
func PasskeyID(s string) ([]byte, error) {
	id, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil || len(id) == 0 {
		return nil, ErrNotFound
	}
	return id, nil
}
