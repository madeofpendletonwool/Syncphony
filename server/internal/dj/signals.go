// SPDX-License-Identifier: AGPL-3.0-only

package dj

import (
	"encoding/json"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// What each thing the room did says about an artist, before decay.
const (
	finishedSignal = 1.0
	// queuedSignal is a member's song playing or waiting: they chose it.
	queuedSignal = 0.8
	// A skip says more the sooner it comes: quickSkipSignal within
	// quickSkip of the start, then from skippedSignal for a skip just after
	// it to lateSkipSignal for one at the very end.
	quickSkip       = 10 * time.Second
	quickSkipSignal = -1.5
	skippedSignal   = -1.0
	lateSkipSignal  = -0.2
	// typicalLength stands in for a song's length when it isn't known.
	typicalLength = 4 * time.Minute
	// autopilotFinished is the room letting one of autopilot's songs play
	// through: a quieter yes than choosing it.
	autopilotFinished = 0.5
	// removedSignal is someone taking one of autopilot's songs off the
	// queue before it played.
	removedSignal = -0.5
	// heartSignal is each heart a song got, up to maxHearts.
	heartSignal = 0.4
	maxHearts   = 3
	// reAddSignal is a member queueing a song again within reAddWindow of
	// it playing: they want it back.
	reAddSignal = 0.6
	reAddWindow = 3 * time.Hour
	// followSignal is a member queueing a song by the same artist as one
	// of autopilot's, or a similar one, while it played or within
	// followWindow of it ending: the pick landed.
	followSignal = 0.7
	followWindow = 5 * time.Minute
	// throwbackFinished scales autopilotFinished for a throwback the room
	// let play, so that one throwback doesn't pull the room back into the
	// old artist's run.
	throwbackFinished = 0.5
)

// reaction is something the room did about a song.
type reaction struct {
	item store.QueueItem
	// name is the song's artist; artist, its ArtistKey.
	name, artist string
	// who it says something about: the member, or "" for autopilot's
	// songs the room let play.
	who string
	v   float64
	at  time.Time
	// chose is a member's song they queued, playing or played through: it
	// counts as one of the artist's plays, and can seed.
	chose bool
	// throwback is one of autopilot's throwbacks.
	throwback bool
}

// reactions reads how the room reacted to its songs: its freshest intent
// first (what's playing and waiting), then its plays, newest first, up to
// reach of them.
func reactions(in Input, reach int) []reaction {
	var out []reaction
	seen := map[string]bool{}
	for _, it := range in.Upcoming {
		seen[it.ID] = true
		if !it.IsAutopilot() {
			out = append(out, reaction{item: it, who: it.AddedBy, v: queuedSignal, at: in.Now, chose: true})
		}
	}
	for i, h := range in.History {
		it := h.QueueItem
		if seen[it.ID] || i >= reach {
			continue
		}
		seen[it.ID] = true
		if !h.PlayHistory.EndedAt.Valid {
			continue
		}
		at, tb := h.PlayHistory.StartedAt, isThrowback(it)
		switch reason := h.PlayHistory.EndReason.String; {
		case reason == store.EndFinished && it.IsAutopilot():
			v := autopilotFinished
			if tb {
				v *= throwbackFinished
			}
			out = append(out, reaction{item: it, v: v, at: at, throwback: tb})
		case reason == store.EndFinished:
			out = append(out, reaction{item: it, who: it.AddedBy, v: finishedSignal, at: at, chose: true})
		case reason == store.EndSkipped || reason == store.EndRemoved:
			out = append(out, reaction{item: it, who: owner(it), v: skipSignal(h), at: at, throwback: tb})
		}
		if n := in.Hearts[it.ID]; n > 0 && endReason(h) != store.EndError {
			out = append(out, reaction{item: it, who: owner(it), v: heartSignal * float64(min(n, maxHearts)), at: at})
		}
	}
	for _, it := range in.Mine {
		if it.State == store.ItemRemoved && !seen[it.ID] {
			out = append(out, reaction{item: it, v: removedSignal, at: it.UpdatedAt, throwback: isThrowback(it)})
		}
	}
	out = append(out, followUps(in, reach)...)
	for i := range out {
		out[i].name = artistOf(TrackOf(out[i].item))
		out[i].artist = ArtistKey(out[i].name)
	}
	return out
}

// followUps finds members asking for more: queueing a song again soon
// after it played, or one by the artist of autopilot's song that was
// playing, or one like it.
func followUps(in Input, reach int) []reaction {
	// Members' songs, newest first.
	var queued []store.QueueItem
	for _, it := range in.Upcoming {
		if !it.IsAutopilot() {
			queued = append(queued, it)
		}
	}
	plays := in.History[:min(len(in.History), reach)]
	for _, h := range plays {
		if !h.QueueItem.IsAutopilot() && h.QueueItem.State != store.ItemPlaying {
			queued = append(queued, h.QueueItem)
		}
	}
	songs, artists := make([]string, len(plays)), make([]string, len(plays))
	for i, h := range plays {
		t := TrackOf(h.QueueItem)
		songs[i], artists[i] = SongKey(artistOf(t), t.Title), ArtistKey(artistOf(t))
	}
	var out []reaction
	for _, it := range queued {
		t := TrackOf(it)
		song, artist := SongKey(artistOf(t), t.Title), ArtistKey(artistOf(t))
		again, followed := false, false
		for i, h := range plays {
			p, ended := h.PlayHistory, endOf(h.PlayHistory)
			if p.QueueItemID == it.ID || p.StartedAt.After(it.AddedAt) {
				continue
			}
			// Queued again: the same song, played within reAddWindow before.
			if !again && songs[i] == song && it.AddedAt.Sub(ended) <= reAddWindow {
				again = true
				out = append(out, reaction{item: it, who: it.AddedBy, v: reAddSignal, at: it.AddedAt})
			}
			// Queued while autopilot's song played, or just after.
			if !followed && h.QueueItem.IsAutopilot() && !it.AddedAt.After(ended.Add(followWindow)) {
				pa := artists[i]
				if pa != "" && (pa == artist || (in.Related != nil && in.Related(pa, artist))) {
					followed = true
					out = append(out, reaction{item: h.QueueItem, who: it.AddedBy, v: followSignal, at: it.AddedAt})
				}
			}
		}
	}
	return out
}

// skipSignal is how much a skip says, by how far into the song it came.
func skipSignal(h store.ListHistoryRow) float64 {
	played := h.PlayHistory.EndedAt.Time.Sub(h.PlayHistory.StartedAt)
	if played < quickSkip {
		return quickSkipSignal
	}
	length := TrackOf(h.QueueItem).Duration
	if length <= 0 {
		length = typicalLength
	}
	return lerp(skippedSignal, lateSkipSignal, float64(played)/float64(length))
}

// owner is whose taste a reaction to a song is about: the member who
// queued it, or "" for autopilot's.
func owner(it store.QueueItem) string {
	if it.IsAutopilot() {
		return ""
	}
	return it.AddedBy
}

func endReason(h store.ListHistoryRow) string { return h.PlayHistory.EndReason.String }

// endOf is when a play ended, or started if it hasn't.
func endOf(p store.PlayHistory) time.Time {
	if p.EndedAt.Valid {
		return p.EndedAt.Time
	}
	return p.StartedAt
}

// isThrowback reports whether autopilot picked an item as a throwback.
func isThrowback(it store.QueueItem) bool {
	if !it.IsAutopilot() {
		return false
	}
	var info struct {
		Reason struct {
			Kind string `json:"kind"`
		} `json:"reason"`
	}
	_ = json.Unmarshal([]byte(it.Autopilot.String), &info)
	return info.Reason.Kind == KindThrowback
}
