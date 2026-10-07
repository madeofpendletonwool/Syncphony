// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

var now = time.Date(2026, 10, 7, 22, 0, 0, 0, time.UTC)

var nextID int

// item is a queued song by artist, added by user; autopilot's if user is "".
func item(user, artist, title string) store.QueueItem {
	nextID++
	meta, _ := json.Marshal(provider.Track{Title: title, Artists: []provider.ArtistCredit{{Name: artist}}, Album: provider.AlbumCredit{Title: title + " LP"}})
	it := store.QueueItem{ID: fmt.Sprintf("i%d", nextID), AddedBy: user, Metadata: string(meta), State: store.ItemPlayed}
	if user == "" {
		it.AddedBy = "owner"
		it.Autopilot = sql.NullString{String: "{}", Valid: true}
	}
	return it
}

// played is it played at, ended for reason.
func played(it store.QueueItem, at time.Time, reason string) store.ListHistoryRow {
	return store.ListHistoryRow{QueueItem: it, PlayHistory: store.PlayHistory{
		StartedAt: at, EndedAt: sql.NullTime{Time: at.Add(3 * time.Minute), Valid: true},
		EndReason: sql.NullString{String: reason, Valid: true},
	}}
}

// skipped is it skipped after playing for d, started at.
func skipped(it store.QueueItem, at time.Time, d time.Duration) store.ListHistoryRow {
	h := played(it, at, store.EndSkipped)
	h.PlayHistory.EndedAt.Time = at.Add(d)
	return h
}

func weight(p Profile, artist string) float64 {
	if t, ok := p.Artists[ArtistKey(artist)]; ok {
		return t.Weight
	}
	return 0
}

func TestProfileIsFairAcrossMembers(t *testing.T) {
	// Alice queued six songs by one artist, Bob one by another: they count
	// the same.
	var h []store.ListHistoryRow
	for i := range 6 {
		h = append(h, played(item("alice", "Portishead", "Song "+string(rune('a'+i))), now.Add(-time.Duration(i)*time.Minute), store.EndFinished))
	}
	h = append(h, played(item("bob", "Blur", "Song 2"), now.Add(-6*time.Minute), store.EndFinished))
	p := NewProfile(Input{History: h, Present: []string{"alice", "bob"}, Now: now})
	if a, b := weight(p, "Portishead"), weight(p, "Blur"); math.Abs(a-b) > 0.01 {
		t.Errorf("Portishead %.3f, Blur %.3f: want equal", a, b)
	}
	if fan := p.Artists["blur"].Fan(); fan != "bob" {
		t.Errorf("Blur's fan = %q", fan)
	}
	if p.Artists["portishead"].Plays != 6 || p.Artists["portishead"].Seed.ID != h[0].QueueItem.ID {
		t.Errorf("Portishead = %+v, want 6 plays seeded by the newest", p.Artists["portishead"])
	}

	// Someone in the room counts more than someone who left.
	p = NewProfile(Input{History: h, Present: []string{"bob"}, Now: now})
	if a, b := weight(p, "Portishead"), weight(p, "Blur"); b <= a {
		t.Errorf("Portishead %.3f, Blur %.3f: want Bob's, who's here, ahead", a, b)
	}
}

func TestProfileDecays(t *testing.T) {
	h := []store.ListHistoryRow{
		played(item("alice", "Now Artist", "New"), now.Add(-5*time.Minute), store.EndFinished),
		played(item("alice", "Old Artist", "Old"), now.Add(-6*time.Hour), store.EndFinished),
	}
	p := NewProfile(Input{History: h, Now: now})
	if n, o := weight(p, "Now Artist"), weight(p, "Old Artist"); o > n/4 {
		t.Errorf("now %.3f, six hours ago %.3f: want the old play to count far less", n, o)
	}
}

func TestProfileSkips(t *testing.T) {
	h := []store.ListHistoryRow{
		skipped(item("alice", "Skipped", "One"), now.Add(-1*time.Minute), 20*time.Second),
		skipped(item("bob", "Skipped", "Two"), now.Add(-2*time.Minute), 20*time.Second),
		played(item("alice", "Skipped", "Three"), now.Add(-3*time.Minute), store.EndFinished),
		skipped(item("alice", "Forgiven", "One"), now.Add(-4*time.Minute), 20*time.Second),
		played(item("bob", "Forgiven", "Two"), now.Add(-5*time.Minute), store.EndFinished),
		played(item("bob", "Forgiven", "Three"), now.Add(-6*time.Minute), store.EndFinished),
		played(item("", "Autopilot Liked", "Four"), now.Add(-7*time.Minute), store.EndFinished),
	}
	removed := item("", "Removed", "Five")
	removed.State, removed.UpdatedAt = store.ItemRemoved, now
	p := NewProfile(Input{History: h, Mine: []store.QueueItem{removed}, Now: now})
	if !p.Avoid["skipped"] || weight(p, "Skipped") != 0 {
		t.Errorf("Skipped: avoid %v, weight %v: want it turned away", p.Avoid["skipped"], weight(p, "Skipped"))
	}
	if p.Avoid["forgiven"] || weight(p, "Forgiven") == 0 {
		t.Error("Forgiven was played through more than it was skipped")
	}
	if weight(p, "Autopilot Liked") == 0 || p.Artists["autopilot liked"].Fan() != "" {
		t.Error("an autopilot song the room let play should count, as nobody's")
	}
	if !p.Avoid["removed"] {
		t.Error("an autopilot song someone removed should turn its artist away")
	}
	if want := []string{"skipped", "skipped", "skipped", "forgiven", "forgiven", "forgiven", "autopilot liked"}; !slices.Equal(p.Recent, want[:recentReach-1]) {
		t.Errorf("recent = %v", p.Recent)
	}
}

// A skip long ago, decayed to almost nothing, doesn't turn an artist away
// (MAD-762); a recent one does.
func TestProfileSkipsFade(t *testing.T) {
	cases := []struct {
		name  string
		h     []store.ListHistoryRow
		avoid bool
	}{
		{"two days ago", []store.ListHistoryRow{skipped(item("alice", "A", "x"), now.Add(-48*time.Hour), 20*time.Second)}, false},
		{"minutes ago", []store.ListHistoryRow{skipped(item("alice", "A", "x"), now.Add(-5*time.Minute), 20*time.Second)}, true},
		{"two skips and a play-through", []store.ListHistoryRow{
			skipped(item("alice", "A", "x"), now.Add(-5*time.Minute), 20*time.Second),
			skipped(item("bob", "A", "y"), now.Add(-10*time.Minute), 20*time.Second),
			played(item("bob", "A", "z"), now.Add(-15*time.Minute), store.EndFinished),
		}, true},
	}
	for _, c := range cases {
		p := NewProfile(Input{History: c.h, Now: now})
		if p.Avoid["a"] != c.avoid {
			t.Errorf("%s: avoid = %v, want %v", c.name, p.Avoid["a"], c.avoid)
		}
	}
	// One skip with a play-through: not turned away, but in doubt.
	p := NewProfile(Input{History: []store.ListHistoryRow{
		skipped(item("alice", "A", "x"), now.Add(-5*time.Minute), 20*time.Second),
		played(item("bob", "A", "z"), now.Add(-15*time.Minute), store.EndFinished),
		played(item("bob", "B", "z"), now.Add(-16*time.Minute), store.EndFinished),
		skipped(item("alice", "B", "x"), now.Add(-17*time.Minute), 3*time.Minute),
	}, Now: now})
	if p.Avoid["a"] || p.Doubt["a"] != 0 || p.Avoid["b"] {
		t.Errorf("avoid %v, doubt %v", p.Avoid, p.Doubt)
	}
}

// A skip in the first seconds says more than one near the end.
func TestSkipTiming(t *testing.T) {
	song := item("alice", "A", "x")
	meta, _ := json.Marshal(provider.Track{Title: "x", Artists: []provider.ArtistCredit{{Name: "A"}}, Duration: 4 * time.Minute})
	song.Metadata = string(meta)
	quick := skipSignal(skipped(song, now, 5*time.Second))
	early := skipSignal(skipped(song, now, 30*time.Second))
	late := skipSignal(skipped(song, now, 160*time.Second))
	if !(quick < early && early < late && late < 0) {
		t.Errorf("skip at 0:05 %.2f, 0:30 %.2f, 2:40 %.2f: want each to say less", quick, early, late)
	}
	if quick != quickSkipSignal || math.Abs(late-lerp(skippedSignal, lateSkipSignal, 160.0/240)) > 1e-9 {
		t.Errorf("quick %.2f, late %.2f", quick, late)
	}
}

// Hearts, songs queued again, and songs queued after one of autopilot's
// by the same or a similar artist all count.
func TestProfileStrongerSignals(t *testing.T) {
	base := func() []store.ListHistoryRow {
		return []store.ListHistoryRow{
			played(item("alice", "Hearted", "x"), now.Add(-10*time.Minute), store.EndFinished),
			played(item("alice", "Plain", "y"), now.Add(-20*time.Minute), store.EndFinished),
		}
	}
	h := base()
	p := NewProfile(Input{History: h, Hearts: map[string]int{h[0].QueueItem.ID: 2}, Now: now})
	if weight(p, "Hearted") <= weight(p, "Plain") {
		t.Errorf("hearted %.3f, plain %.3f: want the hearts to count", weight(p, "Hearted"), weight(p, "Plain"))
	}

	// Bob queues Plain's song again, an hour after it played.
	h = base()
	again := item("bob", "Plain", "y")
	again.State, again.AddedAt = store.ItemQueued, now.Add(-5*time.Minute)
	other := item("bob", "Other", "z")
	other.State, other.AddedAt = store.ItemQueued, now.Add(-5*time.Minute)
	p = NewProfile(Input{History: h, Upcoming: []store.QueueItem{again, other}, Now: now})
	if weight(p, "Plain") <= weight(p, "Other")*1.2 {
		t.Errorf("queued again %.3f, other %.3f: want the re-add to count more", weight(p, "Plain"), weight(p, "Other"))
	}

	// Autopilot played Pilot Pick, and Bob queued a song by an artist like
	// them while it played.
	pick := item("", "Pilot Pick", "p")
	plain := item("", "Pilot Plain", "q")
	h = []store.ListHistoryRow{
		played(pick, now.Add(-10*time.Minute), store.EndFinished),
		played(plain, now.Add(-20*time.Minute), store.EndFinished),
	}
	follow := item("bob", "Pilot Pick Friend", "f")
	follow.State, follow.AddedAt = store.ItemQueued, now.Add(-9*time.Minute)
	related := func(a, b string) bool { return a == "pilot pick" && b == "pilot pick friend" }
	p = NewProfile(Input{History: h, Upcoming: []store.QueueItem{follow}, Related: related, Now: now})
	if weight(p, "Pilot Pick") <= weight(p, "Pilot Plain") || p.Artists["pilot pick"].ByMember["bob"] == 0 {
		t.Errorf("followed %+v, plain %.3f: want Bob's follow-up to count for it", p.Artists["pilot pick"], weight(p, "Pilot Plain"))
	}
}

// When the members' latest songs are unlike the night's, they count more.
func TestProfileFollowsVibeChange(t *testing.T) {
	var h []store.ListHistoryRow
	at := now
	for i := range 8 {
		at = at.Add(-4 * time.Minute)
		h = append(h, played(item("alice", "Miles Davis", fmt.Sprint("jazz ", i)), at, store.EndFinished))
	}
	for i := range 20 {
		at = at.Add(-4 * time.Minute)
		h = append(h, played(item("alice", "Metallica", fmt.Sprint("metal ", i)), at, store.EndFinished))
	}
	in := Input{History: h, Now: now, Tags: map[string][]musicgraph.Tag{
		"miles davis": {{Name: "jazz", Weight: 1}}, "metallica": {{Name: "metal", Weight: 1}},
	}}
	p := NewProfile(in)
	if !p.Shifted {
		t.Fatal("the latest songs are all jazz, after an hour of metal: want a change of vibe")
	}
	unboosted := tastes(in, reactions(in, profileReach), time.Time{})
	if weight(p, "Miles Davis") <= weight(unboosted, "Miles Davis") || weight(p, "Miles Davis") != 1 {
		t.Errorf("Miles Davis %.3f, without following the change %.3f", weight(p, "Miles Davis"), weight(unboosted, "Miles Davis"))
	}
	// A settled night doesn't shift.
	if p := NewProfile(Input{History: h[8:], Now: now, Tags: in.Tags}); p.Shifted {
		t.Error("all metal: no change of vibe")
	}
}

func TestProfileHeardIgnoresVersions(t *testing.T) {
	waiting := item("alice", "Radiohead", "Creep - Remastered 2009")
	waiting.State = store.ItemQueued
	p := NewProfile(Input{Upcoming: []store.QueueItem{waiting}, Now: now})
	if !p.Heard[SongKey("radiohead", "Creep")] || !p.Heard[SongKey("Radiohead", "Creep (Live)")] {
		t.Errorf("heard = %v: want Creep in any version", p.Heard)
	}
	if weight(p, "Radiohead") != 1 {
		t.Error("a song waiting is a member's choice: it should count")
	}
}

// graph is a music graph in memory. Artists in cached answer CachedArtist;
// the rest only Artist, as if fetched.
type graph struct {
	mu      sync.Mutex
	artists map[string]musicgraph.Artist
	cached  map[string]bool
	tracks  map[string]musicgraph.Track
	warmed  []string
	fetched []string
	// warmedSongs are the titles of songs WarmTrack was asked for.
	warmedSongs []string
}

func (g *graph) Artist(_ context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fetched = append(g.fetched, a.Name)
	if out, ok := g.artists[ArtistKey(a.Name)]; ok {
		return out, nil
	}
	return musicgraph.Artist{}, provider.ErrNotFound
}

func (g *graph) CachedArtist(_ context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := ArtistKey(a.Name)
	if !g.cached[k] {
		return musicgraph.Artist{}, false, nil
	}
	if out, ok := g.artists[k]; ok {
		return out, true, nil
	}
	return musicgraph.Artist{}, true, provider.ErrNotFound
}

func (g *graph) CachedTrack(_ context.Context, s musicgraph.SongRef) (musicgraph.Track, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if out, ok := g.tracks[SongKey(s.Artist.Name, s.Title)]; ok {
		return out, true, nil
	}
	return musicgraph.Track{}, false, nil
}

func (g *graph) WarmArtist(as ...musicgraph.ArtistRef) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, a := range as {
		g.warmed = append(g.warmed, a.Name)
	}
}

func (g *graph) WarmTrack(ss ...musicgraph.SongRef) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range ss {
		g.warmedSongs = append(g.warmedSongs, s.Title)
	}
}

func artist(name string, similar map[string]float64, top ...string) musicgraph.Artist {
	a := musicgraph.Artist{Ref: musicgraph.ArtistRef{Name: name}}
	for n, s := range similar {
		a.Similar = append(a.Similar, musicgraph.Similar{Artist: musicgraph.ArtistRef{Name: n}, Score: s, Sources: []string{"lastfm"}})
	}
	slices.SortFunc(a.Similar, func(x, y musicgraph.Similar) int { return int((y.Score - x.Score) * 100) })
	for i, t := range top {
		a.Top = append(a.Top, musicgraph.Song{SongRef: musicgraph.SongRef{Title: t, Artist: a.Ref}, Score: 1 - float64(i)*0.3})
	}
	return a
}

func newGraph(as ...musicgraph.Artist) *graph {
	g := &graph{artists: map[string]musicgraph.Artist{}, cached: map[string]bool{}, tracks: map[string]musicgraph.Track{}}
	for _, a := range as {
		k := ArtistKey(a.Ref.Name)
		g.artists[k] = a
		g.cached[k] = true
	}
	return g
}

func titles(cs []candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.song.Title)
	}
	return out
}

func find(cs []candidate, title string) (candidate, bool) {
	i := slices.IndexFunc(cs, func(c candidate) bool { return c.song.Title == title })
	if i < 0 {
		return candidate{}, false
	}
	return cs[i], true
}

func TestWalk(t *testing.T) {
	g := newGraph(
		artist("Massive Attack", map[string]float64{"Portishead": 0.9, "Tricky": 0.5, "Skipped Band": 0.95, "Uncached": 0.4},
			"Teardrop", "Angel", "Unfinished Sympathy (Live)"),
		artist("Portishead", map[string]float64{"Beth Gibbons": 0.8}, "Glory Box", "Roads", "Sour Times"),
		artist("Tricky", nil, "Hell Is Round the Corner"),
		artist("Beth Gibbons", nil, "Floating on a Moment"),
		artist("Skipped Band", nil, "Nope"),
	)
	g.artists["uncached"] = artist("Uncached", nil, "Later")
	h := []store.ListHistoryRow{
		played(item("alice", "Massive Attack", "Angel"), now.Add(-time.Minute), store.EndFinished),
		played(item("bob", "Skipped Band", "Nah"), now.Add(-2*time.Minute), store.EndSkipped),
	}
	p := NewProfile(Input{History: h, Now: now})
	e := &Engine{Graph: g, Rand: func(int) int { return 0 }}

	cands := e.walk(t.Context(), p, LongTerm{}, 0, nil)
	got := titles(cands)
	for _, want := range []string{"Teardrop", "Glory Box", "Roads", "Hell Is Round the Corner"} {
		if !slices.Contains(got, want) {
			t.Errorf("candidates %v: missing %s", got, want)
		}
	}
	for _, not := range []string{"Angel", "Unfinished Sympathy (Live)", "Nope", "Floating on a Moment"} {
		if slices.Contains(got, not) {
			t.Errorf("candidates %v: %s shouldn't be (heard, a live take, turned away, two steps out)", got, not)
		}
	}
	gb, _ := find(cands, "Glory Box")
	if gb.kind != KindSimilarArtist || gb.hop != 1 || gb.via != "massive attack" || gb.popularity != 1 {
		t.Errorf("Glory Box = %+v", gb)
	}
	if td, _ := find(cands, "Teardrop"); td.kind != KindArtist || td.affinity <= gb.affinity {
		t.Errorf("Teardrop = %+v: want the room's own artist, nearer than Portishead", td)
	}
	if !slices.Contains(g.fetched, "Uncached") || !slices.Contains(got, "Later") {
		t.Errorf("fetched %v: want the artist that wasn't cached fetched, within the budget", g.fetched)
	}

	// Exploring reaches two steps out.
	cands = e.walk(t.Context(), p, LongTerm{}, 0.8, nil)
	if c, ok := find(cands, "Floating on a Moment"); !ok || c.kind != KindTwoSteps || c.via != "massive attack" {
		t.Errorf("exploring: Floating on a Moment = %+v, %v", c, ok)
	}
}

func TestWalkFetchBudget(t *testing.T) {
	names := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
	g := newGraph()
	var h []store.ListHistoryRow
	for i, n := range names {
		g.artists[ArtistKey(n)] = artist(n, nil, n+" hit")
		h = append(h, played(item("u"+n, n, "song"), now.Add(-time.Duration(i)*time.Minute), store.EndFinished))
	}
	p := NewProfile(Input{History: h, Now: now})
	e := &Engine{Graph: g, Rand: func(int) int { return 0 }}
	cands := e.walk(t.Context(), p, LongTerm{}, 0, nil)
	if len(g.fetched) != maxFetches {
		t.Errorf("fetched %v, want %d", g.fetched, maxFetches)
	}
	if len(g.warmed) != len(names)-maxFetches {
		t.Errorf("warmed %v, want the rest", g.warmed)
	}
	if len(cands) != maxFetches {
		t.Errorf("candidates %v: want the hits of the artists fetched", titles(cands))
	}
}

func TestSimilarSongs(t *testing.T) {
	g := newGraph(artist("Radiohead", nil), artist("Portishead", nil, "Glory Box", "Roads"))
	g.tracks[SongKey("Radiohead", "Creep")] = musicgraph.Track{Similar: []musicgraph.Song{
		{SongRef: musicgraph.SongRef{Title: "Roads", Artist: musicgraph.ArtistRef{Name: "Portishead"}}, Score: 0.9},
	}}
	p := NewProfile(Input{History: []store.ListHistoryRow{played(item("alice", "Radiohead", "Creep"), now, store.EndFinished)}, Now: now})
	cands := (&Engine{Graph: g}).walk(t.Context(), p, LongTerm{}, 0, nil)
	c, ok := find(cands, "Roads")
	if !ok || c.kind != KindSimilarSong || c.via != "radiohead" {
		t.Errorf("Roads = %+v, %v: want a song like Creep", c, ok)
	}
}

func cand(title, artist string, hop int, affinity, popularity float64) candidate {
	return candidate{
		song: musicgraph.SongRef{Title: title, Artist: musicgraph.ArtistRef{Name: artist}}, artist: ArtistKey(artist),
		kind: kindOf(hop), hop: hop, affinity: affinity, popularity: popularity,
	}
}

func TestScoreFollowsExplore(t *testing.T) {
	p := Profile{Artists: map[string]*Taste{"home": {Weight: 1, Plays: 1}}}
	cs := []candidate{
		cand("Home Hit", "Home", 0, 1, 1),
		cand("Neighbor Hit", "Neighbor", 1, 0.8, 1),
		cand("Far Hit", "Far", 2, 0.4, 1),
		cand("Neighbor Deep Cut", "Neighbor", 1, 0.8, 0.1),
	}
	if got := titles(score(cs, p, LongTerm{}, 0, false)); got[0] != "Home Hit" || got[len(got)-1] != "Neighbor Deep Cut" {
		t.Errorf("familiar: %v, want the room's artist's hit first and a deep cut last", got)
	}
	if got := titles(score(cs, p, LongTerm{}, 1, false)); got[0] != "Far Hit" {
		t.Errorf("exploring: %v, want the far artist first", got)
	}
}

func TestScoreSpacesArtists(t *testing.T) {
	p := Profile{Artists: map[string]*Taste{}, Recent: []string{"just played", "x", "y", "z", "earlier"}}
	cs := []candidate{
		cand("Again", "Just Played", 1, 1, 1),
		cand("Bit Ago", "Earlier", 1, 1, 1),
		cand("Fresh", "Fresh", 1, 0.9, 0.9),
	}
	if got := titles(score(cs, p, LongTerm{}, 0.3, false)); !slices.Equal(got, []string{"Fresh", "Bit Ago", "Again"}) {
		t.Errorf("got %v: want the artist just heard last, one heard a few songs ago in between", got)
	}
}

func TestScoreDeepCuts(t *testing.T) {
	p := Profile{Artists: map[string]*Taste{"loved": {Weight: 1, Plays: lovedPlays}, "liked": {Weight: 1, Plays: 1}}}
	cs := []candidate{
		cand("Loved Hit", "Loved", 0, 1, 1),
		cand("Loved Deep Cut", "Loved", 0, 1, 0.1),
		cand("Liked Deep Cut", "Liked", 0, 1, 0.1),
	}
	got := score(cs, p, LongTerm{}, 0, true)
	if got[0].song.Title != "Loved Deep Cut" || !got[0].deepCut {
		t.Errorf("deep cut pick: %v, want the loved artist's deep cut first", titles(got))
	}
	if c, _ := find(got, "Liked Deep Cut"); c.deepCut {
		t.Error("an artist the room barely knows isn't one to dig into")
	}
}

func TestDraw(t *testing.T) {
	scored := []candidate{{score: 0.9, artist: "x"}, {score: 0.8, artist: "y"}, {score: 0.1, artist: "z"}}
	scored[0].song.Title, scored[1].song.Title, scored[2].song.Title = "a", "b", "c"
	if got := titles(draw(scored, 3, 0.05, func(int) int { return 0 })); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("lowest draws: %v, want best first", got)
	}
	// The top end of the draw reaches the second best at a low temperature,
	// but almost never one far behind.
	if got := titles(draw(scored, 1, 0.05, func(n int) int { return n - 1 })); got[0] != "b" {
		t.Errorf("highest draw: %v, want the runner-up", got)
	}
	counts := map[string]int{}
	for i := range 1000 {
		r := func(n int) int { return i * n / 1000 }
		counts[draw(scored, 1, 0.05, r)[0].song.Title]++
	}
	if counts["a"] < 800 || counts["c"] > 0 {
		t.Errorf("spread over draws: %v, want mostly the best and never the worst", counts)
	}
}

func TestDrawChoosesBetweenArtists(t *testing.T) {
	var scored []candidate
	for i, a := range []string{"Blur", "Blur", "Blur", "Blur", "Pulp"} {
		c := cand(fmt.Sprintf("%s %d", a, i), a, 1, 1, 1)
		c.score = 1 - float64(i)*0.01
		scored = append(scored, c)
	}
	got := titles(draw(scored, 5, 0.05, func(int) int { return 0 }))
	if !slices.Equal(got, []string{"Blur 0", "Blur 1", "Pulp 4"}) {
		t.Errorf("drawn %v: want Blur's best two, then Pulp", got)
	}
}
