// SPDX-License-Identifier: AGPL-3.0-only

// Package navidrome is the provider for Navidrome, and other servers that
// speak the Subsonic API. Users link with their server URL, username and
// password; requests use Subsonic token auth, so the password itself is
// never sent. Tracks are streamed through the server, in their original
// format when the player can decode it and transcoded by Navidrome when not.
package navidrome

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/artcache"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// ID is the provider ID stored in links and track refs.
const ID = "navidrome"

const (
	// artworkTTL is how long a cached image is served before it's fetched
	// again, so changed cover art shows up eventually.
	artworkTTL = 24 * time.Hour
	// maxArtwork is the largest image we'll fetch or cache.
	maxArtwork = 8 << 20
)

// artKey names a cached image.
type artKey struct {
	// account keeps users' caches apart: Navidrome libraries can be
	// per-user, so one user's art isn't necessarily visible to another.
	account string
	ref     string
	size    int
}

// Options configure the provider. The zero value is ready to use.
type Options struct {
	// Client makes requests to Navidrome servers. The default refuses
	// redirects to other hosts, since every request carries an auth token.
	Client *http.Client
	// ArtworkCacheBytes bounds the artwork cache, shared by every session.
	// Default 64 MiB; negative disables the cache.
	ArtworkCacheBytes int64
	// Now is the clock, for artwork cache expiry.
	Now func() time.Time
}

// Provider is the Navidrome provider.
type Provider struct {
	client *http.Client
	art    *artcache.Cache[artKey]
}

var _ provider.Provider = (*Provider)(nil)

// New returns a Navidrome provider.
func New(opts Options) *Provider {
	if opts.Client == nil {
		opts.Client = defaultClient()
	}
	if opts.ArtworkCacheBytes == 0 {
		opts.ArtworkCacheBytes = 64 << 20
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Provider{client: opts.Client, art: artcache.New[artKey](opts.ArtworkCacheBytes, artworkTTL, opts.Now)}
}

// defaultClient waits at most 30s for response headers but puts no limit
// on bodies, which may be whole songs.
func defaultClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: t, CheckRedirect: sameHostRedirect}
}

// sameHostRedirect follows redirects (http to https, a trailing slash)
// within one host, but not to another host, which would receive the token.
func sameHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("stopped after %d redirects", len(via))
	}
	if !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
		return fmt.Errorf("refusing redirect to another host (%s)", req.URL.Hostname())
	}
	return nil
}

// Info implements provider.Provider.
func (p *Provider) Info() provider.Info {
	return provider.Info{
		ID:   ID,
		Name: "Navidrome",
		Icon: "navidrome",
		Capabilities: provider.Capabilities{
			Playback: provider.PlaybackStream,
			Search:   []provider.EntityKind{provider.KindTrack, provider.KindAlbum, provider.KindArtist},
			Artwork:  true,
			// From embedded tags and .lrc sidecar files.
			Lyrics: true,
			// OpenSubsonic servers (Navidrome 0.53+) report ISRCs from tags.
			ISRC: true,
			// It's the owner's own server; they decide who listens.
			Shareable: true,
			// From Navidrome's metadata agents (Last.fm),
			// when the server has them set up. Random songs need nothing.
			Recommendations: true,
		},
	}
}

// Linker implements provider.Provider.
func (p *Provider) Linker() provider.Linker { return linker{p} }

// creds is the stored credential format. The password is kept, not a
// token, because token auth needs a fresh salt for every request.
type creds struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type linker struct{ p *Provider }

func (linker) Method() provider.LinkMethod { return provider.LinkCredentials }

func (linker) Fields() []provider.LinkField {
	return []provider.LinkField{
		{Name: "url", Label: "Server URL", Kind: provider.FieldURL, Required: true, Placeholder: "https://music.example.com", Help: "The address you open Navidrome at."},
		{Name: "username", Label: "Username", Kind: provider.FieldText, Required: true},
		{Name: "password", Label: "Password", Kind: provider.FieldSecret, Required: true},
	}
}

func (linker) BeginOAuth(context.Context, provider.OAuthRequest) (provider.OAuthStart, error) {
	return provider.OAuthStart{}, provider.ErrUnsupported
}

// Complete checks the credentials with a ping.
func (l linker) Complete(ctx context.Context, in provider.LinkInput) (provider.Credentials, provider.AccountInfo, error) {
	base, err := normalizeURL(in.Fields["url"])
	if err != nil {
		return nil, provider.AccountInfo{}, err
	}
	c := creds{URL: base, Username: in.Fields["username"], Password: in.Fields["password"]}
	if c.Username == "" || c.Password == "" {
		return nil, provider.AccountInfo{}, fmt.Errorf("%w: username and password are required", provider.ErrInvalidCredentials)
	}
	if err := l.p.connect(c).ping(ctx); err != nil {
		if errors.Is(err, provider.ErrAuthExpired) {
			return nil, provider.AccountInfo{}, fmt.Errorf("%w: %w", provider.ErrInvalidCredentials, err)
		}
		return nil, provider.AccountInfo{}, err
	}
	b, err := json.Marshal(c) //nolint:gosec // credentials are only ever stored encrypted, by the vault
	if err != nil {
		return nil, provider.AccountInfo{}, err
	}
	where := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	return b, provider.AccountInfo{ID: c.Username + "@" + where, Name: c.Username + " on " + where}, nil
}

// normalizeURL turns what a user pasted (perhaps the web UI's address, like
// https://music.example.com/app/#/album/x) into the server's base URL.
func normalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("%w: server URL must be an http(s) URL", provider.ErrInvalidCredentials)
	}
	path := strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/app", "/rest"} {
		path = strings.TrimSuffix(path, suffix)
	}
	u = &url.URL{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(u.Host), Path: path}
	return u.String(), nil
}

// Open implements provider.Provider. It doesn't contact the server.
func (p *Provider) Open(_ context.Context, link provider.Link) (provider.Session, error) {
	var c creds
	if err := json.Unmarshal(link.Credentials, &c); err != nil || c.URL == "" || c.Username == "" {
		return nil, fmt.Errorf("%w: malformed credentials", provider.ErrAuthExpired)
	}
	return &session{p: p, link: link, api: p.connect(c)}, nil
}
