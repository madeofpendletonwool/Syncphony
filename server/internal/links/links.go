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
	ErrNotShareable     = errors.New("this service doesn't allow sharing an account")
	ErrPairingExpired   = errors.New("the pairing code expired or isn't yours; try linking again")
	ErrNotPaired        = errors.New("approve the pairing code first")
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

	mu       sync.Mutex
	oauth    map[string]*pendingOAuth   // by state
	pairings map[string]*pendingPairing // by ID

	lastOK sync.Map // link ID -> time.Time of the last recorded success
}

type pendingOAuth struct {
	userID, provider, relinkID, secret string
	paired                             string // a DevicePairer's result, from the pairing before
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
		notifier: cfg.Notifier, now: cfg.Now, oauth: map[string]*pendingOAuth{}, pairings: map[string]*pendingPairing{},
	}
}

// Providers lists the services users can link.
func (s *Service) Providers() []provider.Provider { return s.reg.All() }

// List returns u's links.
func (s *Service) List(ctx context.Context, userID string) ([]store.ServiceLink, error) {
	return s.db.ListServiceLinks(ctx, userID)
}

// Usable returns every link userID can search and queue from: their own,
// then the ones others have shared.
func (s *Service) Usable(ctx context.Context, userID string) ([]store.ServiceLink, error) {
	return s.db.ListUsableServiceLinks(ctx, userID)
}

// GetUsable returns a link userID owns or that's shared. Anything else is
// ErrNotFound.
func (s *Service) GetUsable(ctx context.Context, userID, linkID string) (store.ServiceLink, error) {
	row, err := s.db.GetUsableServiceLink(ctx, store.GetUsableServiceLinkParams{ID: linkID, UserID: userID})
	if store.IsNotFound(err) {
		return row, ErrNotFound
	}
	return row, err
}

// SetShared shares one of userID's links with everyone on the server, or
// stops sharing it. Songs already queued from it keep playing either way.
func (s *Service) SetShared(ctx context.Context, userID, linkID string, shared bool) (store.ServiceLink, error) {
	row, err := s.userLink(ctx, userID, linkID)
	if err != nil {
		return row, err
	}
	if shared {
		p, err := s.provider(row.Provider)
		if err != nil {
			return row, err
		}
		if !p.Info().Capabilities.Shareable {
			return row, ErrNotShareable
		}
	}
	if err := s.db.SetServiceLinkShared(ctx, store.SetServiceLinkSharedParams{Shared: shared, UpdatedAt: s.now(), ID: linkID, UserID: userID}); err != nil {
		return row, err
	}
	return s.userLink(ctx, userID, linkID)
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
// the user. relinkID, if set, is a link of userID's to replace. Providers
// that pair a device first start with BeginPairing instead.
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
	if _, pairs := p.Linker().(provider.DevicePairer); pairs {
		return "", ErrNotPaired
	}
	return s.beginOAuth(ctx, p, &pendingOAuth{userID: userID, provider: providerID, relinkID: relinkID})
}

// BeginOAuthPaired starts the OAuth2 step of a link whose device pairing
// (pairingID) the user has approved.
func (s *Service) BeginOAuthPaired(ctx context.Context, userID, pairingID string) (string, error) {
	s.mu.Lock()
	pp, ok := s.pairings[pairingID]
	if ok && pp.userID == userID && pp.paired != "" {
		delete(s.pairings, pairingID)
	}
	s.mu.Unlock()
	switch {
	case !ok || pp.userID != userID || s.now().After(pp.expires):
		return "", ErrPairingExpired
	case pp.paired == "":
		return "", ErrNotPaired
	}
	p, err := s.provider(pp.provider)
	if err != nil {
		return "", err
	}
	return s.beginOAuth(ctx, p, &pendingOAuth{userID: userID, provider: pp.provider, relinkID: pp.relinkID, paired: pp.paired})
}

func (s *Service) beginOAuth(ctx context.Context, p provider.Provider, pending *pendingOAuth) (string, error) {
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
	pending.secret, pending.expires = start.Secret, now.Add(oauthTTL)
	s.oauth[state] = pending
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
	creds, account, err := p.Linker().Complete(ctx, provider.LinkInput{Code: code, RedirectURL: s.CallbackURL(), OAuthSecret: pending.secret, Paired: pending.paired})
	if err != nil {
		return store.ServiceLink{}, err
	}
	return s.save(ctx, userID, pending.provider, pending.relinkID, creds, account)
}

// --- Linking by device pairing -------------------------------------------------

// maxPairingTTL caps how long a pairing is kept, whatever the service says.
const maxPairingTTL = 15 * time.Minute

// slowDown is how much longer to wait between polls when the service says
// we're polling too often.
const slowDown = 5 * time.Second

type pendingPairing struct {
	userID, provider, relinkID, secret string
	interval                           time.Duration
	expires, nextPoll                  time.Time
	// paired is the provider's result once the user approved, kept for
	// the OAuth2 step that follows.
	paired string
}

// Pairing is a device pairing waiting for the user to approve it.
type Pairing struct {
	ID        string
	VerifyURL string
	UserCode  string
	// Interval is how often to poll.
	Interval  time.Duration
	ExpiresAt time.Time
}

// PairingState is how far a pairing has got.
type PairingState string

// Pairing states.
const (
	// PairingPending: the user hasn't approved it yet.
	PairingPending PairingState = "pending"
	// PairingApproved: approved; continue with BeginOAuthPaired.
	PairingApproved PairingState = "approved"
	// PairingLinked: approved, and the account is linked.
	PairingLinked PairingState = "linked"
)

// PairingStatus is a pairing's state, and the link once it's linked.
type PairingStatus struct {
	State PairingState
	Link  store.ServiceLink
}

// BeginPairing starts linking providerID (or re-linking relinkID) by
// device pairing. The provider's linker must pair: as its method, or
// before its OAuth2 step.
func (s *Service) BeginPairing(ctx context.Context, userID, providerID, relinkID string) (Pairing, error) {
	if relinkID != "" {
		row, err := s.userLink(ctx, userID, relinkID)
		if err != nil {
			return Pairing{}, err
		}
		providerID = row.Provider
	}
	p, err := s.provider(providerID)
	if err != nil {
		return Pairing{}, err
	}
	dp, ok := p.Linker().(provider.DevicePairer)
	if !ok {
		return Pairing{}, ErrWrongMethod
	}
	start, err := dp.BeginPairing(ctx)
	if err != nil {
		return Pairing{}, err
	}
	ttl := min(start.ExpiresIn, maxPairingTTL)
	if ttl <= 0 {
		ttl = maxPairingTTL
	}
	interval := max(start.Interval, time.Second)
	id := rand.Text()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, v := range s.pairings {
		if now.After(v.expires) {
			delete(s.pairings, k)
		}
	}
	pp := &pendingPairing{
		userID: userID, provider: providerID, relinkID: relinkID, secret: start.Secret,
		interval: interval, expires: now.Add(ttl), nextPoll: now.Add(interval),
	}
	s.pairings[id] = pp
	return Pairing{ID: id, VerifyURL: start.VerifyURL, UserCode: start.UserCode, Interval: interval, ExpiresAt: pp.expires}, nil
}

// PollPairing checks whether the user approved a pairing. It asks the
// service at most once per interval; polling sooner just reports pending.
// Once approved, a LinkDevice provider's account is linked; an OAuth2
// provider continues with BeginOAuthPaired.
func (s *Service) PollPairing(ctx context.Context, userID, pairingID string) (PairingStatus, error) {
	s.mu.Lock()
	pp, ok := s.pairings[pairingID]
	now := s.now()
	switch {
	case !ok || pp.userID != userID:
		s.mu.Unlock()
		return PairingStatus{}, ErrPairingExpired
	case now.After(pp.expires):
		delete(s.pairings, pairingID)
		s.mu.Unlock()
		return PairingStatus{}, ErrPairingExpired
	case pp.paired != "":
		s.mu.Unlock()
		return PairingStatus{State: PairingApproved}, nil
	case now.Before(pp.nextPoll):
		s.mu.Unlock()
		return PairingStatus{State: PairingPending}, nil
	}
	pp.nextPoll = now.Add(pp.interval)
	secret, providerID, relinkID := pp.secret, pp.provider, pp.relinkID
	s.mu.Unlock()

	p, err := s.provider(providerID)
	if err != nil {
		return PairingStatus{}, err
	}
	dp, ok := p.Linker().(provider.DevicePairer)
	if !ok {
		return PairingStatus{}, ErrWrongMethod
	}
	paired, err := dp.PollPairing(ctx, secret)
	switch {
	case errors.Is(err, provider.ErrPending):
		return PairingStatus{State: PairingPending}, nil
	case errors.Is(err, provider.ErrRateLimited):
		s.mu.Lock()
		pp.interval += slowDown
		pp.nextPoll = s.now().Add(pp.interval)
		s.mu.Unlock()
		return PairingStatus{State: PairingPending}, nil
	case err != nil:
		s.mu.Lock()
		delete(s.pairings, pairingID)
		s.mu.Unlock()
		return PairingStatus{}, err
	}
	if p.Linker().Method() != provider.LinkDevice {
		s.mu.Lock()
		pp.paired = paired
		s.mu.Unlock()
		return PairingStatus{State: PairingApproved}, nil
	}
	s.mu.Lock()
	delete(s.pairings, pairingID)
	s.mu.Unlock()
	creds, account, err := p.Linker().Complete(ctx, provider.LinkInput{Paired: paired})
	if err != nil {
		return PairingStatus{}, err
	}
	row, err := s.save(ctx, userID, providerID, relinkID, creds, account)
	if err != nil {
		return PairingStatus{}, err
	}
	return PairingStatus{State: PairingLinked, Link: row}, nil
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

// OpenFor is Open for a link userID may use: their own, or a shared one.
// Anyone else's link is ErrNotFound.
func (s *Service) OpenFor(ctx context.Context, userID, linkID string) (provider.Session, error) {
	if _, err := s.GetUsable(ctx, userID, linkID); err != nil {
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
