// SPDX-License-Identifier: AGPL-3.0-only

package quiz

import (
	"math/rand/v2"
	"slices"

	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
)

// --- Name that tune (MAD-793) ----------------------------------------------

// Tuneable reports whether a song can be the tune in name that tune: it's
// named, and long enough to clip from the middle.
func Tuneable(f Facts) bool { return f.Title != "" && f.DurationMs >= 60_000 }

// Clip timing, in ms.
const (
	// outroEnd is how far before the end an outro clip stops: the last
	// moments are often a fade to nothing.
	outroEnd = 4_000
	// snapWithin is how far a clip's start moves to land on a bar.
	snapWithin = 4_000
)

// ClipStart is where a song's clips start, in ms, for a clip spot
// (rooms.Clip*) and the longest clip cut from there. The chorus is the
// loudest section of the beat map, not 0:00 (intros are often silence,
// or too hard), or a third of the way in without one. An intro starts on
// the first bar; an outro ends a little before the song does. Each lands
// on a bar when the song has a beat map, so clips start cleanly.
func ClipStart(f Facts, spot string, longestMs int64) int64 {
	var at int64
	switch spot {
	case rooms.ClipIntro:
		if len(f.Bars) > 0 {
			return f.Bars[0]
		}
		return 0
	case rooms.ClipOutro:
		at = max(f.DurationMs-outroEnd-longestMs, 0)
		// The bar at or before, so the clip still ends in time.
		for i := len(f.Bars) - 1; i >= 0; i-- {
			if f.Bars[i] <= at {
				if at-f.Bars[i] <= snapWithin {
					at = f.Bars[i]
				}
				break
			}
		}
		return at
	}
	if peak := peakSection(f); peak.end > peak.start && peakEnergy(f) > 0 {
		at = peak.start
	} else {
		at = f.DurationMs / 3
	}
	if b, ok := next(f.Bars, at); ok && b-at <= snapWithin {
		at = b
	}
	if f.DurationMs > 0 {
		at = min(at, max(f.DurationMs-outroEnd-longestMs, 0))
	}
	return at
}

func peakEnergy(f Facts) float64 {
	best := 0.0
	for _, s := range f.Sections {
		best = max(best, s.Energy)
	}
	return best
}

// next is the first of times (ascending ms) at or after ms.
func next(times []int64, ms int64) (int64, bool) {
	for _, t := range times {
		if t >= ms {
			return t, true
		}
	}
	return 0, false
}

// NameTune is name that tune: which song is this clip from? As choices,
// the wrong ones are top songs by similar artists, so they sound right.
// Typed (hard mode), the title is matched fuzzily and the artist alone
// scores half. spot is where the clip's from (rooms.Clip*).
func NameTune(f Facts, p Pool, typed bool, spot string, rng *rand.Rand) (Question, bool) {
	if !Tuneable(f) {
		return Question{}, false
	}
	q := Question{
		Kind: rooms.GameTune, Topic: TopicTune, Prompt: "Name that tune",
		Correct: f.Title, Reveal: f.Song.Label(), Detail: release(f),
		Difficulty: difficulty(f.Rank, 0.5),
	}
	switch spot {
	case rooms.ClipIntro:
		q.Topic, q.Prompt = TopicIntro, "Name that tune from its intro"
	case rooms.ClipOutro:
		q.Topic, q.Prompt = TopicOutro, "Name that tune from how it ends"
		q.Difficulty = difficulty(f.Rank, 0.65)
	}
	if typed {
		q.Answer = AnswerSong
		if f.Artist != "" && key(f.Artist) != key(f.Title) {
			q.Half = []string{f.Artist}
		}
		q.Difficulty = difficulty(f.Rank, 0.8)
		return q, true
	}
	wrong := pickSongs(slices.Concat(p.Songs, p.Era), []Song{f.Song}, choices-1, rng)
	if len(wrong) < choices-1 {
		return Question{}, false
	}
	labels := make([]string, 0, choices)
	for _, w := range wrong {
		labels = append(labels, w.Label())
	}
	q.Answer, q.Correct = AnswerChoice, f.Song.Label()
	q.Choices, q.CorrectIndex = shuffle(append(labels, f.Song.Label()), f.Song.Label(), rng)
	return q, true
}
