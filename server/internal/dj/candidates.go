// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"time"

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
	// KindThrowback is a song by an artist the room loved in past nights,
	// but hasn't played lately.
	KindThrowback = "throwback"
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
	// byMember is how much of the affinity is each member's, and
	// memberVia the artist of theirs that leads here most strongly; reach
	// is byMember against each member's nearest artist, 0 to 1.
	byMember  map[string]float64
	memberVia map[string]string
	viaPulls  map[string]float64
	reach     map[string]float64
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
	// tags are the artist's, if known.
	tags []musicgraph.Tag
	// lovedAt is when the room last liked a throwback's artist.
	lovedAt time.Time
	// scored by score.
	similarity, novelty, prior, score float64
	deepCut                           bool
	// flow is how it fits the set, by flowing.
	flow Flow
	// reach is how near the artist is to each member's taste, 0 to 1, and
	// reachVia the artist of theirs (ArtistKey) that leads to it.
	reach    map[string]float64
	reachVia map[string]string
	// bridge is the members' tastes it's between, by mixed, if it's a
	// bridge; turn, the member whose turn the fill was, if it wasn't.
	bridge *Bridge
	turn   string
}

// walk follows the music graph out from the room's favorite artists and
// returns the songs it finds, deduplicated, without songs the room heard
// or artists it turned away. Artists not cached yet are warmed for next
// time. The top songs of throwbacks, artists (ArtistKey) the room loved in
// past nights, are candidates too.
func (e *Engine) walk(ctx context.Context, p Profile, lt LongTerm, explore float64, throwbacks []string) []candidate {
	nodes := map[string]*node{}
	// reach notes an artist reached with pull, shares of which are each
	// member's, by vias (each member's artist that leads there).
	reach := func(ref musicgraph.ArtistRef, pull float64, hop int, via string, sources []string, shares map[string]float64, vias map[string]string) {
		k := ArtistKey(ref.Name)
		if k == "" || p.Avoid[k] {
			return
		}
		n, ok := nodes[k]
		if !ok {
			n = &node{ref: ref, hop: hop, byMember: map[string]float64{}, memberVia: map[string]string{}, viaPulls: map[string]float64{}}
			nodes[k] = n
		}
		n.affinity += pull
		for u, sh := range shares {
			n.byMember[u] += pull * sh
			if pull*sh > n.viaPulls[u] {
				n.viaPulls[u], n.memberVia[u] = pull*sh, cmp.Or(vias[u], via)
			}
		}
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

	for i, t := range p.seeds() {
		k := ArtistKey(t.Artist.Name)
		shares := t.shares()
		reach(t.Artist, t.Weight, 0, k, nil, shares, nil)
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
			reach(s.Artist, t.Weight*s.Score, hop, k, s.Sources, shares, nil)
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
			shares := map[string]float64{}
			for u, v := range n.byMember {
				shares[u] = v / n.affinity
			}
			vias := maps.Clone(n.memberVia)
			for _, s := range a.Similar[:min(len(a.Similar), similarPerHop2)] {
				reach(s.Artist, n.affinity*s.Score*hop2Damping, 2, n.via, s.Sources, shares, vias)
			}
		}
	}

	memberReach(nodes)

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
	// Throwbacks first, so that a song of theirs the walk also reached
	// comes back as a throwback: their top songs, as near to the room as
	// its loves were.
	strongest := 0.0
	for _, k := range throwbacks {
		strongest = max(strongest, lt.Artists[k].Weight)
	}
	for _, k := range throwbacks {
		l := lt.Artists[k]
		a, ok := lookup(musicgraph.ArtistRef{Name: l.Name}, true)
		if !ok {
			continue
		}
		for _, so := range a.Top[:min(len(a.Top), songsPerArtist)] {
			so.Artist.Name = cmp.Or(so.Artist.Name, l.Name)
			add(candidate{
				song: so.SongRef, artist: k, kind: KindThrowback, affinity: l.Weight / strongest,
				via: k, popularity: so.Score, sources: so.Sources, tags: a.Tags, lovedAt: l.LovedAt,
			})
		}
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
				sources: union(n.sources, so.Sources), tags: a.Tags, reach: n.reach, reachVia: n.memberVia,
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
			var tags []musicgraph.Tag
			var near map[string]float64
			var nearVia map[string]string
			if n, ok := nodes[sk]; ok {
				hop, near, nearVia = min(hop, n.hop), n.reach, n.memberVia
				if a, ok := known[sk]; ok {
					pop, tags = popularityIn(a, so.Title), a.Tags
				}
			}
			add(candidate{
				song: so.SongRef, artist: sk, kind: KindSimilarSong,
				affinity: taste.Weight * so.Score, hop: hop, via: k, popularity: pop, sources: so.Sources, tags: tags,
				reach: near, reachVia: nearVia,
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
