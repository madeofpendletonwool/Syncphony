// SPDX-License-Identifier: AGPL-3.0-only

package linernotes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Notes for artist and album pages: who an artist is, from MusicBrainz
// and Wikipedia, and where an album came out. Like songs' notes, they're
// cached, misses included.

// ArtistNotes are the notes on an artist's page.
type ArtistNotes struct {
	MBID string `json:"mbid"`
	Name string `json:"name"`
	// About is a one-liner: "Group from Seattle".
	About string `json:"about,omitempty"`
	// Bio is a short summary from Wikipedia, and BioURL the article.
	Bio    string `json:"bio,omitempty"`
	BioURL string `json:"bioUrl,omitempty"`
	// Facts are where and when they began, and ended.
	Facts   []Fact   `json:"facts"`
	Members []Member `json:"members"`
	Genres  []string `json:"genres"`
	// Releases are their albums, singles and EPs, oldest first.
	Releases []ReleaseKind `json:"releases"`
}

// Fact kinds on an artist's page.
const (
	FactBegan = "began"
	FactEnded = "ended"
)

// Member is someone in a group.
type Member struct {
	Name string `json:"name"`
	// From and To are the years they were in it; 0 if unknown.
	From int `json:"from,omitempty"`
	To   int `json:"to,omitempty"`
	// Current is whether they still are.
	Current bool     `json:"current"`
	Roles   []string `json:"roles"`
}

// ReleaseKind is one of an artist's releases and what kind it is.
type ReleaseKind struct {
	Title string `json:"title"`
	// Kind is "album", "single", "ep", "compilation" or "live".
	Kind string `json:"kind"`
	Year int    `json:"year,omitempty"`
}

// AlbumNotes are the notes on an album's page.
type AlbumNotes struct {
	MBID  string `json:"mbid"`
	Title string `json:"title"`
	// Kind is "album", "single", "ep", "compilation" or "live".
	Kind string `json:"kind"`
	// FirstReleased is YYYY, YYYY-MM or YYYY-MM-DD, or empty.
	FirstReleased string   `json:"firstReleased,omitempty"`
	Labels        []string `json:"labels"`
	Genres        []string `json:"genres"`
	// About is a short summary from Wikipedia, and AboutURL the article.
	About    string `json:"about,omitempty"`
	AboutURL string `json:"aboutUrl,omitempty"`
}

// Page note kinds, as cached.
const (
	pageArtist = "artist"
	pageAlbum  = "album"
)

// Artist returns the notes for the artist called name, or
// provider.ErrNotFound if MusicBrainz doesn't know them.
func (s *Service) Artist(ctx context.Context, name string) (ArtistNotes, error) {
	var n ArtistNotes
	err := s.page(ctx, pageArtist, match.Simplify(name), &n, func(ctx context.Context) (any, bool, error) {
		return s.writeArtist(ctx, name)
	})
	return n, err
}

// Album returns the notes for an album, or provider.ErrNotFound if
// MusicBrainz doesn't know it.
func (s *Service) Album(ctx context.Context, artist, title string) (AlbumNotes, error) {
	var n AlbumNotes
	t, _ := match.Title(title)
	err := s.page(ctx, pageAlbum, match.Simplify(artist)+"\x00"+t, &n, func(ctx context.Context) (any, bool, error) {
		return s.writeAlbum(ctx, artist, title)
	})
	return n, err
}

// page reads cached notes into v, or writes them with write and caches
// them. write returns nil notes for a miss, and sure false when part of
// them couldn't be found for now, so they're kept briefly.
func (s *Service) page(ctx context.Context, kind, key string, v any, write func(context.Context) (any, bool, error)) error {
	if strings.Trim(key, "\x00") == "" {
		return fmt.Errorf("%s notes with no name: %w", kind, provider.ErrNotFound)
	}
	got, err, _ := s.group.Do(kind+"\x00"+key, func() (any, error) {
		now := s.opts.Now()
		row, err := s.db.GetCachedPageNotes(ctx, store.GetCachedPageNotesParams{Kind: kind, Key: key, Now: now})
		switch {
		case err == nil:
			if !row.Found {
				return nil, nil
			}
			return []byte(row.Notes), nil
		case !store.IsNotFound(err):
			return nil, err
		}
		// Shared by everyone waiting, so not cut short by the first to ask.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
		defer cancel()
		n, sure, err := write(ctx)
		if err != nil {
			return nil, err
		}
		s.putPage(ctx, kind, key, n, sure, now)
		if n == nil {
			return nil, nil
		}
		return json.Marshal(n)
	})
	if err != nil {
		return err
	}
	data, _ := got.([]byte)
	if data == nil {
		return fmt.Errorf("%s notes for %q: %w", kind, key, provider.ErrNotFound)
	}
	return json.Unmarshal(data, v)
}

func (s *Service) putPage(ctx context.Context, kind, key string, n any, sure bool, now time.Time) {
	data := []byte("{}")
	if n != nil {
		var err error
		if data, err = json.Marshal(n); err != nil {
			slog.Warn("page notes: encoding", "err", err)
			return
		}
	}
	ttl := s.opts.MissTTL
	if n != nil && sure {
		ttl = s.opts.FoundTTL
	}
	if err := s.db.PutCachedPageNotes(ctx, store.PutCachedPageNotesParams{
		Kind: kind, Key: key, Found: n != nil, Notes: string(data), FetchedAt: now, ExpiresAt: now.Add(ttl),
	}); err != nil {
		slog.Warn("caching page notes", "kind", kind, "err", err)
	}
}

func (s *Service) writeArtist(ctx context.Context, name string) (any, bool, error) {
	mbid, err := s.mb.FindArtist(ctx, name)
	if errors.Is(err, provider.ErrNotFound) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	p, err := s.mb.Profile(ctx, mbid)
	if errors.Is(err, provider.ErrNotFound) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	n := writeArtist(p)
	sure := s.bio(ctx, p.WikidataID, p.WikipediaURL, &n.Bio, &n.BioURL)
	return &n, sure, nil
}

func (s *Service) writeAlbum(ctx context.Context, artist, title string) (any, bool, error) {
	a, err := s.mb.FindAlbum(ctx, artist, title)
	if errors.Is(err, provider.ErrNotFound) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	n := AlbumNotes{
		MBID: a.MBID, Title: a.Title, Kind: a.Kind(), FirstReleased: a.FirstReleased,
		Labels: nonNil(a.Labels), Genres: nonNil(a.Genres),
	}
	sure := s.bio(ctx, a.WikidataID, a.WikipediaURL, &n.About, &n.AboutURL)
	return &n, sure, nil
}

// bio reads a Wikipedia summary into text and url, if there's one. It
// returns false if Wikipedia couldn't be asked, so it's asked again soon.
func (s *Service) bio(ctx context.Context, wikidataID, wikipediaURL string, text, url *string) bool {
	if s.wiki == nil {
		return true
	}
	sum, err := s.wiki.artistSummary(ctx, wikidataID, wikipediaURL)
	switch {
	case err == nil:
		*text, *url = sum.Text, sum.URL
	case errors.Is(err, errNoArticle):
	default:
		slog.Debug("page notes: wikipedia", "wikidata", wikidataID, "err", err)
		return false
	}
	return true
}

// writeArtist turns a MusicBrainz profile into notes.
func writeArtist(p musicbrainz.Profile) ArtistNotes {
	n := ArtistNotes{
		MBID: p.MBID, Name: p.Name, About: about(&p.Artist),
		Facts: []Fact{}, Members: []Member{}, Genres: nonNil(p.Genres), Releases: []ReleaseKind{},
	}
	if f := began(p.Artist); f != "" {
		n.Facts = append(n.Facts, Fact{Kind: FactBegan, Text: f})
	}
	if f := ended(p.Artist); f != "" {
		n.Facts = append(n.Facts, Fact{Kind: FactEnded, Text: f})
	}
	for _, m := range p.Members {
		n.Members = append(n.Members, Member{
			Name: m.Name, From: year(m.Begin), To: year(m.End), Current: !m.Ended && m.End == "", Roles: nonNil(m.Roles),
		})
	}
	for _, rg := range p.ReleaseGroups {
		n.Releases = append(n.Releases, ReleaseKind{Title: rg.Title, Kind: rg.Kind(), Year: year(rg.FirstReleased)})
	}
	return n
}

// began says where and when an artist began: "Formed in Leeds in 1994".
func began(a musicbrainz.Artist) string {
	var verb string
	switch a.Type {
	case "Person":
		verb = "Born"
	case "Group", "Orchestra", "Choir":
		verb = "Formed"
	default:
		return ""
	}
	where, y := a.BeginArea, year(a.Begin)
	if where == "" && y == 0 {
		return ""
	}
	s := verb
	if where != "" {
		s += " in " + where
	}
	if y > 0 {
		s += " in " + strconv.Itoa(y)
	}
	return s
}

// ended says when an artist ended: "Split up in 2012".
func ended(a musicbrainz.Artist) string {
	if !a.Ended {
		return ""
	}
	var verb string
	switch a.Type {
	case "Person":
		verb = "Died"
	case "Group", "Orchestra", "Choir":
		verb = "Split up"
	default:
		return ""
	}
	if y := year(a.End); y > 0 {
		return verb + " in " + strconv.Itoa(y)
	}
	return ""
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
