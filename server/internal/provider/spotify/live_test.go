// SPDX-License-Identifier: AGPL-3.0-only

package spotify_test

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify/streaming"
)

// TestLive runs the provider against real Spotify: search with the app's
// token, then check and stream songs through a paired account. It needs
// SYNCPHONY_SPOTIFY_CLIENT_ID and _SECRET, and a pairing's streaming login
// saved as JSON with "username" and "stored_credentials" (base64) in the
// file named by SPOTIFY_LIVE_STATE.
func TestLive(t *testing.T) {
	id, secret, path := os.Getenv("SYNCPHONY_SPOTIFY_CLIENT_ID"), os.Getenv("SYNCPHONY_SPOTIFY_CLIENT_SECRET"), os.Getenv("SPOTIFY_LIVE_STATE")
	if id == "" || secret == "" || path == "" {
		t.Skip("SYNCPHONY_SPOTIFY_CLIENT_ID, SYNCPHONY_SPOTIFY_CLIENT_SECRET and SPOTIFY_LIVE_STATE not all set")
	}
	b, err := os.ReadFile(path) //nolint:gosec // a test fixture the developer names
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Username          string `json:"username"`
		StoredCredentials []byte `json:"stored_credentials"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	paired, _ := json.Marshal(map[string]any{"username": st.Username, "stored": st.StoredCredentials})

	audio := streaming.New(nil)
	defer audio.Close()
	p, err := spotify.New(spotify.Options{ClientID: id, ClientSecret: secret, Audio: audio, Library: audio})
	if err != nil {
		t.Fatal(err)
	}
	creds, account, err := p.Linker().Complete(t.Context(), provider.LinkInput{Paired: string(paired)})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := p.Open(t.Context(), provider.Link{ID: "live", Account: account, Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	page, err := sess.Search(t.Context(), provider.SearchQuery{Text: "Shape of You Ed Sheeran", Kinds: []provider.EntityKind{provider.KindTrack}})
	if err != nil || len(page.Tracks) == 0 {
		t.Fatalf("Search: %d tracks, %v", len(page.Tracks), err)
	}
	t.Logf("search found %q by %v (%s)", page.Tracks[0].Title, page.Tracks[0].Artists, page.Tracks[0].Ref.ID)

	const playable, refused = "7qiZfU4dY1lWllzX7mPBI3", "4uLU6hMCjMI75M1A2tKUQC" // Shape of You; Never Gonna Give You Up
	pc := sess.(provider.PlayChecker)
	if err := pc.CheckPlayable(t.Context(), playable); err != nil {
		t.Errorf("CheckPlayable(%s): %v", playable, err)
	}
	if err := pc.CheckPlayable(t.Context(), refused); !errors.Is(err, provider.ErrNotPlayable) {
		t.Errorf("CheckPlayable(%s): %v, want ErrNotPlayable", refused, err)
	}
	a, err := sess.(provider.Streamer).Stream(t.Context(), playable, provider.StreamOpts{Range: &provider.ByteRange{Start: 0, End: 3}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer a.Body.Close()
	head, err := io.ReadAll(a.Body)
	if err != nil || string(head) != "OggS" {
		t.Errorf("stream starts %q (%v), want an Ogg page", head, err)
	}
	t.Logf("streamed %s: %s, %d bytes", playable, a.ContentType, a.Size)

	pl := sess.(provider.PlaylistLister)
	lists, err := pl.Playlists(t.Context(), "")
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	t.Logf("%d playlists", len(lists.Items))
	for i, l := range lists.Items[:min(12, len(lists.Items))] {
		t.Logf("  %d. %s (%d tracks, owner %s)", i+1, l.ID, l.TrackCount, l.Owner)
	}
	// Liked Songs leads, newest first.
	if len(lists.Items) > 0 && lists.Items[0].ID == spotify.LikedSongsID {
		tp, err := pl.PlaylistTracks(t.Context(), spotify.LikedSongsID, "")
		if err != nil || len(tp.Items) == 0 {
			t.Errorf("Liked Songs: %d tracks, %v", len(tp.Items), err)
		} else {
			t.Logf("Liked Songs: %d of %d on the first page, next %q", len(tp.Items), lists.Items[0].TrackCount, tp.Next)
		}
	} else {
		t.Error("Liked Songs isn't listed first")
	}
	withArt := 0
	for _, l := range lists.Items {
		if l.Artwork == "" {
			continue
		}
		withArt++
		if withArt > 4 {
			continue
		}
		img, ct, err := sess.Artwork(t.Context(), l.Artwork, 300)
		if err != nil {
			t.Errorf("artwork of playlist %s (%s): %v", l.ID, l.Artwork, err)
			continue
		}
		img.Close()
		t.Logf("playlist %s artwork: %s", l.ID, ct)
	}
	t.Logf("%d playlists have artwork", withArt)
	// The biggest playlist, to page through it.
	var big provider.Playlist
	for _, l := range lists.Items {
		if l.TrackCount > big.TrackCount {
			big = l
		}
	}
	if big.ID == "" {
		return
	}
	n, pages := 0, 0
	for cursor := ""; pages < 20; pages++ {
		tp, err := pl.PlaylistTracks(t.Context(), big.ID, cursor)
		if err != nil {
			t.Fatalf("PlaylistTracks(%s, %q): %v", big.ID, cursor, err)
		}
		for _, tr := range tp.Items {
			if tr.Title == "" || len(tr.Artists) == 0 || tr.Duration <= 0 || tr.Artwork == "" {
				t.Errorf("playlist track %+v", tr)
			}
		}
		if pages == 0 && len(tp.Items) > 0 {
			img, ct, err := sess.Artwork(t.Context(), tp.Items[0].Artwork, 300)
			if err != nil {
				t.Errorf("artwork of track %s: %v", tp.Items[0].Ref.ID, err)
			} else {
				img.Close()
				t.Logf("track artwork: %s", ct)
			}
		}
		n += len(tp.Items)
		if cursor = tp.Next; cursor == "" {
			break
		}
	}
	t.Logf("biggest playlist: %d tracks listed of %d items, in %d pages", n, big.TrackCount, pages+1)
	if n == 0 {
		t.Error("biggest playlist listed no tracks")
	}
}
