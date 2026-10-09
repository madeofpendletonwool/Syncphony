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
	"unicode"
	"unicode/utf8"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
)

// Song names a song.
type Song struct {
	Title  string `json:"title"`
	Artist string `json:"artist,omitempty"`
	// ID is its MusicBrainz recording, if known, for its cover.
	ID string `json:"id,omitempty"`
}

// Label is how a song reads as an answer: “Title” by Artist.
func (s Song) Label() string {
	if s.Artist == "" {
		return "“" + s.Title + "”"
	}
	return "“" + s.Title + "” by " + s.Artist
}

// Dated is a song and the year it first came out.
type Dated struct {
	Song
	Year int
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
	// Label put out the release this copy is from.
	Label string
	// Year is when the song first came out; ReleaseYear, when the release
	// this copy is from did. A 2011 remaster of a 1979 song is 1979 and 2011.
	Year, ReleaseYear int
	// Origin is where the artist is from ("Seattle"); OriginLine says so
	// in the liner notes' words ("Nirvana formed in Aberdeen in 1987").
	Origin, OriginLine string
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

// Pool is the song's neighbourhood in the room: where wrong answers come
// from, and what came before it.
type Pool struct {
	// Songs are top songs by similar artists, and the night's other songs.
	Songs []Song
	// Artists are similar artists.
	Artists []string
	// People are producers and writers of the night's other songs;
	// Players, who sang and played on them.
	People, Players []string
	// Albums, Labels and Places are the night's other songs' albums,
	// labels, and where their artists are from.
	Albums, Labels, Places []string
	// Era are songs of about the same era and genre as what's sampled.
	Era []Song
	// Previous is the song the room played before this one, for higher
	// or lower. Nil if unknown.
	Previous *Dated
	// ThisYear caps wrong years: no song comes from the future.
	ThisYear int
	// AfterMs is where the song will be by the time a question is up: a
	// question about a moment in it (a lyric) picks one after.
	AfterMs int64
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
	// HideLine hides one lyric line: the one sung at the question's AtMs.
	HideLine = "line"
)

// Topics: what a question is about, within its game.
const (
	TopicYear        = "year"
	TopicFirstOut    = "first_released"
	TopicHigherLower = "higher_lower"
	TopicCover       = "cover"
	TopicCredit      = "credit"
	TopicPlayedOn    = "played_on"
	TopicAlbum       = "album"
	TopicLabel       = "label"
	TopicOrigin      = "origin"
	TopicSamples     = "samples"
	TopicSampledBy   = "sampled_by"
	TopicBlanks      = "blanks"
	TopicNextLine    = "next_line"
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
	// Min and Max bound a number answer: the ends of the year slider.
	// They're loose, so they don't give the answer away.
	Min, Max int `json:",omitempty"`
	// Blanks are the words a lyric question blanked out of its line, in
	// order. Each one right scores a share.
	Blanks []string `json:",omitempty"`
	// Hides is what screens keep back until the reveal.
	Hides []string `json:",omitempty"`
	// Difficulty is roughly how hard it is, 0 (easy) to 1, from the song's
	// popularity and how close the wrong answers are.
	Difficulty float64
	// Reveal is a line to show with the answer, and Detail one from the
	// liner notes, so people learn something even when they're wrong.
	Reveal string
	Detail string `json:",omitempty"`
	// Other is the other song in a sample question, shown beside this
	// one at the reveal.
	Other *Song `json:",omitempty"`
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
	if f.CoverOf != nil || len(f.Credits) > 0 || f.Label != "" || f.Origin != "" || (f.Album != "" && key(f.Album) != key(f.Title)) {
		out = append(out, rooms.GameLiner)
	}
	if len(f.Samples) > 0 || len(f.SampledBy) > 0 {
		out = append(out, rooms.GameSample)
	}
	if len(memorable(f, 0)) > 0 {
		out = append(out, rooms.GameLyrics)
	}
	if len(lyricCues(f.Lyrics, 0)) > 0 {
		out = append(out, rooms.GameFinishLyric)
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
	case rooms.GameLyrics:
		qs = blanks(f, p, rng)
	case rooms.GameFinishLyric:
		qs = finish(f, p, rng)
	}
	if len(qs) == 0 {
		return Question{}, false
	}
	return qs[rng.IntN(len(qs))], true
}

// --- Years ---------------------------------------------------------------

// yearTolerance is how many years off a guess may be and still score some.
const yearTolerance = 5

// years are guess the year, on a slider, and higher or lower against the
// song before.
func years(f Facts, p Pool, rng *rand.Rand) []Question {
	if f.Year <= 0 {
		return nil
	}
	year := strconv.Itoa(f.Year)
	q := Question{
		Kind: rooms.GameYear, Topic: TopicYear,
		Prompt: "What year is this from?",
		Answer: AnswerNumber, Correct: year, Tolerance: yearTolerance,
		Hides:      []string{HideNotes},
		Difficulty: difficulty(f.Rank, 0.6),
		Reveal:     year,
	}
	q.Min, q.Max = yearRange(f.Year, p.ThisYear, rng)
	if f.ReleaseYear > f.Year {
		// Reissues and remasters don't fool it: it's the first release.
		q.Topic = TopicFirstOut
		q.Prompt = "This version is from " + strconv.Itoa(f.ReleaseYear) + ". When did the song first come out?"
		q.Reveal = "First released in " + year + ", " + strconv.Itoa(f.ReleaseYear-f.Year) + " years earlier"
	}
	out := []Question{q}
	if hl, ok := higherLower(f, p); ok {
		out = append(out, hl)
	}
	return out
}

// yearRange is the slider's ends: from a decade start well before the
// year (never later than 1970, so modern songs all start there) to this
// year. The start moves at random, so it says little about the answer.
func yearRange(year, thisYear int, rng *rand.Rand) (lo, hi int) {
	hi = max(thisYear, year)
	lo = (year - 10 - rng.IntN(31)) / 10 * 10
	lo = max(min(lo, 1970), 1900)
	return min(lo, year), hi
}

// higherLower asks whether the song is older or newer than the one the
// room played before it. Songs from the same year don't count.
func higherLower(f Facts, p Pool) (Question, bool) {
	prev := p.Previous
	if prev == nil || prev.Year <= 0 || prev.Year == f.Year || key(prev.Title) == key(f.Title) {
		return Question{}, false
	}
	labels := []string{"Older", "Newer"}
	right, side := 0, "before"
	if f.Year > prev.Year {
		right, side = 1, "after"
	}
	gap := abs(f.Year - prev.Year)
	years := "years"
	if gap == 1 {
		years = "year"
	}
	return Question{
		Kind: rooms.GameYear, Topic: TopicHigherLower, Answer: AnswerChoice,
		Prompt:  "Older or newer than the last song?\n" + prev.Label() + ", " + strconv.Itoa(prev.Year),
		Choices: labels, Correct: labels[right], CorrectIndex: right,
		Hides: []string{HideNotes},
		// A year or two apart is hard; a few decades, easy.
		Difficulty: difficulty(f.Rank, 1-float64(gap-1)/15),
		Reveal:     labels[right] + ": " + strconv.Itoa(f.Year) + ", " + strconv.Itoa(gap) + " " + years + " " + side,
	}, true
}

// --- Liner notes ---------------------------------------------------------

// Roles that aren't playing on it.
var notPlaying = []string{"Written by", "Music by", "Lyrics by", "Produced by", "Arranged by", "Mixed by", "Engineered by", "Mastered by"}

// liner is liner-notes trivia: covers, credits, the release, and where
// the artist is from. It only asks what the notes know.
func liner(f Facts, p Pool, rng *rand.Rand) []Question {
	var out []Question
	ask := func(topic, prompt, answer string, pool, right []string, reveal, detail string, near float64, hides ...string) {
		wrong := pick(pool, append(slices.Clone(right), answer), choices-1, rng)
		if answer == "" || len(wrong) < choices-1 {
			return
		}
		if len(hides) == 0 {
			hides = []string{HideNotes}
		}
		q := Question{
			Kind: rooms.GameLiner, Topic: topic, Answer: AnswerChoice, Prompt: prompt, Correct: answer,
			Hides: hides, Difficulty: difficulty(f.Rank, near), Reveal: reveal, Detail: detail,
		}
		q.Choices, q.CorrectIndex = shuffle(append(wrong, answer), answer, rng)
		out = append(out, q)
	}
	everyone := everyone(f)

	if c := f.CoverOf; c != nil && len(c.Writers) > 0 {
		ask(TopicCover, "This is a cover. Who wrote the original, “"+c.Title+"”?", c.Writers[0], names(p), everyone,
			"Written by "+list(c.Writers), "A cover of “"+c.Title+"”", 0.6)
	}
	for _, role := range creditRoles {
		who := credited(f, role)
		if len(who) == 0 {
			continue
		}
		ask(TopicCredit, creditPrompt(role), who[0], names(p), everyone, role+" "+who[0], role+" "+list(who), 0.5)
	}
	if role, name := player(f, rng); name != "" {
		ask(TopicPlayedOn, "Which of these played on it?", name, slices.Concat(p.Players, p.People, p.Artists), everyone,
			role+": "+name, players(f), 0.55)
	}
	if f.Album != "" && key(f.Album) != key(f.Title) {
		// The album's on every screen unless the song's hidden.
		ask(TopicAlbum, "Which album is this from?", f.Album, p.Albums, []string{f.Title},
			"From “"+f.Album+"”", release(f), 0.5, HideSong, HideNotes)
	}
	if f.Label != "" {
		ask(TopicLabel, "What label put it out?", f.Label, p.Labels, nil,
			"Released on "+f.Label, release(f), 0.6)
	}
	if f.Origin != "" {
		ask(TopicOrigin, "Where is "+cmp.Or(f.Artist, "this artist")+" from?", f.Origin, places(f.Origin, p.Places), nil,
			"From "+f.Origin, f.OriginLine, 0.45)
	}
	return out
}

// creditRoles are the roles worth asking who did, best first.
var creditRoles = []string{"Produced by", "Written by", "Music by", "Lyrics by"}

// credited is everyone in a role.
func credited(f Facts, role string) []string {
	for _, c := range f.Credits {
		if c.Role == role {
			return c.Names
		}
	}
	return nil
}

// player is someone who sang or played on the song, but isn't its
// artist: "Guitar", "Jimmy Page".
func player(f Facts, rng *rand.Rand) (role, name string) {
	type credit struct{ role, name string }
	var cs []credit
	for _, c := range f.Credits {
		if slices.Contains(notPlaying, c.Role) {
			continue
		}
		for _, n := range c.Names {
			if key(n) != key(f.Artist) {
				cs = append(cs, credit{c.Role, n})
			}
		}
	}
	if len(cs) == 0 {
		return "", ""
	}
	c := cs[rng.IntN(len(cs))]
	return c.role, c.name
}

// players is who played on it, as the notes say: "Guitar: Jimmy Page ·
// Drums: John Bonham".
func players(f Facts) string {
	var parts []string
	for _, c := range f.Credits {
		if !slices.Contains(notPlaying, c.Role) && len(parts) < 3 {
			parts = append(parts, c.Role+": "+list(c.Names))
		}
	}
	return strings.Join(parts, " · ")
}

// release is the release line: “Nevermind” · DGC · 1991.
func release(f Facts) string {
	var parts []string
	if f.Album != "" {
		parts = append(parts, "“"+f.Album+"”")
	}
	if f.Label != "" {
		parts = append(parts, f.Label)
	}
	if y := cmp.Or(f.ReleaseYear, f.Year); y > 0 {
		parts = append(parts, strconv.Itoa(y))
	}
	return strings.Join(parts, " · ")
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

// Wrong places when the night hasn't enough: countries for a country,
// music cities for a city, so the answer doesn't stand out.
var (
	countries = []string{
		"United States", "United Kingdom", "Canada", "Australia", "Ireland", "Sweden", "Germany", "France",
		"Jamaica", "Iceland", "Norway", "New Zealand", "Japan", "South Korea", "Brazil", "Nigeria", "Netherlands", "Belgium",
	}
	cities = []string{
		"London", "Manchester", "Liverpool", "Glasgow", "Dublin", "New York", "Los Angeles", "Seattle", "Detroit",
		"Chicago", "Atlanta", "Nashville", "Memphis", "New Orleans", "Minneapolis", "Toronto", "Montreal", "Melbourne",
		"Stockholm", "Berlin", "Paris", "Bristol", "Sheffield", "Athens, Georgia",
	}
)

// places are wrong answers for where an artist is from: the night's
// places of the same kind first, then well-known ones.
func places(origin string, night []string) []string {
	isCountry := func(p string) bool {
		return slices.ContainsFunc(countries, func(c string) bool { return key(c) == key(p) })
	}
	country := isCountry(origin)
	out := slices.DeleteFunc(slices.Clone(night), func(p string) bool { return isCountry(p) != country })
	if country {
		return append(out, countries...)
	}
	return append(out, cities...)
}

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
			Difficulty: difficulty(f.Rank, 0.55), Reveal: reveal, Other: &s,
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

// soonest is how many of the next fair lines a lyric question picks
// from, so it's never long until the line comes.
const soonest = 3

// lyricCues are indexes of lines, at or after afterMs, whose next line
// makes a fair question: both long enough, past the opening, and the
// line doesn't recur with a different line after it (a chorus that goes
// two ways is a guess).
func lyricCues(ls []Line, afterMs int64) []int {
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
		if b.Ms < afterMs || words(a.Text) < minWords || words(b.Text) < minWords || len(next[key(a.Text)]) > 1 || b.Ms <= a.Ms {
			continue
		}
		out = append(out, i)
	}
	return out
}

// memorable are indexes of lines, sung at or after afterMs, worth
// blanking: never the first, long enough, with words worth guessing.
// Lines that come back (a chorus) and lines in the song's loudest part
// come first; others only if there are none.
func memorable(f Facts, afterMs int64) []int {
	ls := f.Lyrics
	count := map[string]int{}
	for _, l := range ls {
		count[key(l.Text)]++
	}
	peak := peakSection(f)
	var best, rest []int
	for i := 1; i < len(ls); i++ {
		l := ls[i]
		if l.Ms < afterMs || l.Ms <= ls[i-1].Ms || words(l.Text) < minWords+1 || len(content(l.Text)) == 0 {
			continue
		}
		if count[key(l.Text)] > 1 || (peak.end > 0 && l.Ms >= peak.start && l.Ms < peak.end) {
			best = append(best, i)
		} else {
			rest = append(rest, i)
		}
	}
	if len(best) > 0 {
		return best
	}
	return rest
}

// peakSection is when the song's loudest part plays, from its beat map.
func peakSection(f Facts) (span struct{ start, end int64 }) {
	best := -1.0
	for i, s := range f.Sections {
		end := f.DurationMs
		if i+1 < len(f.Sections) {
			end = f.Sections[i+1].StartMs
		}
		if s.Energy > best && end > s.StartMs {
			best, span.start, span.end = s.Energy, s.StartMs, end
		}
	}
	return span
}

// stopWords are too small to be worth blanking.
var stopWords = map[string]bool{
	"the": true, "and": true, "but": true, "for": true, "you": true, "your": true, "are": true, "was": true,
	"with": true, "that": true, "this": true, "its": true, "it": true, "all": true, "can": true, "our": true,
	"from": true, "have": true, "had": true, "has": true, "not": true, "she": true, "her": true, "him": true,
	"his": true, "they": true, "them": true, "then": true, "than": true, "what": true, "when": true, "who": true,
	"will": true, "just": true, "into": true, "out": true, "too": true, "yeah": true, "ooh": true, "ohh": true,
	"there": true, "were": true, "been": true, "some": true, "like": true, "get": true, "got": true, "yes": true,
	"one": true, "now": true, "how": true, "why": true, "where": true, "dont": true, "im": true, "ive": true,
}

// content are the indexes of a line's words worth blanking.
func content(text string) []int {
	var out []int
	for i, w := range strings.Fields(text) {
		k := key(w)
		if len([]rune(k)) >= 3 && !stopWords[k] && !strings.Contains(k, " ") {
			out = append(out, i)
		}
	}
	return out
}

// blanks is beat the singer: a line coming up with a few words blanked
// out, to type before the singer gets there. The last word worth
// guessing (often the rhyme) is always one.
func blanks(f Facts, p Pool, rng *rand.Rand) []Question {
	cands := memorable(f, p.AfterMs)
	if len(cands) == 0 {
		return nil
	}
	line := f.Lyrics[cands[rng.IntN(min(len(cands), soonest))]]
	words := strings.Fields(line.Text)
	idx := content(line.Text)
	n := 1
	switch {
	case len(words) >= 9 && len(idx) >= 4:
		n = 3
	case len(idx) >= 2:
		n = 2
	}
	pick := []int{idx[len(idx)-1]}
	others := slices.Clone(idx[:len(idx)-1])
	rng.Shuffle(len(others), func(i, j int) { others[i], others[j] = others[j], others[i] })
	pick = append(pick, others[:n-1]...)
	slices.Sort(pick)

	shown := slices.Clone(words)
	var missing []string
	for _, i := range pick {
		lead, core, trail := trim(words[i])
		shown[i] = lead + "____" + trail
		missing = append(missing, core)
	}
	return []Question{{
		Kind: rooms.GameLyrics, Topic: TopicBlanks, Answer: AnswerText,
		Prompt:  "Fill in the blanks before they're sung\n“" + strings.Join(shown, " ") + "”",
		Correct: strings.Join(missing, " "), Blanks: missing, Hides: []string{HideLine},
		Difficulty: difficulty(f.Rank, 0.4+0.15*float64(n)),
		Reveal:     "“" + strings.TrimSpace(line.Text) + "”",
		AtMs:       line.Ms,
	}}
}

// trim splits punctuation off a word: "(love," is "(", "love", ",".
func trim(w string) (lead, core, trail string) {
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’' }
	start := strings.IndexFunc(w, isWord)
	if start < 0 {
		return w, "", ""
	}
	end := strings.LastIndexFunc(w, isWord)
	_, size := utf8.DecodeRuneInString(w[end:])
	return w[:start], w[start : end+size], w[end+size:]
}

// finish is finish the lyric: the music stops as a line begins, and you
// type how it goes on from the line before.
func finish(f Facts, p Pool, rng *rand.Rand) []Question {
	cues := lyricCues(f.Lyrics, p.AfterMs)
	if len(cues) == 0 {
		return nil
	}
	i := cues[rng.IntN(min(len(cues), soonest))]
	cue, line := f.Lyrics[i], f.Lyrics[i+1]
	return []Question{{
		Kind: rooms.GameFinishLyric, Topic: TopicNextLine, Answer: AnswerText,
		Prompt:  "Finish the line\n“" + strings.TrimSpace(cue.Text) + "”",
		Correct: strings.TrimSpace(line.Text), Hides: []string{HideLine},
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
