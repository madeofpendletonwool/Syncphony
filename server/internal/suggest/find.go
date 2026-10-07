// SPDX-License-Identifier: AGPL-3.0-only

package suggest

import (
	"context"
	"log/slog"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/dj"
	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Links opens sessions for links and looks providers up. It's links.Service.
type Links interface {
	Open(ctx context.Context, linkID string) (provider.Session, error)
	Provider(id string) (provider.Provider, error)
}

// How many songs to ask a service for, and how many of an artist's top
// songs go with a similar song's.
const (
	Candidates = 40
	SimilarTop = 5
)

// Seed is a song to look for more like.
type Seed struct {
	Item  store.QueueItem
	Track provider.Track
}

// SeedOf returns it as a seed.
func SeedOf(it store.QueueItem) Seed { return Seed{Item: it, Track: dj.TrackOf(it)} }

// Keys are the ways a song is recognized: on its service, by ISRC on any
// service, and by title and artist.
func Keys(t provider.Track) []string {
	out := []string{t.Ref.Provider + "\x00" + t.Ref.ID}
	if t.ISRC != "" {
		out = append(out, "isrc\x00"+t.ISRC)
	}
	if a := ArtistKey(t); a != "" && t.Title != "" {
		out = append(out, "name\x00"+a+"\x00"+strings.ToLower(strings.TrimSpace(t.Title)))
	}
	return out
}

// ArtistKey is a song's first artist, compared as the DJ does
// (dj.ArtistKey).
func ArtistKey(t provider.Track) string {
	if len(t.Artists) == 0 {
		return ""
	}
	return dj.ArtistKey(t.Artists[0].Name)
}

// Seen is a set of songs, by Keys.
type Seen map[string]bool

// Item adds a queued song, and the stand-in it played through, if any.
func (s Seen) Item(it store.QueueItem) {
	t := dj.TrackOf(it)
	t.Ref = provider.TrackRef{Provider: it.Provider, ID: it.TrackID}
	s.Track(t)
	if it.ViaLinkID.Valid {
		s[it.ViaProvider.String+"\x00"+it.ViaTrackID.String] = true
	}
}

// Track adds a song.
func (s Seen) Track(t provider.Track) {
	for _, k := range Keys(t) {
		s[k] = true
	}
}

// Has reports whether t is in the set, by any of its keys.
func (s Seen) Has(t provider.Track) bool {
	for _, k := range Keys(t) {
		if s[k] {
			return true
		}
	}
	return false
}

// Source is where an item played from: its stand-in, or its own link.
func Source(it store.QueueItem) (linkID, trackID string) {
	if it.ViaLinkID.Valid {
		return it.ViaLinkID.String, it.ViaTrackID.String
	}
	return it.LinkID.String, it.TrackID
}

// Finder looks for songs like seeds on linked services. It keeps the
// sessions it opens until Close.
type Finder struct {
	links Links
	// rand returns a number in [0, n).
	rand     func(n int) int
	sessions map[string]provider.Session
}

// NewFinder returns a Finder. Call Close when done.
func NewFinder(links Links, rand func(n int) int) *Finder {
	return &Finder{links: links, rand: rand, sessions: map[string]provider.Session{}}
}

// Close closes the sessions the Finder opened.
func (f *Finder) Close() {
	for _, sess := range f.sessions {
		sess.Close()
	}
}

// Open returns a session for a link, opening it the first time.
func (f *Finder) Open(ctx context.Context, linkID string) (provider.Session, bool) {
	if sess, ok := f.sessions[linkID]; ok {
		return sess, true
	}
	sess, err := f.links.Open(ctx, linkID)
	if err != nil {
		slog.Debug("suggest: opening a link", "link", linkID, "err", err)
		return nil, false
	}
	f.sessions[linkID] = sess
	return sess, true
}

// Recommender returns a link's session and its recommendations.
func (f *Finder) Recommender(ctx context.Context, linkID string) (provider.Recommender, provider.Session, bool) {
	sess, ok := f.Open(ctx, linkID)
	if !ok {
		return nil, nil, false
	}
	r, ok := sess.(provider.Recommender)
	return r, sess, ok
}

// Like asks one service for songs like sd's, most alike first: similar
// songs from one that recommends, else more by sd's artist.
func (f *Finder) Like(ctx context.Context, l store.ServiceLink, sd Seed, recommends bool) []provider.Track {
	if !recommends {
		sess, ok := f.Open(ctx, l.ID)
		if !ok {
			return nil
		}
		return f.ByArtist(ctx, sess, l, sd)
	}
	rec, sess, ok := f.Recommender(ctx, l.ID)
	if !ok {
		return nil
	}
	return f.Similar(ctx, rec, sess, l, sd, rooms.AdventureSimilar)
}

// Similar asks one service for songs like sd's, most alike first. With
// rooms.AdventureDiscovery, they're by artists like sd's.
func (f *Finder) Similar(ctx context.Context, rec provider.Recommender, sess provider.Session, l store.ServiceLink, sd Seed, adventure string) []provider.Track {
	trackID, artistID := f.Resolve(ctx, sess, l, sd)
	var artist string
	if len(sd.Track.Artists) > 0 {
		artist = sd.Track.Artists[0].Name
	}
	var out []provider.Track
	add := func(what string, ts []provider.Track, err error) {
		if err != nil {
			slog.Debug("suggest: asking for songs", "link", l.ID, "call", what, "err", err)
		}
		out = append(out, ts...)
	}
	switch adventure {
	case rooms.AdventureDiscovery:
		if artistID != "" {
			ts, err := rec.SimilarToArtist(ctx, artistID, Candidates)
			add("SimilarToArtist", ts, err)
		} else if trackID != "" {
			ts, err := rec.SimilarToTrack(ctx, trackID, Candidates)
			add("SimilarToTrack", ts, err)
		}
	default:
		if trackID != "" {
			ts, err := rec.SimilarToTrack(ctx, trackID, Candidates)
			add("SimilarToTrack", ts, err)
		} else if artistID != "" {
			ts, err := rec.SimilarToArtist(ctx, artistID, Candidates)
			add("SimilarToArtist", ts, err)
		}
		if artist != "" {
			ts, err := rec.TopTracks(ctx, artist, SimilarTop*2)
			add("TopTracks", ts, err)
		}
		if len(out) == 0 && artistID != "" {
			// The service knows nothing similar (Navidrome without
			// Last.fm, say). More by the same artist is still like it.
			ts, err := f.catalog(ctx, sess, artistID)
			add("Artist", ts, err)
		}
	}
	return out
}

// ByArtist searches a service that can't recommend for more songs by sd's
// artist: what's like a song, on a service that only searches.
func (f *Finder) ByArtist(ctx context.Context, sess provider.Session, l store.ServiceLink, sd Seed) []provider.Track {
	want := ArtistKey(sd.Track)
	if want == "" {
		return nil
	}
	page, err := sess.Search(ctx, provider.SearchQuery{Text: sd.Track.Artists[0].Name, Kinds: []provider.EntityKind{provider.KindTrack}, Limit: Candidates})
	if err != nil {
		slog.Debug("suggest: searching for an artist's songs", "link", l.ID, "err", err)
		return nil
	}
	var out []provider.Track
	for _, t := range page.Tracks {
		if ArtistKey(t) == want {
			out = append(out, t)
		}
	}
	return out
}

// Resolve finds sd's track and artist on a link: directly if it played
// from there, else by searching.
func (f *Finder) Resolve(ctx context.Context, sess provider.Session, l store.ServiceLink, sd Seed) (trackID, artistID string) {
	if from, id := Source(sd.Item); from == l.ID {
		trackID = id
		if !sd.Item.ViaLinkID.Valid && len(sd.Track.Artists) > 0 {
			artistID = sd.Track.Artists[0].ID
		}
	} else if t, score, err := match.On(ctx, sess, sd.Track); err == nil && score > 0 {
		trackID = t.Ref.ID
		if len(t.Artists) > 0 {
			artistID = t.Artists[0].ID
		}
	}
	if artistID != "" || len(sd.Track.Artists) == 0 {
		return trackID, artistID
	}
	name := sd.Track.Artists[0].Name
	if p, err := f.links.Provider(l.Provider); err != nil || !p.Info().Capabilities.CanSearch(provider.KindArtist) {
		return trackID, ""
	}
	page, err := sess.Search(ctx, provider.SearchQuery{Text: name, Kinds: []provider.EntityKind{provider.KindArtist}, Limit: 5})
	if err != nil {
		return trackID, ""
	}
	for _, a := range page.Artists {
		if strings.EqualFold(strings.TrimSpace(a.Name), strings.TrimSpace(name)) {
			return trackID, a.ID
		}
	}
	return trackID, ""
}

// catalog returns the songs of one of an artist's albums, picked at random.
func (f *Finder) catalog(ctx context.Context, sess provider.Session, artistID string) ([]provider.Track, error) {
	_, albums, err := sess.Artist(ctx, artistID)
	if err != nil || len(albums) == 0 {
		return nil, err
	}
	_, ts, err := sess.Album(ctx, albums[f.rand(len(albums))].ID)
	return ts, err
}
