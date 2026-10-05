// SPDX-License-Identifier: AGPL-3.0-only

// Package links manages users' linked service accounts: linking through a
// provider's Linker, keeping the credentials sealed in the vault, opening
// provider sessions with them, and tracking each link's health.
//
// Credentials are decrypted only inside Open and never leave this package
// except to the provider that made them.
package links

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/vault"
)

// Errors returned by Service, besides the provider package's errors
// (ErrInvalidCredentials, ErrUnavailable, ...) which pass through.
var (
	ErrUnknownProvider  = errors.New("unknown service")
	ErrNotFound         = errors.New("link not found")
	ErrWrongMethod      = errors.New("this service links a different way")
	ErrDifferentAccount = errors.New("that's a different account; unlink this one and link the new one instead")
	ErrOAuthState       = errors.New("the authorization request expired or isn't yours; try linking again")
)

// InvalidInputError is a bad link form field.
type InvalidInputError struct {
	Field, Message string
}

func (e *InvalidInputError) Error() string { return e.Field + ": " + e.Message }

// Notifier is told when a link's status changes: once when its credentials
// stop working, and whenever it's linked or re-linked.
type Notifier interface {
	LinkStatusChanged(ctx context.Context, link store.ServiceLink)
}

// LogNotifier only logs.
type LogNotifier struct{}

// LinkStatusChanged implements Notifier.
func (LogNotifier) LinkStatusChanged(_ context.Context, l store.ServiceLink) {
	if l.Status == store.LinkExpired {
		slog.Warn("service link needs relinking", "link", l.ID, "user", l.UserID, "provider", l.Provider, "account", l.AccountLabel)
	}
}

// BusNotifier pushes status changes to the link owner's connections, and logs.
type BusNotifier struct{ Bus realtime.Bus }

// LinkStatusChanged implements Notifier.
func (n BusNotifier) LinkStatusChanged(ctx context.Context, l store.ServiceLink) {
	LogNotifier{}.LinkStatusChanged(ctx, l)
	n.Bus.Publish(realtime.UserTopic(l.UserID), realtime.Event{Type: realtime.LinkStatus, Data: l})
}

// Config configures a Service.
type Config struct {
	// BaseURL is the public URL. OAuth2 redirects come back to
	// BaseURL + CallbackPath.
	BaseURL  string
	Notifier Notifier // default LogNotifier
	Now      func() time.Time
}

// CallbackPath is where OAuth2 providers redirect back to.
const CallbackPath = "/api/links/oauth/callback"

// oauthTTL is how long a user has to finish an OAuth2 authorization.
const oauthTTL = 10 * time.Minute

// Service manages links.
type Service struct {
	db       *store.Store
	vault    *vault.Vault
	reg      *provider.Registry
	baseURL  string
	notifier Notifier
	now      func() time.Time

	mu    sync.Mutex
	oauth map[string]*pendingOAuth // by state

	lastOK sync.Map // link ID -> time.Time of the last recorded success
}

type pendingOAuth struct {
	userID, provider, relinkID, secret string
	expires                            time.Time
}

// New returns a Service.
func New(db *store.Store, v *vault.Vault, reg *provider.Registry, cfg Config) *Service {
	if cfg.Notifier == nil {
		cfg.Notifier = LogNotifier{}
	}
	if cfg.Now == nil {
		cfg.Now = store.Now
	}
	return &Service{
		db: db, vault: v, reg: reg, baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		notifier: cfg.Notifier, now: cfg.Now, oauth: map[string]*pendingOAuth{},
	}
}

// Providers lists the services users can link.
func (s *Service) Providers() []provider.Provider { return s.reg.All() }

// List returns u's links.
func (s *Service) List(ctx context.Context, userID string) ([]store.ServiceLink, error) {
	return s.db.ListServiceLinks(ctx, userID)
}

// Provider returns a registered provider, or ErrUnknownProvider.
func (s *Service) Provider(id string) (provider.Provider, error) { return s.provider(id) }

// Get returns one of userID's links. Someone else's link is ErrNotFound.
func (s *Service) Get(ctx context.Context, userID, linkID string) (store.ServiceLink, error) {
	return s.userLink(ctx, userID, linkID)
}

func (s *Service) provider(id string) (provider.Provider, error) {
	p, ok := s.reg.Get(id)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, id)
	}
	return p, nil
}

// --- Linking with a form -------------------------------------------------------

// LinkWithCredentials links an account by filling in the provider's form.
// Linking an account that's already linked updates it.
func (s *Service) LinkWithCredentials(ctx context.Context, userID, providerID string, fields map[string]string) (store.ServiceLink, error) {
	return s.linkWithCredentials(ctx, userID, providerID, "", fields)
}

// RelinkWithCredentials replaces the credentials of one of userID's links.
func (s *Service) RelinkWithCredentials(ctx context.Context, userID, linkID string, fields map[string]string) (store.ServiceLink, error) {
	row, err := s.userLink(ctx, userID, linkID)
	if err != nil {
		return store.ServiceLink{}, err
	}
	return s.linkWithCredentials(ctx, userID, row.Provider, linkID, fields)
}

func (s *Service) linkWithCredentials(ctx context.Context, userID, providerID, relinkID string, fields map[string]string) (store.ServiceLink, error) {
	p, err := s.provider(providerID)
	if err != nil {
		return store.ServiceLink{}, err
	}
	l := p.Linker()
	if l.Method() != provider.LinkCredentials {
		return store.ServiceLink{}, ErrWrongMethod
	}
	clean, err := checkFields(l.Fields(), fields)
	if err != nil {
		return store.ServiceLink{}, err
	}
	creds, account, err := l.Complete(ctx, provider.LinkInput{Fields: clean})
	if err != nil {
		return store.ServiceLink{}, err
	}
	return s.save(ctx, userID, providerID, relinkID, creds, account)
}

// checkFields validates form input against the provider's fields and drops
// anything the provider didn't ask for.
func checkFields(spec []provider.LinkField, in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(spec))
	for _, f := range spec {
		v := in[f.Name]
		if f.Kind != provider.FieldSecret {
			v = strings.TrimSpace(v)
		}
		if v == "" {
			if f.Required {
				return nil, &InvalidInputError{Field: f.Name, Message: f.Label + " is required"}
			}
			continue
		}
		if f.Kind == provider.FieldURL {
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return nil, &InvalidInputError{Field: f.Name, Message: f.Label + " must be an http(s) URL"}
			}
		}
		if len(v) > 4096 {
			return nil, &InvalidInputError{Field: f.Name, Message: f.Label + " is too long"}
		}
		out[f.Name] = v
	}
	return out, nil
}

// --- Linking with OAuth2 -------------------------------------------------------

// CallbackURL is the OAuth2 redirect URL to register with services.
func (s *Service) CallbackURL() string { return s.baseURL + CallbackPath }

// BeginOAuth starts linking providerID by OAuth2 and returns where to send
// the user. relinkID, if set, is a link of userID's to replace.
func (s *Service) BeginOAuth(ctx context.Context, userID, providerID, relinkID string) (string, error) {
	if relinkID != "" {
		row, err := s.userLink(ctx, userID, relinkID)
		if err != nil {
			return "", err
		}
		providerID = row.Provider
	}
	p, err := s.provider(providerID)
	if err != nil {
		return "", err
	}
	if p.Linker().Method() != provider.LinkOAuth2 {
		return "", ErrWrongMethod
	}
	state := rand.Text()
	start, err := p.Linker().BeginOAuth(ctx, provider.OAuthRequest{State: state, RedirectURL: s.CallbackURL()})
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, v := range s.oauth {
		if now.After(v.expires) {
			delete(s.oauth, k)
		}
	}
	s.oauth[state] = &pendingOAuth{userID: userID, provider: providerID, relinkID: relinkID, secret: start.Secret, expires: now.Add(oauthTTL)}
	return start.AuthURL, nil
}

// CompleteOAuth finishes an OAuth2 link when the service redirects back.
// The state must have been issued to userID.
func (s *Service) CompleteOAuth(ctx context.Context, userID, state, code string) (store.ServiceLink, error) {
	s.mu.Lock()
	pending, ok := s.oauth[state]
	if ok && pending.userID == userID {
		delete(s.oauth, state)
	}
	s.mu.Unlock()
	if !ok || pending.userID != userID || s.now().After(pending.expires) {
		return store.ServiceLink{}, ErrOAuthState
	}
	p, err := s.provider(pending.provider)
	if err != nil {
		return store.ServiceLink{}, err
	}
	creds, account, err := p.Linker().Complete(ctx, provider.LinkInput{Code: code, RedirectURL: s.CallbackURL(), OAuthSecret: pending.secret})
	if err != nil {
		return store.ServiceLink{}, err
	}
	return s.save(ctx, userID, pending.provider, pending.relinkID, creds, account)
}

// --- Storage -------------------------------------------------------------------

// save seals creds into a new link, or into relinkID, or into the user's
// existing link to the same account.
func (s *Service) save(ctx context.Context, userID, providerID, relinkID string, creds provider.Credentials, account provider.AccountInfo) (store.ServiceLink, error) {
	defer clear(creds)
	if account.ID == "" {
		return store.ServiceLink{}, fmt.Errorf("provider %s returned no account ID", providerID)
	}
	label := account.Name
	if label == "" {
		label = account.ID
	}
	now := s.now()
	var id string
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		existing, err := q.FindServiceLink(ctx, store.FindServiceLinkParams{UserID: userID, Provider: providerID, AccountID: account.ID})
		switch {
		case relinkID != "":
			row, err := q.GetUserServiceLink(ctx, store.GetUserServiceLinkParams{ID: relinkID, UserID: userID})
			if store.IsNotFound(err) {
				return ErrNotFound
			} else if err != nil {
				return err
			}
			if row.AccountID != account.ID {
				return ErrDifferentAccount
			}
			id = row.ID
		case err == nil:
			id = existing.ID
		case store.IsNotFound(err):
			id = store.NewID()
			sealed, err := s.vault.Seal(id, userID, creds)
			if err != nil {
				return err
			}
			_, err = q.CreateServiceLink(ctx, store.CreateServiceLinkParams{
				ID: id, UserID: userID, Provider: providerID, AccountID: account.ID, AccountLabel: label, EncryptedCredentials: sealed, Now: now,
			})
			return err
		default:
			return err
		}
		sealed, err := s.vault.Seal(id, userID, creds)
		if err != nil {
			return err
		}
		return q.RelinkServiceLink(ctx, store.RelinkServiceLinkParams{EncryptedCredentials: sealed, AccountLabel: label, Now: now, ID: id})
	})
	if err != nil {
		return store.ServiceLink{}, err
	}
	s.lastOK.Store(id, now)
	row, err := s.db.GetServiceLink(ctx, id)
	if err == nil {
		s.notifier.LinkStatusChanged(ctx, row)
	}
	return row, err
}

func (s *Service) userLink(ctx context.Context, userID, linkID string) (store.ServiceLink, error) {
	row, err := s.db.GetUserServiceLink(ctx, store.GetUserServiceLinkParams{ID: linkID, UserID: userID})
	if store.IsNotFound(err) {
		return row, ErrNotFound
	}
	return row, err
}

// Unlink deletes one of userID's links, ciphertext included. Queue items
// from it stay, but can no longer play.
func (s *Service) Unlink(ctx context.Context, userID, linkID string) error {
	if _, err := s.userLink(ctx, userID, linkID); err != nil {
		return err
	}
	s.lastOK.Delete(linkID)
	return s.db.DeleteServiceLink(ctx, store.DeleteServiceLinkParams{ID: linkID, UserID: userID})
}

// Rotate re-wraps every link sealed with an old master key, and returns how
// many it changed. Links whose key is missing are reported, not fatal.
func (s *Service) Rotate(ctx context.Context) (rewrapped int, failed []string, err error) {
	rows, err := s.db.ListAllServiceLinks(ctx)
	if err != nil {
		return 0, nil, err
	}
	for _, r := range rows {
		if !s.vault.NeedsRewrap(r.EncryptedCredentials) {
			continue
		}
		sealed, err := s.vault.Rewrap(r.ID, r.UserID, r.EncryptedCredentials)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s (%s, %s): %v", r.ID, r.Provider, r.AccountLabel, err))
			continue
		}
		if err := s.db.RewrapServiceLink(ctx, store.RewrapServiceLinkParams{EncryptedCredentials: sealed, ID: r.ID}); err != nil {
			return rewrapped, failed, err
		}
		rewrapped++
	}
	return rewrapped, failed, nil
}

// --- Using links ---------------------------------------------------------------

// Open returns a provider session for a link. The session reports
// ErrAuthExpired back to the link's health, and saves rotated credentials.
func (s *Service) Open(ctx context.Context, linkID string) (provider.Session, error) {
	row, err := s.db.GetServiceLink(ctx, linkID)
	if store.IsNotFound(err) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if row.Status == store.LinkExpired {
		return nil, fmt.Errorf("link %s: %w", row.ID, provider.ErrAuthExpired)
	}
	p, err := s.provider(row.Provider)
	if err != nil {
		return nil, err
	}
	creds, err := s.vault.Open(row.ID, row.UserID, row.EncryptedCredentials)
	if err != nil {
		slog.Error("can't decrypt service link; was the vault key changed without a rotation?", "link", row.ID, "err", err)
		return nil, s.observe(ctx, row, fmt.Errorf("%w: stored credentials can't be decrypted", provider.ErrAuthExpired))
	}
	link := provider.Link{
		ID:          row.ID,
		Account:     provider.AccountInfo{ID: row.AccountID, Name: row.AccountLabel},
		Credentials: creds,
		Sink:        s.sink(row),
	}
	sess, err := p.Open(ctx, link)
	if err != nil {
		return nil, s.observe(ctx, row, err)
	}
	return wrap(&watcher{s: s, row: row, inner: sess}), nil
}

// OpenFor is Open for one of userID's own links. Someone else's link is
// ErrNotFound.
func (s *Service) OpenFor(ctx context.Context, userID, linkID string) (provider.Session, error) {
	if _, err := s.userLink(ctx, userID, linkID); err != nil {
		return nil, err
	}
	return s.Open(ctx, linkID)
}

// sink saves credentials a session rotated (e.g. after an OAuth2 refresh).
func (s *Service) sink(row store.ServiceLink) provider.CredentialSink {
	return func(ctx context.Context, c provider.Credentials) error {
		sealed, err := s.vault.Seal(row.ID, row.UserID, c)
		if err != nil {
			return err
		}
		return s.db.UpdateServiceLinkCredentials(context.WithoutCancel(ctx), store.UpdateServiceLinkCredentialsParams{
			EncryptedCredentials: sealed, Now: s.now(), ID: row.ID,
		})
	}
}

// okEvery limits how often a working link's last_ok_at is written.
const okEvery = 10 * time.Minute

// observe updates a link's health after a provider call, and returns err.
func (s *Service) observe(ctx context.Context, row store.ServiceLink, err error) error {
	ctx = context.WithoutCancel(ctx)
	switch {
	case err == nil:
		now := s.now()
		// lastOK is cleared when a link expires, so a recent entry means
		// the row is already marked ok.
		if last, ok := s.lastOK.Load(row.ID); ok && now.Sub(last.(time.Time)) < okEvery {
			return nil
		}
		s.lastOK.Store(row.ID, now)
		if dbErr := s.db.MarkServiceLinkOK(ctx, store.MarkServiceLinkOKParams{Now: now, ID: row.ID}); dbErr != nil {
			slog.Warn("recording link health", "link", row.ID, "err", dbErr)
		}
	case errors.Is(err, provider.ErrAuthExpired):
		cur, dbErr := s.db.GetServiceLink(ctx, row.ID)
		if dbErr != nil || cur.Status == store.LinkExpired {
			return err
		}
		s.lastOK.Delete(row.ID)
		if dbErr := s.db.SetServiceLinkStatus(ctx, store.SetServiceLinkStatusParams{
			Status: store.LinkExpired, StatusDetail: "Sign in to " + row.Provider + " again to keep using this link.", UpdatedAt: s.now(), ID: row.ID,
		}); dbErr != nil {
			slog.Warn("recording link health", "link", row.ID, "err", dbErr)
			return err
		}
		cur.Status = store.LinkExpired
		s.notifier.LinkStatusChanged(ctx, cur)
	}
	return err
}

// KeyID is the ID of the vault key new credentials are sealed with.
func (s *Service) KeyID() string { return s.vault.CurrentKeyID() }
