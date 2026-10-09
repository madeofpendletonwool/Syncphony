// SPDX-License-Identifier: AGPL-3.0-only

package quiz_test

import (
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
)

func TestClipStart(t *testing.T) {
	mapped := quiz.Facts{
		DurationMs: 240_000,
		Sections:   []quiz.Section{{StartMs: 0, Energy: 0.2}, {StartMs: 61_000, Energy: 0.9}, {StartMs: 150_000, Energy: 0.6}},
		Bars:       []int64{1_500, 30_000, 62_000, 64_000, 228_000, 232_000},
	}
	for _, tc := range []struct {
		name string
		f    quiz.Facts
		spot string
		want int64
	}{
		// The loudest section, snapped to the bar just after it starts.
		{"chorus", mapped, rooms.ClipChorus, 62_000},
		// No beat map: a third of the way in.
		{"no map", quiz.Facts{DurationMs: 240_000}, rooms.ClipChorus, 80_000},
		{"intro", mapped, rooms.ClipIntro, 1_500},
		{"intro, no map", quiz.Facts{DurationMs: 240_000}, rooms.ClipIntro, 0},
		// Ends 4s before the song does, on the bar before.
		{"outro", mapped, rooms.ClipOutro, 228_000},
		{"outro, no map", quiz.Facts{DurationMs: 240_000}, rooms.ClipOutro, 228_000},
	} {
		if got := quiz.ClipStart(tc.f, tc.spot, 8_000); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestNameTune(t *testing.T) {
	q, ok := quiz.NameTune(heroes, pool, false, rooms.ClipChorus, rng())
	if !ok {
		t.Fatal("no question")
	}
	if q.Kind != rooms.GameTune || q.Topic != quiz.TopicTune || q.Answer != quiz.AnswerChoice || len(q.Choices) != 4 || q.Choices[q.CorrectIndex] != q.Correct {
		t.Fatalf("%+v", q)
	}
	for i, c := range q.Choices {
		// Motörhead's "Heroes" shares the title: it can't be a wrong answer.
		if i != q.CorrectIndex && c == "“Heroes” by Motörhead" {
			t.Errorf("a wrong answer that's right: %s", c)
		}
	}
	if len(q.Hides) != 0 {
		t.Errorf("hides %v: it's not the playing song", q.Hides)
	}
	if _, ok := quiz.NameTune(heroes, quiz.Pool{}, false, rooms.ClipChorus, rng()); ok {
		t.Error("choices with nothing to choose from")
	}

	hard, ok := quiz.NameTune(heroes, quiz.Pool{}, true, rooms.ClipOutro, rng())
	if !ok || hard.Answer != quiz.AnswerSong || hard.Topic != quiz.TopicOutro {
		t.Fatalf("%+v", hard)
	}
	for _, tc := range []struct {
		typed     string
		correct   bool
		closeness float64
	}{
		{"heroes", true, 1},
		{"Heroes (2017 Remaster)", true, 1},
		{"heros", true, 1},
		{"david bowie", false, 0.5},
		{"ashes to ashes", false, 0},
	} {
		if c, cl := quiz.Check(hard, quiz.Response{Text: tc.typed}); c != tc.correct || cl != tc.closeness {
			t.Errorf("%q: %v %v", tc.typed, c, cl)
		}
	}
	if _, ok := quiz.NameTune(quiz.Facts{Song: quiz.Song{Title: "Short"}, DurationMs: 30_000}, pool, true, rooms.ClipChorus, rng()); ok {
		t.Error("a 30s song as a tune")
	}
}
