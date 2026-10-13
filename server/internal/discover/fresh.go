// SPDX-License-Identifier: AGPL-3.0-only

package discover

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/releases"
)

// The Fresh feed (MAD-823): recent releases by a set's seeds and their
// neighbors, "new releases for you". Releases come from internal/releases,
// cached a day per artist and warmed in the background; the feed reads the
// cache, fetches a few seeds' that aren't cached, and warms the rest.
const (
	// freshNeighbors and freshExploreNeighbors are how many of the
	// nearest new artists' releases a Fresh feed looks at: more for a set
	// that explores.
	freshNeighbors        = 15
	freshExploreNeighbors = 35
	// freshFetches is how many artists whose releases aren't cached a
	// feed fetches, seeds first, within freshFetchFor; the rest are
	// warmed, and the feed says it's partial.
	freshFetches  = 4
	freshFetchFor = 6 * time.Second
	// freshPerArtist is the most releases of one artist in a feed, and
	// maxFresh the most in all.
	freshPerArtist = 3
	maxFresh       = 100
	// warmNeighbors is how many of each seed's nearest artists have their
	// releases warmed in the background, and warmEvery how often.
	warmNeighbors = 10
	warmEvery     = 6 * time.Hour
	tracksForFeed = 10 * time.Second
)

// Releases finds artists' recent releases. releases.Service is one.
type Releases interface {
	On() bool
	Cached(ctx context.Context, a musicgraph.ArtistRef) ([]releases.Release, bool, error)
	Get(ctx context.Context, a musicgraph.ArtistRef) ([]releases.Release, error)
	Warm(as ...musicgraph.ArtistRef)
	Tracks(ctx context.Context, artist, title string, deezerID int64) (musicgraph.DeezerAlbum, []musicgraph.DeezerTrack, error)
}

// FreshItem is a release in a Fresh feed.
type FreshItem struct {
	releases.Release
	// Because are the seed artists it's near, nearest first: the seed
	// itself for a seed's release.
	Because []string
	// Seed is whether it's a seed artist's.
	Seed  bool
	Score float64
}

// Fresh is a set's Fresh feed.
type Fresh struct {
	Items []FreshItem
	// Days is how far back it looks.
	Days int
	// Partial is whether some artists' releases weren't known yet:
	// they're being looked up, and loading the feed again shows more.
	Partial bool
	Library library.Status
	// NoGraph is whether the server knows nothing of how music relates,
	// and NoReleases whether it has no source of releases: either way,
	// the feed is empty or only the seeds'.
	NoGraph, NoReleases bool
}

// Fresh returns a set's recent releases: the seeds' first, then their
// neighbors', nearest first; albums and EPs before singles, and
// compilations and live albums last. Releases the user has, wants, or by
// artists they blocked are left out.
func (s *Service) Fresh(ctx context.Context, userID, setID string) (Fresh, error) {
	set, err := s.Get(ctx, userID, setID)
	if err != nil {
		return Fresh{}, err
	}
	out := Fresh{Items: []FreshItem{}, Days: int(cmp.Or(set.FreshDays, DefaultFreshDays)), NoGraph: s.Graph == nil}
	out.NoReleases = s.Releases == nil || !s.Releases.On()
	keep, status, err := s.releaseFilter(ctx, userID)
	if err != nil {
		return Fresh{}, err
	}
	out.Library = status
	if out.NoReleases || len(set.Artists) == 0 {
		return out, nil
	}
	fetchBy := time.Now().Add(freshFetchFor)

	// Who: the seeds, and their nearest neighbors.
	explore := float64(set.Explore) / 100
	m := &mapper{roots: map[string]*node{}, nodes: map[string]*node{}, blocked: map[string]bool{}}
	if s.Graph != nil {
		m.lk = &looker{g: s.Graph, ctx: ctx, budget: mapFetches, fetchBy: fetchBy}
	}
	var roots []musicgraph.ArtistRef
	names := map[string]string{}
	for _, a := range set.Artists {
		roots = append(roots, musicgraph.ArtistRef{Name: a.Name, MBID: a.Mbid.String})
		names[library.ArtistKey(a.Name)] = a.Name
	}
	n := freshNeighbors + int(math.Round(freshExploreNeighbors*explore))
	near := m.walk(roots, 1, len(roots)+n)
	near = near[:min(len(near), n)]
	if m.lk != nil && m.lk.warm() {
		out.Partial = true
	}
	top := 0.0
	for _, nd := range near {
		top = max(top, nd.pull())
	}

	rl := &releaseLooker{r: s.Releases, ctx: ctx, budget: freshFetches, fetchBy: fetchBy}
	cutoff := s.Now().AddDate(0, 0, -out.Days)
	byKey := map[string]int{}
	add := func(nd *node, seed bool) {
		rs, ok := rl.get(nd.ref, seed)
		if !ok {
			return
		}
		pull, lean := 1.0, 1.0
		if !seed {
			pull = nd.pull() / top
			fame := 0.5
			if a, ok := m.get(nd.ref); ok {
				fame = famous(a.Fans)
			}
			lean = 0.5 + 0.5*((1-explore)*fame+explore*(1-fame))
		}
		because := nd.because(names)
		taken := 0
		for _, r := range rs {
			day, ok := r.Day()
			if !ok || day.Before(cutoff) || taken == freshPerArtist || !keep(nd.ref.Name, r) {
				continue
			}
			age := s.Now().Sub(day).Hours() / 24
			it := FreshItem{
				Release: r, Because: because, Seed: seed,
				Score: pull * lean * kindWeight(r.Kind) * (0.8 + 0.2*max(0, 1-age/float64(out.Days))),
			}
			// A release by two artists in the set shows once, as theirs
			// it suits best.
			if i, dup := byKey[r.Key]; dup {
				if better(it, out.Items[i]) {
					out.Items[i] = it
				}
				continue
			}
			byKey[r.Key] = len(out.Items)
			out.Items = append(out.Items, it)
			taken++
		}
	}
	for _, nd := range m.order {
		add(nd, true)
	}
	for _, nd := range near {
		add(nd, false)
	}
	if rl.warm() {
		out.Partial = true
	}
	slices.SortStableFunc(out.Items, func(a, b FreshItem) int {
		if better(a, b) {
			return -1
		}
		if better(b, a) {
			return 1
		}
		return cmp.Compare(a.Key, b.Key)
	})
	out.Items = out.Items[:min(len(out.Items), maxFresh)]
	return out, nil
}

// better says whether a ranks above b: compilations and live albums
// last, then the seeds' releases, then albums and EPs before singles,
// then the higher score, then the newer.
func better(a, b FreshItem) bool {
	if x, y := minor(a.Kind), minor(b.Kind); x != y {
		return y
	}
	if a.Seed != b.Seed {
		return a.Seed
	}
	if x, y := a.Kind == releases.KindSingle, b.Kind == releases.KindSingle; x != y {
		return y
	}
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.Date > b.Date
}

func minor(kind string) bool { return kind == releases.KindCompilation || kind == releases.KindLive }

// kindWeight is how much a kind of release counts.
func kindWeight(kind string) float64 {
	switch kind {
	case releases.KindAlbum, releases.KindEP:
		return 1
	case releases.KindSingle:
		return 0.75
	}
	return 0.4
}

// releaseFilter returns what says whether a release may be shown to a
// user, by an artist it was found under: not one they have or want, nor
// by an artist they blocked.
func (s *Service) releaseFilter(ctx context.Context, userID string) (func(artist string, r releases.Release) bool, library.Status, error) {
	var owned *library.Owned
	var status library.Status
	if s.Library != nil {
		o, err := s.Library.For(ctx, userID)
		if err != nil {
			return nil, status, err
		}
		owned, status = o, o.Status()
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
		if w.Kind == "release" {
			wanted[w.ItemKey] = true
		}
	}
	return func(artist string, r releases.Release) bool {
		return !blocked[library.ArtistKey(artist)] && !blocked[library.ArtistKey(r.Artist)] &&
			!wanted[r.Key] && !wanted[library.ReleaseKey(r.Artist, r.Title)] &&
			!owned.HasRelease(artist, r.Title) && !owned.HasRelease(r.Artist, r.Title)
	}, status, nil
}

// releaseLooker looks artists' releases up for one feed: from the cache,
// or fetched while there's budget and time left. A fetch still going then
// carries on in the background. Those it couldn't find are warmed.
type releaseLooker struct {
	r       Releases
	ctx     context.Context
	budget  int
	fetchBy time.Time
	missing []musicgraph.ArtistRef
}

func (l *releaseLooker) get(a musicgraph.ArtistRef, fetch bool) ([]releases.Release, bool) {
	rs, ok, err := l.r.Cached(l.ctx, a)
	if err != nil {
		slog.Warn("discover: reading an artist's releases", "artist", a.Name, "err", err)
		return nil, false
	}
	if ok {
		return rs, true
	}
	if fetch && l.budget > 0 && time.Now().Before(l.fetchBy) {
		l.budget--
		type answer struct {
			rs  []releases.Release
			err error
		}
		got := make(chan answer, 1)
		go func() {
			// Not cut short: what it finds is cached for next time.
			rs, err := l.r.Get(context.WithoutCancel(l.ctx), a)
			got <- answer{rs, err}
		}()
		wait := time.NewTimer(time.Until(l.fetchBy))
		defer wait.Stop()
		select {
		case r := <-got:
			if r.err == nil {
				return r.rs, true
			}
			if !errors.Is(r.err, provider.ErrNotFound) {
				slog.Debug("discover: fetching an artist's releases", "artist", a.Name, "err", r.err)
			}
			return nil, false
		case <-wait.C:
		case <-l.ctx.Done():
		}
	}
	l.missing = append(l.missing, a)
	return nil, false
}

// warm asks for the artists it couldn't find, and reports whether there
// were any.
func (l *releaseLooker) warm() bool {
	if len(l.missing) > 0 {
		l.r.Warm(l.missing...)
	}
	return len(l.missing) > 0
}

// ReleaseTracks lists a release's songs, each with a preview when Deezer
// has one: by its Deezer ID, or found on Deezer by artist and title.
func (s *Service) ReleaseTracks(ctx context.Context, artist, title string, deezerID int64) ([]Item, error) {
	artist = strings.Join(strings.Fields(artist), " ")
	title = strings.Join(strings.Fields(title), " ")
	if artist == "" || title == "" {
		return nil, &InvalidInputError{"a release's artist and title are needed"}
	}
	if s.Releases == nil {
		return []Item{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, tracksForFeed)
	defer cancel()
	album, tracks, err := s.Releases.Tracks(ctx, artist, title, deezerID)
	if errors.Is(err, provider.ErrNotFound) {
		return []Item{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(tracks))
	for _, t := range tracks {
		a := cmp.Or(t.Artist, artist)
		out = append(out, Item{
			Key: SongKey(a, t.Title), Title: t.Title, Artist: a, Album: cmp.Or(album.Title, title), ISRC: t.ISRC,
			DeezerID: t.ID, Cover: cmp.Or(t.Cover, album.Cover), Because: []string{},
		})
	}
	return out, nil
}

// WarmReleases asks for the releases of every set's seeds, and their
// nearest neighbors the graph knows, to be fetched in the background:
// those not cached already.
func (s *Service) WarmReleases(ctx context.Context) error {
	if s.Releases == nil || !s.Releases.On() {
		return nil
	}
	seeds, err := s.db.ListAllDiscoverySetArtists(ctx)
	if err != nil {
		return err
	}
	var want []musicgraph.ArtistRef
	seen := map[string]bool{}
	consider := func(a musicgraph.ArtistRef) {
		k := library.ArtistKey(a.Name)
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		if _, ok, err := s.Releases.Cached(ctx, a); err == nil && !ok {
			want = append(want, a)
		}
	}
	for _, sa := range seeds {
		consider(musicgraph.ArtistRef{Name: sa.Name, MBID: sa.Mbid})
	}
	// The neighbors after every seed, so seeds go first.
	if s.Graph != nil {
		for _, sa := range seeds {
			a, ok, err := s.Graph.CachedArtist(ctx, musicgraph.ArtistRef{Name: sa.Name, MBID: sa.Mbid})
			if !ok || err != nil {
				continue
			}
			for _, sim := range a.Similar[:min(len(a.Similar), warmNeighbors)] {
				consider(sim.Artist)
			}
		}
	}
	s.Releases.Warm(want...)
	return ctx.Err()
}

// Run warms every set's releases now and then, until ctx is done.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := s.WarmReleases(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("discover: warming releases", "err", err)
		}
		t.Reset(warmEvery)
	}
}
