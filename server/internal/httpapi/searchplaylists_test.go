// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

func TestMatchPlaylists(t *testing.T) {
	lists := []provider.Playlist{
		{ID: "1", Name: "Chill Vibes"},
		{ID: "2", Name: "Beyoncé & Friends"},
		{ID: "3", Name: "Road-Trip '24"},
		{ID: "4", Name: "Chillwave"},
	}
	for text, want := range map[string][]string{
		"chill":          {"1", "4"},
		"CHILL v":        {"1"},
		"vibes chill":    {"1"},
		"beyonce":        {"2"},
		"beyoncé friend": {"2"},
		"road trip":      {"3"},
		"24":             {"3"},
		"ill":            nil, // words match from their start
		"chill jazz":     nil, // every word must match
		"  !! ":          nil,
	} {
		var got []string
		for _, pl := range matchPlaylists(lists, text, 10) {
			got = append(got, pl.ID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("matchPlaylists(%q) = %v, want %v", text, got, want)
		}
	}
	if got := matchPlaylists(lists, "chill", 1); len(got) != 1 || got[0].ID != "1" {
		t.Errorf("limit 1: %+v", got)
	}
}

// countingLister lists one playlist, counting how often it's asked.
type countingLister struct {
	calls int
	err   error
}

func (c *countingLister) Playlists(context.Context, string) (provider.Page[provider.Playlist], error) {
	c.calls++
	return provider.Page[provider.Playlist]{Items: []provider.Playlist{{ID: "1", Name: "Mix"}}}, c.err
}

func (c *countingLister) PlaylistTracks(context.Context, string, string) (provider.Page[provider.Track], error) {
	return provider.Page[provider.Track]{}, nil
}

func TestPlaylistCache(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	c := playlistCache{now: func() time.Time { return now }}
	a, b := &countingLister{}, &countingLister{}
	list := func(linkID string, l *countingLister) {
		t.Helper()
		if got, err := c.list(t.Context(), linkID, l); err != nil || len(got) != 1 {
			t.Fatalf("list %s: %+v, %v", linkID, got, err)
		}
	}

	list("a", a)
	list("a", a)
	list("b", b)
	if a.calls != 1 || b.calls != 1 {
		t.Fatalf("within the TTL: %d and %d listings, want one each", a.calls, b.calls)
	}
	now = now.Add(playlistsTTL)
	list("a", a)
	if a.calls != 2 {
		t.Fatalf("after the TTL: %d listings, want 2", a.calls)
	}
	if _, ok := c.lists["b"]; ok {
		t.Error("an expired list was kept")
	}

	// Failures aren't remembered.
	failing := &countingLister{err: provider.ErrUnavailable}
	for range 2 {
		if _, err := c.list(t.Context(), "c", failing); !errors.Is(err, provider.ErrUnavailable) {
			t.Fatalf("failing lister: %v", err)
		}
	}
	if failing.calls != 2 {
		t.Errorf("a failed listing was cached: %d calls", failing.calls)
	}
}
