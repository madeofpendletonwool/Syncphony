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

func TestWrongYears(t *testing.T) {
	for seed := range uint64(50) {
		r := rand.New(rand.NewPCG(seed, 7)) //nolint:gosec // seeded on purpose
		for _, tc := range []struct{ real, now int }{{1977, 2026}, {2024, 2026}, {1905, 2026}} {
			ys := quiz.WrongYears(tc.real, tc.now, 3, r)
			if len(ys) != 3 {
				t.Fatalf("%d: %v", tc.real, ys)
			}
			for i, y := range ys {
				if abs(y-tc.real) < 2 || y > tc.now || y < 1900 {
					t.Errorf("%d: wrong year %d", tc.real, y)
				}
				for _, o := range ys[i+1:] {
					if abs(o-y) < 2 {
						t.Errorf("%d: %d and %d are adjacent", tc.real, y, o)
					}
				}
			}
		}
	}
}

func TestYearCountsTheFirstRelease(t *testing.T) {
	q, ok := quiz.Ask(rooms.GameYear, heroes, pool, rng())
	if !ok {
		t.Fatal("no year question")
	}
	if q.Correct != "1977" || q.Topic != quiz.TopicFirstOut || !strings.Contains(q.Prompt, "2017") {
		t.Errorf("a remaster: %+v", q)
	}
	if q.Choices[q.CorrectIndex] != "1977" || len(q.Choices) != 4 || !slices.Contains(q.Hides, quiz.HideNotes) {
		t.Errorf("choices %v at %d", q.Choices, q.CorrectIndex)
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
	right := q.CorrectIndex
	if ok, _ := quiz.Check(q, quiz.Response{Choice: &right}); !ok {
		t.Error("the right choice isn't right")
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
		// The right answer may be Gabriel's "Heroes"; Motörhead's cover of
		// the same title may never be a wrong answer.
		if hits != 1 || slices.ContainsFunc(q.Choices, func(c string) bool { return strings.Contains(c, "Motörhead") }) {
			t.Errorf("seed %d: %v", seed, q.Choices)
		}
	}
}

func TestLyrics(t *testing.T) {
	q, ok := quiz.Ask(rooms.GameLyrics, heroes, pool, rng())
	if !ok || q.Answer != quiz.AnswerText || !slices.Contains(q.Hides, quiz.HideLyrics) || q.AtMs == 0 {
		t.Fatalf("%v %+v", ok, q)
	}
	if ok, _ := quiz.Check(q, quiz.Response{Text: strings.ToUpper(q.Correct) + "!"}); !ok {
		t.Errorf("%q doesn't take its own line shouted", q.Correct)
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
	if q, ok := quiz.Ask(rooms.GameLyrics, twoWays, pool, rng()); ok && strings.Contains(q.Prompt, "oh we sing along") {
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

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
