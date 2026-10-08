// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/artistpage"
	"github.com/madeofpendletonwool/syncphony/server/internal/linernotes"
	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/stats"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Artist and album pages (MAD-763): what's known about them, beyond what
// their service says.

// maxGenres is how many genres an artist's page shows.
const maxGenres = 8

// minTagWeight is how strongly a tag must apply to an artist to show as a
// genre, when MusicBrainz doesn't list enough.
const minTagWeight = 0.2

// notGenres are tags people put on artists that aren't genres.
var notGenres = map[string]bool{
	"seen live": true, "favorites": true, "favourites": true, "favorite": true, "favourite": true,
	"my favorite": true, "awesome": true, "love": true, "beautiful": true, "cool": true,
}

// artistPage opens a link the caller may use and reads one of its artists.
func (s *Server) artistPage(ctx context.Context, linkID, artistID string) (store.ServiceLink, artistpage.Page, provider.Artist, []provider.Album, func(), error) {
	l, sess, err := s.openUsable(ctx, linkID)
	if err != nil {
		return l, artistpage.Page{}, provider.Artist{}, nil, nil, err
	}
	p, err := s.Links.Provider(l.Provider)
	if err != nil {
		sess.Close()
		return l, artistpage.Page{}, provider.Artist{}, nil, nil, err
	}
	a, albums, err := sess.Artist(ctx, artistID)
	if err != nil {
		sess.Close()
		return l, artistpage.Page{}, provider.Artist{}, nil, nil, err
	}
	page := artistpage.Page{Sess: sess, Caps: p.Info().Capabilities}
	if s.Graph != nil {
		page.Graph = s.Graph
	}
	return l, page, a, albums, func() { sess.Close() }, nil
}

// GetArtistAbout returns an artist's bio, facts, members and genres, and
// the kinds of their albums the service doesn't say.
func (s *Server) GetArtistAbout(ctx context.Context, req GetArtistAboutRequestObject) (GetArtistAboutResponseObject, error) {
	_, page, a, albums, done, err := s.artistPage(ctx, req.Id, req.ArtistId)
	if err != nil {
		return nil, err
	}
	done()
	out := ArtistAbout{Name: a.Name, Facts: []ArtistFact{}, Members: []ArtistMember{}, Genres: []string{}, AlbumKinds: map[string]AlbumKind{}}

	var notes linernotes.ArtistNotes
	var tags []musicgraph.Tag
	var wg sync.WaitGroup
	if s.LinerNotes != nil {
		wg.Go(func() {
			n, err := s.LinerNotes.Artist(ctx, a.Name)
			if err != nil && !errors.Is(err, provider.ErrNotFound) {
				slog.Warn("artist notes", "artist", a.Name, "err", err)
			}
			notes = n
		})
	}
	if page.Graph != nil {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			if g, err := page.Graph.Artist(ctx, musicgraph.ArtistRef{Name: a.Name}); err == nil {
				tags = g.Tags
			}
		})
	}
	wg.Wait()

	if notes.MBID != "" {
		out.Mbid = &notes.MBID
	}
	out.About, out.Bio, out.BioUrl = optional(notes.About), optional(notes.Bio), optional(notes.BioURL)
	for _, f := range notes.Facts {
		out.Facts = append(out.Facts, ArtistFact{Kind: ArtistFactKind(f.Kind), Text: f.Text})
	}
	for _, m := range notes.Members {
		am := ArtistMember{Name: m.Name, Current: m.Current, Roles: nonNilStrings(m.Roles)}
		if m.From > 0 {
			am.From = ptr(m.From)
		}
		if m.To > 0 {
			am.To = ptr(m.To)
		}
		out.Members = append(out.Members, am)
	}
	out.Genres = genres(notes.Genres, tags)

	// The kinds MusicBrainz knows of the albums the service doesn't say.
	kinds := map[string]string{}
	for _, r := range notes.Releases {
		if t, _ := match.Title(r.Title); t != "" {
			if _, ok := kinds[t]; !ok {
				kinds[t] = r.Kind
			}
		}
	}
	for _, al := range albums {
		if al.Kind != "" {
			continue
		}
		if t, _ := match.Title(al.Title); kinds[t] != "" {
			out.AlbumKinds[al.ID] = AlbumKind(kinds[t])
		}
	}
	return GetArtistAbout200JSONResponse(out), nil
}

// genres are MusicBrainz's curated genres, then the strongest tags others
// put on the artist, each once.
func genres(curated []string, tags []musicgraph.Tag) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(g string) {
		k := match.Simplify(g)
		if k == "" || seen[k] || notGenres[k] || len(out) >= maxGenres {
			return
		}
		seen[k] = true
		out = append(out, strings.ToLower(strings.TrimSpace(g)))
	}
	for _, g := range curated {
		add(g)
	}
	for _, t := range tags {
		if t.Weight >= minTagWeight {
			add(t.Name)
		}
	}
	return out
}

// GetArtistTracks returns an artist's top songs and the songs they're
// featured on.
func (s *Server) GetArtistTracks(ctx context.Context, req GetArtistTracksRequestObject) (GetArtistTracksResponseObject, error) {
	_, page, a, albums, done, err := s.artistPage(ctx, req.Id, req.ArtistId)
	if err != nil {
		return nil, err
	}
	defer done()
	top, appears := page.Top(ctx, a, albums)
	return GetArtistTracks200JSONResponse(ArtistTracks{Top: toTrackResults(top), AppearsOn: toTrackResults(appears)}), nil
}

// GetArtistRelated returns artists like an artist, and songs by them.
func (s *Server) GetArtistRelated(ctx context.Context, req GetArtistRelatedRequestObject) (GetArtistRelatedResponseObject, error) {
	_, page, a, _, done, err := s.artistPage(ctx, req.Id, req.ArtistId)
	if err != nil {
		return nil, err
	}
	defer done()
	rel, songs := page.Similar(ctx, a)
	return GetArtistRelated200JSONResponse(RelatedMusic{Artists: toRelated(rel), Tracks: toTrackResults(songs)}), nil
}

// GetArtistShuffle returns songs by an artist at random.
func (s *Server) GetArtistShuffle(ctx context.Context, req GetArtistShuffleRequestObject) (GetArtistShuffleResponseObject, error) {
	_, page, _, albums, done, err := s.artistPage(ctx, req.Id, req.ArtistId)
	if err != nil {
		return nil, err
	}
	defer done()
	n := 10
	if req.Params.Limit != nil {
		n = min(max(*req.Params.Limit, 1), 50)
	}
	ts, err := page.Shuffle(ctx, albums, n, rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))) //nolint:gosec // not for secrets
	if err != nil {
		return nil, err
	}
	return GetArtistShuffle200JSONResponse(toTrackResults(ts)), nil
}

// GetArtistElsewhere finds the artist on the caller's other links.
func (s *Server) GetArtistElsewhere(ctx context.Context, req GetArtistElsewhereRequestObject) (GetArtistElsewhereResponseObject, error) {
	_, sess, err := s.openUsable(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	a, _, err := sess.Artist(ctx, req.ArtistId)
	sess.Close()
	if err != nil {
		return nil, err
	}
	ls, err := s.Links.Usable(ctx, sessionFrom(ctx).User.ID)
	if err != nil {
		return nil, err
	}
	found := make([]*ArtistElsewhere, len(ls))
	var wg sync.WaitGroup
	for i, l := range ls {
		if l.ID == req.Id {
			continue
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, searchTimeout)
			defer cancel()
			p, err := s.Links.Provider(l.Provider)
			if err != nil {
				return
			}
			sess, err := s.Links.Open(ctx, l.ID)
			if err != nil {
				slog.Debug("artist elsewhere: opening a link", "link", l.ID, "err", err)
				return
			}
			defer sess.Close()
			if other := artistpage.Elsewhere(ctx, sess, p.Info().Capabilities, a.Name); other != nil {
				found[i] = &ArtistElsewhere{LinkId: l.ID, Provider: l.Provider, AccountLabel: l.AccountLabel, Artist: toArtistResult(*other)}
			}
		})
	}
	wg.Wait()
	out := GetArtistElsewhere200JSONResponse{}
	for _, f := range found {
		if f != nil {
			out = append(out, *f)
		}
	}
	return out, nil
}

// GetGenre returns a genre's artists and songs by them, through a link.
func (s *Server) GetGenre(ctx context.Context, req GetGenreRequestObject) (GetGenreResponseObject, error) {
	name := strings.TrimSpace(req.Params.Name)
	if name == "" || s.Graph == nil {
		return nil, provider.ErrNotFound
	}
	l, sess, err := s.openUsable(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	p, err := s.Links.Provider(l.Provider)
	if err != nil {
		return nil, err
	}
	page := artistpage.Page{Sess: sess, Caps: p.Info().Capabilities, Graph: s.Graph}
	rel, songs, err := page.Genre(ctx, name)
	if err != nil {
		return nil, err
	}
	return GetGenre200JSONResponse(GenreDetail{Name: name, Artists: toRelated(rel), Tracks: toTrackResults(songs)}), nil
}

// GetAlbumAbout returns what MusicBrainz and Wikipedia know about an album.
func (s *Server) GetAlbumAbout(ctx context.Context, req GetAlbumAboutRequestObject) (GetAlbumAboutResponseObject, error) {
	if s.LinerNotes == nil {
		return nil, provider.ErrNotFound
	}
	_, sess, err := s.openUsable(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	a, _, err := sess.Album(ctx, req.AlbumId)
	sess.Close()
	if err != nil {
		return nil, err
	}
	if len(a.Artists) == 0 {
		return nil, provider.ErrNotFound
	}
	n, err := s.LinerNotes.Album(ctx, a.Artists[0].Name, a.Title)
	if err != nil {
		return nil, err
	}
	out := AlbumAbout{
		Labels: nonNilStrings(n.Labels), Genres: nonNilStrings(n.Genres),
		FirstReleased: optional(n.FirstReleased), About: optional(n.About), AboutUrl: optional(n.AboutURL),
	}
	if n.Kind != "" {
		out.Kind = ptr(AlbumKind(n.Kind))
	}
	return GetAlbumAbout200JSONResponse(out), nil
}

// GetRoomArtistPlays returns an artist's songs the room played most.
func (s *Server) GetRoomArtistPlays(ctx context.Context, req GetRoomArtistPlaysRequestObject) (GetRoomArtistPlaysResponseObject, error) {
	n := 10
	if req.Params.Limit != nil {
		n = min(max(*req.Params.Limit, 1), 50)
	}
	plays, err := s.Rooms.Plays(ctx, req.RoomId, time.Time{}, rooms.Forever)
	if err != nil {
		return nil, err
	}
	return GetRoomArtistPlays200JSONResponse(toTrackCounts(stats.ArtistTop(plays, req.Params.Name, n))), nil
}

func toRelated(rs []artistpage.Related) []RelatedArtist {
	out := make([]RelatedArtist, len(rs))
	for i, r := range rs {
		out[i] = RelatedArtist{Name: r.Name}
		if r.Score > 0 {
			out[i].Score = ptr(float32(r.Score))
		}
		if r.Artist != nil {
			out[i].Artist = ptr(toArtistResult(*r.Artist))
		}
	}
	return out
}

func toTrackResults(ts []provider.Track) []TrackResult {
	out := make([]TrackResult, len(ts))
	for i, t := range ts {
		out[i] = toTrackResult(t)
	}
	return out
}

// optional is a pointer to s, or nil if it's empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return slices.Clone(s)
}
