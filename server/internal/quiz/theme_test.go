// SPDX-License-Identifier: AGPL-3.0-only

package quiz_test

import (
	"math/rand/v2"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/quiz"
)

func TestThemeFits(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // a test's picks
	pool := quiz.ThemePool{Producers: []string{"Rick Rubin"}, Genres: []string{"shoegaze"}, ThisYear: 2026}
	theme := func(kind string) quiz.Theme {
		th, ok := quiz.MakeTheme(kind, pool, rng)
		if !ok {
			t.Fatalf("no %s theme", kind)
		}
		return th
	}
	for _, tc := range []struct {
		name string
		kind string
		f    quiz.Facts
		want string
	}{
		{"from 1979", quiz.ThemeBefore1985, quiz.Facts{Year: 1979}, quiz.FitYes},
		{"from 1999", quiz.ThemeBefore1985, quiz.Facts{Year: 1999}, quiz.FitNo},
		{"no year", quiz.ThemeNineties, quiz.Facts{}, quiz.FitUnknown},
		{"a cover", quiz.ThemeCover, quiz.Facts{CoverOf: &quiz.Original{Title: "Hurt"}}, quiz.FitYes},
		{"notes, no cover", quiz.ThemeCover, quiz.Facts{Notes: true}, quiz.FitNo},
		{"no notes", quiz.ThemeCover, quiz.Facts{}, quiz.FitUnknown},
		{"samples", quiz.ThemeSamples, quiz.Facts{Samples: []quiz.Song{{Title: "Amen, Brother"}}}, quiz.FitYes},
		{"live", quiz.ThemeLive, quiz.Facts{Song: quiz.Song{Title: "Song (Live at Wembley)"}}, quiz.FitYes},
		{"studio", quiz.ThemeLive, quiz.Facts{Song: quiz.Song{Title: "Song"}}, quiz.FitNo},
		{"produced by", quiz.ThemeProducer, quiz.Facts{Credits: []quiz.Credit{{Role: "Produced by", Names: []string{"Rick Rubin"}}}}, quiz.FitYes},
		{"produced by someone else", quiz.ThemeProducer, quiz.Facts{Credits: []quiz.Credit{{Role: "Produced by", Names: []string{"Eno"}}}}, quiz.FitNo},
		{"fast", quiz.ThemeFast, quiz.Facts{BPM: 174}, quiz.FitYes},
		{"no tempo", quiz.ThemeFast, quiz.Facts{}, quiz.FitUnknown},
		{"slow", quiz.ThemeSlow, quiz.Facts{BPM: 70}, quiz.FitYes},
		{"a build", quiz.ThemeBuild, quiz.Facts{Sections: []quiz.Section{{Energy: 0.6}, {Energy: 0.2}, {Energy: 0.9}}}, quiz.FitYes},
		{"flat", quiz.ThemeBuild, quiz.Facts{Sections: []quiz.Section{{Energy: 0.6}, {Energy: 0.7}}}, quiz.FitNo},
		{"sad", quiz.ThemeSad, quiz.Facts{Tags: []string{"Melancholy", "indie"}}, quiz.FitYes},
		{"a genre", quiz.ThemeGenre, quiz.Facts{Tags: []string{"Shoegaze"}}, quiz.FitYes},
		{"another genre", quiz.ThemeGenre, quiz.Facts{Tags: []string{"techno"}}, quiz.FitNo},
	} {
		if got, note := theme(tc.kind).Fits(tc.f); got != tc.want || (got != quiz.FitYes && note == "") {
			t.Errorf("%s: %s (%q), want %s", tc.name, got, note, tc.want)
		}
	}
}

func TestMakeTheme(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // a test's picks
	if _, ok := quiz.MakeTheme(quiz.ThemeProducer, quiz.ThemePool{}, rng); ok {
		t.Error("a producer theme with no producers")
	}
	th, ok := quiz.MakeTheme(quiz.ThemeYear, quiz.ThemePool{ThisYear: 2026}, rng)
	if !ok || th.Year < 1965 || th.Year > 2010 {
		t.Errorf("year theme %+v", th)
	}
	for range 50 {
		if th := quiz.PickTheme(quiz.ThemePool{ThisYear: 2026}, rng); th.Kind == quiz.ThemeProducer || th.Kind == quiz.ThemeGenre || th.Prompt == "" {
			t.Fatalf("picked %+v from an empty pool", th)
		}
	}
}
