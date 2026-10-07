// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Genres, for genre pages: the artists a tag is most applied to.

// Genre is what's known about a genre (a tag).
type Genre struct {
	Name string `json:"name"`
	// Artists are its top artists, most typical first.
	Artists []ArtistRef `json:"artists"`
	// Sources are the sources that knew it; "cache" when only artists
	// already known to carry the tag stand in.
	Sources []string `json:"sources,omitempty"`
}

// TagSource is a Source that knows a tag's top artists. LastFM is one.
type TagSource interface {
	TagArtists(ctx context.Context, tag string, limit int) ([]ArtistRef, error)
}

// maxGenreArtists is how many of a genre's artists are kept.
const maxGenreArtists = 50

// Genre returns a tag's top artists: from the sources that know tags,
// else from the artists cached with it. It's provider.ErrNotFound if
// nothing carries the tag.
func (s *Service) Genre(ctx context.Context, tag string) (Genre, error) {
	tag = strings.TrimSpace(tag)
	key := match.Simplify(tag)
	if key == "" {
		return Genre{}, notFound("a genre with no name")
	}
	now := s.opts.Now()
	row, err := s.db.GetMusicGraphTag(ctx, store.GetMusicGraphTagParams{Key: key, Now: now})
	switch {
	case err == nil:
		if !row.Found {
			return Genre{}, notFound("genre " + tag)
		}
		var g Genre
		if err := json.Unmarshal([]byte(row.Facts), &g); err != nil {
			return Genre{}, fmt.Errorf("musicgraph: reading cached genre %q: %w", key, err)
		}
		return g, nil
	case !store.IsNotFound(err):
		return Genre{}, err
	}

	fctx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()
	g := Genre{Name: tag, Artists: []ArtistRef{}}
	failed := false
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, src := range s.opts.Sources {
		ts, ok := src.(TagSource)
		if !ok {
			continue
		}
		wg.Go(func() {
			as, err := ts.TagArtists(fctx, tag, maxGenreArtists)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && len(as) > 0:
				g.Sources = append(g.Sources, src.Name())
				g.Artists = mergeRefs(g.Artists, as)
			case err == nil, errors.Is(err, provider.ErrNotFound):
			default:
				failed = true
				slog.Debug("musicgraph: asking about a genre", "source", src.Name(), "tag", tag, "err", err)
			}
		})
	}
	wg.Wait()

	ttl := s.opts.TTL
	if len(g.Artists) == 0 {
		// No source knows it: the artists already known to carry it, kept
		// briefly, since more are learned all the time.
		ttl = s.opts.MissTTL
		rows, err := s.db.TaggedArtists(ctx, store.TaggedArtistsParams{Now: now, Tag: tag, Limit: maxGenreArtists})
		if err != nil {
			return Genre{}, err
		}
		for _, r := range rows {
			g.Artists = mergeRefs(g.Artists, []ArtistRef{{Name: r.Name}})
		}
		if len(g.Artists) > 0 {
			g.Sources = []string{"cache"}
		}
	}
	if failed {
		ttl = s.opts.MissTTL
	}
	found := len(g.Artists) > 0
	facts, err := json.Marshal(g)
	if err != nil {
		return Genre{}, err
	}
	if err := s.db.PutMusicGraphTag(ctx, store.PutMusicGraphTagParams{
		Key: key, Found: found, Facts: string(facts), FetchedAt: now, ExpiresAt: now.Add(ttl),
	}); err != nil {
		return Genre{}, err
	}
	if !found {
		if failed {
			return Genre{}, fmt.Errorf("musicgraph: genre %q: %w", tag, provider.ErrUnavailable)
		}
		return Genre{}, notFound("genre " + tag)
	}
	return g, nil
}

// mergeRefs adds artists not already in a list, by name, up to the most
// a genre keeps.
func mergeRefs(have, more []ArtistRef) []ArtistRef {
	seen := map[string]bool{}
	for _, a := range have {
		seen[artistKey(a)] = true
	}
	for _, a := range more {
		if k := artistKey(a); k != "" && !seen[k] && len(have) < maxGenreArtists {
			seen[k] = true
			have = append(have, a)
		}
	}
	return have
}

// TagArtists implements TagSource with tag.getTopArtists.
func (l *LastFM) TagArtists(ctx context.Context, tag string, limit int) ([]ArtistRef, error) {
	var r struct {
		TopArtists struct {
			Artist []struct {
				Name string `json:"name"`
				MBID string `json:"mbid"`
			} `json:"artist"`
		} `json:"topartists"`
	}
	if err := l.call(ctx, "tag.getTopArtists", url.Values{"tag": {tag}, "limit": {strconv.Itoa(limit)}}, &r); err != nil {
		return nil, err
	}
	var out []ArtistRef
	for _, a := range r.TopArtists.Artist {
		out = append(out, ArtistRef{Name: a.Name, MBID: a.MBID})
	}
	return out, nil
}

var _ TagSource = (*LastFM)(nil)
