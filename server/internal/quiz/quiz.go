// SPDX-License-Identifier: AGPL-3.0-only

// Package quiz is the question kit every trivia round draws on (MAD-786):
// what's known about a song, which games it can carry, and questions with
// plausible wrong answers. Like stats and fairness it's pure: facts in,
// questions out, no I/O and no clock. Package games gathers the facts and
// runs the rounds.
//
// Good trivia lives on its wrong answers. They come from the song's own
// neighbourhood (similar artists' songs, the night's other credits, years
// near the real one) and are never also right: a co-writer isn't offered
// as a wrong writer, nor a cover sharing the title as a wrong sample.
package quiz

import (
	"cmp"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
)

// Song names a song.
type Song struct {
	Title  string `json:"title"`
	Artist string `json:"artist,omitempty"`
}

// Label is how a song reads as an answer: “Title” by Artist.
func (s Song) Label() string {
	if s.Artist == "" {
		return "“" + s.Title + "”"
	}
	return "“" + s.Title + "” by " + s.Artist
}

// Credit is everyone in one role, as liner notes list them.
type Credit struct {
	Role  string
	Names []string
}

// Line is a synced lyric line.
type Line struct {
	Ms   int64
	Text string
}

// Section is a part of the song, from its beat map.
type Section struct {
	StartMs int64
	Energy  float64
}

// Facts are what's known about one song, from its liner notes, lyrics,
// beat map and the music graph. Zero values are unknown.
type Facts struct {
	Song
	Album string
	// Year is when the song first came out; ReleaseYear, when the release
	// this copy is from did. A 2011 remaster of a 1979 song is 1979 and 2011.
	Year, ReleaseYear int
	// CoverOf is the song this covers, with its writers.
	CoverOf *Original
	// Samples are songs it samples; SampledBy, songs that sample it.
	Samples, SampledBy []Song
	Credits            []Credit
	// Lyrics are its synced lines, in order.
	Lyrics   []Line
	Sections []Section
	// Bars are when each bar starts, in ms, for timing rounds to the music.
	Bars []int64
	BPM  float64
	// Energy is how loud and driving it is, 0 (quiet acoustic) – 1.
	Energy     float64
	DurationMs int64
	Tags       []string
	// Rank is how popular it is across all music, 0–1.
	Rank float64
}

// Original is a song as written.
type Original struct {
	Title   string
	Writers []string
}

// Pool is where wrong answers come from: the song's neighbourhood.
type Pool struct {
	// Songs are top songs by similar artists, and the night's other songs.
	Songs []Song
	// Artists are similar artists.
	Artists []string
	// People are producers and writers of the night's other songs.
	People []string
	// Era are songs of about the same era and genre as what's sampled.
	Era []Song
	// ThisYear caps wrong years: no song comes from the future.
	ThisYear int
}

// Answer kinds.
const (
	AnswerChoice = "choice"
	AnswerNumber = "number"
	AnswerText   = "text"
	AnswerSong   = "song"
)

// What a question hides until its reveal, so no screen gives it away.
const (
	// HideSong hides the title, artists, album and artwork.
	HideSong = "song"
	// HideNotes hides liner notes.
	HideNotes = "notes"
	// HideLyrics hides the lyrics.
	HideLyrics = "lyrics"
)

// Topics: what a question is about, within its game.
const (
	TopicYear      = "year"
	TopicFirstOut  = "first_released"
	TopicCover     = "cover"
	TopicCredit    = "credit"
	TopicSamples   = "samples"
	TopicSampledBy = "sampled_by"
	TopicNextLine  = "next_line"
)

// Question is one question about a song.
type Question struct {
	// Kind is the game (rooms.Game*); Topic, what it asks within it.
	Kind, Topic string
	Prompt      string
	// Answer is how it's answered: one of the Answer* kinds.
	Answer string
	// Choices, for AnswerChoice, in the order they're shown.
	Choices []string `json:",omitempty"`
	// Correct is the right answer: the right choice's text, the number,
	// or the text.
	Correct string
	// CorrectIndex is the right choice's index, for AnswerChoice.
	CorrectIndex int
	// Accept are other answers that count, for AnswerText.
	Accept []string `json:",omitempty"`
	// Tolerance is how far off a number may be and still score some.
	Tolerance int `json:",omitempty"`
	// Hides is what screens keep back until the reveal.
	Hides []string `json:",omitempty"`
	// Difficulty is roughly how hard it is, 0 (easy) to 1, from the song's
	// popularity and how close the wrong answers are.
	Difficulty float64
	// Reveal is a line to show with the answer.
	Reveal string
	// AtMs, if set, is when in the song the question is about: a lyric
	// question closes as the singer gets there.
	AtMs int64 `json:",omitempty"`
}

// Choices per multiple-choice question: the right one and three wrong.
const choices = 4

// Ready are the games a song can carry. Queue games (connect, theme,
// bracket) aren't about one song, so they're never here.
func Ready(f Facts) []string {
	var out []string
	if f.Year > 0 {
		out = append(out, rooms.GameYear)
	}
	if f.CoverOf != nil || creditName(f) != "" {
		out = append(out, rooms.GameLiner)
	}
	if len(f.Samples) > 0 || len(f.SampledBy) > 0 {
		out = append(out, rooms.GameSample)
	}
	if len(lyricCues(f.Lyrics)) > 0 {
		out = append(out, rooms.GameLyrics, rooms.GameFinishLyric)
	}
	if len(f.Sections) >= 2 && f.DurationMs >= 60_000 {
		out = append(out, rooms.GameTune)
	}
	return out
}

// Ask makes a question of a game about a song, with wrong answers from
// the pool. ok is false if the song can't carry it, or the pool has too
// few wrong answers that aren't also right.
func Ask(kind string, f Facts, p Pool, rng *rand.Rand) (Question, bool) {
	var qs []Question
	switch kind {
	case rooms.GameYear:
		qs = years(f, p, rng)
	case rooms.GameLiner:
		qs = liner(f, p, rng)
	case rooms.GameSample:
		qs = samples(f, p, rng)
	case rooms.GameLyrics, rooms.GameFinishLyric:
		qs = lyric(kind, f, rng)
	}
	if len(qs) == 0 {
		return Question{}, false
	}
	return qs[rng.IntN(len(qs))], true
}

// --- Years ---------------------------------------------------------------

func years(f Facts, p Pool, rng *rand.Rand) []Question {
	if f.Year <= 0 {
		return nil
	}
	wrong := WrongYears(f.Year, p.ThisYear, choices-1, rng)
	if len(wrong) < choices-1 {
		return nil
	}
	closest := slices.MinFunc(wrong, func(a, b int) int { return cmp.Compare(abs(a-f.Year), abs(b-f.Year)) })
	// The closer the wrong years, the harder: 2 off is hard, 10 easy.
	near := 1 - float64(abs(closest-f.Year)-2)/8
	q := Question{
		Kind: rooms.GameYear, Topic: TopicYear,
		Prompt: "What year did this song first come out?",
		Answer: AnswerNumber, Correct: strconv.Itoa(f.Year), Tolerance: 5,
		Hides:      []string{HideNotes},
		Difficulty: difficulty(f.Rank, near),
		Reveal:     strconv.Itoa(f.Year),
	}
	if f.ReleaseYear > f.Year {
		q.Topic = TopicFirstOut
		q.Prompt = "This version is from " + strconv.Itoa(f.ReleaseYear) + ". When did the song first come out?"
		q.Reveal = "First released in " + strconv.Itoa(f.Year) + ", " + strconv.Itoa(f.ReleaseYear-f.Year) + " years earlier"
	}
	// Ambient screens show it as choices; the number still scores closeness.
	labels := make([]string, 0, choices)
	for _, y := range append(wrong, f.Year) {
		labels = append(labels, strconv.Itoa(y))
	}
	q.Choices, q.CorrectIndex = shuffle(labels, strconv.Itoa(f.Year), rng)
	return []Question{q}
}

// WrongYears picks n years near the right one for wrong answers: none adjacent to
// it or to each other (1978 against 1979 is a coin toss, not trivia),
// none after thisYear, none before recorded music. Fewer if there's no room.
func WrongYears(year, thisYear, n int, rng *rand.Rand) []int {
	if thisYear <= 0 {
		thisYear = year + 10
	}
	var cands []int
	for d := 2; d <= 12; d++ {
		for _, y := range []int{year - d, year + d} {
			if y >= 1900 && y <= thisYear {
				cands = append(cands, y)
			}
		}
	}
	// Prefer near years: walk the candidates in a shuffled order that
	// keeps the closest ones toward the front.
	slices.SortStableFunc(cands, func(a, b int) int {
		return cmp.Compare(abs(a-year)+rng.IntN(5), abs(b-year)+rng.IntN(5))
	})
	var out []int
	for _, y := range cands {
		if len(out) == n {
			break
		}
		if !slices.ContainsFunc(out, func(o int) bool { return abs(o-y) < 2 }) {
			out = append(out, y)
		}
	}
	slices.Sort(out)
	return out
}

// --- Liner notes ---------------------------------------------------------

func liner(f Facts, p Pool, rng *rand.Rand) []Question {
	var out []Question
	right := everyone(f)
	if c := f.CoverOf; c != nil && len(c.Writers) > 0 {
		if wrong := pick(names(p), right, choices-1, rng); len(wrong) == choices-1 {
			q := Question{
				Kind: rooms.GameLiner, Topic: TopicCover, Answer: AnswerChoice,
				Prompt:     "This is a cover. Who wrote the original, “" + c.Title + "”?",
				Correct:    c.Writers[0],
				Hides:      []string{HideNotes},
				Difficulty: difficulty(f.Rank, 0.6),
				Reveal:     "Written by " + list(c.Writers),
			}
			q.Choices, q.CorrectIndex = shuffle(append(wrong, c.Writers[0]), c.Writers[0], rng)
			out = append(out, q)
		}
	}
	if role, name := credit(f); name != "" {
		if wrong := pick(names(p), right, choices-1, rng); len(wrong) == choices-1 {
			q := Question{
				Kind: rooms.GameLiner, Topic: TopicCredit, Answer: AnswerChoice,
				Prompt:     creditPrompt(role),
				Correct:    name,
				Hides:      []string{HideNotes},
				Difficulty: difficulty(f.Rank, 0.5),
				Reveal:     role + " " + name,
			}
			q.Choices, q.CorrectIndex = shuffle(append(wrong, name), name, rng)
			out = append(out, q)
		}
	}
	return out
}

// creditRoles are the roles worth asking about, best first.
var creditRoles = []string{"Produced by", "Written by", "Music by", "Lyrics by"}

// credit is the role to ask about and its first name.
func credit(f Facts) (string, string) {
	for _, role := range creditRoles {
		for _, c := range f.Credits {
			if c.Role == role && len(c.Names) > 0 {
				return role, c.Names[0]
			}
		}
	}
	return "", ""
}

func creditName(f Facts) string {
	_, n := credit(f)
	return n
}

func creditPrompt(role string) string {
	switch role {
	case "Produced by":
		return "Who produced this song?"
	case "Music by":
		return "Who wrote the music for this song?"
	case "Lyrics by":
		return "Who wrote the words to this song?"
	}
	return "Who wrote this song?"
}

// everyone is every name with a hand in the song: none of them may be a
// wrong answer.
func everyone(f Facts) []string {
	out := []string{f.Artist}
	for _, c := range f.Credits {
		out = append(out, c.Names...)
	}
	if f.CoverOf != nil {
		out = append(out, f.CoverOf.Writers...)
	}
	return out
}

// names are the people and artists wrong answers can come from: the
// night's credits first, then similar artists.
func names(p Pool) []string { return slices.Concat(p.People, p.Artists) }

// --- Samples -------------------------------------------------------------

func samples(f Facts, p Pool, rng *rand.Rand) []Question {
	var out []Question
	taken := slices.Concat([]Song{f.Song}, f.Samples, f.SampledBy)
	pool := slices.Concat(p.Era, p.Songs)
	ask := func(topic, prompt string, s Song, reveal string) {
		wrong := pickSongs(pool, taken, choices-1, rng)
		if len(wrong) < choices-1 {
			return
		}
		labels := make([]string, 0, choices)
		for _, w := range wrong {
			labels = append(labels, w.Label())
		}
		q := Question{
			Kind: rooms.GameSample, Topic: topic, Answer: AnswerChoice, Prompt: prompt,
			Correct: s.Label(), Hides: []string{HideNotes},
			Difficulty: difficulty(f.Rank, 0.55), Reveal: reveal,
		}
		q.Choices, q.CorrectIndex = shuffle(append(labels, s.Label()), s.Label(), rng)
		out = append(out, q)
	}
	if len(f.Samples) > 0 {
		s := f.Samples[rng.IntN(len(f.Samples))]
		ask(TopicSamples, "This song samples another. Which one?", s, "It samples "+s.Label())
	}
	if len(f.SampledBy) > 0 {
		s := f.SampledBy[rng.IntN(len(f.SampledBy))]
		ask(TopicSampledBy, "Which of these songs samples this one?", s, "Sampled in "+s.Label())
	}
	return out
}

// pickSongs picks n songs from pool that aren't any of taken, nor share
// a title with one (a cover, or the same song by its other name).
func pickSongs(pool, taken []Song, n int, rng *rand.Rand) []Song {
	seen := map[string]bool{}
	for _, t := range taken {
		seen[key(t.Title)] = true
	}
	var cands []Song
	for _, s := range pool {
		k := key(s.Title)
		if s.Title == "" || seen[k] {
			continue
		}
		seen[k] = true
		cands = append(cands, s)
	}
	rng.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
	return cands[:min(n, len(cands))]
}

// --- Lyrics --------------------------------------------------------------

// minWords is the fewest words a lyric line needs to be a fair question.
const minWords = 3

// lyricCues are indexes of lines whose next line makes a fair question:
// both long enough, past the opening, and the line doesn't recur with a
// different line after it (a chorus that goes two ways is a guess).
func lyricCues(ls []Line) []int {
	next := map[string]map[string]bool{}
	for i := 0; i+1 < len(ls); i++ {
		k := key(ls[i].Text)
		if next[k] == nil {
			next[k] = map[string]bool{}
		}
		next[k][key(ls[i+1].Text)] = true
	}
	var out []int
	for i := 2; i+1 < len(ls); i++ {
		a, b := ls[i], ls[i+1]
		if words(a.Text) < minWords || words(b.Text) < minWords || len(next[key(a.Text)]) > 1 || b.Ms <= a.Ms {
			continue
		}
		out = append(out, i)
	}
	return out
}

func lyric(kind string, f Facts, rng *rand.Rand) []Question {
	cues := lyricCues(f.Lyrics)
	if len(cues) == 0 {
		return nil
	}
	i := cues[rng.IntN(len(cues))]
	cue, line := f.Lyrics[i], f.Lyrics[i+1]
	prompt := "Beat the singer: what's the next line?"
	if kind == rooms.GameFinishLyric {
		prompt = "Finish the line"
	}
	return []Question{{
		Kind: kind, Topic: TopicNextLine, Answer: AnswerText,
		Prompt:  prompt + "\n“" + strings.TrimSpace(cue.Text) + "”",
		Correct: strings.TrimSpace(line.Text), Hides: []string{HideLyrics},
		// Popular songs' lyrics are known; words are harder than choices.
		Difficulty: difficulty(f.Rank, 0.7),
		Reveal:     "“" + strings.TrimSpace(line.Text) + "”",
		AtMs:       line.Ms,
	}}
}

// --- Helpers -------------------------------------------------------------

// difficulty mixes how obscure the song is with how close its wrong
// answers are (0–1). An unknown rank counts as middling.
func difficulty(rank, near float64) float64 {
	if rank <= 0 {
		rank = 0.4
	}
	d := 0.6*(1-rank) + 0.4*clamp01(near)
	return math.Round(clamp01(d)*100) / 100
}

// pick picks n names from pool, none of them right (any spelling of a
// name in right) and no two the same.
func pick(pool, right []string, n int, rng *rand.Rand) []string {
	seen := map[string]bool{}
	for _, r := range right {
		seen[key(r)] = true
	}
	var cands []string
	for _, p := range pool {
		k := key(p)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		cands = append(cands, p)
	}
	rng.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
	return cands[:min(n, len(cands))]
}

// shuffle shuffles labels and returns where right ended up.
func shuffle(labels []string, right string, rng *rand.Rand) ([]string, int) {
	out := slices.Clone(labels)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out, slices.Index(out, right)
}

// key compares names and titles: case, accents, punctuation and a
// leading "the" don't matter.
func key(s string) string { return strings.TrimPrefix(match.Simplify(s), "the ") }

func words(s string) int { return len(strings.Fields(s)) }

func list(names []string) string {
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func clamp01(x float64) float64 { return math.Max(0, math.Min(1, x)) }
