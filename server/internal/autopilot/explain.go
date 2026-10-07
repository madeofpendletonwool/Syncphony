// SPDX-License-Identifier: AGPL-3.0-only

package autopilot

import (
	"context"
	"fmt"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/dj"
)

// Explaining the DJ's picks (MAD-760): people forgive picks they
// understand. Each pick gets a short reason, in words the room can read on
// now playing, up next and the big screen, and the knowledge sources it
// came from.

// sourceNames are how music knowledge sources are shown.
var sourceNames = map[string]string{
	"lastfm":       "Last.fm",
	"listenbrainz": "ListenBrainz",
	"deezer":       "Deezer",
	"musicbrainz":  "MusicBrainz",
}

// explain returns why the DJ chose p, as a short reason, and where the
// knowledge came from ("" if it didn't say). name returns a member's
// display name.
func explain(p dj.Pick, name func(userID string) string) (reason, source string) {
	w := p.Why
	artist := ""
	if len(p.Track.Artists) > 0 {
		artist = p.Track.Artists[0].Name
	}
	// whose is a member's artist: "Sam's Radiohead", or the room's.
	whose := func(userID, a string) string {
		if n := name(userID); n != "" {
			return n + "'s " + a
		}
		return "the room's " + a
	}
	// The member's song it leads from, if any, names the artist it's
	// like: the via artist, or for a member's turn, theirs.
	seedUser, via := "", w.Via
	if p.Seed.ID != "" {
		seedUser = p.Seed.AddedBy
		if t := dj.TrackOf(p.Seed); len(t.Artists) > 0 {
			via = t.Artists[0].Name
		}
	}
	switch {
	case w.Bridge != nil:
		b := w.Bridge
		reason = fmt.Sprintf("Bridging %s and %s", whose(b.Users[0], b.Artists[0]), whose(b.Users[1], b.Artists[1]))
	case w.Kind == dj.KindThrowback:
		reason = fmt.Sprintf("A throwback to %s, a favorite from past nights", w.Via)
	case w.Kind == dj.KindArtist && w.DeepCut:
		reason = fmt.Sprintf("A deep cut by %s, an artist the room loves", artist)
	case w.Kind == dj.KindArtist:
		reason = fmt.Sprintf("A top song by %s, an artist the room loves", artist)
	case w.Kind == dj.KindSimilarSong && p.Seed.ID != "":
		reason = fmt.Sprintf("Like %s", whose(seedUser, dj.TrackOf(p.Seed).Title))
	case w.Kind == dj.KindSimilarSong:
		reason = fmt.Sprintf("Like a song by %s", w.Via)
	case w.Kind == dj.KindTwoSteps:
		reason = fmt.Sprintf("Further out from %s → %s", whose(seedUser, via), artist)
	default:
		reason = fmt.Sprintf("Because %s → %s (similar %.2f)", whose(seedUser, via), artist, w.Similarity)
	}
	var names []string
	for _, s := range w.Sources {
		if n, ok := sourceNames[s]; ok {
			names = append(names, n)
		} else {
			names = append(names, s)
		}
	}
	return reason, strings.Join(names, ", ")
}

// names returns a member's display name, looked up once, or "" if they're
// gone.
func (s *Service) names(ctx context.Context) func(userID string) string {
	seen := map[string]string{}
	return func(id string) string {
		if id == "" {
			return ""
		}
		if n, ok := seen[id]; ok {
			return n
		}
		u, err := s.db.GetUser(ctx, id)
		if err == nil {
			seen[id] = u.DisplayName
		} else {
			seen[id] = ""
		}
		return seen[id]
	}
}
