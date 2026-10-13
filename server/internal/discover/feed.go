// SPDX-License-Identifier: AGPL-3.0-only

package discover

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// How far a feed reaches.
const (
	// PageSize is how many songs a page of a feed holds.
	PageSize = 20
	// perArtist is the most songs of one artist on a page.
	perArtist = 2
	// maxFetches is how many artists that aren't cached a build fetches,
	// seeds first; the rest are warmed for next time.
	maxFetches = 12
	// similarPerSeed and similarPerHop2 are how many of an artist's
	// similar artists the walk follows, one step out and two.
	similarPerSeed = 40
	similarPerHop2 = 15
	// hop2From is how many of the nearest new artists the second step
	// starts from, when the set explores (exploreHop2 or more).
	hop2From    = 10
	exploreHop2 = 0.35
	hop2Damping = 0.6
	// overlapBonus is how much more an artist near several seeds counts,
	// for each seed past the first.
	overlapBonus = 0.5
	// songArtists is how many of the nearest artists' songs are in a
	// feed, songsPerArtist of each.
	songArtists    = 40
	songsPerArtist = 10
	// seedPull is how near a seed's own songs count, against the nearest
	// new artist's: deep cuts of the seeds, among the new names.
	seedPull = 0.5
	// maxItems bounds a feed.
	maxItems = 400
	// resolvePerPage is how many songs without a Deezer ID a page looks
	// up, within resolveFor; the rest show without a preview this time.
	resolvePerPage = 12
	resolveFor     = 6 * time.Second
	// missAgain is how long a song Deezer didn't have waits to be looked
	// up again.
	missAgain = 30 * 24 * time.Hour
)

// Item is a song in a feed.
type Item struct {
	// Key is how the song is compared (SongKey).
	Key           string
	Title, Artist string
	// Album is its album's title, when known.
	Album      string
	MBID, ISRC string
	// DeezerID is the song on Deezer, for its preview and cover; 0 if
	// it isn't known to be there.
	DeezerID int64
	// Cover is the Deezer cover image's ID, when known.
	Cover string
	// Because are the seed artists it's near, nearest first.
	Because []string
	// Seed is whether it's a seed artist's own song.
	Seed  bool
	Score float64
}

// Feed is a page of a set's feed.
type Feed struct {
	Items []Item
	// Next is the cursor for the next page, or "" at the end.
	Next string
	// Library is how far the user's library is indexed: until it's done,
	// songs they have can show up.
	Library library.Status
	// NoGraph is whether the server knows nothing of how music relates
	// (no sources are on), so feeds are empty.
	NoGraph bool
}

// feed is a set's built feed, kept KeepFor.
type feed struct {
	builtAt time.Time
	// setAt is the set's updated_at when built: a change rebuilds it.
	setAt time.Time
	pages [][]Item
	// done is closed once it's built.
	done chan struct{}
}

func (s *Service) forget(setID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.feeds, setID)
}

// Feed returns a page of a set's feed, at cursor ("" for the first
// page). The feed is built at most every KeepFor, or now if refresh is
// set; songs the user has, turned down, blocked or wants are left out of
// every page as it's served, so a thumbs-down takes a song out at once.
func (s *Service) Feed(ctx context.Context, userID, setID, cursor string, refresh bool) (Feed, error) {
	set, err := s.Get(ctx, userID, setID)
	if err != nil {
		return Feed{}, err
	}
	page := 0
	if cursor != "" {
		if page, err = strconv.Atoi(cursor); err != nil || page < 0 {
			return Feed{}, &InvalidInputError{"bad cursor"}
		}
	}
	out := Feed{Items: []Item{}, NoGraph: s.Graph == nil}
	f := s.items(ctx, userID, set, refresh && page == 0)
	keep, status, err := s.filter(ctx, userID)
	if err != nil {
		return Feed{}, err
	}
	out.Library = status
	s.mu.Lock()
	// A page whose songs were all left out since it was built is skipped.
	for ; page < len(f.pages) && len(out.Items) == 0; page++ {
		for _, it := range f.pages[page] {
			if keep(it) {
				out.Items = append(out.Items, it)
			}
		}
	}
	if page < len(f.pages) {
		out.Next = strconv.Itoa(page)
	}
	s.mu.Unlock()
	s.resolve(ctx, f, out.Items)
	return out, nil
}

// items returns a set's feed, building it if it isn't kept or is stale.
// Only one build of a set runs at a time; others wait for it.
func (s *Service) items(ctx context.Context, userID string, set Set, refresh bool) *feed {
	now := s.Now()
	s.mu.Lock()
	f, ok := s.feeds[set.ID]
	fresh := ok && !refresh && f.setAt.Equal(set.UpdatedAt) && (f.builtAt.IsZero() || now.Sub(f.builtAt) < s.KeepFor)
	if fresh {
		s.mu.Unlock()
		select {
		case <-f.done:
			return f
		case <-ctx.Done():
			return &feed{} // gave up waiting for someone else's build
		}
	}
	f = &feed{setAt: set.UpdatedAt, done: make(chan struct{})}
	s.feeds[set.ID] = f
	s.mu.Unlock()
	pages := s.build(ctx, userID, set)
	s.mu.Lock()
	f.pages, f.builtAt = pages, s.Now()
	s.mu.Unlock()
	close(f.done)
	return f
}

// filter returns what says whether a song may be shown to a user, now:
// not one they have, turned down, want already, or by an artist they
// blocked.
func (s *Service) filter(ctx context.Context, userID string) (func(Item) bool, library.Status, error) {
	var owned *library.Owned
	var status library.Status
	if s.Library != nil {
		o, err := s.Library.For(ctx, userID)
		if err != nil {
			return nil, status, err
		}
		owned, status = o, o.Status()
	}
	down := map[string]bool{}
	fb, err := s.db.ListDiscoveryFeedback(ctx, userID)
	if err != nil {
		return nil, status, err
	}
	for _, f := range fb {
		if f.Kind == ThumbDown {
			down[f.SongKey] = true
		}
	}
	blocked := map[string]bool{}
	bs, err := s.db.ListDiscoveryBlocks(ctx, userID)
	if err != nil {
		return nil, status, err
	}
	for _, b := range bs {
		blocked[b.ArtistKey] = true
	}
	wanted := map[string]bool{}
	ws, err := s.db.ListWantItems(ctx, userID)
	if err != nil {
		return nil, status, err
	}
	for _, w := range ws {
		if w.Kind == "track" {
			wanted[w.ItemKey] = true
		}
	}
	return func(it Item) bool {
		return !down[it.Key] && !wanted[it.Key] && !blocked[library.ArtistKey(it.Artist)] &&
			!owned.Has(library.Song{Artist: it.Artist, Title: it.Title, ISRC: it.ISRC, MBID: it.MBID}) &&
			!owned.HasRelease(it.Artist, it.Album)
	}, status, nil
}

// node is an artist the walk reached.
type node struct {
	ref musicgraph.ArtistRef
	// near is how near each seed (by key) they are, 0 to 1.
	near map[string]float64
	hop  int
	seed bool
}

// pull is how near the set an artist is: the sum of how near each seed
// is, more for being near several.
func (n *node) pull() float64 {
	sum := 0.0
	for _, v := range n.near {
		sum += v
	}
	return sum * (1 + overlapBonus*float64(max(len(n.near)-1, 0)))
}

// because names the seeds an artist is nearest, nearest first: two at most.
func (n *node) because(names map[string]string) []string {
	keys := make([]string, 0, len(n.near))
	for k := range n.near {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if c := cmp.Compare(n.near[b], n.near[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	out := make([]string, 0, 2)
	for _, k := range keys[:min(len(keys), 2)] {
		out = append(out, names[k])
	}
	return out
}

// build walks the graph out from a set's seeds and returns its songs in
// pages, best first and taking turns between the seeds, without songs the
// user has or turned down, within BuildFor.
func (s *Service) build(ctx context.Context, userID string, set Set) [][]Item {
	if s.Graph == nil || len(set.Artists) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.BuildFor)
	defer cancel()
	explore := float64(set.Explore) / 100
	keep, _, err := s.filter(ctx, userID)
	if err != nil {
		slog.Warn("discover: reading what to leave out", "set", set.ID, "err", err)
		keep = func(Item) bool { return true }
	}

	known := map[string]musicgraph.Artist{}
	var missing []musicgraph.ArtistRef
	fetches := 0
	lookup := func(ref musicgraph.ArtistRef) (musicgraph.Artist, bool) {
		k := library.ArtistKey(ref.Name)
		if a, ok := known[k]; ok {
			return a, true
		}
		a, ok, err := s.Graph.CachedArtist(ctx, ref)
		if !ok && err == nil && fetches < maxFetches && ctx.Err() == nil {
			fetches++
			a, err = s.Graph.Artist(ctx, ref)
			ok = err == nil
		}
		switch {
		case ok && err == nil:
			known[k] = a
			return a, true
		case !ok && err == nil:
			missing = append(missing, ref)
		case !errors.Is(err, provider.ErrNotFound) && ctx.Err() == nil:
			slog.Debug("discover: looking up an artist", "artist", ref.Name, "err", err)
		}
		return musicgraph.Artist{}, false
	}

	names := map[string]string{}
	nodes := map[string]*node{}
	reach := func(ref musicgraph.ArtistRef, seed string, near float64, hop int) {
		k := library.ArtistKey(ref.Name)
		if k == "" {
			return
		}
		n, ok := nodes[k]
		if !ok {
			n = &node{ref: ref, near: map[string]float64{}, hop: hop}
			nodes[k] = n
		}
		n.near[seed] = max(n.near[seed], near)
		n.hop = min(n.hop, hop)
		n.ref.MBID = cmp.Or(n.ref.MBID, ref.MBID)
	}
	var seeds []*node
	for _, sa := range set.Artists {
		ref := musicgraph.ArtistRef{Name: sa.Name, MBID: sa.Mbid.String}
		k := library.ArtistKey(sa.Name)
		names[k] = sa.Name
		n := &node{ref: ref, near: map[string]float64{k: 1}, seed: true}
		seeds = append(seeds, n)
		a, ok := lookup(ref)
		if !ok {
			continue
		}
		for _, sim := range a.Similar[:min(len(a.Similar), similarPerSeed)] {
			reach(sim.Artist, k, sim.Score, 1)
		}
	}
	for _, n := range seeds {
		delete(nodes, library.ArtistKey(n.ref.Name)) // a seed isn't a new artist
	}
	// A second step, for a set that explores: from the nearest new
	// artists, as far as they lead from the seed they're nearest.
	if explore >= exploreHop2 {
		step := ranked(nodes)
		for _, n := range step[:min(len(step), hop2From)] {
			a, ok := lookup(n.ref)
			if !ok {
				continue
			}
			seed, pull := strongest(n.near)
			for _, sim := range a.Similar[:min(len(a.Similar), similarPerHop2)] {
				if _, isSeed := names[library.ArtistKey(sim.Artist.Name)]; isSeed {
					continue
				}
				reach(sim.Artist, seed, pull*sim.Score*hop2Damping, 2)
			}
		}
	}

	// Songs: the nearest artists' top songs, and the seeds' own.
	near := ranked(nodes)
	near = near[:min(len(near), songArtists)]
	top := 0.0
	for _, n := range near {
		top = max(top, n.pull())
	}
	var items []Item
	taken := map[string]bool{}
	add := func(n *node, pull float64) {
		a, ok := lookup(n.ref)
		if !ok {
			return
		}
		fame := famous(a.Fans)
		because := n.because(names)
		for _, so := range a.Top[:min(len(a.Top), songsPerArtist)] {
			artist := cmp.Or(so.Artist.Name, n.ref.Name)
			if _, variants := match.Title(so.Title); len(variants) > 0 || so.Title == "" {
				continue // live takes, remixes, demos
			}
			it := Item{
				Key: SongKey(artist, so.Title), Title: so.Title, Artist: artist, Album: so.Album,
				MBID: so.MBID, ISRC: so.ISRC, DeezerID: so.DeezerID, Cover: so.Cover,
				Because: because, Seed: n.seed,
			}
			if taken[it.Key] || !keep(it) {
				continue
			}
			taken[it.Key] = true
			pop := fame
			if so.Rank > 0 {
				pop = 0.6*so.Rank + 0.4*fame
			}
			it.Score = score(pull, pop, so.Score, explore)
			items = append(items, it)
		}
	}
	for _, n := range near {
		if ctx.Err() != nil {
			break
		}
		add(n, n.pull()/top)
	}
	for _, n := range seeds {
		if ctx.Err() != nil {
			break
		}
		add(n, seedPull)
	}
	if len(missing) > 0 {
		s.Graph.WarmArtist(missing...)
	}
	return arrange(items)
}

// famous is how well known an artist is, from their fans on Deezer, 0 to
// 1: ten million fans is as famous as it gets. Unknown is middling.
func famous(fans int64) float64 {
	if fans <= 0 {
		return 0.5
	}
	return min(math.Log10(float64(fans))/7, 1)
}

// score ranks a song: how near the set its artist is, how much of a hit
// it is among the artist's own, and how well known it is, which counts
// for it at a low explore and against it at a high one.
func score(pull, pop, hit, explore float64) float64 {
	lean := (1-explore)*pop + explore*(1-pop)
	return pull * (0.5 + 0.5*lean) * (0.6 + 0.4*hit)
}

func strongest(near map[string]float64) (string, float64) {
	var best string
	pull := -1.0
	for k, v := range near {
		if v > pull || (v == pull && k < best) {
			best, pull = k, v
		}
	}
	return best, pull
}

// ranked returns nodes nearest the set first.
func ranked(nodes map[string]*node) []*node {
	out := make([]*node, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n)
	}
	slices.SortFunc(out, func(a, b *node) int {
		if c := cmp.Compare(b.pull(), a.pull()); c != 0 {
			return c
		}
		return cmp.Compare(a.ref.Name, b.ref.Name)
	})
	return out
}

// arrange splits a feed's songs into pages: on each, the seeds take
// turns, each giving its best song left (by the seed it's nearest), and
// no artist has more than perArtist songs. So a set of a few seeds
// doesn't fill its first page with one seed's neighbors.
func arrange(items []Item) [][]Item {
	type group struct {
		best  float64
		items []Item
	}
	bySeed := map[string]*group{}
	var groups []*group
	for _, it := range items[:min(len(items), maxItems)] {
		k := ""
		if len(it.Because) > 0 {
			k = it.Because[0]
		}
		g, ok := bySeed[k]
		if !ok {
			g = &group{}
			bySeed[k] = g
			groups = append(groups, g)
		}
		g.items = append(g.items, it)
	}
	for _, g := range groups {
		slices.SortStableFunc(g.items, func(a, b Item) int { return cmp.Compare(b.Score, a.Score) })
		g.best = g.items[0].Score
	}
	slices.SortStableFunc(groups, func(a, b *group) int { return cmp.Compare(b.best, a.best) })

	var pages [][]Item
	for {
		count := map[string]int{}
		var page []Item
		for progress := true; progress && len(page) < PageSize; {
			progress = false
			for _, g := range groups {
				if len(page) == PageSize {
					break
				}
				i := slices.IndexFunc(g.items, func(it Item) bool { return count[library.ArtistKey(it.Artist)] < perArtist })
				if i < 0 {
					continue
				}
				it := g.items[i]
				g.items = slices.Delete(g.items, i, i+1)
				count[library.ArtistKey(it.Artist)]++
				page = append(page, it)
				progress = true
			}
		}
		if len(page) == 0 {
			return pages
		}
		pages = append(pages, page)
	}
}

// resolve finds the Deezer IDs of a page's songs that have none, so they
// can play a preview: from the database, or by searching Deezer once.
// What it finds is kept in the feed too.
func (s *Service) resolve(ctx context.Context, f *feed, items []Item) {
	if s.Deezer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, resolveFor)
	defer cancel()
	now := s.Now()
	searched := 0
	for i := range items {
		it := &items[i]
		if it.DeezerID != 0 {
			continue
		}
		row, err := s.db.GetDeezerSong(ctx, it.Key)
		switch {
		case err == nil && (row.DeezerID != 0 || now.Sub(row.CheckedAt) < missAgain):
		case (err == nil || store.IsNotFound(err)) && searched < resolvePerPage && ctx.Err() == nil:
			searched++
			row = store.DeezerSong{SongKey: it.Key, CheckedAt: now}
			t, err := s.Deezer.FindTrack(ctx, musicgraph.SongRef{Title: it.Title, Artist: musicgraph.ArtistRef{Name: it.Artist}, ISRC: it.ISRC})
			switch {
			case err == nil:
				row.DeezerID, row.Album, row.Cover = t.ID, t.Album, t.Cover
			case !errors.Is(err, provider.ErrNotFound):
				continue // Deezer's down: try again next time
			}
			if err := s.db.PutDeezerSong(ctx, store.PutDeezerSongParams(row)); err != nil {
				slog.Warn("discover: keeping a song's Deezer ID", "err", err)
			}
		default:
			continue
		}
		it.DeezerID, it.Album, it.Cover = row.DeezerID, cmp.Or(it.Album, row.Album), cmp.Or(it.Cover, row.Cover)
	}
	// Keep what was found, for the next time the page is served.
	found := map[string]Item{}
	for _, it := range items {
		if it.DeezerID != 0 {
			found[it.Key] = it
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, page := range f.pages {
		for i, it := range page {
			if r, ok := found[it.Key]; ok && it.DeezerID == 0 {
				page[i].DeezerID, page[i].Album, page[i].Cover = r.DeezerID, r.Album, r.Cover
			}
		}
	}
}
