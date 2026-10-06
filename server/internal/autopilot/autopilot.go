// SPDX-License-Identifier: AGPL-3.0-only

// Package autopilot keeps a room's music going. When nothing is waiting in
// a room's queue, it adds one song like the ones the room has been
// playing, so the room never goes silent. See docs/adr/0008-autopilot.md.
//
// Seeds are the room's recent songs, taken in turn from each member's, so
// autopilot reflects everyone's taste. Songs come from the linked services
// that can recommend (provider.Recommender), so they're always ones a
// library in the room actually has.
//
// Autopilot songs aren't anyone's: they play after every member's song,
// don't take anyone's turn, and anyone may remove one. The room can skip
// them like any other song, and autopilot steers away from artists it
// skips.
package autopilot

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Links opens sessions for links and looks providers up. It's links.Service.
type Links interface {
	Open(ctx context.Context, linkID string) (provider.Session, error)
	Provider(id string) (provider.Provider, error)
}

// Presence says who's in a room. realtime.Presence is one.
type Presence interface {
	Members(roomID string) []string
}

// Player says whether a room has a speaker. playback.Engine is one.
type Player interface {
	NowPlaying(ctx context.Context, roomID string) (rooms.NowPlaying, error)
}

// How far autopilot looks back, and how hard it tries.
const (
	// historyReach is how many of the room's plays autopilot reads: the
	// songs it won't repeat, and where seeds come from.
	historyReach = 200
	// seedReach is how many recent member songs can seed.
	seedReach = 25
	// autopilotReach is how many of its own recent songs autopilot reads,
	// to rotate seeds and learn what the room skipped.
	autopilotReach = 50
	// skipMemory is how many of autopilot's recent songs a skip of an
	// artist holds for.
	skipMemory = 20
	// seedUsers, seedsPerUser and linksPerSeed bound how many seeds and
	// services one fill tries before falling back to random songs.
	seedUsers    = 3
	seedsPerUser = 2
	linksPerSeed = 2
	// candidates is how many songs autopilot asks a service for.
	candidates = 40
	// similarTop is how close to the top of the list a similar pick stays.
	similarTop = 5
	// discoveryAvoid is how many of the room's last songs' artists
	// discovery stays away from.
	discoveryAvoid = 5
)

// Service adds songs to rooms whose queue has run dry.
type Service struct {
	db       *store.Store
	rooms    *rooms.Service
	queue    *queue.Service
	links    Links
	presence Presence
	// Player, if set, limits autopilot to rooms with a speaker: nobody
	// hears songs added to a room with none.
	Player Player
	// Timeout bounds one fill. Default 30s.
	Timeout time.Duration
	// Rand returns a number in [0, n). Default math/rand/v2's IntN.
	Rand func(n int) int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	closed bool
	byID   map[string]*roomState
}

// roomState is one room's fills. Service.mu guards it.
type roomState struct {
	running, again bool
	// missed is the room's newest play when autopilot last came up empty.
	// It doesn't look again until the room plays something new, or its
	// settings change.
	missed string
}

// New returns a Service. Call Close when done.
func New(db *store.Store, rs *rooms.Service, qs *queue.Service, links Links, presence Presence) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		db: db, rooms: rs, queue: qs, links: links, presence: presence,
		Timeout: 30 * time.Second, Rand: rand.IntN,
		ctx: ctx, cancel: cancel, byID: map[string]*roomState{},
	}
}

// Close stops fills in progress and waits for them.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

// Kick checks a room after its queue changed, and adds a song if it ran
// dry. The work happens on another goroutine, one fill per room at a time.
func (s *Service) Kick(roomID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	st, ok := s.byID[roomID]
	if !ok {
		st = &roomState{}
		s.byID[roomID] = st
	}
	if st.running {
		st.again = true
		return
	}
	st.running = true
	s.wg.Add(1)
	go s.run(roomID, st)
}

// RoomUpdated follows a change to a room's settings: autopilot may have
// been turned on, or set to a new adventure.
func (s *Service) RoomUpdated(row store.Room) {
	s.mu.Lock()
	if st, ok := s.byID[row.ID]; ok {
		st.missed = ""
	}
	s.mu.Unlock()
	s.Kick(row.ID)
}

func (s *Service) run(roomID string, st *roomState) {
	defer s.wg.Done()
	for {
		ctx, cancel := context.WithTimeout(s.ctx, s.Timeout)
		err := s.Fill(ctx, roomID)
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("autopilot: filling a room", "room", roomID, "err", err)
		}
		s.mu.Lock()
		if !st.again {
			st.running = false
			s.mu.Unlock()
			return
		}
		st.again = false
		s.mu.Unlock()
	}
}

// Fill adds a song to a room if autopilot is on, nothing is waiting, the
// room has a speaker, and it has played something to go on. Kick runs it
// in the background; tests call it directly.
func (s *Service) Fill(ctx context.Context, roomID string) error {
	room, err := s.rooms.Get(ctx, roomID)
	if err != nil {
		return err
	}
	settings := rooms.ParseSettings(room.Settings).Autopilot
	if !settings.On {
		return nil
	}
	snap, err := s.rooms.QueueSnapshot(ctx, roomID)
	if err != nil || len(snap.UpNext) > 0 {
		return err
	}
	if s.Player != nil {
		np, err := s.Player.NowPlaying(ctx, roomID)
		if err != nil || np.Player == nil {
			return err
		}
	}
	history, err := s.db.ListHistory(ctx, store.ListHistoryParams{RoomID: roomID, Limit: historyReach})
	if err != nil || len(history) == 0 {
		return err
	}
	newest := history[0].PlayHistory.ID
	if s.missed(roomID) == newest {
		return nil
	}
	mine, err := s.db.ListAutopilot(ctx, store.ListAutopilotParams{RoomID: roomID, Limit: autopilotReach})
	if err != nil {
		return err
	}
	f := &fill{
		s: s, roomID: roomID, room: room, adventure: settings.Adventure,
		history: history, mine: mine, sessions: map[string]provider.Session{},
	}
	defer f.close()
	f.remember(snap.Items)
	picks, err := f.choose(ctx)
	if err != nil {
		return err
	}
	for _, p := range picks {
		_, err := s.queue.AddAutopilot(ctx, roomID, p.forUser, p.track, p.info)
		var repeat *queue.RepeatError
		switch {
		case err == nil, errors.Is(err, queue.ErrNotDry):
			return nil
		case errors.As(err, &repeat):
			continue // the room's repeat guard reaches further back than we did
		default:
			return err
		}
	}
	s.setMissed(roomID, newest)
	s.rooms.PublishNotice(rooms.Notice{RoomID: roomID, Message: "Autopilot couldn't find anything new to play. Add a song to keep going."})
	return nil
}

func (s *Service) missed(roomID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.byID[roomID]; ok {
		return st.missed
	}
	return ""
}

func (s *Service) setMissed(roomID, playID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.byID[roomID]
	if !ok {
		st = &roomState{}
		s.byID[roomID] = st
	}
	st.missed = playID
}

// pick is a song autopilot chose.
type pick struct {
	track   provider.Track
	forUser string
	info    queue.AutopilotInfo
}

// fill is one attempt to add a song to a room.
type fill struct {
	s         *Service
	roomID    string
	room      store.Room
	adventure string
	history   []store.ListHistoryRow
	mine      []store.QueueItem

	// seen holds the songs not to play: recent, waiting, or removed.
	seen map[string]bool
	// skipped holds the artists of autopilot songs the room skipped.
	skipped map[string]bool
	// recentArtists are the artists of the room's last few songs.
	recentArtists map[string]bool

	links    []store.ServiceLink
	sessions map[string]provider.Session
}

func (f *fill) close() {
	for _, sess := range f.sessions {
		sess.Close()
	}
}

// remember notes the songs autopilot mustn't pick, and the artists it
// should avoid.
func (f *fill) remember(upcoming []store.QueueItem) {
	f.seen, f.skipped, f.recentArtists = map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, it := range upcoming {
		f.see(it)
	}
	for i, h := range f.history {
		f.see(h.QueueItem)
		if i < discoveryAvoid {
			if a := artistKey(trackOf(h.QueueItem)); a != "" {
				f.recentArtists[a] = true
			}
		}
		// A skip of autopilot's song says the room didn't want that artist.
		// A failed song says nothing.
		if h.QueueItem.IsAutopilot() && h.PlayHistory.EndReason.String == store.EndSkipped && f.recentlyMine(h.QueueItem.ID) {
			if a := artistKey(trackOf(h.QueueItem)); a != "" {
				f.skipped[a] = true
			}
		}
	}
	for _, it := range f.mine {
		f.see(it) // removed ones too: someone didn't want that song
	}
}

// recentlyMine reports whether itemID is among autopilot's last
// skipMemory songs.
func (f *fill) recentlyMine(itemID string) bool {
	for _, it := range f.mine[:min(len(f.mine), skipMemory)] {
		if it.ID == itemID {
			return true
		}
	}
	return false
}

func (f *fill) see(it store.QueueItem) {
	t := trackOf(it)
	t.Ref = provider.TrackRef{Provider: it.Provider, ID: it.TrackID}
	for _, k := range keys(t) {
		f.seen[k] = true
	}
	if it.ViaLinkID.Valid {
		f.seen[it.ViaProvider.String+"\x00"+it.ViaTrackID.String] = true
	}
}

// fresh reports whether t may play: not heard lately, and not by an artist
// the room skipped.
func (f *fill) fresh(t provider.Track) bool {
	for _, k := range keys(t) {
		if f.seen[k] {
			return false
		}
	}
	return !f.skipped[artistKey(t)]
}

// keys are the ways a song is recognized: on its service, by ISRC on any
// service, and by title and artist.
func keys(t provider.Track) []string {
	out := []string{t.Ref.Provider + "\x00" + t.Ref.ID}
	if t.ISRC != "" {
		out = append(out, "isrc\x00"+t.ISRC)
	}
	if a := artistKey(t); a != "" && t.Title != "" {
		out = append(out, "name\x00"+a+"\x00"+strings.ToLower(strings.TrimSpace(t.Title)))
	}
	return out
}

func artistKey(t provider.Track) string {
	if len(t.Artists) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(t.Artists[0].Name))
}

func trackOf(it store.QueueItem) provider.Track {
	var t provider.Track
	_ = json.Unmarshal([]byte(it.Metadata), &t)
	return t
}

// seed is a song autopilot looks for more like.
type seed struct {
	item  store.QueueItem
	track provider.Track
}

// choose returns songs to try adding, best first: ones like a member's
// recent song, members taking turns; failing that, like autopilot's own
// recent songs; failing that, random ones.
func (f *fill) choose(ctx context.Context) ([]pick, error) {
	users, seeds := f.seeds()
	if err := f.loadLinks(ctx, users); err != nil {
		return nil, err
	}
	if len(f.links) == 0 {
		return nil, nil
	}
	var out []pick
	for _, u := range users[:min(len(users), seedUsers)] {
		for _, sd := range seeds[u][:min(len(seeds[u]), seedsPerUser)] {
			if p, ok := f.like(ctx, sd, u); ok {
				out = append(out, p)
			}
			if len(out) >= 2 {
				return out, nil
			}
		}
	}
	// Nobody's songs led anywhere new. Follow autopilot's own trail.
	n := 0
	for _, h := range f.history {
		if !h.QueueItem.IsAutopilot() || h.PlayHistory.EndReason.String != store.EndFinished {
			continue
		}
		if p, ok := f.like(ctx, seed{item: h.QueueItem, track: trackOf(h.QueueItem)}, h.QueueItem.AddedBy); ok {
			out = append(out, p)
		}
		if n++; n >= seedsPerUser || len(out) > 0 {
			break
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	forUser := f.room.OwnerID
	if len(users) > 0 {
		forUser = users[0]
	}
	return f.random(ctx, forUser), nil
}

// seeds returns the members whose songs can seed, in the order autopilot
// takes their turns, and each one's seeds, newest first. People in the
// room come first; then whoever autopilot chose for longest ago (or
// never), then whoever played most recently.
func (f *fill) seeds() ([]string, map[string][]seed) {
	seeds := map[string][]seed{}
	var users []string
	latest := map[string]int{}
	n := 0
	for i, h := range f.history {
		it := h.QueueItem
		// Songs the room skipped, or that failed, don't seed.
		if it.IsAutopilot() || (h.PlayHistory.EndedAt.Valid && h.PlayHistory.EndReason.String != store.EndFinished) {
			continue
		}
		if _, ok := seeds[it.AddedBy]; !ok {
			users = append(users, it.AddedBy)
			latest[it.AddedBy] = i
		}
		seeds[it.AddedBy] = append(seeds[it.AddedBy], seed{item: it, track: trackOf(it)})
		if n++; n >= seedReach {
			break
		}
	}
	// lastFor is how many of autopilot's songs ago each user had one.
	lastFor := map[string]int{}
	for i, it := range f.mine {
		if _, ok := lastFor[it.AddedBy]; !ok {
			lastFor[it.AddedBy] = i
		}
	}
	here := map[string]bool{}
	for _, u := range f.s.presence.Members(f.roomID) {
		here[u] = true
	}
	ago := func(u string) int {
		if i, ok := lastFor[u]; ok {
			return i
		}
		return len(f.mine) + 1 // never: longest ago of all
	}
	slices.SortStableFunc(users, func(a, b string) int {
		switch {
		case here[a] != here[b]:
			if here[a] {
				return -1
			}
			return 1
		case ago(a) != ago(b):
			return ago(b) - ago(a)
		}
		return latest[a] - latest[b]
	})
	return users, seeds
}

// loadLinks finds the services autopilot can draw on: those of the people
// in the room and of the seeds' members, and shared ones, that can
// recommend.
func (f *fill) loadLinks(ctx context.Context, users []string) error {
	who := append(f.s.presence.Members(f.roomID), users...)
	if len(who) == 0 {
		who = []string{f.room.OwnerID}
	}
	rows, err := f.s.db.ListMatchLinks(ctx, who)
	if err != nil {
		return err
	}
	for _, l := range rows {
		if p, err := f.s.links.Provider(l.Provider); err == nil && p.Info().Capabilities.Recommendations {
			f.links = append(f.links, l)
		}
	}
	return nil
}

// linksFor orders the links to try for a seed: the one it played from,
// then its member's own, then the rest.
func (f *fill) linksFor(sd seed) []store.ServiceLink {
	from, _ := source(sd.item)
	rank := func(l store.ServiceLink) int {
		switch {
		case l.ID == from:
			return 0
		case l.UserID == sd.item.AddedBy:
			return 1
		}
		return 2
	}
	out := slices.Clone(f.links)
	slices.SortStableFunc(out, func(a, b store.ServiceLink) int { return rank(a) - rank(b) })
	return out[:min(len(out), linksPerSeed)]
}

// source is where an item played from: its stand-in, or its own link.
func source(it store.QueueItem) (linkID, trackID string) {
	if it.ViaLinkID.Valid {
		return it.ViaLinkID.String, it.ViaTrackID.String
	}
	return it.LinkID.String, it.TrackID
}

func (f *fill) session(ctx context.Context, linkID string) (provider.Recommender, provider.Session, bool) {
	sess, ok := f.sessions[linkID]
	if !ok {
		var err error
		if sess, err = f.s.links.Open(ctx, linkID); err != nil {
			slog.Debug("autopilot: opening a link", "link", linkID, "err", err)
			return nil, nil, false
		}
		f.sessions[linkID] = sess
	}
	r, ok := sess.(provider.Recommender)
	return r, sess, ok
}

// like finds a song like sd's on the room's services.
func (f *fill) like(ctx context.Context, sd seed, forUser string) (pick, bool) {
	if sd.track.Title == "" {
		return pick{}, false
	}
	for _, l := range f.linksFor(sd) {
		rec, sess, ok := f.session(ctx, l.ID)
		if !ok {
			continue
		}
		cands := f.candidates(ctx, rec, sess, l, sd)
		if t, ok := f.choosePick(cands, sd); ok {
			info := queue.AutopilotInfo{SeedItemID: sd.item.ID, SeedTitle: sd.track.Title}
			if len(sd.track.Artists) > 0 {
				info.SeedArtist = sd.track.Artists[0].Name
			}
			return pick{track: t, forUser: forUser, info: info}, true
		}
	}
	return pick{}, false
}

// candidates asks one service for songs like sd's, most alike first.
func (f *fill) candidates(ctx context.Context, rec provider.Recommender, sess provider.Session, l store.ServiceLink, sd seed) []provider.Track {
	trackID, artistID := f.resolve(ctx, sess, l, sd)
	var artist string
	if len(sd.track.Artists) > 0 {
		artist = sd.track.Artists[0].Name
	}
	var out []provider.Track
	add := func(what string, ts []provider.Track, err error) {
		if err != nil {
			slog.Debug("autopilot: asking for songs", "link", l.ID, "call", what, "err", err)
		}
		out = append(out, ts...)
	}
	switch f.adventure {
	case rooms.AdventureDiscovery:
		if artistID != "" {
			ts, err := rec.SimilarToArtist(ctx, artistID, candidates)
			add("SimilarToArtist", ts, err)
		} else if trackID != "" {
			ts, err := rec.SimilarToTrack(ctx, trackID, candidates)
			add("SimilarToTrack", ts, err)
		}
	default:
		if trackID != "" {
			ts, err := rec.SimilarToTrack(ctx, trackID, candidates)
			add("SimilarToTrack", ts, err)
		} else if artistID != "" {
			ts, err := rec.SimilarToArtist(ctx, artistID, candidates)
			add("SimilarToArtist", ts, err)
		}
		if artist != "" {
			ts, err := rec.TopTracks(ctx, artist, similarTop*2)
			add("TopTracks", ts, err)
		}
		if len(out) == 0 && artistID != "" {
			// The service knows nothing similar (Navidrome without
			// Last.fm, say). More by the same artist is still like it.
			ts, err := f.catalog(ctx, sess, artistID)
			add("Artist", ts, err)
		}
	}
	return out
}

// resolve finds sd's track and artist on a link: directly if it played
// from there, else by searching.
func (f *fill) resolve(ctx context.Context, sess provider.Session, l store.ServiceLink, sd seed) (trackID, artistID string) {
	if from, id := source(sd.item); from == l.ID {
		trackID = id
		if !sd.item.ViaLinkID.Valid && len(sd.track.Artists) > 0 {
			artistID = sd.track.Artists[0].ID
		}
	} else if t, score, err := match.On(ctx, sess, sd.track); err == nil && score > 0 {
		trackID = t.Ref.ID
		if len(t.Artists) > 0 {
			artistID = t.Artists[0].ID
		}
	}
	if artistID != "" || len(sd.track.Artists) == 0 {
		return trackID, artistID
	}
	name := sd.track.Artists[0].Name
	if p, err := f.s.links.Provider(l.Provider); err != nil || !p.Info().Capabilities.CanSearch(provider.KindArtist) {
		return trackID, ""
	}
	page, err := sess.Search(ctx, provider.SearchQuery{Text: name, Kinds: []provider.EntityKind{provider.KindArtist}, Limit: 5})
	if err != nil {
		return trackID, ""
	}
	for _, a := range page.Artists {
		if strings.EqualFold(strings.TrimSpace(a.Name), strings.TrimSpace(name)) {
			return trackID, a.ID
		}
	}
	return trackID, ""
}

// catalog returns the songs of one of an artist's albums, picked at random.
func (f *fill) catalog(ctx context.Context, sess provider.Session, artistID string) ([]provider.Track, error) {
	_, albums, err := sess.Artist(ctx, artistID)
	if err != nil || len(albums) == 0 {
		return nil, err
	}
	_, ts, err := sess.Album(ctx, albums[f.s.Rand(len(albums))].ID)
	return ts, err
}

// choosePick picks one of the fresh candidates. Similar stays near the top
// of the list; discovery reaches anywhere in it, and leaves out the seed's
// artist and the artists the room just heard.
func (f *fill) choosePick(cands []provider.Track, sd seed) (provider.Track, bool) {
	var ok []provider.Track
	dup := map[string]bool{}
	for _, t := range cands {
		k := t.Ref.Provider + "\x00" + t.Ref.ID
		if dup[k] || !f.fresh(t) {
			continue
		}
		dup[k] = true
		if f.adventure == rooms.AdventureDiscovery {
			a := artistKey(t)
			if a == artistKey(sd.track) || f.recentArtists[a] {
				continue
			}
		}
		ok = append(ok, t)
	}
	if len(ok) == 0 {
		return provider.Track{}, false
	}
	if f.adventure != rooms.AdventureDiscovery {
		ok = ok[:min(len(ok), similarTop)]
	}
	return ok[f.s.Rand(len(ok))], true
}

// random returns fresh random songs from the first service that has any:
// the last resort that keeps the room from going silent.
func (f *fill) random(ctx context.Context, forUser string) []pick {
	for _, l := range f.links {
		rec, _, ok := f.session(ctx, l.ID)
		if !ok {
			continue
		}
		ts, err := rec.RandomTracks(ctx, candidates)
		if err != nil {
			slog.Debug("autopilot: asking for random songs", "link", l.ID, "err", err)
			continue
		}
		var out []pick
		for _, t := range ts {
			if f.fresh(t) {
				out = append(out, pick{track: t, forUser: forUser})
			}
		}
		if len(out) > 0 {
			return out[:min(len(out), 3)]
		}
	}
	return nil
}
