// SPDX-License-Identifier: AGPL-3.0-only

// Package rooms manages rooms and publishes changes to their state. The
// queue and playback engines call QueueChanged and PublishNowPlaying after
// they change a room.
package rooms

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/fairness"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Errors returned by Service.
var (
	ErrNotFound  = errors.New("room not found")
	ErrForbidden = errors.New("only the room's owner or an admin can change it")
)

// InvalidInputError is a bad room field.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string { return e.Message }

// Service manages rooms and announces changes to them.
type Service struct {
	db  *store.Store
	bus realtime.Bus
	// Now is the clock. Default store.Now.
	Now func() time.Time
	// OnUpdate, if set, is called after a room's settings or owner change.
	OnUpdate func(store.Room)
	// OnDelete, if set, is called after a room is deleted.
	OnDelete func(roomID string)
}

// Actor is who's changing a room. The room's owner and admins may.
type Actor struct {
	UserID string
	Admin  bool
}

func (a Actor) manages(r store.Room) bool { return a.Admin || a.UserID == r.OwnerID }

// New returns a Service.
func New(db *store.Store, bus realtime.Bus) *Service {
	return &Service{db: db, bus: bus, Now: store.Now}
}

// Permission levels: who may do something in a room. The owner always may.
const (
	Everyone = "everyone"
	Owner    = "owner"
	// Vote is a skip level only: members vote, and the song is skipped
	// once enough of them have.
	Vote = "vote"
)

// Permissions say who may control playback in a room. Whoever queued a
// song may always skip it.
type Permissions struct {
	// PlayPause is who may play and pause: Everyone or Owner.
	PlayPause string `json:"playPause"`
	// Seek is who may seek: Everyone or Owner.
	Seek string `json:"seek"`
	// Skip is who may skip: Everyone, Vote or Owner.
	Skip string `json:"skip"`
	// Speaker is who may become the room's speaker: Everyone or Owner.
	Speaker string `json:"speaker"`
}

// DefaultSkipVotePercent makes a skip vote need a majority.
const DefaultSkipVotePercent = 50

// SettingsVersion is the version of Settings this server writes. Bump it
// when a change needs old settings migrated, and migrate in ParseSettings.
//
//   - 0: before versions. Controls was the only permission.
//   - 1: Permissions, fairness, matching, autopilot and guests.
const SettingsVersion = 1

// Settings are a room's options, stored as JSON in rooms.settings. Add new
// options here, with defaults that keep how rooms behaved before.
type Settings struct {
	// Version is the version of Settings these were written as.
	Version int `json:"version"`
	// Controls is the single setting rooms had before Permissions (who may
	// do everything, Everyone or Owner). It's only read, as the default
	// for each permission, and never written.
	Controls    string      `json:"controls,omitempty"`
	Permissions Permissions `json:"permissions"`
	// SkipVotePercent: a skip vote passes once more than this percent of
	// the room has voted (see VotesNeeded). 0 to 99.
	SkipVotePercent *int      `json:"skipVotePercent,omitempty"`
	Fairness        Fairness  `json:"fairness"`
	Matching        Matching  `json:"matching"`
	Autopilot       Autopilot `json:"autopilot"`
	Guests          Guests    `json:"guests"`
	// ApproveJoins: while the room is private, someone using one of its
	// invites asks to join, and the owner lets them in.
	ApproveJoins bool `json:"approveJoins,omitempty"`
}

// Guests says whether people without an account may join the room with a
// guest pass, and what they may do once in.
type Guests struct {
	Allowed bool `json:"allowed,omitempty"`
	// MaxSongs is how many songs each guest may add over their visit:
	// 0 (no limit) to MaxGuestSongs. Nil means DefaultGuestSongs.
	MaxSongs *int `json:"maxSongs,omitempty"`
	// NoVote stops guests voting to skip and hearting songs. Stored
	// negated so that guests vote by default.
	NoVote bool `json:"noVote,omitempty"`
}

// Guest limits.
const (
	DefaultGuestSongs = 10
	MaxGuestSongs     = 100
)

// SongLimit is how many songs each guest may add; 0 means no limit.
func (g Guests) SongLimit() int {
	if g.MaxSongs == nil {
		return DefaultGuestSongs
	}
	return *g.MaxSongs
}

// CanVote reports whether guests may vote to skip and heart songs.
func (g Guests) CanVote() bool { return !g.NoVote }

// Autopilot keeps the music going when a room's queue runs dry, with
// songs like the ones the room has been playing (see package autopilot).
type Autopilot struct {
	On bool `json:"on,omitempty"`
	// Adventure is how far autopilot strays from the room's songs:
	// AdventureSimilar (the default) or AdventureDiscovery. Explore
	// replaces it; it's kept in step, as the half Explore falls in.
	Adventure string `json:"adventure,omitempty"`
	// Explore is how far autopilot strays, from 0 (the room's own artists
	// and their hits) to 100 (artists further afield, deeper cuts). Nil
	// reads Adventure: ExploreSimilar or ExploreDiscovery.
	Explore *int `json:"explore,omitempty"`
	// EnergyCurve shapes the DJ's set to how long the room has been going
	// and the time of day: a gentle rise, then settling. Nil means the
	// default, on.
	EnergyCurve *bool `json:"energyCurve,omitempty"`
}

// EnergyCurveOn reports whether EnergyCurve is on.
func (a Autopilot) EnergyCurveOn() bool { return a.EnergyCurve == nil || *a.EnergyCurve }

// What Explore reads as for a room set up before it, by Adventure.
const (
	ExploreSimilar   = 25
	ExploreDiscovery = 75
	MaxExplore       = 100
)

// ExploreLevel is Explore, or what Adventure stands for.
func (a Autopilot) ExploreLevel() int {
	switch {
	case a.Explore != nil:
		return *a.Explore
	case a.Adventure == AdventureDiscovery:
		return ExploreDiscovery
	}
	return ExploreSimilar
}

// adventureFor is the adventure an explore level falls in.
func adventureFor(explore int) string {
	if explore >= MaxExplore/2 {
		return AdventureDiscovery
	}
	return AdventureSimilar
}

// Autopilot adventure levels.
const (
	// AdventureSimilar plays songs close to the seed, its artist included.
	AdventureSimilar = "similar"
	// AdventureDiscovery plays other artists, reaching further down the
	// list of similar songs.
	AdventureDiscovery = "discovery"
)

// Matching says how a room uses the same song on other services (see
// package match).
type Matching struct {
	// Fallback plays a song through another service in the room when its
	// own can't play it. Nil means the default, on.
	Fallback *bool `json:"fallback,omitempty"`
	// Borrow lets anyone queue a song the room played again, even if it's
	// only on someone else's service, which they couldn't search.
	Borrow bool `json:"borrow,omitempty"`
}

// FallbackOn reports whether Fallback is on.
func (m Matching) FallbackOn() bool { return m.Fallback == nil || *m.Fallback }

// Fairness tunes a room's fairness mode. The zero value is plain round
// robin or FIFO.
type Fairness struct {
	// MaxInARow caps one person's songs in a row while others wait: 0
	// (no cap) to MaxLimit.
	MaxInARow int `json:"maxInARow,omitempty"`
	// Cooldown is how many other songs play between one person's songs
	// while others wait: 0 to MaxLimit.
	Cooldown int `json:"cooldown,omitempty"`
	// Weights are songs per turn in round robin, by user ID: 2 to
	// MaxWeight. Everyone else gets 1.
	Weights map[string]int `json:"weights,omitempty"`
	// RepeatWindowMinutes refuses a song that's waiting, playing, or
	// started within this many minutes: 0 (off) to MaxRepeatWindow.
	RepeatWindowMinutes int `json:"repeatWindowMinutes,omitempty"`
}

// Fairness limits.
const (
	MaxLimit        = 10
	MaxWeight       = 4
	MaxWeights      = 64
	MaxRepeatWindow = 24 * 60
)

// Options are the fairness engine's view of f.
func (f Fairness) Options() fairness.Options {
	return fairness.Options{MaxInARow: f.MaxInARow, Cooldown: f.Cooldown, Weights: f.Weights}
}

// Equal reports whether f and g order the queue alike.
func (f Fairness) Equal(g Fairness) bool {
	return f.MaxInARow == g.MaxInARow && f.Cooldown == g.Cooldown && maps.Equal(f.Weights, g.Weights)
}

// clean drops weights of 1, which are the default.
func (f *Fairness) clean() {
	maps.DeleteFunc(f.Weights, func(_ string, w int) bool { return w == 1 })
	if len(f.Weights) == 0 {
		f.Weights = nil
	}
}

func (f Fairness) validate() error {
	switch {
	case f.MaxInARow < 0 || f.MaxInARow > MaxLimit:
		return &InvalidInputError{fmt.Sprintf("songs in a row is 0 (no cap) to %d", MaxLimit)}
	case f.Cooldown < 0 || f.Cooldown > MaxLimit:
		return &InvalidInputError{fmt.Sprintf("the cooldown is 0 to %d songs", MaxLimit)}
	case len(f.Weights) > MaxWeights:
		return &InvalidInputError{fmt.Sprintf("weight at most %d people", MaxWeights)}
	case f.RepeatWindowMinutes < 0 || f.RepeatWindowMinutes > MaxRepeatWindow:
		return &InvalidInputError{fmt.Sprintf("the repeat window is 0 (off) to %d minutes", MaxRepeatWindow)}
	}
	for _, w := range f.Weights {
		if w < 1 || w > MaxWeight {
			return &InvalidInputError{fmt.Sprintf("a weight is 1 to %d songs a turn", MaxWeight)}
		}
	}
	return nil
}

// ParseSettings reads a room's settings, filling in defaults. Unknown or
// bad values fall back to the defaults rather than failing.
func ParseSettings(raw string) Settings {
	var st Settings
	_ = json.Unmarshal([]byte(raw), &st)
	// Version 0 had Controls only; it's the default for each permission.
	fallback := Everyone
	if st.Version < 1 && st.Controls == Owner {
		fallback = Owner
	}
	st.Version, st.Controls = SettingsVersion, ""
	p := &st.Permissions
	for _, level := range []*string{&p.PlayPause, &p.Seek, &p.Speaker} {
		if *level != Everyone && *level != Owner {
			*level = fallback
		}
	}
	if p.Skip != Everyone && p.Skip != Owner && p.Skip != Vote {
		p.Skip = fallback
	}
	if st.SkipVotePercent == nil || *st.SkipVotePercent < 0 || *st.SkipVotePercent > 99 {
		st.SkipVotePercent = ptr(DefaultSkipVotePercent)
	}
	st.Fairness.clean()
	if st.Fairness.validate() != nil {
		st.Fairness = Fairness{}
	}
	if st.Autopilot.Adventure != AdventureDiscovery {
		st.Autopilot.Adventure = AdventureSimilar
	}
	if e := st.Autopilot.Explore; e != nil && (*e < 0 || *e > MaxExplore) {
		st.Autopilot.Explore = nil
	}
	st.Autopilot.Explore = ptr(st.Autopilot.ExploreLevel())
	if m := st.Guests.MaxSongs; m != nil && (*m < 0 || *m > MaxGuestSongs) {
		st.Guests.MaxSongs = nil
	}
	return st
}

// Allowed reports whether userID may act at level in a room owned by
// ownerID. Only the owner may act directly at the Vote level.
func Allowed(level, ownerID, userID string) bool {
	return userID == ownerID || level == Everyone
}

// VotesNeeded is how many votes skip a song when voters members could
// vote: more than percent of them, and at least one.
func VotesNeeded(voters, percent int) int {
	return min(max(voters*percent/100+1, 1), max(voters, 1))
}

// List returns every room, oldest first.
func (s *Service) List(ctx context.Context) ([]store.Room, error) { return s.db.ListRooms(ctx) }

// Create makes an open room owned by ownerID. mode "" means round robin,
// and empty settings are defaults.
func (s *Service) Create(ctx context.Context, ownerID, name, mode string, st Settings) (store.Room, error) {
	return s.CreateWith(ctx, ownerID, name, mode, Open, st)
}

// CreateWith is Create with a visibility: Open, Unlisted or Private ("" is
// Open).
func (s *Service) CreateWith(ctx context.Context, ownerID, name, mode, visibility string, st Settings) (store.Room, error) {
	if mode == "" {
		mode = store.FairnessRoundRobin
	}
	if visibility == "" {
		visibility = Open
	}
	if err := validateVisibility(visibility); err != nil {
		return store.Room{}, err
	}
	name, raw, err := validate(name, mode, st)
	if err != nil {
		return store.Room{}, err
	}
	var r store.Room
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		r, err = q.CreateRoom(ctx, store.CreateRoomParams{
			ID: store.NewID(), Name: name, OwnerID: ownerID, FairnessMode: mode, Settings: raw, CreatedAt: s.Now(),
		})
		if err != nil || visibility == Open {
			return err
		}
		r, err = q.SetRoomVisibility(ctx, store.SetRoomVisibilityParams{Visibility: visibility, ID: r.ID})
		return err
	})
	return r, err
}

// Update is a change to a room. Nil fields, and empty permissions, are
// left alone.
type Update struct {
	Name, FairnessMode *string
	Permissions        Permissions
	SkipVotePercent    *int
	// Fairness, if set, replaces the room's fairness options.
	Fairness *Fairness
	// Matching, if set, replaces the room's matching options.
	Matching *Matching
	// Autopilot, if set, replaces the room's autopilot options.
	Autopilot *Autopilot
	// Guests, if set, replaces the room's guest options.
	Guests *Guests
	// Visibility, if set, is who can see and join the room. Changing it
	// revokes the room's invites. Closing an open room makes Present, and
	// everyone with songs waiting, members, so no one is shut out mid-song.
	Visibility *string
	Present    []string
	// ApproveJoins, if set, is whether the owner lets in each person who
	// uses an invite to the room while it's private.
	ApproveJoins *bool
}

// Update changes a room. Only its owner or an admin may. Everyone in the
// room hears about it, and a new fairness mode reorders the queue at once.
func (s *Service) Update(ctx context.Context, by Actor, id string, u Update) (store.Room, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return r, err
	}
	if !by.manages(r) {
		return r, ErrForbidden
	}
	name, mode, st := r.Name, r.FairnessMode, ParseSettings(r.Settings)
	if u.Name != nil {
		name = *u.Name
	}
	if u.FairnessMode != nil {
		mode = *u.FairnessMode
	}
	for _, f := range []struct {
		to   *string
		from string
	}{
		{&st.Permissions.PlayPause, u.Permissions.PlayPause},
		{&st.Permissions.Seek, u.Permissions.Seek},
		{&st.Permissions.Skip, u.Permissions.Skip},
		{&st.Permissions.Speaker, u.Permissions.Speaker},
	} {
		if f.from != "" {
			*f.to = f.from
		}
	}
	if u.SkipVotePercent != nil {
		st.SkipVotePercent = u.SkipVotePercent
	}
	before := ParseSettings(r.Settings).Fairness
	if u.Fairness != nil {
		st.Fairness = *u.Fairness
	}
	if u.Matching != nil {
		st.Matching = *u.Matching
	}
	if u.Autopilot != nil {
		st.Autopilot = *u.Autopilot
	}
	if u.Guests != nil {
		st.Guests = *u.Guests
	}
	if u.ApproveJoins != nil {
		st.ApproveJoins = *u.ApproveJoins
	}
	name, raw, err := validate(name, mode, st)
	if err != nil {
		return r, err
	}
	visibility := r.Visibility
	if u.Visibility != nil {
		if err := validateVisibility(*u.Visibility); err != nil {
			return r, err
		}
		visibility = *u.Visibility
	}
	var updated store.Room
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		updated, err = q.UpdateRoom(ctx, store.UpdateRoomParams{Name: name, FairnessMode: mode, Settings: raw, ID: id})
		if err != nil || visibility == r.Visibility {
			return err
		}
		return s.changeVisibility(ctx, q, &updated, visibility, u.Present)
	})
	if err != nil {
		return updated, err
	}
	s.Updated(updated)
	if mode != r.FairnessMode || !before.Equal(ParseSettings(raw).Fairness) {
		if _, err := s.QueueChanged(ctx, id); err != nil {
			return updated, err
		}
	}
	return updated, nil
}

// Updated tells everyone in a room that its settings or owner changed.
// Call it after changing a room other than through Service.
func (s *Service) Updated(r store.Room) {
	s.bus.Publish(realtime.RoomTopic(r.ID), realtime.Event{Type: realtime.RoomUpdated, Data: r})
	if s.OnUpdate != nil {
		s.OnUpdate(r)
	}
}

// Transfer makes userID the room's owner. Only its owner or an admin may.
// The caller checks userID may own rooms (a member who can sign in). The
// old owner stays a member, so handing over a private room doesn't lock
// them out of it.
func (s *Service) Transfer(ctx context.Context, by Actor, id, userID string) (store.Room, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return r, err
	}
	if !by.manages(r) {
		return r, ErrForbidden
	}
	if r.OwnerID == userID {
		return r, nil
	}
	old := r.OwnerID
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		r, err = q.SetRoomOwner(ctx, store.SetRoomOwnerParams{OwnerID: userID, ID: id})
		if err != nil {
			return err
		}
		return q.AddRoomMember(ctx, store.AddRoomMemberParams{
			RoomID: id, UserID: old, AddedBy: sql.NullString{String: by.UserID, Valid: true}, CreatedAt: s.Now(),
		})
	})
	if err != nil {
		return r, err
	}
	s.Updated(r)
	return r, nil
}

// Delete deletes a room: its queue, history, nights, displays and guest
// passes go with it. Only its owner or an admin may. Everyone in the room
// hears about it first. guests are the room's guests, whose accounts go
// too: they can't be anywhere else.
func (s *Service) Delete(ctx context.Context, by Actor, id string) error {
	r, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if !by.manages(r) {
		return ErrForbidden
	}
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		guests, err := q.ListRoomGuestIDs(ctx, id)
		if err != nil {
			return err
		}
		if err := q.DeleteRoom(ctx, id); err != nil {
			return err
		}
		for _, g := range guests {
			if err := q.DeleteUser(ctx, g); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.bus.Publish(realtime.RoomTopic(id), realtime.Event{Type: realtime.RoomDeleted, Data: Deleted{RoomID: id}})
	if s.OnDelete != nil {
		s.OnDelete(id)
	}
	return nil
}

// Deleted is the event for a deleted room.
type Deleted struct{ RoomID string }

func validate(name, mode string, st Settings) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return "", "", &InvalidInputError{"a room name is 1 to 64 characters"}
	}
	if mode != store.FairnessRoundRobin && mode != store.FairnessFIFO {
		return "", "", &InvalidInputError{"fairness mode is round_robin or fifo"}
	}
	p := &st.Permissions
	for _, level := range []*string{&p.PlayPause, &p.Seek, &p.Skip, &p.Speaker} {
		if *level == "" {
			*level = Everyone
		}
	}
	for _, level := range []string{p.PlayPause, p.Seek, p.Speaker} {
		if level != Everyone && level != Owner {
			return "", "", &InvalidInputError{"play/pause, seek and speaker permissions are everyone or owner"}
		}
	}
	if p.Skip != Everyone && p.Skip != Owner && p.Skip != Vote {
		return "", "", &InvalidInputError{"the skip permission is everyone, vote or owner"}
	}
	if st.SkipVotePercent == nil {
		st.SkipVotePercent = ptr(DefaultSkipVotePercent)
	}
	if *st.SkipVotePercent < 0 || *st.SkipVotePercent > 99 {
		return "", "", &InvalidInputError{"the skip vote percent is 0 to 99"}
	}
	if err := st.Fairness.validate(); err != nil {
		return "", "", err
	}
	switch st.Autopilot.Adventure {
	case "":
		st.Autopilot.Adventure = AdventureSimilar
	case AdventureSimilar, AdventureDiscovery:
	default:
		return "", "", &InvalidInputError{"autopilot's adventure is similar or discovery"}
	}
	if e := st.Autopilot.Explore; e != nil && (*e < 0 || *e > MaxExplore) {
		return "", "", &InvalidInputError{fmt.Sprintf("autopilot's explore is 0 to %d", MaxExplore)}
	}
	// Explore, when given, decides; an older client's adventure stands for
	// its half.
	st.Autopilot.Explore = ptr(st.Autopilot.ExploreLevel())
	st.Autopilot.Adventure = adventureFor(*st.Autopilot.Explore)
	if m := st.Guests.MaxSongs; m != nil && (*m < 0 || *m > MaxGuestSongs) {
		return "", "", &InvalidInputError{fmt.Sprintf("a guest may add 0 (no limit) to %d songs", MaxGuestSongs)}
	}
	st.Fairness.clean()
	st.Version, st.Controls = SettingsVersion, ""
	raw, err := json.Marshal(st)
	return name, string(raw), err
}

func ptr[T any](v T) *T { return &v }

// QueueSnapshot is a room's queue at a version: the playing item and the
// queued items, each user's lane in order, and the play order the room's
// fairness policy makes of them. Autopilot songs play after every member's.
type QueueSnapshot struct {
	RoomID  string
	Version int64
	Items   []store.QueueItem
	// UpNext is the IDs of the queued items, in the order they will play.
	UpNext []string
}

// NowPlaying is a room's playback state, as the playback engine sees it.
// Item is nil when nothing is playing.
type NowPlaying struct {
	RoomID string
	State  string
	Item   *store.QueueItem
	// Driver is how Item plays: provider.PlaybackStream (through the
	// player device) or provider.PlaybackRemote (on the service's own
	// device). Empty when nothing is playing.
	Driver string
	// Position is the playback position at At. While playing it advances
	// with the clock.
	Position time.Duration
	At       time.Time
	// Revision increases whenever the player must act: a new item, play,
	// pause or seek. Players apply a state once per revision.
	Revision int64
	// Player is the device acting as the room's speaker, if any.
	Player *Player
	// Next is the item that plays after this one, for preloading.
	Next *store.QueueItem
	// SkipVotes is the vote to skip Item, while the room votes on skips.
	SkipVotes *SkipVotes
}

// SkipVotes is a vote to skip the playing song.
type SkipVotes struct {
	// Voters are the IDs of users who voted to skip, in the order they did.
	Voters []string
	// Needed is how many votes skip the song, given who's in the room now.
	Needed int
}

// Player is the device a room plays through.
type Player struct {
	DeviceID string
	UserID   string
	Name     string
	// LastSeen is when the device last reported in.
	LastSeen time.Time
}

// Notice is something members should hear about, such as a song skipped
// because its service failed.
type Notice struct {
	RoomID string
	// ItemID is the item it's about, if any.
	ItemID  string
	Message string
}

// Get returns a room.
func (s *Service) Get(ctx context.Context, id string) (store.Room, error) {
	r, err := s.db.GetRoom(ctx, id)
	if store.IsNotFound(err) {
		return r, ErrNotFound
	}
	return r, err
}

// Played is a song the room played, and how it ended.
type Played struct {
	Item      store.QueueItem
	StartedAt time.Time
	EndedAt   time.Time
	EndReason string
}

// HistoryQuery picks a page of a room's history.
type HistoryQuery struct {
	Limit int
	// Before, if set, returns plays that started before it: the last
	// play's StartedAt from the previous page.
	Before time.Time
	// UserID, if set, returns only that user's songs.
	UserID string
}

// History returns songs the room finished playing, newest first. The song
// playing now isn't one until it ends.
func (s *Service) History(ctx context.Context, id string, hq HistoryQuery) ([]Played, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	before := hq.Before
	if before.IsZero() {
		before = Forever
	}
	rows, err := s.db.ListPlayed(ctx, store.ListPlayedParams{RoomID: id, Before: before, UserID: hq.UserID, Limit: int64(hq.Limit)})
	if err != nil {
		return nil, err
	}
	out := make([]Played, len(rows))
	for i, r := range rows {
		out[i] = played(r.PlayHistory, r.QueueItem)
	}
	return out, nil
}

// Forever is later than any play, for open-ended time ranges.
var Forever = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// MaxPlays is the most plays Plays reads at once, so a long-lived room's
// stats stay cheap.
const MaxPlays = 10_000

// Plays returns the songs the room finished playing that started in
// [from, to), oldest first: at most MaxPlays, the earliest.
func (s *Service) Plays(ctx context.Context, id string, from, to time.Time) ([]Played, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.ListPlaysBetween(ctx, store.ListPlaysBetweenParams{RoomID: id, FromTime: from, ToTime: to, Limit: MaxPlays})
	if err != nil {
		return nil, err
	}
	out := make([]Played, len(rows))
	for i, r := range rows {
		out[i] = played(r.PlayHistory, r.QueueItem)
	}
	return out, nil
}

// PlayTimes returns when each of the room's finished plays started and
// ended, and whose song it was, oldest first: at most MaxPlays * 5.
func (s *Service) PlayTimes(ctx context.Context, id string) ([]store.ListPlayTimesRow, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	return s.db.ListPlayTimes(ctx, store.ListPlayTimesParams{RoomID: id, Limit: MaxPlays * 5})
}

func played(h store.PlayHistory, it store.QueueItem) Played {
	return Played{Item: it, StartedAt: h.StartedAt, EndedAt: h.EndedAt.Time, EndReason: h.EndReason.String}
}

// QueueSnapshot reads a room's queue and its version together.
func (s *Service) QueueSnapshot(ctx context.Context, id string) (QueueSnapshot, error) {
	var snap QueueSnapshot
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		var err error
		snap, err = SnapshotTx(ctx, q, id)
		return err
	})
	return snap, err
}

// SnapshotTx reads a room's queue within a transaction, for engines that
// need the fair order while changing the queue.
func SnapshotTx(ctx context.Context, q *store.Queries, id string) (QueueSnapshot, error) {
	r, err := q.GetRoom(ctx, id)
	if store.IsNotFound(err) {
		return QueueSnapshot{}, ErrNotFound
	} else if err != nil {
		return QueueSnapshot{}, err
	}
	items, err := q.ListUpcoming(ctx, id)
	if err != nil {
		return QueueSnapshot{}, err
	}
	history, err := q.LastPlayedByUser(ctx, id)
	if err != nil {
		return QueueSnapshot{}, err
	}
	opts := ParseSettings(r.Settings).Fairness.Options()
	recent, err := q.RecentPlayers(ctx, store.RecentPlayersParams{RoomID: id, Limit: int64(opts.Reach())})
	if err != nil {
		return QueueSnapshot{}, err
	}
	fs := fairnessState(items, history)
	fs.Recent = recent
	order := fairness.ForMode(r.FairnessMode, opts).Order(fs)
	upNext := make([]string, len(order), len(items))
	for i, it := range order {
		upNext[i] = it.ID
	}
	// Autopilot songs aren't in any lane: they wait behind everyone's, in
	// the order autopilot added them, so a member's song always goes first.
	var autopilot []store.QueueItem
	for _, it := range items {
		if it.State == store.ItemQueued && it.IsAutopilot() {
			autopilot = append(autopilot, it)
		}
	}
	slices.SortFunc(autopilot, func(a, b store.QueueItem) int {
		return cmp.Or(a.AddedAt.Compare(b.AddedAt), cmp.Compare(a.ID, b.ID))
	})
	for _, it := range autopilot {
		upNext = append(upNext, it.ID)
	}
	return QueueSnapshot{RoomID: id, Version: r.QueueVersion, Items: items, UpNext: upNext}, nil
}

// fairnessState builds the fairness engine's input from the upcoming items
// (lanes in order, as ListUpcoming returns them) and each user's last play.
// Autopilot songs are nobody's turn, so the engine doesn't see them.
func fairnessState(items []store.QueueItem, history []store.LastPlayedByUserRow) fairness.State {
	s := fairness.State{Lanes: map[string][]fairness.Item{}, LastPlayed: map[string]time.Time{}}
	for _, it := range items {
		if it.IsAutopilot() {
			continue
		}
		switch it.State {
		case store.ItemPlaying:
			s.Playing = it.AddedBy
		case store.ItemQueued:
			s.Lanes[it.AddedBy] = append(s.Lanes[it.AddedBy], fairness.Item{ID: it.ID, User: it.AddedBy, AddedAt: it.AddedAt})
		}
	}
	for _, h := range history {
		s.LastPlayed[h.UserID] = h.StartedAt
	}
	return s
}

// QueueChanged bumps the room's queue version and pushes the new snapshot
// to everyone in the room, and returns it. Call it after committing a queue
// change.
func (s *Service) QueueChanged(ctx context.Context, id string) (QueueSnapshot, error) {
	var snap QueueSnapshot
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		if _, err := q.BumpQueueVersion(ctx, id); store.IsNotFound(err) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		var err error
		snap, err = SnapshotTx(ctx, q, id)
		return err
	})
	if err != nil {
		return QueueSnapshot{}, err
	}
	s.bus.Publish(realtime.RoomTopic(id), realtime.Event{Type: realtime.QueueUpdated, Version: snap.Version, Data: snap})
	return snap, nil
}

// PublishNowPlaying pushes a room's playback state to everyone in it.
func (s *Service) PublishNowPlaying(np NowPlaying) {
	s.bus.Publish(realtime.RoomTopic(np.RoomID), realtime.Event{Type: realtime.NowPlayingUpdated, Data: np})
}

// PublishNotice pushes a notice to everyone in a room.
func (s *Service) PublishNotice(n Notice) {
	s.bus.Publish(realtime.RoomTopic(n.RoomID), realtime.Event{Type: realtime.PlaybackNotice, Data: n})
}
