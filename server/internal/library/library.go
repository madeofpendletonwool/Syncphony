// SPDX-License-Identifier: AGPL-3.0-only

// Package library knows what music a user already has, so discovery can
// leave it out (MAD-816, ADR 0017). It indexes the songs on each user's
// own links that can list their whole library (Navidrome's), not shared
// links and not Spotify: what's on your own server is what you have.
//
// The library is only ever a filter. It says nothing about taste: plenty
// of what's on a server isn't its owner's favorite music.
//
// Each link's index is kept in memory, for lookups well under a
// millisecond, and in the database, so a restart doesn't rebuild it. A
// link is indexed the first time its user asks, then rebuilt daily in the
// background, or when they ask. A rebuild is skipped while the service
// says its library hasn't been scanned since.
package library

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Links lists and opens users' links. It's links.Service in production.
type Links interface {
	List(ctx context.Context, userID string) ([]store.ServiceLink, error)
	Open(ctx context.Context, linkID string) (provider.Session, error)
}

// How big and how often.
const (
	// maxSongs bounds one link's index, against a runaway catalog.
	maxSongs = 2_000_000
	// checkEvery is how often Run looks for indexes due a rebuild.
	checkEvery = time.Hour
)

// Service indexes users' libraries. Call Run to keep them fresh.
type Service struct {
	db    *store.Store
	links Links
	// Every is how often a link's index is rebuilt. Default a day.
	Every time.Duration
	// Now is the clock. Default store.Now.
	Now func() time.Time
	// Built, if set, is called after each index build, with the link's
	// user, so what waits on their library (the Want list) can look
	// again.
	Built func(ctx context.Context, userID string)

	// build runs one build at a time, so indexing a big library never
	// takes more than one request's worth of the server at once.
	build sync.Mutex
	// ctx is what builds run under: Run's, so they stop with the server.
	ctx context.Context

	mu       sync.RWMutex
	indexes  map[string]*index // by link ID
	building map[string]bool
	// cannot are links whose service can't list its library.
	cannot  map[string]bool
	pending sync.WaitGroup
}

// New returns a Service.
func New(db *store.Store, links Links) *Service {
	return &Service{
		db: db, links: links, Every: 24 * time.Hour, Now: store.Now, ctx: context.Background(),
		indexes: map[string]*index{}, building: map[string]bool{}, cannot: map[string]bool{},
	}
}

// Song is a song to look up.
type Song struct {
	Artist, Title string
	// ISRC and MBID, when known, are matched before names.
	ISRC, MBID string
}

// index is what one link's library holds, by key.
type index struct {
	songs, isrcs, mbids, releases, artists set
	scannedAt, builtAt                     time.Time
	count                                  int
}

type set map[string]struct{}

func (s set) has(k string) bool {
	_, ok := s[k]
	return ok
}

// ArtistKey is how artists are compared: the simplified name, without a
// leading "the".
func ArtistKey(name string) string {
	return strings.TrimPrefix(match.Simplify(name), "the ")
}

// titleKey is a title without its harmless qualifiers (remasters, feat.),
// keeping the ones that make it a different recording: "Song (Live)" isn't
// "Song".
func titleKey(title string) string {
	t, variants := match.Title(title)
	if len(variants) > 0 {
		t += " (" + strings.Join(variants, " ") + ")"
	}
	return t
}

// SongKey is how songs are compared: artist and title.
func SongKey(artist, title string) string { return ArtistKey(artist) + "\x00" + titleKey(title) }

// ReleaseKey is how releases are compared: artist and release title.
func ReleaseKey(artist, release string) string { return ArtistKey(artist) + "\x00" + titleKey(release) }

// Owned is what one user has, across their links' indexes, at the time
// it was asked for. Its lookups are map reads.
type Owned struct {
	indexes []*index
	// Building is whether one of the user's links is being indexed now,
	// so the answer may be incomplete.
	Building bool
}

// Has reports whether the user has a song: by ISRC or MBID, or else by
// artist and title.
func (o *Owned) Has(s Song) bool {
	if o == nil {
		return false
	}
	isrc, mbid := strings.ToUpper(strings.TrimSpace(s.ISRC)), strings.ToLower(strings.TrimSpace(s.MBID))
	k := SongKey(s.Artist, s.Title)
	for _, ix := range o.indexes {
		if (isrc != "" && ix.isrcs.has(isrc)) || (mbid != "" && ix.mbids.has(mbid)) || ix.songs.has(k) {
			return true
		}
	}
	return false
}

// HasRelease reports whether the user has a release (an album, an EP) by
// an artist.
func (o *Owned) HasRelease(artist, release string) bool {
	if o == nil || strings.TrimSpace(release) == "" {
		return false
	}
	k := ReleaseKey(artist, release)
	for _, ix := range o.indexes {
		if ix.releases.has(k) {
			return true
		}
	}
	return false
}

// KnowsArtist reports whether the user has anything by an artist. It's
// for showing, never for filtering: a new album by an artist you have is
// still new to you.
func (o *Owned) KnowsArtist(artist string) bool {
	if o == nil {
		return false
	}
	k := ArtistKey(artist)
	for _, ix := range o.indexes {
		if ix.artists.has(k) {
			return true
		}
	}
	return false
}

// Holding returns an Owned holding just these songs, as if one link's
// library had them: for testing what uses a library.
func Holding(ts ...provider.Track) *Owned {
	ix := newIndex()
	for _, t := range ts {
		ix.add(t)
	}
	return &Owned{indexes: []*index{ix}}
}

// Status says how far a user's library is indexed.
type Status struct {
	// Links is how many of their links are indexed, Songs how many songs
	// those hold.
	Links, Songs int
	// BuiltAt is when the oldest of those indexes was built; zero if none.
	BuiltAt time.Time
	// Building is whether one is being indexed now.
	Building bool
}

// Status reports how far a user's library is indexed, without starting
// anything.
func (o *Owned) Status() Status {
	out := Status{Building: o.Building, Links: len(o.indexes)}
	for _, ix := range o.indexes {
		out.Songs += ix.count
		if out.BuiltAt.IsZero() || ix.builtAt.Before(out.BuiltAt) {
			out.BuiltAt = ix.builtAt
		}
	}
	return out
}

// For returns what a user has. Links never indexed are indexed in the
// background, starting now: until they're done, the answer leaves them out.
func (s *Service) For(ctx context.Context, userID string) (*Owned, error) {
	rows, err := s.libraryLinks(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := &Owned{}
	for _, l := range rows {
		ix, err := s.load(ctx, l.ID)
		if err != nil {
			return nil, err
		}
		if ix != nil {
			out.indexes = append(out.indexes, ix)
		} else {
			s.kick(l.ID, false)
		}
		s.mu.RLock()
		out.Building = out.Building || s.building[l.ID]
		s.mu.RUnlock()
	}
	return out, nil
}

// Has reports whether a user has a song. Asking about many songs, call For
// once and ask it.
func (s *Service) Has(ctx context.Context, userID string, song Song) (bool, error) {
	o, err := s.For(ctx, userID)
	return o.Has(song), err
}

// HasRelease reports whether a user has a release by an artist.
func (s *Service) HasRelease(ctx context.Context, userID, artist, release string) (bool, error) {
	o, err := s.For(ctx, userID)
	return o.HasRelease(artist, release), err
}

// KnowsArtist reports whether a user has anything by an artist.
func (s *Service) KnowsArtist(ctx context.Context, userID, artist string) (bool, error) {
	o, err := s.For(ctx, userID)
	return o.KnowsArtist(artist), err
}

// Refresh rebuilds a user's indexes in the background, now, even if the
// service says nothing changed: they just added music, perhaps.
func (s *Service) Refresh(ctx context.Context, userID string) error {
	rows, err := s.libraryLinks(ctx, userID)
	if err != nil {
		return err
	}
	for _, l := range rows {
		s.kick(l.ID, true)
	}
	return nil
}

// Wait waits for the builds in progress, for tests.
func (s *Service) Wait() { s.pending.Wait() }

// libraryLinks are a user's own links that may hold a library: not
// expired, and not known to be unable to list one.
func (s *Service) libraryLinks(ctx context.Context, userID string) ([]store.ServiceLink, error) {
	rows, err := s.links.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.DeleteFunc(rows, func(l store.ServiceLink) bool {
		return l.Status == store.LinkExpired || s.cannot[l.ID]
	}), nil
}

// load returns a link's index from memory, or from the database the
// first time; nil if it has none yet.
func (s *Service) load(ctx context.Context, linkID string) (*index, error) {
	s.mu.RLock()
	ix, ok := s.indexes[linkID]
	s.mu.RUnlock()
	if ok {
		return ix, nil
	}
	row, err := s.db.GetLibraryIndex(ctx, linkID)
	if store.IsNotFound(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	ix, err = decode(row)
	if err != nil {
		slog.Warn("library: reading a saved index; rebuilding it", "link", linkID, "err", err)
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.indexes[linkID]; ok {
		return cur, nil // a build finished meanwhile
	}
	s.indexes[linkID] = ix
	return ix, nil
}

// kick starts building a link's index in the background, unless it's
// being built already.
func (s *Service) kick(linkID string, force bool) {
	s.mu.Lock()
	if s.building[linkID] {
		s.mu.Unlock()
		return
	}
	s.building[linkID] = true
	ctx := s.ctx
	s.mu.Unlock()
	s.pending.Go(func() {
		defer func() {
			s.mu.Lock()
			delete(s.building, linkID)
			s.mu.Unlock()
		}()
		if err := s.Build(ctx, linkID, force); err != nil && ctx.Err() == nil {
			slog.Warn("library: indexing a link", "link", linkID, "err", err)
		}
	})
}

// Run rebuilds indexes older than Every, checking hourly, until ctx is
// done. Builds it starts, and ones For and Refresh start, stop with ctx.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	t := time.NewTicker(checkEvery)
	defer t.Stop()
	for {
		s.refreshStale(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// refreshStale rebuilds the indexes that are due.
func (s *Service) refreshStale(ctx context.Context) {
	rows, err := s.db.ListAllServiceLinks(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("library: listing links", "err", err)
		}
		return
	}
	now := s.Now()
	for _, l := range rows {
		if l.Status == store.LinkExpired {
			continue
		}
		ix, err := s.load(ctx, l.ID)
		if err != nil || ix == nil {
			continue // never indexed: its user hasn't asked
		}
		if now.Sub(ix.builtAt) >= s.Every {
			s.kick(l.ID, false)
		}
	}
}

// Build indexes one link's library, now, and waits for it. Unless forced,
// it only notes the time when the service says the library hasn't been
// scanned since the last build.
func (s *Service) Build(ctx context.Context, linkID string, force bool) error {
	if err := s.buildIndex(ctx, linkID, force); err != nil || s.Built == nil {
		return err
	}
	l, err := s.db.GetServiceLink(ctx, linkID)
	if err != nil {
		return err
	}
	s.Built(ctx, l.UserID)
	return nil
}

func (s *Service) buildIndex(ctx context.Context, linkID string, force bool) error {
	s.build.Lock()
	defer s.build.Unlock()
	sess, err := s.links.Open(ctx, linkID)
	if err != nil {
		return err
	}
	defer sess.Close()
	cat, ok := sess.(provider.Cataloger)
	if !ok {
		s.cannotList(linkID)
		return nil
	}
	scanned, err := cat.LibraryChanged(ctx)
	if errors.Is(err, provider.ErrUnsupported) {
		s.cannotList(linkID)
		return nil
	} else if err != nil {
		return err
	}
	prev, err := s.load(ctx, linkID)
	if err != nil {
		return err
	}
	now := s.Now()
	if !force && prev != nil && !scanned.IsZero() && scanned.Equal(prev.scannedAt) {
		if err := s.db.TouchLibraryIndex(ctx, store.TouchLibraryIndexParams{BuiltAt: now, LinkID: linkID}); err != nil {
			return err
		}
		s.mu.Lock()
		next := *prev
		next.builtAt = now
		s.indexes[linkID] = &next
		s.mu.Unlock()
		return nil
	}

	ix := newIndex()
	cursor := ""
	for {
		page, err := cat.CatalogSongs(ctx, cursor)
		if errors.Is(err, provider.ErrUnsupported) {
			s.cannotList(linkID)
			return nil
		} else if err != nil {
			return err
		}
		for _, t := range page.Items {
			ix.add(t)
		}
		if page.Next == "" || ix.count >= maxSongs {
			break
		}
		cursor = page.Next
	}
	ix.scannedAt, ix.builtAt = scanned, now
	data, err := encode(ix)
	if err != nil {
		return err
	}
	if err := s.db.PutLibraryIndex(ctx, store.PutLibraryIndexParams{
		LinkID: linkID, ScannedAt: sql.NullTime{Time: scanned, Valid: !scanned.IsZero()}, BuiltAt: now,
		Songs: int64(ix.count), Data: data,
	}); err != nil {
		return err
	}
	s.mu.Lock()
	s.indexes[linkID] = ix
	s.mu.Unlock()
	slog.Debug("library: indexed a link", "link", linkID, "songs", ix.count)
	return nil
}

// Starred lists what a user starred on their own links that can say:
// artists, albums and songs, from each link in turn. A link that fails is
// skipped, unless they all do.
func (s *Service) Starred(ctx context.Context, userID string) (provider.Starred, error) {
	rows, err := s.libraryLinks(ctx, userID)
	if err != nil {
		return provider.Starred{}, err
	}
	var out provider.Starred
	var errs []error
	for _, l := range rows {
		st, err := s.starred(ctx, l.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out.Artists = append(out.Artists, st.Artists...)
		out.Albums = append(out.Albums, st.Albums...)
		out.Songs = append(out.Songs, st.Songs...)
	}
	if len(errs) > 0 && len(errs) == len(rows) {
		return out, errors.Join(errs...)
	}
	return out, nil
}

func (s *Service) starred(ctx context.Context, linkID string) (provider.Starred, error) {
	sess, err := s.links.Open(ctx, linkID)
	if err != nil {
		return provider.Starred{}, err
	}
	defer sess.Close()
	st, ok := sess.(provider.Starrer)
	if !ok {
		return provider.Starred{}, nil
	}
	return st.Starred(ctx)
}

func (s *Service) cannotList(linkID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cannot[linkID] = true
}

func newIndex() *index {
	return &index{songs: set{}, isrcs: set{}, mbids: set{}, releases: set{}, artists: set{}}
}

// add indexes a song under each artist credited on it.
func (ix *index) add(t provider.Track) {
	ix.count++
	if isrc := strings.ToUpper(strings.TrimSpace(t.ISRC)); isrc != "" {
		ix.isrcs[isrc] = struct{}{}
	}
	if mbid := strings.ToLower(strings.TrimSpace(t.MBID)); mbid != "" {
		ix.mbids[mbid] = struct{}{}
	}
	for _, a := range t.Artists {
		k := ArtistKey(a.Name)
		if k == "" {
			continue
		}
		ix.artists[k] = struct{}{}
		if t.Title != "" {
			ix.songs[SongKey(a.Name, t.Title)] = struct{}{}
		}
		if t.Album.Title != "" {
			ix.releases[ReleaseKey(a.Name, t.Album.Title)] = struct{}{}
		}
	}
}

// saved is an index as the database keeps it, gzipped.
type saved struct {
	Songs    []string `json:"songs"`
	ISRCs    []string `json:"isrcs"`
	MBIDs    []string `json:"mbids"`
	Releases []string `json:"releases"`
	Artists  []string `json:"artists"`
}

func keys(s set) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func toSet(ks []string) set {
	out := make(set, len(ks))
	for _, k := range ks {
		out[k] = struct{}{}
	}
	return out
}

func encode(ix *index) ([]byte, error) {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	if err := json.NewEncoder(zw).Encode(saved{
		Songs: keys(ix.songs), ISRCs: keys(ix.isrcs), MBIDs: keys(ix.mbids), Releases: keys(ix.releases), Artists: keys(ix.artists),
	}); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func decode(row store.LibraryIndex) (*index, error) {
	zr, err := gzip.NewReader(bytes.NewReader(row.Data))
	if err != nil {
		return nil, fmt.Errorf("library: index of %s: %w", row.LinkID, err)
	}
	defer zr.Close()
	var sv saved
	if err := json.NewDecoder(zr).Decode(&sv); err != nil {
		return nil, fmt.Errorf("library: index of %s: %w", row.LinkID, err)
	}
	return &index{
		songs: toSet(sv.Songs), isrcs: toSet(sv.ISRCs), mbids: toSet(sv.MBIDs), releases: toSet(sv.Releases), artists: toSet(sv.Artists),
		scannedAt: row.ScannedAt.Time, builtAt: row.BuiltAt, count: int(row.Songs),
	}, nil
}
