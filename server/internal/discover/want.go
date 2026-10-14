// SPDX-License-Identifier: AGPL-3.0-only

package discover

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// The Want list (MAD-824): songs and releases you liked in Discover, kept
// until they turn up in your library. Syncphony never gets them for you;
// once your Navidrome has them, the next index marks them got.

// Kinds of things wanted.
const (
	WantTrack   = "track"
	WantRelease = "release"
)

// What became of a thing wanted.
const (
	Wanted    = "wanted"
	Got       = "got"
	Dismissed = "dismissed"
)

// How a thing came to be got.
const (
	GotAuto   = "auto"
	GotManual = "manual"
)

// Want is a song or release to want.
type Want struct {
	// Kind is WantTrack or WantRelease.
	Kind string
	// Artist and Title name it; for a song, Release is its album's title,
	// if known.
	Artist, Title, Release string
	// MBID is a song's recording or a release's release group, ISRC a
	// song's; DeezerID and Cover are its ID and cover on Deezer.
	MBID, ISRC string
	DeezerID   int64
	Cover      string
	// SetID is the set whose feed it was in, if any.
	SetID string
}

// WantList is everything a user wants, or wanted.
type WantList struct {
	// Items are newest first.
	Items []store.WantItem
	// Landed is how many turned up in the library since the user last
	// looked (SeeLanded).
	Landed int
}

// wantKey is how a thing wanted is kept: its simplified artist and title.
func wantKey(kind, artist, title string) string {
	if kind == WantRelease {
		return library.ReleaseKey(artist, title)
	}
	return SongKey(artist, title)
}

// AddWant puts a song or release on the user's Want list. Wanting one
// that's there already returns it as it is, unless it was dismissed: then
// it's wanted again.
func (s *Service) AddWant(ctx context.Context, userID string, w Want) (store.WantItem, error) {
	if w.Kind != WantTrack && w.Kind != WantRelease {
		return store.WantItem{}, &InvalidInputError{"a want is a track or a release"}
	}
	w.Artist, w.Title = strings.Join(strings.Fields(w.Artist), " "), strings.Join(strings.Fields(w.Title), " ")
	key := wantKey(w.Kind, w.Artist, w.Title)
	if w.Artist == "" || w.Title == "" || strings.HasPrefix(key, "\x00") {
		return store.WantItem{}, &InvalidInputError{"an artist and a title are needed"}
	}
	if w.SetID != "" {
		if _, err := s.Get(ctx, userID, w.SetID); err != nil {
			return store.WantItem{}, err
		}
	}
	err := s.db.AddWantItem(ctx, store.AddWantItemParams{
		ID: store.NewID(), UserID: userID, SetID: sql.NullString{String: w.SetID, Valid: w.SetID != ""},
		Kind: w.Kind, ItemKey: key, Artist: w.Artist, Title: w.Title, ReleaseTitle: strings.TrimSpace(w.Release),
		Mbid:     sql.NullString{String: w.MBID, Valid: w.MBID != ""},
		Isrc:     sql.NullString{String: w.ISRC, Valid: w.ISRC != ""},
		DeezerID: sql.NullInt64{Int64: w.DeezerID, Valid: w.DeezerID != 0},
		Cover:    w.Cover, AddedAt: s.Now(),
	})
	if err != nil {
		return store.WantItem{}, err
	}
	it, err := s.db.GetWantItemByKey(ctx, store.GetWantItemByKeyParams{UserID: userID, Kind: w.Kind, ItemKey: key})
	if err != nil || it.Status != Dismissed {
		return it, err
	}
	return s.SetWant(ctx, userID, it.ID, Wanted)
}

// Wants lists the user's Want list, and how many of it landed since they
// last looked.
func (s *Service) Wants(ctx context.Context, userID string) (WantList, error) {
	items, err := s.db.ListWantItems(ctx, userID)
	if err != nil {
		return WantList{}, err
	}
	landed, err := s.db.CountLandedWantItems(ctx, userID)
	return WantList{Items: items, Landed: int(landed)}, err
}

// SetWant marks a thing wanted, got (you say so) or dismissed.
func (s *Service) SetWant(ctx context.Context, userID, id, status string) (store.WantItem, error) {
	p := store.SetWantItemStatusParams{Status: status, ID: id, UserID: userID}
	switch status {
	case Wanted, Dismissed:
	case Got:
		p.GotAt = sql.NullTime{Time: s.Now(), Valid: true}
		p.GotBy = sql.NullString{String: GotManual, Valid: true}
		p.GotSeen = 1 // you know: you said so
	default:
		return store.WantItem{}, &InvalidInputError{"a want is wanted, got or dismissed"}
	}
	n, err := s.db.SetWantItemStatus(ctx, p)
	if err != nil {
		return store.WantItem{}, err
	}
	if n == 0 {
		return store.WantItem{}, ErrNotFound
	}
	return s.db.GetWantItem(ctx, store.GetWantItemParams{ID: id, UserID: userID})
}

// SeeLanded notes that the user saw what landed in their library.
func (s *Service) SeeLanded(ctx context.Context, userID string) error {
	return s.db.SeeLandedWantItems(ctx, userID)
}

// CheckWants marks what the user wants got once it's in their library:
// a song by ISRC or MBID first, then by artist and title; a release by
// artist and title. The library calls it after each index it builds. It
// returns how many it marked.
func (s *Service) CheckWants(ctx context.Context, userID string) (int, error) {
	if s.Library == nil {
		return 0, nil
	}
	owned, err := s.Library.For(ctx, userID)
	if err != nil {
		return 0, err
	}
	items, err := s.db.ListWantItems(ctx, userID)
	if err != nil {
		return 0, err
	}
	got := 0
	for _, it := range items {
		if it.Status != Wanted || !hasWant(owned, it) {
			continue
		}
		n, err := s.db.MarkWantItemGot(ctx, store.MarkWantItemGotParams{GotAt: sql.NullTime{Time: s.Now(), Valid: true}, ID: it.ID})
		if err != nil {
			return got, err
		}
		got += int(n)
	}
	if got > 0 {
		slog.Debug("discover: wanted things landed in a library", "user", userID, "got", got)
	}
	return got, nil
}

// hasWant reports whether a library has a thing wanted.
func hasWant(o *library.Owned, it store.WantItem) bool {
	if it.Kind == WantRelease {
		return o.HasRelease(it.Artist, it.Title)
	}
	return o.Has(library.Song{Artist: it.Artist, Title: it.Title, ISRC: it.Isrc.String, MBID: it.Mbid.String})
}

// wantFromSong is the Want of a song given a thumbs-up.
func wantFromSong(setID string, song Song) Want {
	return Want{
		Kind: WantTrack, Artist: song.Artist, Title: song.Title, Release: song.Album,
		MBID: song.MBID, ISRC: song.ISRC, DeezerID: song.DeezerID, Cover: song.Cover, SetID: setID,
	}
}
