// SPDX-License-Identifier: AGPL-3.0-only

package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// DuplicateWindow is how far back a played song still counts as a
// duplicate worth warning about: about one night.
const DuplicateWindow = 3 * time.Hour

// AddOptions changes how Add treats the songs it's given.
type AddOptions struct {
	// WarnDuplicates refuses the add with a *DuplicateError if a song is
	// already waiting or playing, or started in the last DuplicateWindow.
	// The caller can then ask and add again without it.
	WarnDuplicates bool
}

// Duplicate is a song being added that the room already has.
type Duplicate struct {
	// Title is the song being added.
	Title string
	// Item is the room's copy: waiting, playing, or played.
	Item store.QueueItem
	// PlayedAt is when the copy last started, if it already played.
	PlayedAt time.Time
}

// DuplicateError is an add refused because some songs are already in the
// room or played recently. It's a warning: adding again without
// AddOptions.WarnDuplicates goes through.
type DuplicateError struct {
	Duplicates []Duplicate
}

func (e *DuplicateError) Error() string {
	if len(e.Duplicates) == 1 {
		return fmt.Sprintf("%q is already in the queue or played recently.", e.Duplicates[0].Title)
	}
	return fmt.Sprintf("%d of these songs are already in the queue or played recently.", len(e.Duplicates))
}

// copyOf is one of the room's items, with what's known to match it by.
type copyOf struct {
	item     store.QueueItem
	track    provider.Track
	mbid     string
	playedAt time.Time
}

// duplicates finds the room's copies of tracks: the same track, the same
// ISRC or MusicBrainz recording, or the same song by match.Score's strict
// rules. A copy waiting or playing wins over one that played; between
// plays, the latest.
func (s *Service) duplicates(ctx context.Context, q *store.Queries, roomID string, tracks []provider.Track, now time.Time) ([]Duplicate, error) {
	upcoming, err := q.ListUpcoming(ctx, roomID)
	if err != nil {
		return nil, err
	}
	plays, err := q.ListPlaysBetween(ctx, store.ListPlaysBetweenParams{
		RoomID: roomID, FromTime: now.Add(-DuplicateWindow), ToTime: now.Add(time.Second), Limit: 500,
	})
	if err != nil {
		return nil, err
	}

	var copies []copyOf
	for _, it := range upcoming {
		copies = append(copies, s.copyOf(ctx, q, it, time.Time{}, now))
	}
	// Newest first, so the first match is the latest play.
	for i := len(plays) - 1; i >= 0; i-- {
		p := plays[i]
		copies = append(copies, s.copyOf(ctx, q, p.QueueItem, p.PlayHistory.StartedAt, now))
	}

	var out []Duplicate
	for _, t := range tracks {
		mbid := t.MBID
		if mbid == "" {
			mbid = cachedMBID(ctx, q, t.Ref.Provider, t.Ref.ID, now)
		}
		for _, c := range copies {
			if sameSong(t, mbid, c) {
				out = append(out, Duplicate{Title: t.Title, Item: c.item, PlayedAt: c.playedAt})
				break
			}
		}
	}
	return out, nil
}

func (s *Service) copyOf(ctx context.Context, q *store.Queries, it store.QueueItem, playedAt, now time.Time) copyOf {
	c := copyOf{item: it, playedAt: playedAt}
	_ = json.Unmarshal([]byte(it.Metadata), &c.track)
	c.mbid = c.track.MBID
	if c.mbid == "" {
		c.mbid = cachedMBID(ctx, q, it.Provider, it.TrackID, now)
	}
	return c
}

func sameSong(t provider.Track, mbid string, c copyOf) bool {
	if t.Ref.Provider == c.item.Provider && t.Ref.ID == c.item.TrackID {
		return true
	}
	if mbid != "" && strings.EqualFold(mbid, c.mbid) {
		return true
	}
	_, ok := match.Score(t, c.track)
	return ok
}

// cachedMBID is a track's MusicBrainz recording, if enrichment found it.
func cachedMBID(ctx context.Context, q *store.Queries, providerID, trackID string, now time.Time) string {
	mb, err := q.GetMusicBrainzTrack(ctx, store.GetMusicBrainzTrackParams{Provider: providerID, TrackID: trackID, Now: now})
	if err != nil {
		// Not looked up yet, or the lookup failed: match by the rest.
		return ""
	}
	return mb.RecordingMbid
}
