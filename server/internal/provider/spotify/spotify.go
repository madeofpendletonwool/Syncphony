// SPDX-License-Identifier: AGPL-3.0-only

// Package spotify is the provider for Spotify. Users link by pairing a
// device: they approve a code at spotify.com/pair (OAuth2's device code
// flow), which gives the server a login for Spotify's streaming protocol
// (see pair.go). Search, metadata and artwork come from the Web API, called
// with the server's developer app's own token, so users never sign into the
// app. Playlists come from the streaming protocol through a Library, since
// the Web API won't list them without a user signed into the app. Audio
// comes from the streaming protocol through an Audio backend,
// decrypted on the server and proxied to the player like any other stream,
// so a track plays through the account of whoever queued it. That needs a
// Premium account.
package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// ID is the provider ID stored in links and track refs.
const ID = "spotify"

// Default service endpoints. Options can point them elsewhere for tests.
const (
	defaultAccountsURL = "https://accounts.spotify.com"
	defaultAPIURL      = "https://api.spotify.com/v1"
	defaultImageURL    = "https://i.scdn.co/image"
)

// Options configure the provider.
type Options struct {
	// ClientID is the Spotify developer app's client ID. Required.
	ClientID string
	// ClientSecret is the app's client secret. Required: the Web API is
	// called with the app's own token (the client credentials grant).
	ClientSecret string
	// Audio fetches audio over Spotify's streaming protocol. Required.
	Audio Audio
	// Library lists accounts' playlists. Without it, Spotify has none.
	Library Library
	// Client makes Web API and accounts requests.
	Client *http.Client
	// AccountsURL, APIURL and ImageURL override the service endpoints, for
	// tests. ImageURL is also the only place artwork is fetched from.
	AccountsURL, APIURL, ImageURL string
	// Now is the clock, for token expiry.
	Now func() time.Time
}

// Provider is the Spotify provider.
type Provider struct {
	clientID, clientSecret string
	audio                  Audio
	library                Library
	client                 *http.Client
	accountsURL, apiURL    string
	imageURL               string
	now                    func() time.Time
	app                    appTokens
}

var _ provider.Provider = (*Provider)(nil)

// New returns a Spotify provider.
func New(opts Options) (*Provider, error) {
	if opts.ClientID == "" || opts.ClientSecret == "" {
		return nil, errors.New("spotify: a client ID and secret are required")
	}
	if opts.Audio == nil {
		return nil, errors.New("spotify: an audio backend is required")
	}
	if opts.Client == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.ResponseHeaderTimeout = 30 * time.Second
		opts.Client = &http.Client{Transport: t}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	trim := func(u, def string) string {
		if u == "" {
			u = def
		}
		return strings.TrimRight(u, "/")
	}
	return &Provider{
		clientID:     opts.ClientID,
		clientSecret: opts.ClientSecret,
		audio:        opts.Audio,
		library:      opts.Library,
		client:       opts.Client,
		accountsURL:  trim(opts.AccountsURL, defaultAccountsURL),
		apiURL:       trim(opts.APIURL, defaultAPIURL),
		imageURL:     trim(opts.ImageURL, defaultImageURL),
		now:          opts.Now,
	}, nil
}

// Info implements provider.Provider.
func (p *Provider) Info() provider.Info {
	search := []provider.EntityKind{provider.KindTrack, provider.KindAlbum, provider.KindArtist}
	if p.library != nil {
		// Everyone's public playlists, read through the library.
		search = append(search, provider.KindPlaylist)
	}
	return provider.Info{
		ID:   ID,
		Name: "Spotify",
		Icon: "spotify",
		Capabilities: provider.Capabilities{
			Playback: provider.PlaybackStream,
			Search:   search,
			// Listed over the streaming protocol (library.go): the Web API
			// only lists playlists to a user signed into the app.
			Playlists: p.library != nil,
			Artwork:   true,
			// The Web API stopped returning external_ids (ISRCs) to
			// development-mode apps in February 2026.
			ISRC: false,
		},
	}
}

// Linker implements provider.Provider.
func (p *Provider) Linker() provider.Linker { return linker{p} }

// creds is the stored credential format: the streaming login from the
// device pairing.
type creds struct {
	StreamUser  string `json:"stream_user"`
	StreamCreds []byte `json:"stream_creds"`
}

// Open implements provider.Provider. It doesn't contact Spotify.
func (p *Provider) Open(_ context.Context, link provider.Link) (provider.Session, error) {
	var c creds
	if err := json.Unmarshal(link.Credentials, &c); err != nil || c.StreamUser == "" || len(c.StreamCreds) == 0 {
		return nil, fmt.Errorf("%w: malformed credentials", provider.ErrAuthExpired)
	}
	s := &session{p: p, link: link, creds: c}
	if p.library != nil {
		return playlistSession{s}, nil
	}
	return s, nil
}
