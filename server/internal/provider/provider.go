// SPDX-License-Identifier: AGPL-3.0-only

// Package provider defines the interface every music service implements
// (Navidrome, Spotify, and future ones), the canonical types they exchange
// with the rest of the server, and the registry they are looked up in.
//
// The rest of the server only imports this package, never a specific
// provider. See docs/adr/0001-provider-interface.md for the design.
package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
)

// Provider is a music service. Each one is registered once at startup.
type Provider interface {
	// Info describes the provider. It must not change after registration.
	Info() Info
	// Linker connects a user's account to the provider.
	Linker() Linker
	// Open returns a client for one linked account. Open should be cheap; it
	// may defer contacting the service until the first call. Expired
	// credentials are reported as ErrAuthExpired, from Open or a later call.
	Open(ctx context.Context, link Link) (Session, error)
}

// Info describes a provider.
type Info struct {
	// ID is the stable identifier stored in links and track refs, e.g.
	// "navidrome". Lowercase letters, digits and dashes.
	ID string
	// Name is the display name, e.g. "Navidrome".
	Name string
	// Icon names the web app's icon for the provider, e.g. "navidrome".
	Icon string
	// Capabilities lets the core and the UI adapt to what the provider supports.
	Capabilities Capabilities
}

// Capabilities declares what a provider supports. Each optional session
// interface has a matching field here, and the providertest suite checks
// that they agree.
type Capabilities struct {
	// Playback says how tracks are played. PlaybackStream sessions
	// implement Streamer; PlaybackRemote sessions implement Remote.
	Playback PlaybackMode
	// Search lists the entity kinds Session.Search can return.
	Search []EntityKind
	// Playlists means sessions implement PlaylistLister.
	Playlists bool
	// Artwork means Session.Artwork returns images.
	Artwork bool
	// Lyrics means sessions implement Lyricist.
	Lyrics bool
	// ISRC means tracks carry ISRCs, for matching across services.
	ISRC bool
	// Shareable means a link's owner may let everyone on the server search
	// and queue from it. Say yes for libraries the owner runs (Navidrome),
	// no for personal subscriptions whose terms forbid sharing (Spotify).
	Shareable bool
}

// CanSearch reports whether Search can return entities of kind k.
func (c Capabilities) CanSearch(k EntityKind) bool { return slices.Contains(c.Search, k) }

// PlaybackMode says how a provider's tracks reach the speaker.
type PlaybackMode string

const (
	// PlaybackStream providers hand the server audio bytes, which it proxies to the
	// player (Navidrome, librespot-style Spotify).
	PlaybackStream PlaybackMode = "stream"
	// PlaybackRemote providers are told what to play and play it on their own
	// device (Spotify Connect).
	PlaybackRemote PlaybackMode = "remote"
)

// EntityKind is a kind of searchable item.
type EntityKind string

// Entity kinds.
const (
	KindTrack    EntityKind = "track"
	KindAlbum    EntityKind = "album"
	KindArtist   EntityKind = "artist"
	KindPlaylist EntityKind = "playlist"
)

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Validate reports whether the info is well formed and agrees with the linker.
func Validate(p Provider) error {
	info := p.Info()
	if !idPattern.MatchString(info.ID) {
		return fmt.Errorf("provider id %q: must match %s", info.ID, idPattern)
	}
	if info.Name == "" {
		return fmt.Errorf("provider %s: empty name", info.ID)
	}
	c := info.Capabilities
	if c.Playback != PlaybackStream && c.Playback != PlaybackRemote {
		return fmt.Errorf("provider %s: unknown playback mode %q", info.ID, c.Playback)
	}
	if !c.CanSearch(KindTrack) {
		return fmt.Errorf("provider %s: must support track search", info.ID)
	}
	for _, k := range c.Search {
		switch k {
		case KindTrack, KindAlbum, KindArtist, KindPlaylist:
		default:
			return fmt.Errorf("provider %s: unknown search kind %q", info.ID, k)
		}
	}
	l := p.Linker()
	if l == nil {
		return fmt.Errorf("provider %s: nil linker", info.ID)
	}
	switch l.Method() {
	case LinkCredentials:
		fields := l.Fields()
		if len(fields) == 0 {
			return fmt.Errorf("provider %s: credentials linker has no fields", info.ID)
		}
		seen := map[string]bool{}
		for _, f := range fields {
			if f.Name == "" || seen[f.Name] {
				return fmt.Errorf("provider %s: empty or duplicate link field %q", info.ID, f.Name)
			}
			seen[f.Name] = true
		}
	case LinkOAuth2:
	default:
		return fmt.Errorf("provider %s: unknown link method %q", info.ID, l.Method())
	}
	return nil
}
