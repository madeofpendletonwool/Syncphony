// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"database/sql"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// room is a room in a real database, for the DJ's memory.
type room struct {
	t     *testing.T
	db    *store.Store
	id    string
	users map[string]string // name → ID
}

func newRoom(t *testing.T) *room {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &room{t: t, db: db, users: map[string]string{}}
	for _, name := range []string{"alice", "bob"} {
		u, err := db.CreateUser(t.Context(), store.CreateUserParams{
			ID: store.NewID(), Username: name, DisplayName: name, Color: "#000000", Role: store.RoleMember, CreatedAt: store.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		r.users[name] = u.ID
	}
	rm, err := db.CreateRoom(t.Context(), store.CreateRoomParams{
		ID: store.NewID(), Name: "Living room", OwnerID: r.users["alice"], FairnessMode: store.FairnessRoundRobin, Settings: "{}", CreatedAt: store.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.id = rm.ID
	return r
}

// play adds a song queued by user ("" for autopilot, picked as kind) and
// plays it at at for d, ended for reason.
func (r *room) play(user, kind, artist, title string, at time.Time, d time.Duration, reason string) {
	r.t.Helper()
	ctx := r.t.Context()
	meta := fmt.Sprintf(`{"Title":%q,"Artists":[{"Name":%q}],"Duration":%d}`, title, artist, 4*time.Minute)
	p := store.AddQueueItemParams{ID: store.NewID(), RoomID: r.id, AddedBy: r.users[user], Provider: "fake", TrackID: title, Metadata: meta, Now: at.Add(-time.Minute)}
	if user == "" {
		p.AddedBy = r.users["alice"]
		p.Autopilot = sql.NullString{String: fmt.Sprintf(`{"reason":{"kind":%q}}`, kind), Valid: true}
	}
	it, err := r.db.AddQueueItem(ctx, p)
	if err != nil {
		r.t.Fatal(err)
	}
	ph, err := r.db.StartPlay(ctx, store.StartPlayParams{ID: store.NewID(), RoomID: r.id, QueueItemID: it.ID, StartedAt: at})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.db.EndPlay(ctx, store.EndPlayParams{
		ID: ph.ID, EndedAt: sql.NullTime{Time: at.Add(d), Valid: true}, EndReason: sql.NullString{String: reason, Valid: true},
	}); err != nil {
		r.t.Fatal(err)
	}
}

// night plays songs by an artist, alice and bob taking turns, through an
// evening that starts at start, and ends the night.
func (r *room) night(artist string, start time.Time, songs int) {
	r.t.Helper()
	at := start
	for i := range songs {
		r.play([]string{"alice", "bob"}[i%2], "", artist, fmt.Sprintf("%s %s %d", artist, start.Format("Jan 2"), i), at, 4*time.Minute, store.EndFinished)
		at = at.Add(4 * time.Minute)
	}
	if _, err := r.db.CreateNight(r.t.Context(), store.CreateNightParams{
		ID: store.NewID(), RoomID: r.id, StartedAt: start, EndedAt: at, EndedBy: "idle", Plays: int64(songs),
	}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *room) history() []store.ListHistoryRow {
	r.t.Helper()
	h, err := r.db.ListHistory(r.t.Context(), store.ListHistoryParams{RoomID: r.id, Limit: 200})
	if err != nil {
		r.t.Fatal(err)
	}
	return h
}

// band is an artist with tags and plenty of songs.
func band(name string, tags []string, similar map[string]float64) musicgraph.Artist {
	a := artist(name, similar)
	for i := range 15 {
		a.Top = append(a.Top, musicgraph.Song{SongRef: musicgraph.SongRef{Title: fmt.Sprintf("%s hit %d", name, i), Artist: a.Ref}, Score: 1 - float64(i)*0.05})
	}
	for _, t := range tags {
		a.Tags = append(a.Tags, musicgraph.Tag{Name: t, Weight: 1})
	}
	return a
}

var (
	psych  = []string{"psychedelic rock"}
	sixty  = []string{"60s", "british invasion"}
	garage = []string{"indie rock", "garage rock"}
)

// longRunningRoom is a room with weeks of history: three weeks ago, lots
// of Arctic Monkeys; then a week of the Beatles; tonight, a few songs of
// Tame Impala. Its graph has Tame Impala's psychedelic neighbors, then the
// Beatles, with the Kinks and the Strokes a little further. With amNear,
// the Arctic Monkeys are as near as the Beatles, and like the Strokes.
func longRunningRoom(t *testing.T, amNear bool) (*room, *graph) {
	r := newRoom(t)
	evening := func(daysAgo int) time.Time { return now.Add(-time.Duration(daysAgo)*24*time.Hour - 3*time.Hour) }
	for d := 21; d >= 15; d-- {
		r.night("Arctic Monkeys", evening(d), 8)
	}
	for d := 7; d >= 1; d-- {
		r.night("The Beatles", evening(d), 8)
	}
	for i := range 3 {
		r.play([]string{"alice", "bob", "alice"}[i], "", "Tame Impala", fmt.Sprint("Tonight ", i), now.Add(-time.Duration(12-4*i)*time.Minute), 4*time.Minute, store.EndFinished)
	}
	near := map[string]float64{
		"Pond": 0.9, "King Gizzard": 0.85, "MGMT": 0.8, "Temples": 0.8, "Unknown Mortal Orchestra": 0.75, "Melody's Echo Chamber": 0.7,
		"The Beatles": 0.6, "The Kinks": 0.5, "The Strokes": 0.5,
	}
	var strokes map[string]float64
	if amNear {
		near["Arctic Monkeys"] = 0.6
		strokes = map[string]float64{"Arctic Monkeys": 0.8}
	}
	g := newGraph(
		band("Tame Impala", psych, near),
		band("Pond", psych, nil), band("King Gizzard", psych, nil), band("MGMT", psych, nil), band("Temples", psych, nil),
		band("Unknown Mortal Orchestra", psych, nil), band("Melody's Echo Chamber", psych, nil),
		band("The Beatles", sixty, map[string]float64{"The Kinks": 0.8}),
		band("The Kinks", sixty, map[string]float64{"The Beatles": 0.8}),
		band("Arctic Monkeys", garage, strokes),
		band("The Strokes", garage, strokes),
	)
	return r, g
}

func TestMemoryFoldsNights(t *testing.T) {
	r, g := longRunningRoom(t, true)
	mem := &Memory{DB: r.db, Graph: g}
	lt, err := mem.Load(t.Context(), r.id)
	if err != nil {
		t.Fatal(err)
	}
	am, beatles := lt.Artists["arctic monkeys"], lt.Artists["the beatles"]
	if lt.Nights != 14 || beatles.Weight != 1 || beatles.Rest != 0 || am.Rest != 7 {
		t.Fatalf("nights %d, Beatles %+v, Arctic Monkeys %+v", lt.Nights, beatles, am)
	}
	// The Arctic Monkeys' last night was eight nights before the Beatles'
	// last: it's faint, but there.
	if am.Weight < throwbackMin || am.Weight > 0.15 {
		t.Errorf("Arctic Monkeys weight %.3f: want faint, but enough for a throwback", am.Weight)
	}
	if lt.Tags["60s"] != 1 || lt.Tags["garage rock"] >= 0.15 {
		t.Errorf("tags %v", lt.Tags)
	}
	// Folding is done once per night.
	again, err := mem.Load(t.Context(), r.id)
	if err != nil {
		t.Fatal(err)
	}
	if again.Nights != 14 || again.Artists["the beatles"] != beatles || again.Artists["arctic monkeys"] != am {
		t.Errorf("loaded again: %+v", again)
	}
}

// Tonight's songs lead. Of two artists equally near them, the one the
// room loved last week comes before the one from three weeks ago.
func TestTonightThenHistory(t *testing.T) {
	r, g := longRunningRoom(t, true)
	lt, err := (&Memory{DB: r.db, Graph: g}).Load(t.Context(), r.id)
	if err != nil {
		t.Fatal(err)
	}
	in := Input{History: r.history(), Now: now}
	in.Tags, in.Related = CachedTags(t.Context(), g, in), Related(t.Context(), g)
	p := NewProfile(in)
	if top := p.Top(1); len(top) == 0 || top[0].Artist.Name != "Tame Impala" {
		t.Fatalf("tonight's favorite: %v", top)
	}
	e := &Engine{Graph: g, Rand: func(int) int { return 99 }}
	scored := score(e.walk(t.Context(), p, lt, 0.25, nil), p, lt, 0.25, false)
	rank := map[string]int{}
	for i, c := range scored {
		if _, ok := rank[c.artist]; !ok {
			rank[c.artist] = i
		}
	}
	if c := scored[0]; c.artist != "pond" {
		t.Errorf("best %s (%+v): want tonight's nearest, Pond", c.song.Title, c)
	}
	if rank["the beatles"] > rank["arctic monkeys"] || rank["the kinks"] > rank["the strokes"] {
		t.Errorf("ranks %v: want the Beatles' side of the room's history ahead of the Arctic Monkeys'", rank)
	}
	// History tips a choice between near-equal artists, at most
	// priorShare of a score; it doesn't outvote tonight's nearest.
	for _, a := range []string{"pond", "king gizzard"} {
		if rank[a] > rank["the beatles"] {
			t.Errorf("ranks %v: the Beatles ahead of %s on history alone", rank, a)
		}
	}
}

// simulate runs fills through a night: autopilot plays each pick, or the
// room skips it quickly if it's a throwback and skip is set. It returns
// the picks.
func simulate(t *testing.T, g *graph, h []store.ListHistoryRow, lt LongTerm, seed uint64, fills int, skip bool) []candidate {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seedBase)) //nolint:gosec // reproducible draws for a simulated night
	e := &Engine{Graph: g, Rand: rng.IntN}
	h = slices.Clone(h)
	at := now
	var picks []candidate
	for range fills {
		in := Input{History: h, Now: at}
		in.Tags, in.Related = CachedTags(t.Context(), g, in), Related(t.Context(), g)
		order := e.shortlist(t.Context(), NewProfile(in), Request{Input: in, LongTerm: lt, Explore: 25}, in.Tags)
		if len(order) == 0 {
			break
		}
		c := order[0]
		picks = append(picks, c)
		it := item("", c.song.Artist.Name, c.song.Title)
		it.Autopilot.String = fmt.Sprintf(`{"reason":{"kind":%q}}`, c.kind)
		row := played(it, at, store.EndFinished)
		if skip && c.kind == KindThrowback {
			row = skipped(it, at, 5*time.Second)
		}
		h = append([]store.ListHistoryRow{row}, h...)
		at = at.Add(4 * time.Minute)
	}
	return picks
}

// Through a night of fills, tonight's sound leads, then the Beatles'
// side; the Arctic Monkeys, from three weeks ago and unlike tonight, come
// only as an occasional throwback, never as a run, and rarer once the room
// skips one.
func TestThrowbacksNeverRun(t *testing.T) {
	r, g := longRunningRoom(t, false)
	lt, err := (&Memory{DB: r.db, Graph: g}).Load(t.Context(), r.id)
	if err != nil {
		t.Fatal(err)
	}
	h := r.history()
	const seeds, fills = 10, 30
	count := map[string]int{}
	// Throwbacks after a run's first: whether the room let it play through
	// or skipped it.
	afterPlayed, afterSkipped := 0, 0
	for seed := range uint64(seeds) {
		picks := simulate(t, g, h, lt, seed, fills, false)
		if len(picks) < fills {
			t.Fatalf("seed %d: ran out after %d picks", seed, len(picks))
		}
		last := -10
		for i, c := range picks {
			count[c.artist]++
			if i < 5 && slices.Contains(psych, tagOf(g, c.artist)) {
				count["tonight's first five"]++
			}
			if c.kind == KindThrowback {
				if count["throwbacks this run"]++; count["throwbacks this run"] > 1 {
					afterPlayed++
				}
				if c.artist != "arctic monkeys" || c.lovedAt.IsZero() {
					t.Errorf("throwback %+v: want the Arctic Monkeys, loved three weeks ago", c)
				}
			}
			if c.artist == "arctic monkeys" {
				if c.kind != KindThrowback {
					t.Errorf("seed %d: %+v: want the Arctic Monkeys only as a throwback", seed, c)
				}
				if i-last < 5 {
					t.Errorf("seed %d: Arctic Monkeys at picks %d and %d: a run", seed, last, i)
				}
				last = i
			}
		}
		delete(count, "throwbacks this run")
		first := true
		for _, c := range simulate(t, g, h, lt, seed, fills, true) {
			if c.kind == KindThrowback {
				if !first {
					afterSkipped++
				}
				first = false
			}
		}
	}
	total := seeds * fills
	if count["tonight's first five"] < seeds*5*6/10 {
		t.Errorf("%d of the first five picks follow tonight's songs: want most", count["tonight's first five"])
	}
	t.Logf("picks over %d fills: %v; throwbacks after the first: %d if it played through, %d if skipped", total, count, afterPlayed, afterSkipped)
	if am := count["arctic monkeys"]; am == 0 || am*10 > total {
		t.Errorf("Arctic Monkeys %d of %d picks: want an occasional throwback, under 10%%", am, total)
	}
	tonight := 0
	for a, n := range count {
		if slices.Contains(psych, tagOf(g, a)) {
			tonight += n
		}
	}
	if beatles, am := count["the beatles"]+count["the kinks"], count["arctic monkeys"]+count["the strokes"]; tonight <= beatles || beatles <= am {
		t.Errorf("picks %v: want tonight's sound, then last week's Beatles, then the Arctic Monkeys from three weeks ago", count)
	}
	if afterPlayed == 0 || afterSkipped*2 > afterPlayed {
		t.Errorf("throwbacks after the first: %d if it played through, %d if skipped: want skipping one to make the next rarer", afterPlayed, afterSkipped)
	}
}

func tagOf(g *graph, artist string) string {
	if a, ok := g.artists[artist]; ok && len(a.Tags) > 0 {
		return a.Tags[0].Name
	}
	return ""
}

// A throwback the room skips counts against its artist more, in the long
// run, than an ordinary skip; and a veto fades over the nights after.
func TestLongTermSkips(t *testing.T) {
	night := func(kind string) []store.DjAffinity {
		it := item("", "Arctic Monkeys", "505")
		it.Autopilot.String = fmt.Sprintf(`{"reason":{"kind":%q}}`, kind)
		in := Input{History: []store.ListHistoryRow{skipped(it, now, 5*time.Second)}, Now: now}
		return fold("room", nil, in, 1, now, func(string) []musicgraph.Tag { return nil })
	}
	plain := readLongTerm(store.DjRoom{Nights: 1}, night(KindSimilarArtist)).Artists["arctic monkeys"]
	back := readLongTerm(store.DjRoom{Nights: 1}, night(KindThrowback)).Artists["arctic monkeys"]
	if back.Veto <= plain.Veto || plain.Veto == 0 {
		t.Errorf("veto after a skipped throwback %.2f, an ordinary skip %.2f", back.Veto, plain.Veto)
	}

	rows := night(KindThrowback)
	vetoes := []float64{back.Veto}
	for n := int64(2); n <= 4; n++ {
		other := Input{History: []store.ListHistoryRow{played(item("alice", "Blur", fmt.Sprint(n)), now, store.EndFinished)}, Now: now}
		rows = fold("room", rows, other, n, now, func(string) []musicgraph.Tag { return nil })
		vetoes = append(vetoes, readLongTerm(store.DjRoom{Nights: n}, rows).Artists["arctic monkeys"].Veto)
	}
	if !slices.IsSortedFunc(vetoes, func(a, b float64) int { return int((b - a) * 1e6) }) || vetoes[3] >= vetoes[0]/2 || vetoes[3] == 0 {
		t.Errorf("vetoes over the nights after: %v, want them to fade", vetoes)
	}
}

// Each member's taste counts the same over past nights: a member who's
// been around for months doesn't outweigh someone new.
func TestLongTermIsFair(t *testing.T) {
	var rows []store.DjAffinity
	for n := int64(1); n <= 10; n++ {
		var h []store.ListHistoryRow
		for i := range 10 {
			h = append(h, played(item("alice", "Old Favorite", fmt.Sprint(n, i)), now, store.EndFinished))
		}
		if n == 10 {
			h = append(h, played(item("bob", "New Favorite", "x"), now, store.EndFinished))
		}
		rows = fold("room", rows, Input{History: h, Now: now}, n, now, func(string) []musicgraph.Tag { return nil })
	}
	lt := readLongTerm(store.DjRoom{Nights: 10}, rows)
	if o, n := lt.Artists["old favorite"].Weight, lt.Artists["new favorite"].Weight; o != 1 || n != 1 {
		t.Errorf("old favorite %.3f, new %.3f: want each member's taste to count the same", o, n)
	}
}

// seedBase varies the simulated nights, to check the scenario holds
// beyond one set of draws.
var seedBase uint64 = 756
