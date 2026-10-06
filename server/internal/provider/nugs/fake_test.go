// SPDX-License-Identifier: AGPL-3.0-only

package nugs

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// The fake nugs.net speaks just enough of each API, in the shapes the real
// one uses (IDs that are numbers in some places and strings in others
// included), for the provider to run against.

const (
	fakeEmail    = "fan@example.com"
	fakePassword = "jamband"
	fakeUserID   = "424242"
)

type fakeShow struct {
	id, artistID       int
	artist, title      string
	date               string // M/D/YYYY, or "" for an album
	venue, city, state string
	img                string
	sold               bool // sold only, not in the subscription
	tracks             []fakeTrack
}

type fakeTrack struct {
	id      int
	title   string
	secs    int
	hlsOnly bool
}

var fakeCatalog = []fakeShow{
	{
		id: 1001, artistID: 62, artist: "Phish", date: "12/31/2024",
		venue: "Madison Square Garden", city: "New York", state: "NY", img: "/images/shows/msg.jpg",
		tracks: []fakeTrack{{5001, "Tweezer", 900, false}, {5002, "Tweezer Reprise", 300, false}},
	},
	{
		id: 1002, artistID: 62, artist: "Phish", date: "7/4/2023",
		venue: "Red Rocks Amphitheatre", city: "Morrison", state: "CO", img: "/images/shows/rr.jpg",
		tracks: []fakeTrack{{5003, "Tweezer", 1200, false}, {5004, "Sand", 600, false}, {5007, "Red Rocks Jam", 420, false}},
	},
	{
		id: 1003, artistID: 62, artist: "Phish", title: "Sharin' In The Groove", img: "/images/shows/sitg.jpg", sold: true,
		tracks: []fakeTrack{{5005, "Tweezer", 400, false}},
	},
	{
		id: 1004, artistID: 1034, artist: "Holly Bowling", date: "2/2/2018",
		venue: "Aspen District Theater", city: "Aspen", state: "CO",
		tracks: []fakeTrack{{5006, "Hls Song", 300, true}},
	},
}

type fakeNugs struct {
	srv *httptest.Server

	mu        sync.Mutex
	n         int
	refresh   map[string]bool
	access    map[string]bool
	refreshes int
	lapsed    bool
	hlsKey    []byte
	// hlsPlain is what the high-bandwidth HLS variant decrypts to.
	hlsPlain [][]byte
}

func newFake(t *testing.T) *fakeNugs {
	t.Helper()
	f := &fakeNugs{
		refresh:  map[string]bool{},
		access:   map[string]bool{},
		hlsKey:   []byte("0123456789abcdef"),
		hlsPlain: [][]byte{bytes.Repeat([]byte("segment-zero "), 40), bytes.Repeat([]byte("segment-one "), 33)},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /connect/token", f.token)
	mux.HandleFunc("GET /connect/userinfo", f.authed(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"sub": fakeUserID, "email": fakeEmail})
	}))
	mux.HandleFunc("GET /api/v1/me/subscriptions", f.authed(f.subscription))
	mux.HandleFunc("GET /api.aspx", f.legacy)
	mux.HandleFunc("GET /api/v1/shows/{id}", f.show)
	mux.HandleFunc("GET /bigriver/subPlayer.aspx", f.subPlayer)
	mux.HandleFunc("GET /media/{format}/{file}", f.media)
	mux.HandleFunc("GET /hls/{file}", f.hls)
	mux.HandleFunc("GET /images/shows/{file}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("\xff\xd8\xff fake jpeg"))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeNugs) provider(now func() time.Time) *Provider {
	u := f.srv.URL
	return New(Options{
		Client:    f.srv.Client(),
		Endpoints: Endpoints{Auth: u, Stream: u, Catalog: u, Subscriptions: u, Images: u},
		Now:       now,
	})
}

func (f *fakeNugs) setLapsed(v bool) {
	f.mu.Lock()
	f.lapsed = v
	f.mu.Unlock()
}

func (f *fakeNugs) refreshCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshes
}

// revoke makes every token the fake has issued stop working.
func (f *fakeNugs) revoke() {
	f.mu.Lock()
	defer f.mu.Unlock()
	clear(f.refresh)
	clear(f.access)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeNugs) token(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("client_id") != clientID {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_client"})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ok := false
	switch r.FormValue("grant_type") {
	case "password":
		ok = r.FormValue("username") == fakeEmail && r.FormValue("password") == fakePassword &&
			strings.Contains(r.FormValue("scope"), "offline_access")
	case "refresh_token":
		rt := r.FormValue("refresh_token")
		// Refresh tokens work once.
		ok = f.refresh[rt]
		delete(f.refresh, rt)
		f.refreshes++
	}
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	f.n++
	rt, at := fmt.Sprintf("rt-%d", f.n), fmt.Sprintf("at-%d", f.n)
	f.refresh[rt], f.access[at] = true, true
	writeJSON(w, map[string]any{"access_token": at, "refresh_token": rt, "expires_in": 3600, "token_type": "Bearer"})
}

func (f *fakeNugs) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ok := f.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (f *fakeNugs) subscription(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	lapsed := f.lapsed
	f.mu.Unlock()
	if lapsed {
		writeJSON(w, map[string]any{"legacySubscriptionId": 0, "plan": nil, "promo": nil})
		return
	}
	writeJSON(w, map[string]any{
		"legacySubscriptionId": 777,
		"plan":                 map[string]any{"id": "plan-abc", "description": "Hi-Fi"},
		"startedAt":            "01/01/2026 00:00:00",
		"endsAt":               "12/31/2099 23:59:59",
	})
}

func findShow(id int) (fakeShow, bool) {
	for _, s := range fakeCatalog {
		if s.id == id {
			return s, true
		}
	}
	return fakeShow{}, false
}

func findFakeTrack(id int) (fakeTrack, bool) {
	for _, s := range fakeCatalog {
		for _, t := range s.tracks {
			if t.id == id {
				return t, true
			}
		}
	}
	return fakeTrack{}, false
}

// showJSON renders a show as the catalog does. Listings leave durations
// out.
func showJSON(s fakeShow, details bool) map[string]any {
	m := map[string]any{
		"containerID": s.id, "artistID": s.artistID, "artistName": s.artist,
		"venueName": " ", "venueCity": " ", "venueState": " ",
		"isInSubscriptionProgram": !s.sold,
		"img":                     map[string]any{"url": s.img},
		"performanceDate":         nil, "performanceDateYear": "",
		"containerInfo": s.title, "containerTypeStr": "Album",
	}
	if s.date != "" {
		d, _ := time.Parse("1/2/2006", s.date)
		m["performanceDate"], m["performanceDateYear"] = s.date, strconv.Itoa(d.Year())
		m["venueName"], m["venueCity"], m["venueState"] = s.venue, s.city, s.state
		m["containerInfo"] = d.Format("01/02/06") + " " + s.venue + ", " + s.city + ", " + s.state + " "
		m["containerTypeStr"] = "Show"
	}
	var songs, tracks []map[string]any
	for _, t := range s.tracks {
		songs = append(songs, map[string]any{"trackID": t.id, "songTitle": t.title})
		tracks = append(tracks, map[string]any{"trackID": t.id, "songTitle": t.title, "totalRunningTime": t.secs})
	}
	m["songs"] = songs
	if details {
		m["tracks"] = tracks
	}
	return m
}

// item renders a search result as catalog.search does: the artist ID is a
// string here.
func item(s fakeShow, trackID int) map[string]any {
	m := map[string]any{
		"containerID": s.id, "trackID": trackID, "artistID": strconv.Itoa(s.artistID), "artistName": s.artist,
		"containerName": s.title, "performanceDate": nil, "img": map[string]any{"url": s.img}, "availability": 1,
		"venueName": nil, "venueCity": nil, "venueState": nil,
	}
	if s.date != "" {
		m["performanceDate"], m["venueName"], m["venueCity"], m["venueState"] = s.date, s.venue, s.city, s.state
		m["containerName"] = s.artist + ", " + s.date + " " + s.venue
	}
	return m
}

func (f *fakeNugs) legacy(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var resp any
	switch q.Get("method") {
	case "catalog.search":
		resp = searchJSON(strings.ToLower(q.Get("searchStr")))
	case "catalog.containersAll":
		var cs []map[string]any
		for _, s := range fakeCatalog {
			if strconv.Itoa(s.artistID) == q.Get("artistList") {
				cs = append(cs, showJSON(s, false))
			}
		}
		resp = map[string]any{"containers": cs, "totalMatchedRecords": len(cs)}
	default:
		http.Error(w, "unknown method", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{
		"methodName": q.Get("method"), "responseAvailabilityCode": 0, "responseAvailabilityCodeStr": "AVAILABLE", "Response": resp,
	})
}

func searchJSON(q string) map[string]any {
	type group struct {
		matched string
		items   []map[string]any
	}
	byType := map[int][]*group{}
	add := func(mt int, matched string, it map[string]any) {
		for _, g := range byType[mt] {
			if g.matched == matched {
				g.items = append(g.items, it)
				return
			}
		}
		byType[mt] = append(byType[mt], &group{matched, []map[string]any{it}})
	}
	for _, s := range fakeCatalog {
		if strings.Contains(strings.ToLower(s.artist), q) {
			add(matchArtist, s.artist, item(s, 0))
		}
		if s.date != "" && strings.Contains(strings.ToLower(s.venue), q) {
			add(matchVenue, s.venue, item(s, 0))
		}
		if s.date == "" && strings.Contains(strings.ToLower(s.title), q) {
			add(matchAlbum, s.title, item(s, 0))
		}
		for _, t := range s.tracks {
			if strings.Contains(strings.ToLower(t.title), q) {
				add(matchSong, t.title, item(s, t.id))
			}
		}
	}
	var types []map[string]any
	for _, mt := range []int{matchArtist, matchSong, matchVenue, matchAlbum} {
		var gs []map[string]any
		for _, g := range byType[mt] {
			gs = append(gs, map[string]any{"matchType": mt, "matchedStr": g.matched, "catalogSearchResultItems": g.items})
		}
		if gs != nil {
			types = append(types, map[string]any{"matchType": mt, "catalogSearchContainers": gs})
		}
	}
	return map[string]any{"catalogSearchTypeContainers": types, "searchError": 0}
}

func (f *fakeNugs) show(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	s, ok := findShow(id)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, map[string]any{"methodName": "catalog.container", "responseAvailabilityCode": 0, "Response": showJSON(s, true)})
}

// fakePlatforms are the formats the fake's platforms answer with.
var fakePlatforms = map[string]string{
	"1":  "/media/a.alac16/t.m4a",
	"4":  "/media/b.flac16/t.flac",
	"7":  "/media/c.aac150/t.m4a",
	"10": "/media/d.s360/t.mp4",
}

func (f *fakeNugs) subPlayer(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("subscriptionID") != "777" || q.Get("subCostplanIDAccessList") != "plan-abc" ||
		q.Get("nn_userID") != fakeUserID || q.Get("startDateStamp") == "0" || q.Get("app") != "1" {
		writeJSON(w, map[string]any{"streamLink": ""})
		return
	}
	id, _ := strconv.Atoi(q.Get("trackID"))
	t, ok := findFakeTrack(id)
	link := ""
	switch {
	case !ok:
	case t.hlsOnly:
		link = f.srv.URL + "/hls/master.m3u8?sig=x"
	default:
		link = f.srv.URL + fakePlatforms[q.Get("platformID")] + "?sig=" + q.Get("trackID")
	}
	// The stream API sometimes wraps its JSON in a callback.
	b, _ := json.Marshal(map[string]any{"streamLink": link, "streamer": "x"})
	_, _ = fmt.Fprintf(w, "callback(%s);", b)
}

// fakeAudio is a track's bytes in a format: distinct per format and track,
// so tests can tell which was played.
func fakeAudio(format, track string) []byte {
	return bytes.Repeat([]byte(format+":"+track+";"), 64)
}

func (f *fakeNugs) media(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(fakeAudio(r.PathValue("format"), r.URL.Query().Get("sig"))))
}

func (f *fakeNugs) hls(w http.ResponseWriter, r *http.Request) {
	switch r.PathValue("file") {
	case "master.m3u8":
		_, _ = fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=64000,CODECS=\"mp4a.40.2\"\nlow.m3u8?sig=x\n#EXT-X-STREAM-INF:BANDWIDTH=256000,CODECS=\"mp4a.40.2\"\nhigh.m3u8?sig=x\n")
	case "low.m3u8":
		_, _ = fmt.Fprint(w, "#EXTM3U\n#EXTINF:10,\nlow0.ts\n#EXT-X-ENDLIST\n")
	case "high.m3u8":
		_, _ = fmt.Fprint(w, "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:5\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin?sig=x\"\n#EXTINF:10,\nseg0.ts\n#EXTINF:10,\nseg1.ts\n#EXT-X-ENDLIST\n")
	case "key.bin":
		_, _ = w.Write(f.hlsKey)
	case "low0.ts":
		_, _ = w.Write([]byte("the low variant"))
	case "seg0.ts", "seg1.ts":
		i := 0
		if r.PathValue("file") == "seg1.ts" {
			i = 1
		}
		_, _ = w.Write(encrypt(f.hlsKey, int64(5+i), f.hlsPlain[i]))
	default:
		http.NotFound(w, r)
	}
}

// encrypt is HLS's AES-128 with the IV taken from the sequence number.
func encrypt(key []byte, seq int64, plain []byte) []byte {
	block, _ := aes.NewCipher(key)
	iv := make([]byte, aes.BlockSize)
	binary.BigEndian.PutUint64(iv[8:], uint64(seq)) //nolint:gosec // test data
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	in := append(bytes.Clone(plain), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(in))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, in) //nolint:gosec // HLS's IV is the sequence number, set above
	return out
}

// link signs the fake account in.
func link(t *testing.T, p *Provider, sink provider.CredentialSink) provider.Link {
	t.Helper()
	creds, account, err := p.Linker().Complete(t.Context(), provider.LinkInput{Fields: map[string]string{"email": fakeEmail, "password": fakePassword}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return provider.Link{ID: "link-1", Account: account, Credentials: creds, Sink: sink}
}

func open(t *testing.T, p *Provider, l provider.Link) *session {
	t.Helper()
	sess, err := p.Open(t.Context(), l)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess.(*session)
}
