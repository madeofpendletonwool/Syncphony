// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// How far candidates reach.
const (
	// seedArtists is how many of the room's favorite artists the walk
	// starts from. Artists that aren't cached yet are fetched, up to
	// maxFetches a walk: the room's first fetchArtists favorites, then the
	// nearest artists it reaches. The rest are warmed for next time.
	seedArtists  = 8
	fetchArtists = 3
	maxFetches   = 6
	// similarPerArtist and similarPerHop2 are how many of an artist's
	// similar artists the walk follows, one step out and two.
	similarPerArtist = 30
	similarPerHop2   = 15
	// hop2From is how many of the nearest one-step artists the second
	// step starts from.
	hop2From = 10
	// hop2Damping is how much a second step's similarity counts.
	hop2Damping = 0.6
	// songArtists is how many of the nearest artists' songs are
	// candidates, songsPerArtist of each.
	songArtists    = 40
	songsPerArtist = 15
	// similarSongs is how many songs like each recent song are candidates.
	similarSongSeeds = 3
	similarSongs     = 20
	// unknownPopularity stands in for a song's popularity among its
	// artist's when none is known.
	unknownPopularity = 0.4
)

// Kinds of candidate: how a song relates to the room.
const (
	// KindArtist is a song by an artist the room likes.
	KindArtist = "artist"
	// KindSimilarArtist is a song by an artist like one the room likes.
	KindSimilarArtist = "similar-artist"
	// KindTwoSteps is a song by an artist like one like the room's.
	KindTwoSteps = "two-steps"
	// KindSimilarSong is a song like one the room liked.
	KindSimilarSong = "similar-song"
)

// node is an artist the walk reached.
type node struct {
	ref musicgraph.ArtistRef
	// affinity is how near the room's taste they are: the room's liking
	// of each artist that leads to them, times how alike. Several paths
	// add up.
	affinity float64
	hop      int
	// via is the room's artist that leads to them most strongly.
	via     string
	viaPull float64
	sources []string
}

// candidate is a song that might play next.
type candidate struct {
	song     musicgraph.SongRef
	artist   string // ArtistKey
	kind     string
	affinity float64
	hop      int
	via      string
	// popularity is among the artist's own songs, 0 to 1.
	popularity float64
	sources    []string
	// scored by score.
	similarity, novelty, score float64
	deepCut                    bool
}

// walk follows the music graph out from the room's favorite artists and
// returns the songs it finds, deduplicated, without songs the room heard
// or artists it turned away. Artists not cached yet are warmed for next
// time.
func (e *Engine) walk(ctx context.Context, p Profile, explore float64) []candidate {
	nodes := map[string]*node{}
	reach := func(ref musicgraph.ArtistRef, pull float64, hop int, via string, sources []string) {
		k := ArtistKey(ref.Name)
		if k == "" || p.Avoid[k] {
			return
		}
		n, ok := nodes[k]
		if !ok {
			n = &node{ref: ref, hop: hop}
			nodes[k] = n
		}
		n.affinity += pull
		n.hop = min(n.hop, hop)
		n.ref.MBID = cmp.Or(n.ref.MBID, ref.MBID)
		if pull > n.viaPull {
			n.via, n.viaPull = via, pull
		}
		for _, s := range sources {
			if !slices.Contains(n.sources, s) {
				n.sources = append(n.sources, s)
			}
		}
	}
	known := map[string]musicgraph.Artist{}
	var missing []musicgraph.ArtistRef
	fetches := 0
	asked := map[string]bool{} // fetched this walk, found or not
	lookup := func(ref musicgraph.ArtistRef, fetch bool) (musicgraph.Artist, bool) {
		k := ArtistKey(ref.Name)
		if a, ok := known[k]; ok {
			return a, true
		}
		a, ok, err := e.Graph.CachedArtist(ctx, ref)
		if !ok && err == nil && fetch && fetches < maxFetches && ctx.Err() == nil {
			fetches++
			asked[k] = true
			a, err = e.Graph.Artist(ctx, ref)
			ok = err == nil || errors.Is(err, provider.ErrNotFound)
		}
		switch {
		case ok && err == nil:
			known[k] = a
			return a, true
		case !ok && err == nil:
			if !slices.ContainsFunc(missing, func(m musicgraph.ArtistRef) bool { return ArtistKey(m.Name) == k }) {
				missing = append(missing, ref)
			}
		case !errors.Is(err, provider.ErrNotFound):
			slog.Debug("dj: looking up an artist", "artist", ref.Name, "err", err)
		}
		return musicgraph.Artist{}, false
	}

	for i, t := range p.Top(seedArtists) {
		k := ArtistKey(t.Artist.Name)
		reach(t.Artist, t.Weight, 0, k, nil)
		a, ok := lookup(t.Artist, i < fetchArtists)
		if !ok {
			continue
		}
		if n := nodes[k]; n != nil {
			n.ref.MBID = cmp.Or(n.ref.MBID, a.Ref.MBID)
		}
		for _, s := range a.Similar[:min(len(a.Similar), similarPerArtist)] {
			hop := 1
			if _, mine := p.Artists[ArtistKey(s.Artist.Name)]; mine {
				hop = 0
			}
			reach(s.Artist, t.Weight*s.Score, hop, k, s.Sources)
		}
	}
	// A second step, for a room that wants to explore: from the nearest
	// new artists, cached only.
	if explore >= 0.35 {
		var step []*node
		for _, n := range nodes {
			if n.hop == 1 {
				step = append(step, n)
			}
		}
		slices.SortFunc(step, func(a, b *node) int { return cmp.Compare(b.affinity, a.affinity) })
		for _, n := range step[:min(len(step), hop2From)] {
			a, ok := lookup(n.ref, true)
			if !ok {
				continue
			}
			for _, s := range a.Similar[:min(len(a.Similar), similarPerHop2)] {
				reach(s.Artist, n.affinity*s.Score*hop2Damping, 2, n.via, s.Sources)
			}
		}
	}

	// Songs: the nearest artists' top songs.
	ranked := make([]*node, 0, len(nodes))
	for _, n := range nodes {
		ranked = append(ranked, n)
	}
	slices.SortFunc(ranked, func(a, b *node) int {
		if c := cmp.Compare(b.affinity, a.affinity); c != 0 {
			return c
		}
		return cmp.Compare(a.ref.Name, b.ref.Name)
	})
	var out []candidate
	taken := map[string]bool{}
	add := func(c candidate) {
		k := SongKey(c.song.Artist.Name, c.song.Title)
		if c.song.Title == "" || taken[k] || p.Heard[k] || p.Avoid[c.artist] {
			return
		}
		if _, variants := match.Title(c.song.Title); len(variants) > 0 {
			return // live takes, remixes, demos: not what a DJ reaches for
		}
		taken[k] = true
		out = append(out, c)
	}
	for _, n := range ranked[:min(len(ranked), songArtists)] {
		a, ok := lookup(n.ref, true)
		if !ok {
			continue
		}
		for _, so := range a.Top[:min(len(a.Top), songsPerArtist)] {
			so.Artist.Name = cmp.Or(so.Artist.Name, n.ref.Name)
			add(candidate{
				song: so.SongRef, artist: ArtistKey(n.ref.Name), kind: kindOf(n.hop),
				affinity: n.affinity, hop: n.hop, via: n.via, popularity: so.Score,
				sources: union(n.sources, so.Sources),
			})
		}
	}

	// Songs like the songs the room just liked.
	for _, it := range p.RecentSongs[:min(len(p.RecentSongs), similarSongSeeds)] {
		t := TrackOf(it)
		name := artistOf(t)
		k := ArtistKey(name)
		taste, ok := p.Artists[k]
		if !ok {
			continue
		}
		tr, ok, err := e.Graph.CachedTrack(ctx, musicgraph.SongRef{Title: t.Title, Artist: musicgraph.ArtistRef{Name: name}, ISRC: t.ISRC, MBID: t.MBID})
		if !ok || err != nil {
			continue
		}
		for _, so := range tr.Similar[:min(len(tr.Similar), similarSongs)] {
			sk := ArtistKey(so.Artist.Name)
			hop, pop := 1, unknownPopularity
			if _, mine := p.Artists[sk]; mine {
				hop = 0
			}
			if n, ok := nodes[sk]; ok {
				hop = min(hop, n.hop)
				if a, ok := known[sk]; ok {
					pop = popularityIn(a, so.Title)
				}
			}
			add(candidate{
				song: so.SongRef, artist: sk, kind: KindSimilarSong,
				affinity: taste.Weight * so.Score, hop: hop, via: k, popularity: pop, sources: so.Sources,
			})
		}
	}
	missing = slices.DeleteFunc(missing, func(m musicgraph.ArtistRef) bool { return asked[ArtistKey(m.Name)] })
	e.Graph.WarmArtist(missing...)
	return out
}

func kindOf(hop int) string {
	switch hop {
	case 0:
		return KindArtist
	case 1:
		return KindSimilarArtist
	}
	return KindTwoSteps
}

// popularityIn is how popular a song is among an artist's top songs.
func popularityIn(a musicgraph.Artist, title string) float64 {
	want, _ := match.Title(title)
	for _, so := range a.Top {
		if t, _ := match.Title(so.Title); t == want {
			return so.Score
		}
	}
	return unknownPopularity / 2 // not among their top songs: a deep cut
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, s := range b {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
