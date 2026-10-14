// SPDX-License-Identifier: AGPL-3.0-only

package discover

import (
	"cmp"
	"context"
	"math"
	"slices"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/library"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// Sets that learn (MAD-825): thumbs grow a set and turn its feed away
// from an artist, artists turned down often enough are offered for
// blocking, and a set can take artists you starred on Navidrome, when
// you pick them. None of it reaches a room.
const (
	// growAfter is how many thumbs-up an artist gets from a set's feed
	// before they're offered for the set, or join it by themselves.
	growAfter = 2
	// blockAfter is how many thumbs-down an artist gets, in any feed,
	// before they're offered for blocking.
	blockAfter = 3
	// downPenalty is how much each thumbs-down from a set's feed lowers
	// its artist's songs in that feed: three take them to about a fifth.
	downPenalty = 0.6
)

// Kinds of suggestion.
const (
	SuggestGrow  = "grow"
	SuggestBlock = "block"
)

// Offer is an artist suggested for a set, or for blocking.
type Offer struct {
	// Key is the artist's key (library.ArtistKey), Name how they're
	// named.
	Key, Name string
	// Thumbs is how many thumbs-up (to grow) or down (to block) led to it.
	Thumbs int
}

// Suggestions are what a set's thumbs suggest.
type Suggestions struct {
	// Grow are artists thumbed up from the set's feed growAfter times, to
	// add to it.
	Grow []Offer
	// Block are artists thumbed down blockAfter times, in any feed.
	Block []Offer
}

// thumbCounts are the user's thumbs by artist: ups from a set's feed,
// and downs from every feed.
type thumbCounts struct {
	ups, downs map[string]*Offer
	// setDowns are the thumbs-down from the set's feed, for its penalty.
	setDowns map[string]int
}

func (s *Service) thumbCounts(ctx context.Context, userID, setID string) (thumbCounts, error) {
	fb, err := s.db.ListDiscoveryFeedback(ctx, userID)
	if err != nil {
		return thumbCounts{}, err
	}
	tc := thumbCounts{ups: map[string]*Offer{}, downs: map[string]*Offer{}, setDowns: map[string]int{}}
	count := func(m map[string]*Offer, f store.DiscoveryFeedback) {
		k := library.ArtistKey(f.Artist)
		if k == "" {
			return
		}
		o, ok := m[k]
		if !ok {
			o = &Offer{Key: k, Name: f.Artist}
			m[k] = o
		}
		o.Thumbs++
	}
	for _, f := range fb {
		inSet := setID != "" && f.SetID.String == setID
		switch {
		case f.Kind == ThumbUp && inSet:
			count(tc.ups, f)
		case f.Kind == ThumbDown:
			count(tc.downs, f)
			if inSet {
				tc.setDowns[library.ArtistKey(f.Artist)]++
			}
		}
	}
	return tc, nil
}

// downWeight is how much a set's thumbs-down on an artist lower their songs.
func downWeight(downs int) float64 { return math.Pow(downPenalty, float64(downs)) }

// isSeed reports whether an artist (by library.ArtistKey) is one of a set's seeds.
func (set Set) isSeed(key string) bool {
	return slices.ContainsFunc(set.Artists, func(a store.DiscoverySetArtist) bool { return library.ArtistKey(a.Name) == key })
}

// Suggestions returns the artists a set's thumbs suggest adding to it, and
// the ones the user's thumbs suggest blocking, most thumbs first. Ones
// already in the set, blocked, or that the user said no to are left out.
func (s *Service) Suggestions(ctx context.Context, userID, setID string) (Suggestions, error) {
	set, err := s.Get(ctx, userID, setID)
	if err != nil {
		return Suggestions{}, err
	}
	tc, err := s.thumbCounts(ctx, userID, setID)
	if err != nil {
		return Suggestions{}, err
	}
	no, err := s.said(ctx, userID)
	if err != nil {
		return Suggestions{}, err
	}
	out := Suggestions{Grow: []Offer{}, Block: []Offer{}}
	for k, o := range tc.ups {
		if o.Thumbs >= growAfter && !set.isSeed(k) && !no.blocked[k] && !no.grow[setID+"\x00"+k] {
			out.Grow = append(out.Grow, *o)
		}
	}
	for k, o := range tc.downs {
		if o.Thumbs >= blockAfter && !set.isSeed(k) && !no.blocked[k] && !no.block[k] {
			out.Block = append(out.Block, *o)
		}
	}
	byThumbs := func(a, b Offer) int { return cmp.Or(cmp.Compare(b.Thumbs, a.Thumbs), strings.Compare(a.Key, b.Key)) }
	slices.SortFunc(out.Grow, byThumbs)
	slices.SortFunc(out.Block, byThumbs)
	return out, nil
}

// saidNo is what a user turned away: artists they blocked, and
// suggestions they dismissed (grow by set and artist, block by artist).
type saidNo struct {
	blocked, grow, block map[string]bool
}

func (s *Service) said(ctx context.Context, userID string) (saidNo, error) {
	out := saidNo{blocked: map[string]bool{}, grow: map[string]bool{}, block: map[string]bool{}}
	bs, err := s.db.ListDiscoveryBlocks(ctx, userID)
	if err != nil {
		return out, err
	}
	for _, b := range bs {
		out.blocked[b.ArtistKey] = true
	}
	ds, err := s.db.ListDismissedDiscoverySuggestions(ctx, userID)
	if err != nil {
		return out, err
	}
	for _, d := range ds {
		if d.Kind == SuggestGrow {
			out.grow[d.Scope+"\x00"+d.ArtistKey] = true
		} else {
			out.block[d.ArtistKey] = true
		}
	}
	return out, nil
}

// Dismiss says no to a suggestion, so it isn't made again: an artist for
// a set (SuggestGrow), or for blocking (SuggestBlock).
func (s *Service) Dismiss(ctx context.Context, userID, setID, kind, artist string) error {
	if _, err := s.Get(ctx, userID, setID); err != nil {
		return err
	}
	scope := setID
	switch kind {
	case SuggestGrow:
	case SuggestBlock:
		scope = ""
	default:
		return &InvalidInputError{"a suggestion is grow or block"}
	}
	key := library.ArtistKey(artist)
	if key == "" {
		return &InvalidInputError{"an artist's name is needed"}
	}
	return s.db.DismissDiscoverySuggestion(ctx, store.DismissDiscoverySuggestionParams{
		UserID: userID, Kind: kind, Scope: scope, ArtistKey: key, CreatedAt: s.Now(),
	})
}

// growBy adds an artist to a set that grows by itself, if they've had
// growAfter thumbs-up from its feed and the user hasn't turned them away.
// It returns their name if they joined.
func (s *Service) growBy(ctx context.Context, userID string, set Set, artist string) (string, error) {
	sg, err := s.Suggestions(ctx, userID, set.ID)
	if err != nil || len(set.Artists) >= MaxArtists {
		return "", err
	}
	k := library.ArtistKey(artist)
	i := slices.IndexFunc(sg.Grow, func(o Offer) bool { return o.Key == k })
	if i < 0 {
		return "", nil
	}
	if err := s.addArtist(ctx, set.ID, Seed{Name: sg.Grow[i].Name}, FromThumb); err != nil {
		return "", err
	}
	return sg.Grow[i].Name, s.touch(ctx, set.ID)
}

// Import adds artists to a set at once: ones the user picked from a
// suggestion, their Navidrome stars or a room's taste, noted as coming
// from there. Ones in the set already are skipped; more than the set has
// room for is ErrTooMany, and adds none.
func (s *Service) Import(ctx context.Context, userID, setID, from string, seeds []Seed) (Set, error) {
	switch from {
	case FromManual, FromThumb, FromStarred, FromRoom:
	default:
		return Set{}, &InvalidInputError{"artists come from manual, thumb, starred or room"}
	}
	set, err := s.Get(ctx, userID, setID)
	if err != nil {
		return Set{}, err
	}
	have := map[string]bool{}
	for _, a := range set.Artists {
		have[a.ArtistKey] = true
	}
	var add []Seed
	for _, a := range seeds {
		k := ArtistKey(a.Name, a.MBID)
		if k == "" || have[k] {
			continue
		}
		have[k] = true
		add = append(add, a)
	}
	if len(set.Artists)+len(add) > MaxArtists {
		return Set{}, ErrTooMany
	}
	if len(add) == 0 {
		return set, nil
	}
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		for _, a := range add {
			if err := s.addArtistWith(ctx, q, setID, a, from); err != nil {
				return err
			}
		}
		return q.TouchDiscoverySet(ctx, store.TouchDiscoverySetParams{UpdatedAt: s.Now(), ID: setID})
	})
	if err != nil {
		return Set{}, err
	}
	return s.Get(ctx, userID, setID)
}

// StarredArtist is an artist the user starred on Navidrome, or whose
// albums or songs they starred: one to pick for a set.
type StarredArtist struct {
	Name string
	// Artist is whether they starred the artist; Albums and Songs how many
	// of their albums and songs they starred.
	Artist        bool
	Albums, Songs int
}

// Stars lists what users starred on their own links. library.Service is one.
type Stars interface {
	Starred(ctx context.Context, userID string) (provider.Starred, error)
}

// Starred lists the artists the user starred on their own Navidrome, and
// the artists of the albums and songs they starred: starred artists
// first, then by how much of theirs they starred. It's only ever a list
// to pick from: nothing joins a set unless picked (library ≠ taste).
func (s *Service) Starred(ctx context.Context, userID string) ([]StarredArtist, error) {
	out := []StarredArtist{}
	if s.Stars == nil {
		return out, nil
	}
	st, err := s.Stars.Starred(ctx, userID)
	if err != nil {
		return nil, err
	}
	by := map[string]*StarredArtist{}
	var order []string
	of := func(name string) *StarredArtist {
		name = strings.Join(strings.Fields(name), " ")
		k := library.ArtistKey(name)
		if k == "" {
			return nil
		}
		a, ok := by[k]
		if !ok {
			a = &StarredArtist{Name: name}
			by[k] = a
			order = append(order, k)
		}
		return a
	}
	for _, a := range st.Artists {
		if sa := of(a.Name); sa != nil {
			sa.Artist = true
		}
	}
	for _, al := range st.Albums {
		for _, a := range al.Artists {
			if sa := of(a.Name); sa != nil {
				sa.Albums++
			}
		}
	}
	for _, t := range st.Songs {
		for _, a := range t.Artists {
			if sa := of(a.Name); sa != nil {
				sa.Songs++
			}
		}
	}
	for _, k := range order {
		out = append(out, *by[k])
	}
	weight := func(a StarredArtist) int {
		w := a.Albums*3 + a.Songs
		if a.Artist {
			w += 1000
		}
		return w
	}
	slices.SortStableFunc(out, func(a, b StarredArtist) int {
		return cmp.Or(cmp.Compare(weight(b), weight(a)), strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)))
	})
	return out, nil
}
