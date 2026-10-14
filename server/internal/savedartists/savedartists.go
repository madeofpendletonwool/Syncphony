// SPDX-License-Identifier: AGPL-3.0-only

// Package savedartists keeps the artists someone saved (MAD-839): "that's
// good, who is it?" while a friend's song plays. A saved artist is personal
// and belongs to no service. It's known by its simplified name, so saving
// it again from another service finds the same one, and the services it
// was seen on are kept only as hints for opening its page.
package savedartists

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// ErrNotFound is a saved artist that isn't the caller's, or doesn't exist.
var ErrNotFound = errors.New("saved artist not found")

// InvalidInputError is a bad request, such as no name.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

const (
	// MaxName is the longest name, in characters.
	MaxName = 200
	// maxRefs is how many services an artist remembers being seen on.
	maxRefs = 10
)

// Ref is where an artist was seen: its ID on a link.
type Ref struct {
	LinkID   string `json:"linkId"`
	ArtistID string `json:"artistId"`
}

// Artist is a saved artist.
type Artist struct {
	store.SavedArtist
	// Refs are the links it was seen on, newest first.
	Refs []Ref
}

// Pictures knows artists' pictures. It's musicgraph.Service in production.
type Pictures interface {
	CachedArtist(ctx context.Context, a musicgraph.ArtistRef) (musicgraph.Artist, bool, error)
	WarmArtist(as ...musicgraph.ArtistRef)
}

// Service keeps saved artists.
type Service struct {
	db    *store.Store
	rooms *rooms.Service
	// pics may be nil: saved artists then have no pictures.
	pics Pictures
	// Now is the clock. Default store.Now.
	Now func() time.Time
}

// New returns a Service. pics may be nil.
func New(db *store.Store, rs *rooms.Service, pics Pictures) *Service {
	return &Service{db: db, rooms: rs, pics: pics, Now: store.Now}
}

// Input is an artist to save: by name, with where it was seen (LinkID and
// ArtistID) if known, or as the ArtistIndex-th artist of a room's song
// (ItemID).
type Input struct {
	Name             string
	LinkID, ArtistID string
	ItemID           string
	ArtistIndex      int
}

// Save saves an artist for userID. Saving one already saved adds where it
// was seen this time, and reports created false.
func (s *Service) Save(ctx context.Context, userID string, in Input) (Artist, bool, error) {
	var roomID, queuedBy sql.NullString
	if in.ItemID != "" {
		it, err := s.item(ctx, userID, in.ItemID)
		if err != nil {
			return Artist{}, false, err
		}
		var t provider.Track
		if err := json.Unmarshal([]byte(it.Metadata), &t); err != nil {
			return Artist{}, false, err
		}
		if in.ArtistIndex < 0 || in.ArtistIndex >= len(t.Artists) {
			return Artist{}, false, &InvalidInputError{"that song has no such artist"}
		}
		a := t.Artists[in.ArtistIndex]
		in.Name, in.LinkID, in.ArtistID = a.Name, it.LinkID.String, a.ID
		roomID = sql.NullString{String: it.RoomID, Valid: true}
		// An autopilot song is nobody's: it's only seeded by AddedBy's taste.
		queuedBy = sql.NullString{String: it.AddedBy, Valid: !it.Autopilot.Valid}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > MaxName {
		return Artist{}, false, &InvalidInputError{"name an artist, up to 200 characters"}
	}
	key := match.Simplify(name)
	if key == "" {
		return Artist{}, false, &InvalidInputError{"that name has no letters or digits"}
	}
	var seen []Ref
	if in.LinkID != "" && in.ArtistID != "" {
		seen = []Ref{{LinkID: in.LinkID, ArtistID: in.ArtistID}}
	}

	old, err := s.db.GetSavedArtistByKey(ctx, store.GetSavedArtistByKeyParams{UserID: userID, Key: key})
	if err == nil {
		a, err := toArtist(old)
		if err != nil {
			return Artist{}, false, err
		}
		if merged := mergeRefs(seen, a.Refs); !slices.Equal(merged, a.Refs) {
			if err := s.db.UpdateSavedArtistRefs(ctx, store.UpdateSavedArtistRefsParams{Refs: encode(merged), ID: a.ID}); err != nil {
				return Artist{}, false, err
			}
			a.Refs = merged
			a.SavedArtist.Refs = encode(merged)
		}
		return a, false, nil
	} else if !store.IsNotFound(err) {
		return Artist{}, false, err
	}

	row := store.SavedArtist{
		ID: store.NewID(), UserID: userID, Key: key, Name: name, Refs: encode(mergeRefs(seen, nil)),
		Picture: s.picture(ctx, name), RoomID: roomID, QueuedBy: queuedBy, SavedAt: s.Now(),
	}
	if err := s.db.InsertSavedArtist(ctx, store.InsertSavedArtistParams(row)); err != nil {
		return Artist{}, false, err
	}
	a, err := toArtist(row)
	return a, true, err
}

// List returns userID's saved artists, most recently saved first. Pictures
// the music graph has learned since are filled in.
func (s *Service) List(ctx context.Context, userID string) ([]Artist, error) {
	rows, err := s.db.ListSavedArtists(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Artist, len(rows))
	for i, r := range rows {
		if !r.Picture.Valid {
			if p := s.picture(ctx, r.Name); p.Valid {
				if err := s.db.SetSavedArtistPicture(ctx, store.SetSavedArtistPictureParams{Picture: p, ID: r.ID}); err != nil {
					return nil, err
				}
				r.Picture = p
			}
		}
		if out[i], err = toArtist(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Delete unsaves one of userID's artists.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	n, err := s.db.DeleteSavedArtist(ctx, store.DeleteSavedArtistParams{ID: id, UserID: userID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// picture is the artist's picture if the music graph has it cached. If
// not, it's fetched in the background, for next time.
func (s *Service) picture(ctx context.Context, name string) sql.NullString {
	if s.pics == nil {
		return sql.NullString{}
	}
	ref := musicgraph.ArtistRef{Name: name}
	a, ok, err := s.pics.CachedArtist(ctx, ref)
	if err != nil || !ok {
		s.pics.WarmArtist(ref)
		return sql.NullString{}
	}
	return sql.NullString{String: a.Picture, Valid: a.Picture != ""}
}

// item returns a room's song, if userID can enter the room.
func (s *Service) item(ctx context.Context, userID, itemID string) (store.QueueItem, error) {
	it, err := s.db.GetQueueItem(ctx, itemID)
	if store.IsNotFound(err) {
		return it, &InvalidInputError{"that song isn't in any room"}
	} else if err != nil {
		return it, err
	}
	r, err := s.rooms.Get(ctx, it.RoomID)
	if err != nil {
		return it, err
	}
	ok, err := s.rooms.CanEnter(ctx, userID, r)
	if err != nil {
		return it, err
	}
	if !ok {
		return it, rooms.ErrNotFound
	}
	return it, nil
}

// mergeRefs puts seen in front of refs, without repeats, keeping maxRefs.
func mergeRefs(seen, refs []Ref) []Ref {
	out := make([]Ref, 0, len(seen)+len(refs))
	for _, r := range slices.Concat(seen, refs) {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out[:min(len(out), maxRefs)]
}

func encode(refs []Ref) string {
	b, _ := json.Marshal(refs) //nolint:errchkjson // a slice of plain structs always marshals
	return string(b)
}

func toArtist(r store.SavedArtist) (Artist, error) {
	a := Artist{SavedArtist: r}
	if err := json.Unmarshal([]byte(r.Refs), &a.Refs); err != nil {
		return a, err
	}
	return a, nil
}
