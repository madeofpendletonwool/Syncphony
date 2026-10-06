// SPDX-License-Identifier: AGPL-3.0-only

// Package fake is an in-memory provider for tests and UI development. It has
// a small fixed library of sine-tone tracks, links with fixed credentials, a
// pretend OAuth2 flow or a pretend device pairing, and can play as either a Stream or a Remote
// provider. Tests can revoke links and inject faults.
package fake

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// What the fake linker accepts.
const (
	Username = "demo"
	Password = "demo"
	// Code is the OAuth2 authorization code Complete accepts.
	Code = "approved"
)

// Options configure a fake provider. The zero value is a Stream provider
// with ID "fake" that links with Username and Password.
type Options struct {
	ID       string // default "fake"
	Name     string // default "Fake"
	Playback provider.PlaybackMode
	Link     provider.LinkMethod
	// Private makes links unshareable, like a personal subscription.
	Private bool
	// Pair makes an OAuth2 linker pair a device first, as Spotify's does.
	// LinkDevice linkers always pair.
	Pair bool
	// NotPlayable lists track IDs the fake refuses to play, as Spotify does
	// some tracks: CheckPlayable and Stream return ErrNotPlayable.
	NotPlayable []string
	// Now is the clock, for token expiry and remote playback position.
	Now func() time.Time
	// TokenTTL is how long OAuth2 access tokens last before the session
	// refreshes them and calls the link's CredentialSink. Default 1h.
	TokenTTL time.Duration
}

// Provider is the fake provider.
type Provider struct {
	opts Options

	mu    sync.Mutex
	gen   map[string]int // per account; credentials from older generations are revoked
	fault error
	seq   int
	polls map[string]int // pairing secret -> polls so far
}

var _ provider.Provider = (*Provider)(nil)

// New returns a fake provider.
func New(opts Options) *Provider {
	if opts.ID == "" {
		opts.ID = "fake"
	}
	if opts.Name == "" {
		opts.Name = "Fake"
	}
	if opts.Playback == "" {
		opts.Playback = provider.PlaybackStream
	}
	if opts.Link == "" {
		opts.Link = provider.LinkCredentials
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.TokenTTL == 0 {
		opts.TokenTTL = time.Hour
	}
	return &Provider{opts: opts, gen: map[string]int{}, polls: map[string]int{}}
}

// Info implements provider.Provider.
func (p *Provider) Info() provider.Info {
	return provider.Info{
		ID:   p.opts.ID,
		Name: p.opts.Name,
		Icon: "fake",
		Capabilities: provider.Capabilities{
			Playback:  p.opts.Playback,
			Search:    []provider.EntityKind{provider.KindTrack, provider.KindAlbum, provider.KindArtist, provider.KindPlaylist},
			Playlists: true,
			Artwork:   true,
			Lyrics:    true,
			ISRC:      true,
			Shareable: !p.opts.Private,
		},
	}
}

// Linker implements provider.Provider.
func (p *Provider) Linker() provider.Linker {
	if p.pairs() {
		return pairingLinker{linker{p}}
	}
	return linker{p}
}

func (p *Provider) pairs() bool {
	return p.opts.Link == provider.LinkDevice || (p.opts.Link == provider.LinkOAuth2 && p.opts.Pair)
}

// Revoke invalidates every credential issued so far for account, as if the
// user revoked access on the service. Sessions start returning ErrAuthExpired.
func (p *Provider) Revoke(account string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gen[account]++
}

// Fail makes every session call return err until Fail(nil), to simulate
// outages (provider.ErrUnavailable) or throttling (provider.RateLimitError).
func (p *Provider) Fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fault = err
}

// creds is the fake's credential format.
type creds struct {
	Account string    `json:"account"`
	Gen     int       `json:"gen"`
	Token   string    `json:"token"`
	Expires time.Time `json:"expires,omitzero"` // OAuth2 only
}

func (p *Provider) issue(account string) creds {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	c := creds{Account: account, Gen: p.gen[account], Token: fmt.Sprintf("token-%d", p.seq)}
	if p.opts.Link == provider.LinkOAuth2 {
		c.Expires = p.opts.Now().Add(p.opts.TokenTTL)
	}
	return c
}

// check returns the injected fault, or ErrAuthExpired if c was revoked.
func (p *Provider) check(c creds) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fault != nil {
		return p.fault
	}
	if c.Gen != p.gen[c.Account] {
		return provider.ErrAuthExpired
	}
	return nil
}

type linker struct{ p *Provider }

func (l linker) Method() provider.LinkMethod { return l.p.opts.Link }

func (l linker) Fields() []provider.LinkField {
	if l.p.opts.Link != provider.LinkCredentials {
		return nil
	}
	return []provider.LinkField{
		{Name: "username", Label: "Username", Kind: provider.FieldText, Required: true, Placeholder: Username},
		{Name: "password", Label: "Password", Kind: provider.FieldSecret, Required: true, Help: "It's " + Password + "."},
	}
}

func (l linker) BeginOAuth(_ context.Context, req provider.OAuthRequest) (provider.OAuthStart, error) {
	if l.p.opts.Link != provider.LinkOAuth2 {
		return provider.OAuthStart{}, provider.ErrUnsupported
	}
	verifier := rand.Text()
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {"syncphony"},
		"redirect_uri":          {req.RedirectURL},
		"state":                 {req.State},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	return provider.OAuthStart{AuthURL: "https://fake.invalid/authorize?" + q.Encode(), Secret: verifier}, nil
}

func (l linker) Complete(_ context.Context, in provider.LinkInput) (provider.Credentials, provider.AccountInfo, error) {
	switch l.p.opts.Link {
	case provider.LinkCredentials:
		if in.Fields["username"] != Username || in.Fields["password"] != Password {
			return nil, provider.AccountInfo{}, provider.ErrInvalidCredentials
		}
	case provider.LinkOAuth2:
		if in.Code != Code || in.OAuthSecret == "" {
			return nil, provider.AccountInfo{}, provider.ErrInvalidCredentials
		}
	}
	if l.p.pairs() && !strings.HasPrefix(in.Paired, pairedPrefix) {
		return nil, provider.AccountInfo{}, provider.ErrInvalidCredentials
	}
	b, err := json.Marshal(l.p.issue(Username))
	if err != nil {
		return nil, provider.AccountInfo{}, err
	}
	return b, provider.AccountInfo{ID: Username, Name: Username + " (" + l.p.opts.Name + ")"}, nil
}

// pairingLinker is a linker that pairs a device. Pairings are approved on
// their second poll.
type pairingLinker struct{ linker }

const pairedPrefix = "paired:"

func (l pairingLinker) BeginPairing(context.Context) (provider.Pairing, error) {
	p := l.p
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	secret := fmt.Sprintf("pairing-%d", p.seq)
	p.polls[secret] = 0
	code := fmt.Sprintf("FAKE%04d", p.seq)
	return provider.Pairing{
		VerifyURL: "https://fake.invalid/pair?code=" + code,
		UserCode:  code,
		Interval:  time.Second,
		ExpiresIn: 10 * time.Minute,
		Secret:    secret,
	}, nil
}

func (l pairingLinker) PollPairing(_ context.Context, secret string) (string, error) {
	p := l.p
	p.mu.Lock()
	defer p.mu.Unlock()
	n, ok := p.polls[secret]
	if !ok {
		return "", provider.ErrInvalidCredentials
	}
	if n == 0 {
		p.polls[secret] = 1
		return "", provider.ErrPending
	}
	delete(p.polls, secret)
	return pairedPrefix + secret, nil
}

// Open implements provider.Provider.
func (p *Provider) Open(_ context.Context, link provider.Link) (provider.Session, error) {
	var c creds
	if err := json.Unmarshal(link.Credentials, &c); err != nil || c.Token == "" {
		return nil, fmt.Errorf("%w: malformed credentials", provider.ErrAuthExpired)
	}
	s := &session{p: p, link: link, creds: c}
	if p.opts.Playback == provider.PlaybackRemote {
		return &remoteSession{session: s}, nil
	}
	return &streamSession{s}, nil
}
