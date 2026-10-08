// SPDX-License-Identifier: AGPL-3.0-only

// Package artistpage fills artist and genre pages from what's known about
// music (internal/musicgraph) and one linked service: an artist's top
// songs and the songs they're featured on, artists like them and songs
// by those, a genre's artists, and the same artist on other services.
//
// What's known says what to show, the service what can be played: songs
// and artists are looked up on the link, and only those found are shown
// as playable.
package artistpage

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Graph is what's known about music. musicgraph.Service is one.
type Graph interface {
	Artist(ctx context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, error)
	CachedArtist(ctx context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, bool, error)
	Genre(ctx context.Context, tag string) (musicgraph.Genre, error)
	WarmArtist(as ...musicgraph.ArtistRef)
}

// Limits, so a page stays quick on a service that searches slowly.
const (
	// TopSongs is how many of an artist's top songs a page shows.
	TopSongs = 10
	// maxAppearances is how many songs they're featured on it shows.
	maxAppearances = 20
	// poolSize is how many of an artist's songs one search finds, to
	// match their top songs against.
	poolSize = 50
	// maxLookups is how many top songs missing from that search are
	// looked for one by one.
	maxLookups = 6
	// RelatedArtists is how many similar artists a page shows, and
	// songsEach how many songs by each.
	RelatedArtists = 12
	songsEach      = 2
	// GenreArtists is how many of a genre's artists are looked for.
	GenreArtists = 24
	// parallel is how many lookups run at once on one service.
	parallel = 4
	// graphWait bounds waiting on what's known about an artist the first
	// time; the page falls back to what the service says.
	graphWait = 12 * time.Second
)

// Page fills artist pages through one link's session.
type Page struct {
	Sess provider.Session
	// Caps are the link's provider's capabilities.
	Caps provider.Capabilities
	// Graph is what's known about music; nil without it.
	Graph Graph
}

// Top returns an artist's most popular songs on the service, most popular
// first, and songs on others' records they're credited on. albums are
// the artist's own, as the service lists them.
func (p Page) Top(ctx context.Context, artist provider.Artist, albums []provider.Album) (top, appearsOn []provider.Track) {
	pool := p.songsBy(ctx, artist, poolSize)
	if want := p.topSongs(ctx, artist.Name); len(want) > 0 {
		top = p.find(ctx, artist, want, pool)
	}
	if len(top) == 0 {
		if rec, ok := p.Sess.(provider.Recommender); ok && p.Caps.Recommendations {
			ts, err := rec.TopTracks(ctx, artist.Name, TopSongs*2)
			if err != nil {
				slog.Debug("artistpage: top tracks", "artist", artist.Name, "err", err)
			}
			top = by(artist, ts)
		}
	}
	if len(top) == 0 {
		// Nothing knows what's popular: what the service finds first.
		top = pool
	}
	top = distinct(top)
	top = top[:min(len(top), TopSongs)]

	own := map[string]bool{}
	for _, al := range albums {
		own[al.ID] = true
		own["title\x00"+match.Simplify(al.Title)] = true
	}
	shown := map[string]bool{}
	for _, t := range top {
		shown[t.Ref.ID] = true
	}
	for _, t := range pool {
		if shown[t.Ref.ID] || t.Album.Title == "" || own[t.Album.ID] || own["title\x00"+match.Simplify(t.Album.Title)] {
			continue
		}
		appearsOn = append(appearsOn, t)
	}
	appearsOn = distinct(appearsOn)
	return top, appearsOn[:min(len(appearsOn), maxAppearances)]
}

// topSongs are an artist's most popular songs, as far as anyone knows.
func (p Page) topSongs(ctx context.Context, name string) []musicgraph.Song {
	if p.Graph == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, graphWait)
	defer cancel()
	a, err := p.Graph.Artist(ctx, musicgraph.ArtistRef{Name: name})
	if err != nil {
		slog.Debug("artistpage: what's known about an artist", "artist", name, "err", err)
		return nil
	}
	return a.Top
}

// find looks for songs on the service, in order, until it has
// TopSongs: first among pool, then by searching for a few.
func (p Page) find(ctx context.Context, artist provider.Artist, want []musicgraph.Song, pool []provider.Track) []provider.Track {
	found := make([]*provider.Track, len(want))
	var missing []int
	for i, s := range want {
		if t, ok := best(wanted(s), pool); ok {
			found[i] = &t
		} else if len(missing) < maxLookups && i < TopSongs*2 {
			missing = append(missing, i)
		}
	}
	// Only look further for songs that would make the list.
	have := 0
	for i := range found {
		if found[i] != nil {
			have++
		}
	}
	if have < TopSongs && len(missing) > 0 {
		each(len(missing), func(j int) {
			i := missing[j]
			t, score, err := match.On(ctx, p.Sess, wanted(want[i]))
			if err != nil {
				slog.Debug("artistpage: finding a song", "title", want[i].Title, "err", err)
			}
			if err == nil && score > 0 && credited(artist, t) {
				found[i] = &t
			}
		})
	}
	var out []provider.Track
	for _, t := range found {
		if t != nil {
			out = append(out, *t)
		}
	}
	return out
}

// wanted is a song to look for.
func wanted(s musicgraph.Song) provider.Track {
	return provider.Track{Title: s.Title, ISRC: s.ISRC, MBID: s.MBID, Artists: []provider.ArtistCredit{{Name: s.Artist.Name}}}
}

// best is the song among ts that matches want best.
func best(want provider.Track, ts []provider.Track) (provider.Track, bool) {
	var out provider.Track
	top := 0.0
	for _, t := range ts {
		if score, ok := match.Score(want, t); ok && score > top {
			out, top = t, score
		}
	}
	return out, top > 0
}

// songsBy searches the service for an artist's songs: those credited to
// them, in the service's order.
func (p Page) songsBy(ctx context.Context, artist provider.Artist, limit int) []provider.Track {
	if !p.Caps.CanSearch(provider.KindTrack) {
		return nil
	}
	page, err := p.Sess.Search(ctx, provider.SearchQuery{Text: artist.Name, Kinds: []provider.EntityKind{provider.KindTrack}, Limit: limit})
	if err != nil {
		slog.Debug("artistpage: searching for an artist's songs", "artist", artist.Name, "err", err)
		return nil
	}
	return by(artist, page.Tracks)
}

// by keeps the songs credited to artist.
func by(artist provider.Artist, ts []provider.Track) []provider.Track {
	var out []provider.Track
	for _, t := range ts {
		if credited(artist, t) {
			out = append(out, t)
		}
	}
	return out
}

// credited reports whether artist is credited on t: by ID when both have
// one, else by name.
func credited(artist provider.Artist, t provider.Track) bool {
	want := match.Simplify(artist.Name)
	return slices.ContainsFunc(t.Artists, func(a provider.ArtistCredit) bool {
		if artist.ID != "" && a.ID != "" {
			return a.ID == artist.ID
		}
		return match.Simplify(a.Name) == want
	})
}

// distinct drops songs with the same title as one before, and the same
// song twice: services list remasters and live takes alongside.
func distinct(ts []provider.Track) []provider.Track {
	seen := map[string]bool{}
	var out []provider.Track
	for _, t := range ts {
		title, _ := match.Title(t.Title)
		k := title
		if len(t.Artists) > 0 {
			k = match.Simplify(t.Artists[0].Name) + "\x00" + title
		}
		if seen[k] || seen["id\x00"+t.Ref.ID] {
			continue
		}
		seen[k], seen["id\x00"+t.Ref.ID] = true, true
		out = append(out, t)
	}
	return out
}

// Related is an artist like another, or in a genre.
type Related struct {
	Name string
	// Score is how alike, from 0 to 1; 0 when not known.
	Score float64
	// Artist is them on the service, if they're there.
	Artist *provider.Artist
}

// Similar returns artists like artist, most alike first, and songs by
// them, a few from each in turn.
func (p Page) Similar(ctx context.Context, artist provider.Artist) ([]Related, []provider.Track) {
	var refs []Related
	if p.Graph != nil {
		gctx, cancel := context.WithTimeout(ctx, graphWait)
		a, err := p.Graph.Artist(gctx, musicgraph.ArtistRef{Name: artist.Name})
		cancel()
		if err != nil {
			slog.Debug("artistpage: similar artists", "artist", artist.Name, "err", err)
		}
		for _, s := range a.Similar[:min(len(a.Similar), RelatedArtists)] {
			refs = append(refs, Related{Name: s.Artist.Name, Score: s.Score})
		}
	}
	if len(refs) > 0 {
		return p.look(ctx, refs)
	}
	// Nothing knows who's alike: the service's own recommendations.
	rec, ok := p.Sess.(provider.Recommender)
	if !ok || !p.Caps.Recommendations || artist.ID == "" {
		return []Related{}, nil
	}
	ts, err := rec.SimilarToArtist(ctx, artist.ID, RelatedArtists*songsEach*2)
	if err != nil {
		slog.Debug("artistpage: similar to an artist", "artist", artist.Name, "err", err)
		return []Related{}, nil
	}
	out := []Related{}
	seen := map[string]bool{match.Simplify(artist.Name): true}
	count := map[string]int{}
	var songs []provider.Track
	for _, t := range ts {
		if len(t.Artists) == 0 {
			continue
		}
		c := t.Artists[0]
		k := match.Simplify(c.Name)
		if count[k] < songsEach && k != match.Simplify(artist.Name) {
			count[k]++
			songs = append(songs, t)
		}
		if !seen[k] && len(out) < RelatedArtists {
			seen[k] = true
			r := Related{Name: c.Name}
			if c.ID != "" {
				r.Artist = &provider.Artist{ID: c.ID, Name: c.Name}
			}
			out = append(out, r)
		}
	}
	return out, songs
}

// Genre returns a genre's artists, the ones on the service first, and
// songs by them.
func (p Page) Genre(ctx context.Context, tag string) ([]Related, []provider.Track, error) {
	if p.Graph == nil {
		return nil, nil, provider.ErrNotFound
	}
	g, err := p.Graph.Genre(ctx, tag)
	if err != nil {
		return nil, nil, err
	}
	refs := make([]Related, 0, GenreArtists)
	for _, a := range g.Artists[:min(len(g.Artists), GenreArtists)] {
		refs = append(refs, Related{Name: a.Name})
	}
	rel, songs := p.look(ctx, refs)
	// The ones you can open first, in the genre's order.
	slices.SortStableFunc(rel, func(a, b Related) int {
		switch {
		case a.Artist != nil && b.Artist == nil:
			return -1
		case a.Artist == nil && b.Artist != nil:
			return 1
		}
		return 0
	})
	return rel, songs, nil
}

// look finds artists on the service, and a few songs by each, with one
// search apiece. Songs come a few from each artist in turn, in order.
func (p Page) look(ctx context.Context, refs []Related) ([]Related, []provider.Track) {
	var kinds []provider.EntityKind
	for _, k := range []provider.EntityKind{provider.KindArtist, provider.KindTrack} {
		if p.Caps.CanSearch(k) {
			kinds = append(kinds, k)
		}
	}
	songs := make([][]provider.Track, len(refs))
	if len(kinds) > 0 {
		each(len(refs), func(i int) {
			page, err := p.Sess.Search(ctx, provider.SearchQuery{Text: refs[i].Name, Kinds: kinds, Limit: 10})
			if err != nil {
				slog.Debug("artistpage: looking for an artist", "artist", refs[i].Name, "err", err)
				return
			}
			want := match.Simplify(refs[i].Name)
			for _, a := range page.Artists {
				if match.Simplify(a.Name) == want {
					refs[i].Artist = &a
					break
				}
			}
			who := provider.Artist{Name: refs[i].Name}
			if refs[i].Artist != nil {
				who = *refs[i].Artist
			}
			songs[i] = distinct(by(who, page.Tracks))
		})
	}
	var out []provider.Track
	for n := range songsEach {
		for _, ts := range songs {
			if n < len(ts) {
				out = append(out, ts[n])
			}
		}
	}
	return refs, out
}

// Shuffle returns up to n of an artist's songs at random, spread across
// their albums.
func (p Page) Shuffle(ctx context.Context, albums []provider.Album, n int, rnd *rand.Rand) ([]provider.Track, error) {
	if len(albums) == 0 || n <= 0 {
		return nil, nil
	}
	albums = slices.Clone(albums)
	rnd.Shuffle(len(albums), func(i, j int) { albums[i], albums[j] = albums[j], albums[i] })
	albums = albums[:min(len(albums), (n+1)/2, maxShuffleAlbums)]
	lists := make([][]provider.Track, len(albums))
	errs := make([]error, len(albums))
	each(len(albums), func(i int) {
		_, lists[i], errs[i] = p.Sess.Album(ctx, albums[i].ID)
	})
	var out []provider.Track
	for i := range lists {
		rnd.Shuffle(len(lists[i]), func(a, b int) { lists[i][a], lists[i][b] = lists[i][b], lists[i][a] })
	}
	for k := 0; len(out) < n; k++ {
		added := false
		for _, ts := range lists {
			if k < len(ts) && len(out) < n {
				out = append(out, ts[k])
				added = true
			}
		}
		if !added {
			break
		}
	}
	if len(out) == 0 {
		for _, err := range errs {
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// maxShuffleAlbums bounds the albums a shuffle opens.
const maxShuffleAlbums = 6

// Elsewhere finds the artist called name on another service's session:
// nil if they're not there.
func Elsewhere(ctx context.Context, sess provider.Session, caps provider.Capabilities, name string) *provider.Artist {
	if !caps.CanSearch(provider.KindArtist) {
		return nil
	}
	page, err := sess.Search(ctx, provider.SearchQuery{Text: name, Kinds: []provider.EntityKind{provider.KindArtist}, Limit: 5})
	if err != nil {
		slog.Debug("artistpage: looking for an artist elsewhere", "artist", name, "err", err)
		return nil
	}
	want := match.Simplify(name)
	for _, a := range page.Artists {
		if match.Simplify(a.Name) == want {
			return &a
		}
	}
	return nil
}

// each runs f for 0..n-1, a few at a time.
func each(n int, f func(i int)) {
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i := range n {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			f(i)
		})
	}
	wg.Wait()
}
