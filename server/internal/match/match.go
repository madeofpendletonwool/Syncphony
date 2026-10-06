// SPDX-License-Identifier: AGPL-3.0-only

package match

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// ErrNoMatch means no other service in the room has the song.
var ErrNoMatch = errors.New("no other service here has that song")

// Links opens sessions for links and names providers. It's links.Service.
type Links interface {
	Open(ctx context.Context, linkID string) (provider.Session, error)
	Provider(id string) (provider.Provider, error)
}

// Presence says who's in a room. realtime.Presence is one.
type Presence interface {
	Members(roomID string) []string
}

// Service finds songs on the services of the people in a room.
type Service struct {
	db       *store.Store
	links    Links
	presence Presence
	// Timeout bounds the search on each link. Default 6s.
	Timeout time.Duration
}

// New returns a Service.
func New(db *store.Store, links Links, presence Presence) *Service {
	return &Service{db: db, links: links, presence: presence, Timeout: 6 * time.Second}
}

// Via is where a song plays from instead of its own link.
type Via struct {
	Provider string
	// For notices: whose link it plays through, the provider's display
	// name, and the display name of the song's own provider.
	OwnerName, ProviderName, FromName string
	LinkID                            string
	TrackID                           string
}

// Find looks for item's song on the links of the people in the room
// (whoever queued it included) and on shared links, other than the ones it
// already tried. It returns the best match, or ErrNoMatch.
func (s *Service) Find(ctx context.Context, roomID string, it store.QueueItem) (Via, error) {
	var want provider.Track
	if err := json.Unmarshal([]byte(it.Metadata), &want); err != nil || want.Title == "" {
		return Via{}, ErrNoMatch
	}
	users := append(s.presence.Members(roomID), it.AddedBy)
	links, err := s.db.ListMatchLinks(ctx, users)
	if err != nil {
		return Via{}, err
	}
	var best Via
	var bestOwner string
	bestScore := 0.0
	for _, l := range links {
		if (it.LinkID.Valid && l.ID == it.LinkID.String) || (it.ViaLinkID.Valid && l.ID == it.ViaLinkID.String) {
			continue
		}
		t, score, err := s.search(ctx, l.ID, want)
		if err != nil {
			slog.Debug("match: searching a link", "link", l.ID, "err", err)
			continue
		}
		if score > bestScore {
			best, bestOwner, bestScore = Via{Provider: l.Provider, LinkID: l.ID, TrackID: t.Ref.ID}, l.UserID, score
			if score == 1 {
				break // the same ISRC: it won't get better
			}
		}
	}
	if bestScore == 0 {
		return Via{}, ErrNoMatch
	}
	best.ProviderName, best.FromName = s.name(best.Provider), s.name(it.Provider)
	if u, err := s.db.GetUser(ctx, bestOwner); err == nil {
		best.OwnerName = u.DisplayName
	}
	return best, nil
}

func (s *Service) name(providerID string) string {
	if p, err := s.links.Provider(providerID); err == nil {
		return p.Info().Name
	}
	return providerID
}

// search finds want's best match on one link.
func (s *Service) search(ctx context.Context, linkID string, want provider.Track) (provider.Track, float64, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	sess, err := s.links.Open(ctx, linkID)
	if err != nil {
		return provider.Track{}, 0, err
	}
	defer sess.Close()
	return On(ctx, sess, want)
}

// On finds want's best match on one session, and its score (see Score):
// 0 if there's none. It searches by title and artist first, then by title
// alone, since services search differently.
func On(ctx context.Context, sess provider.Session, want provider.Track) (provider.Track, float64, error) {
	name := searchTitle(want.Title)
	queries := []string{name}
	if len(want.Artists) > 0 {
		queries = []string{name + " " + want.Artists[0].Name, name}
	}
	var best provider.Track
	bestScore := 0.0
	for _, q := range queries {
		page, err := sess.Search(ctx, provider.SearchQuery{Text: q, Kinds: []provider.EntityKind{provider.KindTrack}, Limit: 10})
		if err != nil {
			return provider.Track{}, 0, err
		}
		for _, t := range page.Tracks {
			if score, ok := Score(want, t); ok && score > bestScore && playable(ctx, sess, t) {
				best, bestScore = t, score
			}
		}
		if bestScore > 0 {
			break
		}
	}
	return best, bestScore, nil
}

// playable asks the service, if it can say, whether it will play t.
func playable(ctx context.Context, sess provider.Session, t provider.Track) bool {
	pc, ok := sess.(provider.PlayChecker)
	return !ok || !errors.Is(pc.CheckPlayable(ctx, t.Ref.ID), provider.ErrNotPlayable)
}

// searchTitle is a title without its qualifiers, for searching:
// "Heroes - 2017 Remaster" searches as "Heroes".
func searchTitle(s string) string {
	s = brackets.ReplaceAllString(s, " ")
	s = dashTail.ReplaceAllString(s, "")
	s = feat.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}
