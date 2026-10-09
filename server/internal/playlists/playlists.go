// SPDX-License-Identifier: AGPL-3.0-only

// Package playlists keeps Syncphony's own playlists (MAD-737, ADR 0016).
// They live on the server, not on a service: each song keeps the snapshot
// and link it came from, like a queue item, so one playlist can hold
// everyone's services. That's what lets a night be saved as it played.
//
// A playlist belongs to whoever made it. Shared with a room, everyone who
// can enter the room sees it and can queue from it; only its owner (or an
// admin) changes it.
package playlists

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Errors returned by Service, besides rooms.ErrNotFound and the errors of
// opening links and looking up tracks.
var (
	ErrNotFound  = errors.New("playlist not found")
	ErrForbidden = errors.New("only the playlist's owner can change it")
	// ErrNothingPlayed is saving a night that played nothing to keep.
	ErrNothingPlayed = errors.New("nothing played then to save")
)

// InvalidInputError is a bad request, such as an empty name.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

// Limits.
const (
	// MaxSongs is the most songs a playlist holds: a long night, several
	// times over.
	MaxSongs = 1000
	// MaxAdd is the most songs one AddSongs adds, like queue.MaxAdd.
	MaxAdd = 100
	// MaxName is the longest name, in characters.
	MaxName = 100
	// Covers is how many songs' artwork make a playlist's cover, picked
	// from its first coverReach songs: different albums where it can.
	Covers     = 4
	coverReach = 50
)

// Tracks opens a user's links, to look up songs added from search. It's
// links.Service in production.
type Tracks interface {
	OpenFor(ctx context.Context, userID, linkID string) (provider.Session, error)
}

// Actor is who's asking: a user, and whether they're an admin.
type Actor = rooms.Actor

// Service keeps playlists.
type Service struct {
	db     *store.Store
	rooms  *rooms.Service
	tracks Tracks
	// Now is the clock. Default store.Now.
	Now func() time.Time
}

// New returns a Service.
func New(db *store.Store, rs *rooms.Service, tracks Tracks) *Service {
	return &Service{db: db, rooms: rs, tracks: tracks, Now: store.Now}
}

// Summary is a playlist in a list: how many songs, and its first few for
// a cover.
type Summary struct {
	store.Playlist
	Songs  int
	Covers []store.PlaylistSong
}

// Playlist is a playlist and its songs, in order.
type Playlist struct {
	store.Playlist
	Songs []store.PlaylistSong
}

// SongRef is a song to add: from search (LinkID and TrackID), or one a
// room had (ItemID).
type SongRef struct {
	LinkID, TrackID string
	ItemID          string
}

// NightOptions say what of a night goes into its playlist.
type NightOptions struct {
	Name string
	// KeepSkipped keeps songs that were skipped. KeepRepeats keeps a song
	// every time it played, not just the first.
	KeepSkipped, KeepRepeats bool
	// Share shares it with the room.
	Share bool
}

// Track is a playlist song's track, with its ref, as the queue wants it.
func Track(s store.PlaylistSong) (provider.Track, error) {
	var t provider.Track
	if err := json.Unmarshal([]byte(s.Metadata), &t); err != nil {
		return t, err
	}
	t.Ref = provider.TrackRef{Provider: s.Provider, LinkID: s.LinkID.String, ID: s.TrackID}
	return t, nil
}

// canSee says whether a can see p: it's theirs, or shared with a room
// they can enter.
func (s *Service) canSee(ctx context.Context, a Actor, p store.Playlist) (bool, error) {
	if p.OwnerID == a.UserID {
		return true, nil
	}
	if !p.RoomID.Valid {
		return false, nil
	}
	r, err := s.rooms.Get(ctx, p.RoomID.String)
	if errors.Is(err, rooms.ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return s.rooms.CanEnter(ctx, a.UserID, r)
}

// get returns a playlist a can see.
func (s *Service) get(ctx context.Context, a Actor, id string) (store.Playlist, error) {
	p, err := s.db.GetPlaylist(ctx, id)
	if store.IsNotFound(err) {
		return p, ErrNotFound
	} else if err != nil {
		return p, err
	}
	ok, err := s.canSee(ctx, a, p)
	if err != nil {
		return p, err
	}
	if !ok {
		return p, ErrNotFound
	}
	return p, nil
}

// editable returns a playlist a may change: theirs, or any they can see
// if they're an admin.
func (s *Service) editable(ctx context.Context, a Actor, id string) (store.Playlist, error) {
	p, err := s.get(ctx, a, id)
	if err != nil {
		return p, err
	}
	if p.OwnerID != a.UserID && !a.Admin {
		return p, ErrForbidden
	}
	return p, nil
}

// List returns the playlists a can see, most recently changed first.
func (s *Service) List(ctx context.Context, a Actor) ([]Summary, error) {
	rows, err := s.db.ListPlaylistsFor(ctx, a.UserID)
	if err != nil {
		return nil, err
	}
	enter := map[string]bool{}
	out := make([]Summary, 0, len(rows))
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		p := store.Playlist{
			ID: r.ID, OwnerID: r.OwnerID, Name: r.Name, RoomID: r.RoomID,
			NightRoomID: r.NightRoomID, NightFrom: r.NightFrom, NightTo: r.NightTo,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}
		if p.OwnerID != a.UserID {
			ok, seen := enter[p.RoomID.String]
			if !seen {
				var err error
				if ok, err = s.canSee(ctx, a, p); err != nil {
					return nil, err
				}
				enter[p.RoomID.String] = ok
			}
			if !ok {
				continue
			}
		}
		out = append(out, Summary{Playlist: p, Songs: int(r.Songs)})
		ids = append(ids, p.ID)
	}
	if len(ids) == 0 {
		return out, nil
	}
	songs, err := s.db.PlaylistCovers(ctx, store.PlaylistCoversParams{Ids: ids, N: coverReach})
	if err != nil {
		return nil, err
	}
	byID := map[string][]store.PlaylistSong{}
	for _, c := range songs {
		byID[c.PlaylistID] = append(byID[c.PlaylistID], c)
	}
	for i := range out {
		out[i].Covers = covers(byID[out[i].ID])
	}
	return out, nil
}

// covers picks the songs whose artwork makes a playlist's cover: the
// first Covers with artwork from different albums, or failing that, the
// first with artwork.
func covers(songs []store.PlaylistSong) []store.PlaylistSong {
	seen := map[string]bool{}
	var out []store.PlaylistSong
	for _, sg := range songs {
		var t provider.Track
		if err := json.Unmarshal([]byte(sg.Metadata), &t); err != nil || t.Artwork == "" {
			continue
		}
		k := sg.Provider + "\x00" + cmp.Or(t.Album.Title, string(t.Artwork))
		if seen[k] {
			continue
		}
		seen[k] = true
		if out = append(out, sg); len(out) == Covers {
			break
		}
	}
	return out
}

// Get returns a playlist a can see, with its songs.
func (s *Service) Get(ctx context.Context, a Actor, id string) (Playlist, error) {
	p, err := s.get(ctx, a, id)
	if err != nil {
		return Playlist{}, err
	}
	songs, err := s.db.ListPlaylistSongs(ctx, id)
	return Playlist{Playlist: p, Songs: songs}, err
}

// Song returns one of the songs of a playlist a can see.
func (s *Service) Song(ctx context.Context, a Actor, playlistID, songID string) (store.PlaylistSong, error) {
	if _, err := s.get(ctx, a, playlistID); err != nil {
		return store.PlaylistSong{}, err
	}
	song, err := s.db.GetPlaylistSong(ctx, songID)
	if store.IsNotFound(err) || (err == nil && song.PlaylistID != playlistID) {
		return song, ErrNotFound
	}
	return song, err
}

// SongByID returns a song of any playlist a can see, for queueing it.
func (s *Service) SongByID(ctx context.Context, a Actor, songID string) (store.PlaylistSong, error) {
	song, err := s.db.GetPlaylistSong(ctx, songID)
	if store.IsNotFound(err) {
		return song, ErrNotFound
	} else if err != nil {
		return song, err
	}
	if _, err := s.get(ctx, a, song.PlaylistID); err != nil {
		return song, err
	}
	return song, nil
}

// cleanName trims a name and checks it.
func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", &InvalidInputError{"give the playlist a name"}
	case utf8.RuneCountInString(name) > MaxName:
		return "", &InvalidInputError{fmt.Sprintf("keep the name under %d characters", MaxName)}
	}
	return name, nil
}

// shareable checks a may share with a room: they can enter it.
func (s *Service) shareable(ctx context.Context, a Actor, roomID string) (sql.NullString, error) {
	if roomID == "" {
		return sql.NullString{}, nil
	}
	r, err := s.rooms.Get(ctx, roomID)
	if err != nil {
		return sql.NullString{}, err
	}
	ok, err := s.rooms.CanEnter(ctx, a.UserID, r)
	if err != nil {
		return sql.NullString{}, err
	}
	if !ok {
		return sql.NullString{}, rooms.ErrNotFound
	}
	return sql.NullString{String: roomID, Valid: true}, nil
}

// Create makes an empty playlist for a, shared with roomID unless it's "".
func (s *Service) Create(ctx context.Context, a Actor, name, roomID string) (Playlist, error) {
	name, err := cleanName(name)
	if err != nil {
		return Playlist{}, err
	}
	room, err := s.shareable(ctx, a, roomID)
	if err != nil {
		return Playlist{}, err
	}
	p, err := s.db.CreatePlaylist(ctx, store.CreatePlaylistParams{
		ID: store.NewID(), OwnerID: a.UserID, Name: name, RoomID: room, Now: s.Now(),
	})
	return Playlist{Playlist: p, Songs: []store.PlaylistSong{}}, err
}

// Update renames a playlist, or shares it with a room ("" to stop
// sharing). Nil leaves a field as it is.
func (s *Service) Update(ctx context.Context, a Actor, id string, name, roomID *string) (Playlist, error) {
	p, err := s.editable(ctx, a, id)
	if err != nil {
		return Playlist{}, err
	}
	if name != nil {
		if p.Name, err = cleanName(*name); err != nil {
			return Playlist{}, err
		}
	}
	if roomID != nil {
		if p.RoomID, err = s.shareable(ctx, a, *roomID); err != nil {
			return Playlist{}, err
		}
	}
	if err := s.db.UpdatePlaylist(ctx, store.UpdatePlaylistParams{Name: p.Name, RoomID: p.RoomID, UpdatedAt: s.Now(), ID: id}); err != nil {
		return Playlist{}, err
	}
	return s.Get(ctx, a, id)
}

// Delete deletes a playlist.
func (s *Service) Delete(ctx context.Context, a Actor, id string) error {
	if _, err := s.editable(ctx, a, id); err != nil {
		return err
	}
	return s.db.DeletePlaylist(ctx, id)
}

// song is a song to put in a playlist.
type song struct {
	track   provider.Track
	addedBy string
}

// lookup fetches the songs to add: from search through a's own links, or
// from a room a can enter.
func (s *Service) lookup(ctx context.Context, a Actor, refs []SongRef) ([]song, error) {
	sessions := map[string]provider.Session{}
	defer func() {
		for _, sess := range sessions {
			sess.Close()
		}
	}()
	out := make([]song, len(refs))
	for i, r := range refs {
		if r.ItemID != "" {
			it, err := s.item(ctx, a, r.ItemID)
			if err != nil {
				return nil, err
			}
			var t provider.Track
			if err := json.Unmarshal([]byte(it.Metadata), &t); err != nil {
				return nil, err
			}
			t.Ref = provider.TrackRef{Provider: it.Provider, LinkID: it.LinkID.String, ID: it.TrackID}
			out[i] = song{track: t, addedBy: a.UserID}
			continue
		}
		if r.LinkID == "" || r.TrackID == "" {
			return nil, &InvalidInputError{"each song needs a linkId and trackId, or an itemId"}
		}
		sess, ok := sessions[r.LinkID]
		if !ok {
			var err error
			if sess, err = s.tracks.OpenFor(ctx, a.UserID, r.LinkID); err != nil {
				return nil, err
			}
			sessions[r.LinkID] = sess
		}
		t, err := sess.Track(ctx, r.TrackID)
		if err != nil {
			return nil, err
		}
		out[i] = song{track: t, addedBy: a.UserID}
	}
	return out, nil
}

// item returns a queue item of a room a can enter.
func (s *Service) item(ctx context.Context, a Actor, itemID string) (store.QueueItem, error) {
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
	ok, err := s.rooms.CanEnter(ctx, a.UserID, r)
	if err != nil {
		return it, err
	}
	if !ok {
		return it, rooms.ErrNotFound
	}
	return it, nil
}

// AddSongs adds songs to the end of a playlist, in the order given.
func (s *Service) AddSongs(ctx context.Context, a Actor, id string, refs []SongRef) (Playlist, error) {
	if len(refs) == 0 {
		return Playlist{}, &InvalidInputError{"add at least one song"}
	}
	if len(refs) > MaxAdd {
		return Playlist{}, &InvalidInputError{fmt.Sprintf("add at most %d songs at a time", MaxAdd)}
	}
	if _, err := s.editable(ctx, a, id); err != nil {
		return Playlist{}, err
	}
	// Look the songs up first: services can be slow.
	songs, err := s.lookup(ctx, a, refs)
	if err != nil {
		return Playlist{}, err
	}
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		return s.append(ctx, q, id, songs)
	})
	if err != nil {
		return Playlist{}, err
	}
	return s.Get(ctx, a, id)
}

// append adds songs to the end of a playlist and marks it changed.
func (s *Service) append(ctx context.Context, q *store.Queries, id string, songs []song) error {
	n, err := q.CountPlaylistSongs(ctx, id)
	if err != nil {
		return err
	}
	if int(n)+len(songs) > MaxSongs {
		return &InvalidInputError{fmt.Sprintf("a playlist holds at most %d songs", MaxSongs)}
	}
	now := s.Now()
	for i, sg := range songs {
		meta, err := json.Marshal(sg.track)
		if err != nil {
			return err
		}
		if err := q.AddPlaylistSong(ctx, store.AddPlaylistSongParams{
			ID: store.NewID(), PlaylistID: id, Position: n + int64(i),
			Provider: sg.track.Ref.Provider, LinkID: sql.NullString{String: sg.track.Ref.LinkID, Valid: sg.track.Ref.LinkID != ""},
			TrackID: sg.track.Ref.ID, Metadata: string(meta),
			AddedBy: sql.NullString{String: sg.addedBy, Valid: sg.addedBy != ""}, AddedAt: now,
		}); err != nil {
			return err
		}
	}
	return q.TouchPlaylist(ctx, store.TouchPlaylistParams{UpdatedAt: now, ID: id})
}

// RemoveSong takes a song out of a playlist.
func (s *Service) RemoveSong(ctx context.Context, a Actor, id, songID string) (Playlist, error) {
	if _, err := s.editable(ctx, a, id); err != nil {
		return Playlist{}, err
	}
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		songs, err := q.ListPlaylistSongs(ctx, id)
		if err != nil {
			return err
		}
		at := indexOf(songs, songID)
		if at < 0 {
			return ErrNotFound
		}
		if err := q.DeletePlaylistSong(ctx, songID); err != nil {
			return err
		}
		return s.renumber(ctx, q, id, append(songs[:at:at], songs[at+1:]...))
	})
	if err != nil {
		return Playlist{}, err
	}
	return s.Get(ctx, a, id)
}

// MoveSong puts a song at position (0 is the top). Positions past the end
// mean the end.
func (s *Service) MoveSong(ctx context.Context, a Actor, id, songID string, position int) (Playlist, error) {
	if position < 0 {
		return Playlist{}, &InvalidInputError{"position can't be negative"}
	}
	if _, err := s.editable(ctx, a, id); err != nil {
		return Playlist{}, err
	}
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		songs, err := q.ListPlaylistSongs(ctx, id)
		if err != nil {
			return err
		}
		at := indexOf(songs, songID)
		if at < 0 {
			return ErrNotFound
		}
		moved := songs[at]
		rest := append(songs[:at:at], songs[at+1:]...)
		position = min(position, len(rest))
		order := append(append(rest[:position:position], moved), rest[position:]...)
		return s.renumber(ctx, q, id, order)
	})
	if err != nil {
		return Playlist{}, err
	}
	return s.Get(ctx, a, id)
}

// renumber gives songs the positions of their order, where they differ,
// and marks the playlist changed.
func (s *Service) renumber(ctx context.Context, q *store.Queries, id string, songs []store.PlaylistSong) error {
	for i, sg := range songs {
		if sg.Position == int64(i) {
			continue
		}
		if err := q.SetPlaylistSongPosition(ctx, store.SetPlaylistSongPositionParams{Position: int64(i), ID: sg.ID}); err != nil {
			return err
		}
	}
	return q.TouchPlaylist(ctx, store.TouchPlaylistParams{UpdatedAt: s.Now(), ID: id})
}

func indexOf(songs []store.PlaylistSong, id string) int {
	for i, sg := range songs {
		if sg.ID == id {
			return i
		}
	}
	return -1
}

// NightSongs picks the songs of a night to save, in the order they
// played: songs that played (to the end, or skipped if keepSkipped), each
// song once unless keepRepeats. Songs whose service was unlinked are left
// out: they can't be queued again.
func NightSongs(plays []rooms.Played, keepSkipped, keepRepeats bool) []rooms.Played {
	seen := map[string]bool{}
	var out []rooms.Played
	for _, p := range plays {
		switch {
		case p.EndReason == store.EndSkipped && !keepSkipped,
			p.EndReason != store.EndFinished && p.EndReason != store.EndSkipped,
			!p.Item.LinkID.Valid:
			continue
		}
		if !keepRepeats {
			keys := []string{"ref\x00" + p.Item.Provider + "\x00" + p.Item.TrackID}
			var t provider.Track
			if err := json.Unmarshal([]byte(p.Item.Metadata), &t); err == nil && t.ISRC != "" {
				keys = append(keys, "isrc\x00"+t.ISRC)
			}
			dup := false
			for _, k := range keys {
				dup = dup || seen[k]
				seen[k] = true
			}
			if dup {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// SaveNight saves the songs a room played in [from, to) as a playlist of
// a's. Each song keeps who queued it.
func (s *Service) SaveNight(ctx context.Context, a Actor, roomID string, from, to time.Time, opts NightOptions) (Playlist, error) {
	name, err := cleanName(opts.Name)
	if err != nil {
		return Playlist{}, err
	}
	if !to.After(from) {
		return Playlist{}, &InvalidInputError{"the night has to end after it starts"}
	}
	share := ""
	if opts.Share {
		share = roomID
	}
	room, err := s.shareable(ctx, a, share)
	if err != nil {
		return Playlist{}, err
	}
	plays, err := s.rooms.Plays(ctx, roomID, from, to)
	if err != nil {
		return Playlist{}, err
	}
	picked := NightSongs(plays, opts.KeepSkipped, opts.KeepRepeats)
	if len(picked) == 0 {
		return Playlist{}, ErrNothingPlayed
	}
	picked = picked[:min(len(picked), MaxSongs)]
	songs := make([]song, len(picked))
	for i, p := range picked {
		var t provider.Track
		if err := json.Unmarshal([]byte(p.Item.Metadata), &t); err != nil {
			return Playlist{}, err
		}
		t.Ref = provider.TrackRef{Provider: p.Item.Provider, LinkID: p.Item.LinkID.String, ID: p.Item.TrackID}
		// Autopilot's songs were nobody's pick.
		by := p.Item.AddedBy
		if p.Item.IsAutopilot() {
			by = ""
		}
		songs[i] = song{track: t, addedBy: by}
	}
	var id string
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		p, err := q.CreatePlaylist(ctx, store.CreatePlaylistParams{
			ID: store.NewID(), OwnerID: a.UserID, Name: name, RoomID: room,
			NightRoomID: sql.NullString{String: roomID, Valid: true},
			NightFrom:   sql.NullTime{Time: from, Valid: true}, NightTo: sql.NullTime{Time: to, Valid: true},
			Now: s.Now(),
		})
		if err != nil {
			return err
		}
		id = p.ID
		return s.append(ctx, q, id, songs)
	})
	if err != nil {
		return Playlist{}, err
	}
	return s.Get(ctx, a, id)
}

// ForNight returns the playlists a can see that were saved from a room's
// night beginning in [from, to). With a zero Actor (the big screen), only
// those shared with the room.
func (s *Service) ForNight(ctx context.Context, a Actor, roomID string, from, to time.Time) ([]store.Playlist, error) {
	ps, err := s.db.NightPlaylists(ctx, store.NightPlaylistsParams{RoomID: sql.NullString{String: roomID, Valid: true}, FromAt: sql.NullTime{Time: from, Valid: true}, ToAt: sql.NullTime{Time: to, Valid: true}})
	if err != nil {
		return nil, err
	}
	out := []store.Playlist{}
	for _, p := range ps {
		shared := p.RoomID.Valid && p.RoomID.String == roomID
		if shared || (a.UserID != "" && p.OwnerID == a.UserID) {
			out = append(out, p)
		}
	}
	return out, nil
}
