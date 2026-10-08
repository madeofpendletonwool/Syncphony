// SPDX-License-Identifier: AGPL-3.0-only

package quiz_test

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
)

// heroes is a fixture: a 2011 remaster of a 1977 song, with credits,
// samples and synced lyrics.
var heroes = quiz.Facts{
	Song:  quiz.Song{Title: "“Heroes”", Artist: "David Bowie"},
	Album: "“Heroes” (2017 Remaster)", Year: 1977, ReleaseYear: 2017,
	Credits: []quiz.Credit{
		{Role: "Written by", Names: []string{"David Bowie", "Brian Eno"}},
		{Role: "Produced by", Names: []string{"Tony Visconti", "David Bowie"}},
	},
	SampledBy: []quiz.Song{{Title: "Heroes", Artist: "Peter Gabriel"}, {Title: "Hero Worship", Artist: "Someone"}},
	Lyrics: []quiz.Line{
		{Ms: 0, Text: "I, I will be king"},
		{Ms: 4000, Text: "And you, you will be queen"},
		{Ms: 8000, Text: "Though nothing will drive them away"},
		{Ms: 12000, Text: "We can beat them just for one day"},
		{Ms: 16000, Text: "We can be heroes just for one day"},
	},
	Sections:   []quiz.Section{{StartMs: 0, Energy: 0.4}, {StartMs: 60000, Energy: 0.9}},
	DurationMs: 371000,
	Rank:       0.9,
}

var pool = quiz.Pool{
	Songs:    []quiz.Song{{Title: "Heroes", Artist: "Motörhead"}, {Title: "Ashes to Ashes", Artist: "David Bowie"}, {Title: "Once in a Lifetime", Artist: "Talking Heads"}, {Title: "The Passenger", Artist: "Iggy Pop"}, {Title: "Atmosphere", Artist: "Joy Division"}},
	Artists:  []string{"Iggy Pop", "Roxy Music", "Talking Heads", "brian eno"},
	People:   []string{"Nile Rodgers", "Tony Visconti", "Conny Plank", "Steve Lillywhite"},
	ThisYear: 2026,
}

func rng() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) } //nolint:gosec // seeded on purpose, for repeatable tests

func TestReady(t *testing.T) {
	got := quiz.Ready(heroes)
	want := []string{rooms.GameYear, rooms.GameLiner, rooms.GameSample, rooms.GameLyrics, rooms.GameFinishLyric, rooms.GameTune}
	if !slices.Equal(got, want) {
		t.Errorf("Ready = %v, want %v", got, want)
	}
	if got := quiz.Ready(quiz.Facts{Song: quiz.Song{Title: "Unknown"}}); len(got) != 0 {
		t.Errorf("a song nobody knows is ready for %v", got)
	}
}

func TestYearSlider(t *testing.T) {
	for seed := range uint64(50) {
		for _, year := range []int{1905, 1977, 2024} {
			f := quiz.Facts{Song: quiz.Song{Title: "Song"}, Year: year}
			q, ok := quiz.Ask(rooms.GameYear, f, pool, rand.New(rand.NewPCG(seed, 7))) //nolint:gosec // seeded on purpose
			if !ok || q.Topic != quiz.TopicYear || q.Answer != quiz.AnswerNumber || len(q.Choices) != 0 {
				t.Fatalf("%d: %+v", year, q)
			}
			// The slider holds the year, starts on a decade no later than
			// 1970, and ends this year.
			if q.Min > year || q.Max != 2026 || q.Min < 1900 || q.Min > 1970 || q.Min%10 != 0 {
				t.Errorf("%d: slider %d–%d", year, q.Min, q.Max)
			}
		}
	}
}

func TestYearCountsTheFirstRelease(t *testing.T) {
	q, ok := quiz.Ask(rooms.GameYear, heroes, pool, rng())
	if !ok {
		t.Fatal("no year question")
	}
	if q.Correct != "1977" || q.Topic != quiz.TopicFirstOut || !strings.Contains(q.Prompt, "2017") || !slices.Contains(q.Hides, quiz.HideNotes) {
		t.Errorf("a remaster: %+v", q)
	}
	// Exact scores, near scores partly, far doesn't.
	for _, tc := range []struct {
		n       int
		correct bool
		close   bool
	}{{1977, true, true}, {1979, false, true}, {1990, false, false}} {
		correct, c := quiz.Check(q, quiz.Response{Number: &tc.n})
		if correct != tc.correct || (c > 0) != tc.close {
			t.Errorf("%d: correct %v closeness %v", tc.n, correct, c)
		}
	}
}

func TestHigherOrLower(t *testing.T) {
	p := pool
	p.Previous = &quiz.Dated{Song: quiz.Song{Title: "Smells Like Teen Spirit", Artist: "Nirvana"}, Year: 1991}
	f := quiz.Facts{Song: quiz.Song{Title: "Heroes", Artist: "David Bowie"}, Year: 1977}
	var q quiz.Question
	for seed := range uint64(20) {
		if c, ok := quiz.Ask(rooms.GameYear, f, p, rand.New(rand.NewPCG(seed, 9))); ok && c.Topic == quiz.TopicHigherLower { //nolint:gosec // seeded on purpose
			q = c
			break
		}
	}
	if q.Topic != quiz.TopicHigherLower || q.Correct != "Older" || q.Choices[q.CorrectIndex] != "Older" || !strings.Contains(q.Prompt, "1991") {
		t.Fatalf("%+v", q)
	}
	if !strings.Contains(q.Reveal, "14 years before") {
		t.Errorf("reveal %q", q.Reveal)
	}
	// The same year isn't a question.
	p.Previous.Year = 1977
	for seed := range uint64(20) {
		if c, _ := quiz.Ask(rooms.GameYear, f, p, rand.New(rand.NewPCG(seed, 9))); c.Topic == quiz.TopicHigherLower { //nolint:gosec // seeded on purpose
			t.Fatalf("asked about two songs from one year: %+v", c)
		}
	}
}

func TestLinerNeverOffersARightAnswerAsWrong(t *testing.T) {
	for seed := range uint64(40) {
		q, ok := quiz.Ask(rooms.GameLiner, heroes, pool, rand.New(rand.NewPCG(seed, 3))) //nolint:gosec // seeded on purpose
		if !ok {
			t.Fatal("no liner question")
		}
		if q.Topic != quiz.TopicCredit || q.Answer != quiz.AnswerChoice || len(q.Choices) != 4 {
			t.Fatalf("%+v", q)
		}
		for i, c := range q.Choices {
			// Brian Eno co-wrote it and Visconti produced it: neither may be
			// offered as wrong, whatever their spelling in the pool.
			if i != q.CorrectIndex && (strings.EqualFold(c, "brian eno") || c == "Tony Visconti" || c == "David Bowie") {
				t.Errorf("seed %d: %q is offered as wrong in %v", seed, c, q.Choices)
			}
		}
	}
}

func TestLinerNeedsEnoughWrongAnswers(t *testing.T) {
	if _, ok := quiz.Ask(rooms.GameLiner, heroes, quiz.Pool{People: []string{"Tony Visconti", "Nile Rodgers"}}, rng()); ok {
		t.Error("asked with only one wrong answer to offer")
	}
}

func TestCover(t *testing.T) {
	f := quiz.Facts{Song: quiz.Song{Title: "Hurt", Artist: "Johnny Cash"}, CoverOf: &quiz.Original{Title: "Hurt", Writers: []string{"Trent Reznor"}}}
	q, ok := quiz.Ask(rooms.GameLiner, f, pool, rng())
	if !ok || q.Topic != quiz.TopicCover || q.Correct != "Trent Reznor" || q.Choices[q.CorrectIndex] != "Trent Reznor" {
		t.Errorf("%v %+v", ok, q)
	}
}

func TestLinerTopics(t *testing.T) {
	f := quiz.Facts{
		Song: quiz.Song{Title: "Whole Lotta Love", Artist: "Led Zeppelin"}, Album: "Led Zeppelin II", Label: "Atlantic", Year: 1969,
		Origin: "London", OriginLine: "Led Zeppelin formed in London in 1968",
		Credits: []quiz.Credit{{Role: "Guitar", Names: []string{"Jimmy Page"}}, {Role: "Drums", Names: []string{"John Bonham"}}},
	}
	p := quiz.Pool{
		Albums:  []string{"Abbey Road", "Paranoid", "Let It Bleed"},
		Labels:  []string{"Apple", "Vertigo", "Decca", "atlantic"},
		Players: []string{"Tony Iommi", "Keith Richards", "Ringo Starr"},
		Places:  []string{"Birmingham", "United Kingdom"},
	}
	seen := map[string]quiz.Question{}
	for seed := range uint64(60) {
		q, ok := quiz.Ask(rooms.GameLiner, f, p, rand.New(rand.NewPCG(seed, 13))) //nolint:gosec // seeded on purpose
		if !ok {
			t.Fatal("no liner question")
		}
		seen[q.Topic] = q
		if q.Detail == "" || len(q.Choices) != 4 || q.Choices[q.CorrectIndex] != q.Correct {
			t.Errorf("%+v", q)
		}
		if slices.Contains(q.Choices, "atlantic") {
			t.Errorf("the right label offered as wrong: %v", q.Choices)
		}
	}
	for _, topic := range []string{quiz.TopicAlbum, quiz.TopicLabel, quiz.TopicOrigin, quiz.TopicPlayedOn} {
		if _, ok := seen[topic]; !ok {
			t.Errorf("never asked %s", topic)
		}
	}
	// The album's on every screen, so asking about it hides the song.
	if q := seen[quiz.TopicAlbum]; !slices.Contains(q.Hides, quiz.HideSong) {
		t.Errorf("album question hides %v", q.Hides)
	}
	// A city's wrong answers are cities, never the night's countries.
	if q := seen[quiz.TopicOrigin]; slices.Contains(q.Choices, "United Kingdom") || q.Detail != f.OriginLine {
		t.Errorf("origin %+v", q)
	}
	if q := seen[quiz.TopicPlayedOn]; !strings.HasPrefix(q.Detail, "Guitar: Jimmy Page") {
		t.Errorf("played on %+v", q)
	}
}

func TestSamplesSkipSongsSharingATitle(t *testing.T) {
	for seed := range uint64(40) {
		q, ok := quiz.Ask(rooms.GameSample, heroes, pool, rand.New(rand.NewPCG(seed, 5))) //nolint:gosec // seeded on purpose
		if !ok {
			t.Fatal("no sample question")
		}
		hits := 0
		for _, c := range q.Choices {
			if strings.Contains(c, "“Heroes”") || strings.Contains(c, "Hero Worship") {
				hits++
			}
		}
		if q.Other == nil || q.Correct != q.Other.Label() {
			t.Errorf("seed %d: other song %+v", seed, q.Other)
		}
		// The right answer may be Gabriel's "Heroes"; Motörhead's cover of
		// the same title may never be a wrong answer.
		if hits != 1 || slices.ContainsFunc(q.Choices, func(c string) bool { return strings.Contains(c, "Motörhead") }) {
			t.Errorf("seed %d: %v", seed, q.Choices)
		}
	}
}

func TestBeatTheSinger(t *testing.T) {
	for seed := range uint64(30) {
		q, ok := quiz.Ask(rooms.GameLyrics, heroes, pool, rand.New(rand.NewPCG(seed, 11))) //nolint:gosec // seeded on purpose
		if !ok || q.Topic != quiz.TopicBlanks || q.Answer != quiz.AnswerText || !slices.Contains(q.Hides, quiz.HideLine) {
			t.Fatalf("%v %+v", ok, q)
		}
		// Never the first line, and the line's still to come.
		if q.AtMs == 0 || len(q.Blanks) == 0 || len(q.Blanks) > 3 || !strings.Contains(q.Prompt, "____") {
			t.Fatalf("seed %d: %+v", seed, q)
		}
		for _, b := range q.Blanks {
			if len(b) < 3 || strings.EqualFold(b, "the") || strings.EqualFold(b, "and") {
				t.Errorf("seed %d: blanked %q", seed, b)
			}
		}
		if ok, _ := quiz.Check(q, quiz.Response{Text: strings.ToUpper(q.Correct) + "!"}); !ok {
			t.Errorf("%q doesn't take its own words shouted", q.Correct)
		}
	}
	// Lines sung before the question could be up aren't asked.
	p := pool
	p.AfterMs = 13000
	q, ok := quiz.Ask(rooms.GameLyrics, heroes, p, rng())
	if !ok || q.AtMs != 16000 {
		t.Errorf("after 13s: %v %+v", ok, q)
	}
	p.AfterMs = 20000
	if _, ok := quiz.Ask(rooms.GameLyrics, heroes, p, rng()); ok {
		t.Error("asked about a line already sung")
	}
}

func TestBlanksScoreEachWord(t *testing.T) {
	q := quiz.Question{Topic: quiz.TopicBlanks, Answer: quiz.AnswerText, Correct: "don't running", Blanks: []string{"don't", "running"}}
	for _, tc := range []struct {
		got       string
		correct   bool
		closeness float64
	}{
		{"do not runnin'", true, 1},
		{"Running, dont", true, 1},
		{"dont walking", false, 0.5},
		{"nothing", false, 0},
	} {
		correct, c := quiz.Check(q, quiz.Response{Text: tc.got})
		if correct != tc.correct || c != tc.closeness {
			t.Errorf("%q: %v %v", tc.got, correct, c)
		}
	}
}

func TestFinishTheLyric(t *testing.T) {
	q, ok := quiz.Ask(rooms.GameFinishLyric, heroes, pool, rng())
	if !ok || q.Topic != quiz.TopicNextLine || !slices.Contains(q.Hides, quiz.HideLine) || q.AtMs == 0 {
		t.Fatalf("%v %+v", ok, q)
	}
	line := quiz.Question{Topic: quiz.TopicNextLine, Answer: quiz.AnswerText, Correct: "We can be heroes just for one day"}
	for _, tc := range []struct {
		got     string
		correct bool
		some    bool
	}{
		{"we can be heroes just for one day", true, true},
		{"We can be heroes for one day", true, true},
		{"we can be kings for a day", false, true},
		{"something else", false, false},
	} {
		correct, c := quiz.Check(line, quiz.Response{Text: tc.got})
		if correct != tc.correct || (c > 0) != tc.some {
			t.Errorf("%q: %v %v", tc.got, correct, c)
		}
	}
	// A chorus that goes two ways is a guess, not a question.
	twoWays := quiz.Facts{Lyrics: []quiz.Line{
		{Ms: 0, Text: "intro line here"},
		{Ms: 1000, Text: "second line here"},
		{Ms: 2000, Text: "oh we sing along"},
		{Ms: 3000, Text: "into the night sky"},
		{Ms: 4000, Text: "oh we sing along"},
		{Ms: 5000, Text: "out to the sea"},
	}}
	if q, ok := quiz.Ask(rooms.GameFinishLyric, twoWays, pool, rng()); ok && strings.Contains(q.Prompt, "oh we sing along") {
		t.Errorf("asked about an ambiguous line: %+v", q)
	}
}

func TestMatches(t *testing.T) {
	for _, tc := range []struct {
		got, want string
		ok        bool
	}{
		{"the beatles", "Beatles", true},
		{"Beyonce", "Beyoncé", true},
		{"dont stop me now", "Don't Stop Me Now", true},
		{"Bohemian Rapsody", "Bohemian Rhapsody", true},
		{"Heroes", "Heroes (2017 Remaster)", true},
		{"We can be heros just for one day", "We can be heroes just for one day", true},
		{"Help", "Hello", false},
		{"ABBA", "Abc", false},
		{"", "Anything", false},
		{"Something else entirely", "Bohemian Rhapsody", false},
	} {
		if got := quiz.Matches(tc.got, tc.want); got != tc.ok {
			t.Errorf("Matches(%q, %q) = %v", tc.got, tc.want, got)
		}
	}
}

func TestDifficulty(t *testing.T) {
	obscure := heroes
	obscure.Rank = 0.05
	easy, _ := quiz.Ask(rooms.GameLiner, heroes, pool, rng())
	hard, _ := quiz.Ask(rooms.GameLiner, obscure, pool, rng())
	if !(easy.Difficulty < hard.Difficulty) || easy.Difficulty < 0 || hard.Difficulty > 1 {
		t.Errorf("a hit %v, a deep cut %v", easy.Difficulty, hard.Difficulty)
	}
}
