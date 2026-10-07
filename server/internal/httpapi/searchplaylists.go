// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Searching finds a link's own playlists by name: services' search APIs
// don't cover a library's playlists (Subsonic's doesn't search playlists
// at all), so the link's list is fetched and matched here.

// playlistsTTL is how long a link's playlists are reused for searching.
// A search is asked again every few letters typed, and listing Spotify's
// takes several requests.
const playlistsTTL = time.Minute

// playlistCache holds links' playlists for searching. The zero value is
// ready to use.
type playlistCache struct {
	// now is the clock; nil means time.Now.
	now func() time.Time

	mu    sync.Mutex
	lists map[string]cachedPlaylists // by link ID
}

type cachedPlaylists struct {
	at    time.Time
	items []provider.Playlist
}

// list returns a link's playlists: its first page, which for the services
// with playlists is all of them.
func (c *playlistCache) list(ctx context.Context, linkID string, pl provider.PlaylistLister) ([]provider.Playlist, error) {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	c.mu.Lock()
	e, ok := c.lists[linkID]
	c.mu.Unlock()
	if ok && now().Sub(e.at) < playlistsTTL {
		return e.items, nil
	}
	page, err := pl.Playlists(ctx, "")
	if err != nil {
		return nil, err
	}
	at := now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lists == nil {
		c.lists = map[string]cachedPlaylists{}
	}
	for id, e := range c.lists {
		if at.Sub(e.at) >= playlistsTTL {
			delete(c.lists, id)
		}
	}
	c.lists[linkID] = cachedPlaylists{at: at, items: page.Items}
	return page.Items, nil
}

// matchPlaylists returns up to limit of lists whose names hold every word
// of text, in their order. Words match the start of a word in the name,
// ignoring case, accents and punctuation, so "chill v" finds "Chill Vibes"
// and so does "vibes chill".
func matchPlaylists(lists []provider.Playlist, text string, limit int) []provider.Playlist {
	words := strings.Fields(match.Simplify(text))
	if len(words) == 0 {
		return nil
	}
	var out []provider.Playlist
	for _, pl := range lists {
		if len(out) == limit {
			break
		}
		if startsWords(strings.Fields(match.Simplify(pl.Name)), words) {
			out = append(out, pl)
		}
	}
	return out
}

// startsWords reports whether each of words starts one of name's.
func startsWords(name, words []string) bool {
	for _, w := range words {
		if !slices.ContainsFunc(name, func(n string) bool { return strings.HasPrefix(n, w) }) {
			return false
		}
	}
	return true
}
