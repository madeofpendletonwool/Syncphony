// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

const (
	radioheadMBID = "a74b1b7f-71a5-4011-9441-d0b5e4122711"
	portisheadID  = "8f6bd1e4-fbe1-4f50-aa9b-94c450ec0f11"
	creepMBID     = "c0000000-0000-4000-8000-000000000001"
)

// Responses shaped like the real services'. Last.fm sends numbers as
// strings in some methods and not others; both are here.
var routes = map[string]string{
	"/lastfm/?artist.getSimilar&Radiohead": `{"similarartists":{"artist":[
		{"name":"Thom Yorke","mbid":"","match":"1"},
		{"name":"Portishead","mbid":"` + portisheadID + `","match":"0.5"},
		{"name":"Muse","mbid":"","match":"0.4"}],"@attr":{"artist":"Radiohead"}}}`,
	"/lastfm/?artist.getTopTracks&Radiohead": `{"toptracks":{"track":[
		{"name":"Creep","playcount":"1000000","mbid":"` + creepMBID + `","@attr":{"rank":"1"}},
		{"name":"Karma Police","playcount":"100000","mbid":"","@attr":{"rank":"2"}},
		{"name":"Lucky","playcount":"1000","mbid":"","@attr":{"rank":"3"}}]}}`,
	"/lastfm/?artist.getTopTags&Radiohead": `{"toptags":{"tag":[
		{"name":"Alternative","count":100},{"name":"rock","count":60},{"name":"seen live","count":0}]}}`,
	"/lastfm/?track.getSimilar&Radiohead&Creep": `{"similartracks":{"track":[
		{"name":"Glory Box","playcount":5000,"mbid":"","match":1,"artist":{"name":"Portishead","mbid":"` + portisheadID + `"}},
		{"name":"Creep","playcount":5000,"mbid":"","match":0.9,"artist":{"name":"Radiohead","mbid":""}}]}}`,

	"/deezer/search/artist?Radiohead": `{"data":[
		{"id":53477202,"name":"DJ Radiohead","nb_fan":12},
		{"id":7,"name":"Radiohead","nb_fan":40},
		{"id":399,"name":"Radiohead","nb_fan":4101295}],"total":3}`,
	"/deezer/artist/399/related": `{"data":[
		{"id":1,"name":"Portishead","nb_fan":10},{"id":2,"name":"Blur","nb_fan":10},{"id":3,"name":"Radiohead","nb_fan":1}],"total":3}`,
	"/deezer/artist/399/top": `{"data":[
		{"id":138547415,"title":"Creep","duration":238,"rank":978547,"artist":{"id":399,"name":"Radiohead"}},
		{"id":2,"title":"No Surprises","duration":229,"rank":800000,"artist":{"id":399,"name":"Radiohead"}}],"total":2}`,
	"/deezer/track/isrc:GBAYE9200070": `{"id":138547415,"title":"Creep","isrc":"GBAYE9200070","duration":238,"rank":978547,"bpm":92,"release_date":"2008-01-01","artist":{"id":399,"name":"Radiohead"}}`,

	"/labs/similar-artists/json?" + radioheadMBID: `[
		{"artist_mbid":"` + portisheadID + `","name":"Portishead","score":10000,"reference_mbid":"` + radioheadMBID + `"},
		{"artist_mbid":"5b11f4ce-a62d-471e-81fc-a69a8278c7da","name":"Nirvana","score":5000,"reference_mbid":"` + radioheadMBID + `"}]`,
	"/lb/1/popularity/top-recordings-for-artist/" + radioheadMBID: `[
		{"artist_name":"Radiohead","recording_mbid":"r1","recording_name":"Karma Police","total_listen_count":3890683},
		{"artist_name":"Radiohead","recording_mbid":"` + creepMBID + `","recording_name":"Creep","total_listen_count":2000000}]`,
}

// fake stands in for Last.fm, Deezer and ListenBrainz.
type fake struct {
	*httptest.Server
	mu       sync.Mutex
	routes   map[string]string
	status   map[string]int // a prefix to fail with a status
	requests []*http.Request
}

func newFake(t *testing.T) *fake {
	f := &fake{routes: map[string]string{}, status: map[string]int{}}
	for k, v := range routes {
		f.routes[k] = v
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// route names a request the way routes does: the path, then for Last.fm
// the method and names, for Deezer's search its query, and for
// ListenBrainz's labs the MBIDs.
func route(r *http.Request) string {
	q := r.URL.Query()
	switch {
	case strings.HasPrefix(r.URL.Path, "/lastfm/"):
		k := r.URL.Path + "?" + q.Get("method") + "&" + q.Get("artist")
		if q.Get("track") != "" {
			k += "&" + q.Get("track")
		}
		return k
	case strings.HasPrefix(r.URL.Path, "/deezer/search"):
		return r.URL.Path + "?" + q.Get("q")
	case strings.HasPrefix(r.URL.Path, "/labs/"):
		return r.URL.Path + "?" + q.Get("artist_mbids")
	}
	return r.URL.Path
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	k := route(r)
	body, ok := f.routes[k]
	status := 0
	for prefix, s := range f.status {
		if strings.HasPrefix(r.URL.Path, prefix) {
			status = s
		}
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case status != 0:
		w.WriteHeader(status)
	case ok:
		fmt.Fprint(w, body)
	case strings.HasPrefix(k, "/lastfm/"):
		fmt.Fprint(w, `{"error":6,"message":"The artist you supplied could not be found"}`)
	case strings.HasPrefix(k, "/deezer/search"):
		fmt.Fprint(w, `{"data":[],"total":0}`)
	case strings.HasPrefix(k, "/deezer/"):
		fmt.Fprint(w, `{"error":{"type":"DataException","message":"no data","code":800}}`)
	case strings.HasPrefix(k, "/labs/"):
		fmt.Fprint(w, `[]`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fake) fail(prefix string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[prefix] = status
}

func (f *fake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fake) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		out = append(out, route(r))
	}
	return out
}

// mb stands in for MusicBrainz.
type mb struct {
	mu      sync.Mutex
	artists map[string]string // name → MBID
	tags    map[string][]musicbrainz.Tag
	dates   map[string]string
	calls   int
	down    bool
}

func (m *mb) FindArtist(_ context.Context, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.down {
		return "", provider.ErrUnavailable
	}
	if id, ok := m.artists[name]; ok {
		return id, nil
	}
	return "", provider.ErrNotFound
}

func (m *mb) ArtistTags(_ context.Context, mbid string) ([]musicbrainz.Tag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if ts, ok := m.tags[mbid]; ok {
		return ts, nil
	}
	return nil, provider.ErrNotFound
}

func (m *mb) FirstReleased(_ context.Context, mbid string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if d, ok := m.dates[mbid]; ok {
		return d, nil
	}
	return "", provider.ErrNotFound
}

type env struct {
	svc  *musicgraph.Service
	http *fake
	mb   *mb
	now  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{
		http: newFake(t),
		mb: &mb{
			artists: map[string]string{"Radiohead": radioheadMBID},
			tags: map[string][]musicbrainz.Tag{radioheadMBID: {
				{Name: "alternative rock", Count: 40, Genre: true}, {Name: "art rock", Count: 30, Genre: true}, {Name: "british", Count: 20},
			}},
			dates: map[string]string{creepMBID: "1992-09-21"},
		},
		now: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
	}
	ua := "Syncphony/test ( https://example.com )"
	e.svc = musicgraph.New(db, musicgraph.Options{
		Sources: []musicgraph.Source{
			musicgraph.NewLastFM(musicgraph.LastFMOptions{Key: "k", UserAgent: ua, BaseURL: e.http.URL + "/lastfm/", Interval: -1}),
			musicgraph.NewListenBrainz(musicgraph.ListenBrainzOptions{UserAgent: ua, Token: "tok", BaseURL: e.http.URL + "/lb", LabsURL: e.http.URL + "/labs", Interval: -1}),
			musicgraph.NewDeezer(musicgraph.DeezerOptions{UserAgent: ua, BaseURL: e.http.URL + "/deezer", Interval: -1}),
			musicgraph.NewMusicBrainz(e.mb),
		},
		Finder:  e.mb,
		TTL:     7 * 24 * time.Hour,
		MissTTL: time.Hour,
		Now:     func() time.Time { return e.now },
	})
	return e
}

var radiohead = musicgraph.ArtistRef{Name: "Radiohead"}

func names(ss []musicgraph.Similar) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Artist.Name)
	}
	return out
}

func TestArtistMergesSources(t *testing.T) {
	e := newEnv(t)
	a, err := e.svc.Artist(t.Context(), radiohead)
	if err != nil {
		t.Fatal(err)
	}
	if a.Ref.MBID != radioheadMBID {
		t.Errorf("MBID = %q, want it found on MusicBrainz", a.Ref.MBID)
	}
	if want := []string{"lastfm", "listenbrainz", "deezer", "musicbrainz"}; !slices.Equal(a.Sources, want) {
		t.Errorf("sources = %v, want %v", a.Sources, want)
	}
	// Portishead is the only artist all three call similar, so it beats
	// Thom Yorke, Last.fm's closest. Radiohead isn't similar to itself.
	got := names(a.Similar)
	if got[0] != "Portishead" {
		t.Errorf("most similar = %v, want Portishead first", got)
	}
	if slices.Contains(got, "Radiohead") {
		t.Errorf("similar = %v, includes the artist", got)
	}
	p := a.Similar[0]
	if p.Artist.MBID != portisheadID || !slices.Equal(p.Sources, []string{"lastfm", "listenbrainz", "deezer"}) {
		t.Errorf("Portishead = %+v, want its MBID and all three sources", p)
	}
	for _, s := range a.Similar {
		if s.Score <= 0 || s.Score > 1 {
			t.Errorf("%s scored %v, want (0, 1]", s.Artist.Name, s.Score)
		}
	}
	// Creep: Last.fm's top, Deezer's top, ListenBrainz's second.
	if a.Top[0].Title != "Creep" || a.Top[0].MBID != creepMBID || a.Top[0].Artist.MBID != radioheadMBID {
		t.Errorf("top song = %+v, want Creep with its MBIDs", a.Top[0])
	}
	if !slices.IsSortedFunc(a.Top, func(x, y musicgraph.Song) int { return cmp.Compare(y.Score, x.Score) }) {
		t.Errorf("top songs aren't most popular first: %+v", a.Top)
	}
	var tags []string
	for _, tg := range a.Tags {
		tags = append(tags, tg.Name)
	}
	if !slices.Contains(tags, "alternative rock") || !slices.Contains(tags, "alternative") || slices.Contains(tags, "seen live") {
		t.Errorf("tags = %v, want MusicBrainz's genres and Last.fm's tags, without empty ones", tags)
	}
}

func TestArtistIsCached(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Artist(t.Context(), radiohead); err != nil {
		t.Fatal(err)
	}
	n, calls := e.http.count(), e.mb.calls
	a, err := e.svc.Artist(t.Context(), musicgraph.ArtistRef{Name: "radiohead!"})
	if err != nil || len(a.Similar) == 0 {
		t.Fatalf("Artist = %+v, %v", a, err)
	}
	if e.http.count() != n || e.mb.calls != calls {
		t.Errorf("asked again: %v", e.http.paths()[n:])
	}
	if _, ok, err := e.svc.CachedArtist(t.Context(), musicgraph.ArtistRef{MBID: radioheadMBID}); !ok || err != nil {
		t.Errorf("CachedArtist by MBID = %v, %v", ok, err)
	}
	e.now = e.now.Add(8 * 24 * time.Hour)
	if _, ok, _ := e.svc.CachedArtist(t.Context(), radiohead); ok {
		t.Error("still cached after the TTL")
	}
}

func TestUnknownArtistIsCachedAsAMiss(t *testing.T) {
	e := newEnv(t)
	nobody := musicgraph.ArtistRef{Name: "Nobody At All"}
	if _, err := e.svc.Artist(t.Context(), nobody); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
	n := e.http.count()
	if _, err := e.svc.Artist(t.Context(), nobody); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("again: err = %v, want not found", err)
	}
	if e.http.count() != n {
		t.Errorf("asked again about a cached miss: %v", e.http.paths()[n:])
	}
	e.now = e.now.Add(2 * time.Hour) // past MissTTL
	_, _ = e.svc.Artist(t.Context(), nobody)
	if e.http.count() == n {
		t.Error("didn't ask again after the miss expired")
	}
}

func TestOutagesAreNotCached(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/lastfm/", "/deezer/", "/labs/", "/lb/"} {
		e.http.fail(p, http.StatusInternalServerError)
	}
	e.mb.tags = nil
	if _, err := e.svc.Artist(t.Context(), radiohead); !errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("err = %v, want unavailable", err)
	}
	if _, ok, _ := e.svc.CachedArtist(t.Context(), radiohead); ok {
		t.Error("an outage was cached")
	}
}

func TestPartialAnswersExpireSooner(t *testing.T) {
	e := newEnv(t)
	e.http.fail("/deezer/", http.StatusInternalServerError)
	a, err := e.svc.Artist(t.Context(), radiohead)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(a.Sources, "deezer") {
		t.Errorf("sources = %v, include the one that failed", a.Sources)
	}
	e.now = e.now.Add(2 * time.Hour)
	if _, ok, _ := e.svc.CachedArtist(t.Context(), radiohead); ok {
		t.Error("an answer missing a source was kept past MissTTL")
	}
}

// Without an MBID, ListenBrainz and MusicBrainz can't answer: if finding
// it failed, the answer is kept only until MissTTL.
func TestMBIDOutageExpiresSooner(t *testing.T) {
	e := newEnv(t)
	e.mb.down = true
	a, err := e.svc.Artist(t.Context(), radiohead)
	if err != nil {
		t.Fatal(err)
	}
	if a.Ref.MBID != "" || slices.Contains(a.Sources, "listenbrainz") {
		t.Errorf("artist = %+v, want no MBID or ListenBrainz", a)
	}
	e.now = e.now.Add(2 * time.Hour)
	if _, ok, _ := e.svc.CachedArtist(t.Context(), radiohead); ok {
		t.Error("an answer without its MBID was kept past MissTTL")
	}
}

func TestRateLimits(t *testing.T) {
	e := newEnv(t)
	e.http.routes["/lastfm/?artist.getSimilar&Radiohead"] = `{"error":29,"message":"Rate Limit Exceeded"}`
	e.http.fail("/deezer/", http.StatusTooManyRequests)
	e.http.fail("/labs/", http.StatusServiceUnavailable)
	e.mb.tags = nil
	_, err := e.svc.Artist(t.Context(), radiohead)
	if !errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("err = %v, want unavailable", err)
	}
}

func TestTrackMergesSources(t *testing.T) {
	e := newEnv(t)
	creep := musicgraph.SongRef{Title: "Creep", Artist: radiohead, ISRC: "GBAYE9200070", MBID: creepMBID}
	tr, err := e.svc.Track(t.Context(), creep)
	if err != nil {
		t.Fatal(err)
	}
	if tr.BPM != 92 {
		t.Errorf("BPM = %v, want Deezer's 92", tr.BPM)
	}
	if tr.Year != 1992 {
		t.Errorf("year = %d, want MusicBrainz's first release, not Deezer's reissue", tr.Year)
	}
	if tr.Rank < 0.97 || tr.Rank > 1 {
		t.Errorf("rank = %v, want about 0.98", tr.Rank)
	}
	if len(tr.Similar) != 1 || tr.Similar[0].Title != "Glory Box" || tr.Similar[0].Artist.MBID != portisheadID {
		t.Errorf("similar = %+v, want Glory Box and not the song itself", tr.Similar)
	}
	n := e.http.count()
	if _, err := e.svc.Track(t.Context(), creep); err != nil || e.http.count() != n {
		t.Errorf("not cached: %v, %v", err, e.http.paths()[n:])
	}
}

func TestDeezerTrackFallsBackToSearch(t *testing.T) {
	e := newEnv(t)
	e.http.routes["/deezer/search?Radiohead Lucky"] = `{"data":[
		{"id":5,"title":"Lucky (Live)","artist":{"name":"Radiohead"},"rank":1},
		{"id":6,"title":"Lucky","artist":{"name":"Radiohead"},"rank":500000}],"total":2}`
	e.http.routes["/deezer/track/6"] = `{"id":6,"title":"Lucky","bpm":0,"rank":500000,"release_date":"1997-05-21","artist":{"name":"Radiohead"}}`
	tr, err := e.svc.Track(t.Context(), musicgraph.SongRef{Title: "Lucky", Artist: radiohead, ISRC: "XX0000000000"})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Year != 1997 || tr.Rank != 0.5 || tr.BPM != 0 {
		t.Errorf("track = %+v, want the studio version's year and rank", tr)
	}
	if !slices.Contains(e.http.paths(), "/deezer/track/isrc:XX0000000000") {
		t.Errorf("didn't try the ISRC first: %v", e.http.paths())
	}
}

func TestListenBrainzReadsOnlyTheTop(t *testing.T) {
	e := newEnv(t)
	var b strings.Builder
	b.WriteString("[")
	for i := range 500 {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"recording_mbid":"r%d","recording_name":"Song %d","total_listen_count":%d}`, i, i, 1000-i)
	}
	b.WriteString(",oops") // never read
	e.http.routes["/lb/1/popularity/top-recordings-for-artist/"+radioheadMBID] = b.String()
	src := musicgraph.NewListenBrainz(musicgraph.ListenBrainzOptions{BaseURL: e.http.URL + "/lb", LabsURL: e.http.URL + "/labs", Token: "tok", Interval: -1})
	a, err := src.Artist(t.Context(), musicgraph.ArtistRef{Name: "Radiohead", MBID: radioheadMBID})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Top) != 50 {
		t.Errorf("read %d songs, want 50", len(a.Top))
	}
	if got := e.http.requests[len(e.http.requests)-1].Header.Get("Authorization"); got != "Token tok" {
		t.Errorf("Authorization = %q", got)
	}
	if _, err := src.Artist(t.Context(), musicgraph.ArtistRef{Name: "Radiohead"}); !errors.Is(err, provider.ErrUnsupported) {
		t.Errorf("without an MBID: err = %v, want unsupported", err)
	}
}

func TestWarm(t *testing.T) {
	e := newEnv(t)
	e.http.routes["/lastfm/?artist.getSimilar&Portishead"] = `{"similarartists":{"artist":[{"name":"Massive Attack","mbid":"","match":"1"}]}}`
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	e.svc.Warm(provider.Track{Title: "Creep", Artists: []provider.ArtistCredit{{Name: "Radiohead"}}, ISRC: "GBAYE9200070"})

	// The artist, the song, and the artist's nearest neighbors get cached.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, a, _ := e.svc.CachedArtist(t.Context(), radiohead)
		_, s, _ := e.svc.CachedTrack(t.Context(), musicgraph.SongRef{Title: "Creep", Artist: radiohead})
		_, p, _ := e.svc.CachedArtist(t.Context(), musicgraph.ArtistRef{Name: "Portishead"})
		if a && s && p {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not warmed: artist %v, song %v, neighbor %v; asked %v", a, s, p, e.http.paths())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Neighbors' neighbors aren't: Portishead's similar artists aren't asked about.
	time.Sleep(100 * time.Millisecond)
	for _, p := range e.http.paths() {
		if strings.Contains(p, "Massive Attack") {
			t.Errorf("warmed two hops out: %s", p)
		}
	}
}

func TestSweep(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Artist(t.Context(), radiohead); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(30 * 24 * time.Hour)
	if err := e.svc.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(-30 * 24 * time.Hour)
	if _, ok, _ := e.svc.CachedArtist(t.Context(), radiohead); ok {
		t.Error("expired entry survived a sweep")
	}
}
