// SPDX-License-Identifier: AGPL-3.0-only

// Package discover finds music a user doesn't have yet (MAD-817, ADR
// 0017). Taste comes from seed sets the user builds by hand: named lists
// of artists, each its own feed. The feed walks the music graph out from a
// set's seeds, as the DJ does, and keeps the songs the user doesn't
// already have (internal/library), hasn't turned down, and isn't blocking.
//
// It's personal: every set, thumb and block is one user's, and none of it
// touches a room. A thumbs-down here doesn't steer a room's DJ, and a skip
// in a room doesn't touch a set.
package discover

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Errors returned by Service.
var (
	ErrNotFound = errors.New("discovery set not found")
	// ErrTooMany is a user with as many sets, or a set with as many
	// artists, as they can have.
	ErrTooMany = errors.New("that's as many as there can be")
)

// InvalidInputError is a bad request, such as an empty name.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

// Limits.
const (
	MaxSets    = 50
	MaxArtists = 50
	MaxName    = 100
	// DefaultExplore is a new set's explore, 0 to 100.
	DefaultExplore = 40
	// DefaultFreshDays is how far back a new set's Fresh feed looks.
	DefaultFreshDays = 60
)

// FreshWindows are how far back a Fresh feed can look, in days.
var FreshWindows = []int{30, 60, 90}

// Modes: which feeds a set shows.
const (
	ModeDeep  = "deep"
	ModeFresh = "fresh"
	ModeBoth  = "both"
)

// Where a seed artist came from.
const (
	FromManual  = "manual"
	FromThumb   = "thumb"
	FromStarred = "starred"
	FromRoom    = "room"
)

// Thumbs.
const (
	ThumbUp   = "up"
	ThumbDown = "down"
)

// Graph is what's known about how music relates. musicgraph.Service is one.
type Graph interface {
	CachedArtist(ctx context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, bool, error)
	Artist(ctx context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, error)
	WarmArtist(as ...musicgraph.ArtistRef)
}

// Library says what a user already has. library.Service is one.
type Library interface {
	For(ctx context.Context, userID string) (*library.Owned, error)
}

// Deezer finds songs and artists on Deezer. musicgraph.Deezer is one.
type Deezer interface {
	FindTrack(ctx context.Context, s musicgraph.SongRef) (musicgraph.DeezerTrack, error)
	SearchArtists(ctx context.Context, q string, limit int) ([]musicgraph.DeezerArtist, error)
}

// Service keeps users' seed sets and makes their feeds.
type Service struct {
	db *store.Store
	// Graph is the music graph. Nil makes every feed empty.
	Graph Graph
	// Library is what users have. Nil leaves nothing out.
	Library Library
	// Deezer finds songs' previews and artists by name. Nil means no
	// previews, and artist search only offers the name typed.
	Deezer Deezer
	// Releases finds artists' recent releases, for Fresh feeds. Nil
	// makes every Fresh feed empty.
	Releases Releases
	// Now is the clock. Default store.Now.
	Now func() time.Time
	// BuildFor bounds building a feed; what's found by then is the feed.
	// Default 15s.
	BuildFor time.Duration
	// KeepFor is how long a built feed is kept. Default 10 minutes.
	KeepFor time.Duration

	mu    sync.Mutex
	feeds map[string]*feed
}

// New returns a Service.
func New(db *store.Store) *Service {
	return &Service{db: db, Now: store.Now, BuildFor: 15 * time.Second, KeepFor: 10 * time.Minute, feeds: map[string]*feed{}}
}

// Set is a seed set and its artists, in the order they were added.
type Set struct {
	store.DiscoverySet
	Artists []store.DiscoverySetArtist
}

// Summary is a set in a list.
type Summary = store.ListDiscoverySetsRow

// ArtistKey is how a seed artist is kept: their MBID, else their
// simplified name.
func ArtistKey(name, mbid string) string {
	if mbid = strings.ToLower(strings.TrimSpace(mbid)); mbid != "" {
		return mbid
	}
	return library.ArtistKey(name)
}

// SongKey is how a song is kept: its simplified artist and title.
func SongKey(artist, title string) string { return library.SongKey(artist, title) }

func cleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	switch {
	case name == "":
		return "", &InvalidInputError{"a name is needed"}
	case utf8.RuneCountInString(name) > MaxName:
		return "", &InvalidInputError{"that name is too long"}
	}
	return name, nil
}

func checkMode(mode string) error {
	switch mode {
	case ModeDeep, ModeFresh, ModeBoth:
		return nil
	}
	return &InvalidInputError{"mode is deep, fresh or both"}
}

func checkFreshDays(days int) error {
	if !slices.Contains(FreshWindows, days) {
		return &InvalidInputError{"fresh days is 30, 60 or 90"}
	}
	return nil
}

func checkExplore(explore int) error {
	if explore < 0 || explore > 100 {
		return &InvalidInputError{"explore is 0 to 100"}
	}
	return nil
}

// Sets lists a user's sets, most recently changed first.
func (s *Service) Sets(ctx context.Context, userID string) ([]Summary, error) {
	return s.db.ListDiscoverySets(ctx, userID)
}

// Seed is an artist to add to a set.
type Seed struct {
	Name, MBID string
}

// Create makes a set, with seeds if given.
func (s *Service) Create(ctx context.Context, userID, name string, seeds []Seed) (Set, error) {
	name, err := cleanName(name)
	if err != nil {
		return Set{}, err
	}
	if len(seeds) > MaxArtists {
		return Set{}, ErrTooMany
	}
	have, err := s.db.ListDiscoverySets(ctx, userID)
	if err != nil {
		return Set{}, err
	}
	if len(have) >= MaxSets {
		return Set{}, ErrTooMany
	}
	set, err := s.db.CreateDiscoverySet(ctx, store.CreateDiscoverySetParams{
		ID: store.NewID(), UserID: userID, Name: name, Explore: DefaultExplore, Mode: ModeBoth, Now: s.Now(),
	})
	if err != nil {
		return Set{}, err
	}
	for _, a := range seeds {
		if err := s.addArtist(ctx, set.ID, a, FromManual); err != nil {
			return Set{}, err
		}
	}
	return s.Get(ctx, userID, set.ID)
}

// Get returns one of a user's sets.
func (s *Service) Get(ctx context.Context, userID, setID string) (Set, error) {
	row, err := s.db.GetDiscoverySet(ctx, store.GetDiscoverySetParams{ID: setID, UserID: userID})
	if store.IsNotFound(err) {
		return Set{}, ErrNotFound
	} else if err != nil {
		return Set{}, err
	}
	artists, err := s.db.ListDiscoverySetArtists(ctx, setID)
	if err != nil {
		return Set{}, err
	}
	return Set{DiscoverySet: row, Artists: artists}, nil
}

// Changes are what Update changes; nil leaves a field as it is.
type Changes struct {
	Name      *string
	Explore   *int
	Mode      *string
	FreshDays *int
}

// Update renames a set, or changes its explore, mode or Fresh window.
func (s *Service) Update(ctx context.Context, userID, setID string, c Changes) (Set, error) {
	set, err := s.Get(ctx, userID, setID)
	if err != nil {
		return Set{}, err
	}
	p := store.UpdateDiscoverySetParams{
		ID: setID, UserID: userID, Name: set.Name, Explore: set.Explore, Mode: set.Mode, FreshDays: set.FreshDays, UpdatedAt: s.Now(),
	}
	if c.Name != nil {
		if p.Name, err = cleanName(*c.Name); err != nil {
			return Set{}, err
		}
	}
	if c.Explore != nil {
		if err := checkExplore(*c.Explore); err != nil {
			return Set{}, err
		}
		p.Explore = int64(*c.Explore)
	}
	if c.Mode != nil {
		if err := checkMode(*c.Mode); err != nil {
			return Set{}, err
		}
		p.Mode = *c.Mode
	}
	if c.FreshDays != nil {
		if err := checkFreshDays(*c.FreshDays); err != nil {
			return Set{}, err
		}
		p.FreshDays = int64(*c.FreshDays)
	}
	if err := s.db.UpdateDiscoverySet(ctx, p); err != nil {
		return Set{}, err
	}
	return s.Get(ctx, userID, setID)
}

// Delete deletes a set. Its thumbs stay: a song turned down stays down.
func (s *Service) Delete(ctx context.Context, userID, setID string) error {
	n, err := s.db.DeleteDiscoverySet(ctx, store.DeleteDiscoverySetParams{ID: setID, UserID: userID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	s.forget(setID)
	return nil
}

// AddArtist adds a seed artist to a set. Adding one that's there already
// changes nothing.
func (s *Service) AddArtist(ctx context.Context, userID, setID string, a Seed) (Set, error) {
	set, err := s.Get(ctx, userID, setID)
	if err != nil {
		return Set{}, err
	}
	if len(set.Artists) >= MaxArtists {
		return Set{}, ErrTooMany
	}
	if err := s.addArtist(ctx, setID, a, FromManual); err != nil {
		return Set{}, err
	}
	if err := s.touch(ctx, setID); err != nil {
		return Set{}, err
	}
	return s.Get(ctx, userID, setID)
}

func (s *Service) addArtist(ctx context.Context, setID string, a Seed, from string) error {
	name := strings.Join(strings.Fields(a.Name), " ")
	key := ArtistKey(name, a.MBID)
	if name == "" || key == "" || utf8.RuneCountInString(name) > MaxName*2 {
		return &InvalidInputError{"an artist's name is needed"}
	}
	mbid := strings.ToLower(strings.TrimSpace(a.MBID))
	return s.db.AddDiscoverySetArtist(ctx, store.AddDiscoverySetArtistParams{
		SetID: setID, ArtistKey: key, Name: name, Mbid: sql.NullString{String: mbid, Valid: mbid != ""},
		AddedFrom: from, AddedAt: s.Now(),
	})
}

// RemoveArtist takes a seed artist out of a set, by their key.
func (s *Service) RemoveArtist(ctx context.Context, userID, setID, key string) (Set, error) {
	if _, err := s.Get(ctx, userID, setID); err != nil {
		return Set{}, err
	}
	n, err := s.db.RemoveDiscoverySetArtist(ctx, store.RemoveDiscoverySetArtistParams{SetID: setID, ArtistKey: key})
	if err != nil {
		return Set{}, err
	}
	if n == 0 {
		return Set{}, ErrNotFound
	}
	if err := s.touch(ctx, setID); err != nil {
		return Set{}, err
	}
	return s.Get(ctx, userID, setID)
}

// touch marks a set changed, so its feed is built again.
func (s *Service) touch(ctx context.Context, setID string) error {
	return s.db.TouchDiscoverySet(ctx, store.TouchDiscoverySetParams{UpdatedAt: s.Now(), ID: setID})
}

// Song is a song given a thumb.
type Song struct {
	Title, Artist string
	MBID, ISRC    string
	DeezerID      int64
}

// Thumb records a thumbs-up or -down on a song, from a set's feed if
// setID is given. A thumbs-down hides the song from every one of the
// user's feeds, for good; a later thumbs-up replaces it.
func (s *Service) Thumb(ctx context.Context, userID, setID, kind string, song Song) error {
	if kind != ThumbUp && kind != ThumbDown {
		return &InvalidInputError{"a thumb is up or down"}
	}
	key := SongKey(song.Artist, song.Title)
	if strings.TrimSpace(song.Title) == "" || strings.TrimSpace(song.Artist) == "" || strings.HasPrefix(key, "\x00") {
		return &InvalidInputError{"a song's title and artist are needed"}
	}
	if setID != "" {
		if _, err := s.Get(ctx, userID, setID); err != nil {
			return err
		}
	}
	return s.db.PutDiscoveryFeedback(ctx, store.PutDiscoveryFeedbackParams{
		ID: store.NewID(), UserID: userID, SetID: sql.NullString{String: setID, Valid: setID != ""}, Kind: kind,
		SongKey: key, Title: song.Title, Artist: song.Artist,
		Mbid:      sql.NullString{String: song.MBID, Valid: song.MBID != ""},
		Isrc:      sql.NullString{String: song.ISRC, Valid: song.ISRC != ""},
		DeezerID:  sql.NullInt64{Int64: song.DeezerID, Valid: song.DeezerID != 0},
		CreatedAt: s.Now(),
	})
}

// Block keeps an artist out of every one of the user's feeds.
func (s *Service) Block(ctx context.Context, userID, name string) (store.DiscoveryBlock, error) {
	name = strings.Join(strings.Fields(name), " ")
	key := library.ArtistKey(name)
	if key == "" {
		return store.DiscoveryBlock{}, &InvalidInputError{"an artist's name is needed"}
	}
	b := store.DiscoveryBlock{UserID: userID, ArtistKey: key, Name: name, CreatedAt: s.Now()}
	return b, s.db.BlockDiscoveryArtist(ctx, store.BlockDiscoveryArtistParams(b))
}

// Unblock lets a blocked artist back in, by their key.
func (s *Service) Unblock(ctx context.Context, userID, key string) error {
	n, err := s.db.UnblockDiscoveryArtist(ctx, store.UnblockDiscoveryArtistParams{UserID: userID, ArtistKey: key})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Blocks lists the artists a user blocked.
func (s *Service) Blocks(ctx context.Context, userID string) ([]store.DiscoveryBlock, error) {
	return s.db.ListDiscoveryBlocks(ctx, userID)
}

// Artist is an artist found by name, to add as a seed.
type Artist struct {
	Name string
	// Fans is how many fans they have on Deezer, Picture their picture's
	// ID there; zero if unknown.
	Fans    int64
	Picture string
}

// SearchArtists finds artists to seed a set with: any artist, not just
// ones in the user's library. Without Deezer, or when it fails, it offers
// the name as typed, which works as a seed as well as any.
func (s *Service) SearchArtists(ctx context.Context, q string) ([]Artist, error) {
	q = strings.Join(strings.Fields(q), " ")
	if q == "" {
		return []Artist{}, nil
	}
	typed := []Artist{{Name: q}}
	if s.Deezer == nil {
		return typed, nil
	}
	found, err := s.Deezer.SearchArtists(ctx, q, 10)
	if err != nil || len(found) == 0 {
		return typed, nil //nolint:nilerr // the name as typed is still a seed
	}
	out := make([]Artist, 0, len(found))
	for _, a := range found {
		out = append(out, Artist{Name: a.Name, Fans: a.Fans, Picture: a.Picture})
	}
	return out, nil
}

// LibraryStatus reports how far a user's library is indexed.
func (s *Service) LibraryStatus(ctx context.Context, userID string) (library.Status, error) {
	if s.Library == nil {
		return library.Status{}, nil
	}
	o, err := s.Library.For(ctx, userID)
	if err != nil {
		return library.Status{}, err
	}
	return o.Status(), nil
}
