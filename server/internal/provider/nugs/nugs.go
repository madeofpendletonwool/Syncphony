// SPDX-License-Identifier: AGPL-3.0-only

// Package nugs is the provider for nugs.net, the archive of live concert
// recordings. nugs.net has no public API, so this speaks the private one its
// own apps use; see docs/adr/0009-nugs.md for what that means.
//
// Users link with their nugs.net email and password. They're traded once
// for a refresh token, which is what's stored: the password isn't kept.
// Searching and browsing the catalog needs no account, but playing does,
// with an active subscription.
//
// nugs.net is organized by show, not album: a "Tweezer" search finds every
// night it was played. Shows are albums here, titled by date and venue so
// the versions can be told apart, and a track's ID names its show too
// ("<show>.<track>"), since nugs.net can't look a track up on its own.
package nugs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/artcache"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// ID is the provider ID stored in links and track refs.
const ID = "nugs"

const (
	// artworkTTL is how long a cached image is served before it's fetched
	// again. Show art rarely changes.
	artworkTTL = 24 * time.Hour
	// maxArtwork is the largest image we'll fetch or cache.
	maxArtwork = 8 << 20
)

// Endpoints are the nugs.net hosts the provider talks to. Tests point them
// at a fake.
type Endpoints struct {
	// Auth is the OpenID Connect server: sign-in, tokens and user info.
	Auth string
	// Stream serves the catalog's legacy API (search, artists' shows) and
	// stream URLs.
	Stream string
	// Catalog serves show details.
	Catalog string
	// Subscriptions says which plan an account is on.
	Subscriptions string
	// Images serves show artwork, by the paths the catalog returns.
	Images string
}

// DefaultEndpoints are nugs.net's.
var DefaultEndpoints = Endpoints{
	Auth:          "https://id.nugs.net",
	Stream:        "https://streamapi.nugs.net",
	Catalog:       "https://catalog.nugs.net",
	Subscriptions: "https://subscriptions.nugs.net",
	Images:        "https://api.livedownloads.com",
}

// Options configure the provider. The zero value is ready to use.
type Options struct {
	// Client makes every request: API calls and audio.
	Client *http.Client
	// Endpoints default to DefaultEndpoints.
	Endpoints Endpoints
	// ArtworkCacheBytes bounds the artwork cache, shared by every session.
	// Default 32 MiB; negative disables the cache.
	ArtworkCacheBytes int64
	// Now is the clock, for token expiry and caches.
	Now func() time.Time
}

// Provider is the nugs.net provider.
type Provider struct {
	client *http.Client
	ep     Endpoints
	now    func() time.Time
	art    *artcache.Cache[string]
	// shows caches show details. The catalog is the same for everyone, so
	// sessions share it.
	shows *showCache
	// searches caches search results briefly, so paging through one
	// doesn't search again.
	searches *searchCache

	mu sync.Mutex
	// auths holds each link's tokens, shared by its sessions: a refresh
	// token works once, so two sessions refreshing on their own would
	// sign each other out.
	auths map[string]*auth
}

var _ provider.Provider = (*Provider)(nil)

// New returns a nugs.net provider.
func New(opts Options) *Provider {
	if opts.Client == nil {
		opts.Client = defaultClient()
	}
	if opts.Endpoints == (Endpoints{}) {
		opts.Endpoints = DefaultEndpoints
	}
	if opts.ArtworkCacheBytes == 0 {
		opts.ArtworkCacheBytes = 32 << 20
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Provider{
		client:   opts.Client,
		ep:       opts.Endpoints,
		now:      opts.Now,
		art:      artcache.New[string](opts.ArtworkCacheBytes, artworkTTL, opts.Now),
		shows:    newShowCache(opts.Now),
		searches: newSearchCache(opts.Now),
		auths:    map[string]*auth{},
	}
}

// defaultClient waits at most 30s for response headers but puts no limit
// on bodies, which may be whole songs.
func defaultClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: t}
}

// Info implements provider.Provider.
func (p *Provider) Info() provider.Info {
	return provider.Info{
		ID:   ID,
		Name: "nugs.net",
		Icon: "nugs",
		Capabilities: provider.Capabilities{
			Playback: provider.PlaybackStream,
			Search:   []provider.EntityKind{provider.KindTrack, provider.KindAlbum, provider.KindArtist},
			Artwork:  true,
			// Live recordings have no ISRCs, and no lyrics or similar-song
			// data to speak of.
			// A personal subscription.
			Shareable: false,
		},
	}
}

// Linker implements provider.Provider.
func (p *Provider) Linker() provider.Linker { return linker{p} }

// creds is the stored credential format.
type creds struct {
	Email        string `json:"email"`
	RefreshToken string `json:"refreshToken"`
	// UserID is the account's ID, which stream requests carry.
	UserID string `json:"userId"`
}

type linker struct{ p *Provider }

func (linker) Method() provider.LinkMethod { return provider.LinkCredentials }

func (linker) Fields() []provider.LinkField {
	return []provider.LinkField{
		{Name: "email", Label: "Email", Kind: provider.FieldText, Required: true, Placeholder: "you@example.com"},
		{
			Name: "password", Label: "Password", Kind: provider.FieldSecret, Required: true,
			Help: "Your nugs.net login. It's used once to sign in; Syncphony keeps a sign-in token, not your password. Playing needs an active subscription.",
		},
	}
}

func (linker) BeginOAuth(context.Context, provider.OAuthRequest) (provider.OAuthStart, error) {
	return provider.OAuthStart{}, provider.ErrUnsupported
}

// Complete signs in, and checks the account has a subscription to play with.
func (l linker) Complete(ctx context.Context, in provider.LinkInput) (provider.Credentials, provider.AccountInfo, error) {
	email := strings.TrimSpace(in.Fields["email"])
	password := in.Fields["password"]
	if email == "" || password == "" {
		return nil, provider.AccountInfo{}, fmt.Errorf("%w: email and password are required", provider.ErrInvalidCredentials)
	}
	tok, err := l.p.signIn(ctx, email, password)
	if err != nil {
		return nil, provider.AccountInfo{}, err
	}
	userID, err := l.p.userID(ctx, tok.AccessToken)
	if err != nil {
		return nil, provider.AccountInfo{}, err
	}
	sub, err := l.p.subscription(ctx, tok.AccessToken)
	if err != nil {
		return nil, provider.AccountInfo{}, err
	}
	if !sub.active(l.p.now()) {
		return nil, provider.AccountInfo{}, fmt.Errorf("%w: this nugs.net account has no active subscription, which playing needs", provider.ErrInvalidCredentials)
	}
	c := creds{Email: email, RefreshToken: tok.RefreshToken, UserID: userID}
	b, err := json.Marshal(c) //nolint:gosec // credentials are only ever stored encrypted, by the vault
	if err != nil {
		return nil, provider.AccountInfo{}, err
	}
	return b, provider.AccountInfo{ID: userID, Name: email}, nil
}

// Open implements provider.Provider. It doesn't contact nugs.net.
func (p *Provider) Open(_ context.Context, link provider.Link) (provider.Session, error) {
	var c creds
	if err := json.Unmarshal(link.Credentials, &c); err != nil || c.RefreshToken == "" || c.UserID == "" {
		return nil, fmt.Errorf("%w: malformed credentials", provider.ErrAuthExpired)
	}
	return &session{p: p, link: link, auth: p.authFor(link, c)}, nil
}

// authFor returns the link's shared tokens, starting over if the link's
// credentials are new to them (the account was linked again).
func (p *Provider) authFor(link provider.Link, c creds) *auth {
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.auths[link.ID]
	if a == nil || !a.knows(c.RefreshToken) {
		a = &auth{p: p, creds: c}
		p.auths[link.ID] = a
	}
	if link.Sink != nil {
		a.setSink(link.Sink)
	}
	return a
}

// errNoSubscription means the account can't play anything.
var errNoSubscription = fmt.Errorf("nugs: this account has no active subscription: %w", provider.ErrNotPlayable)
