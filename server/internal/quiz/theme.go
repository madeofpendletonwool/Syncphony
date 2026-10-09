// SPDX-License-Identifier: AGPL-3.0-only

package quiz

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/match"
)

// Theme rounds (MAD-795): the big screen gives a prompt, everyone queues a
// song for it, and the server says whether each fits, from what's known.
// The data has gaps, so a song it can't check is let through, and one that
// doesn't fit still plays: it's only marked.

// Theme kinds: what a prompt asks for.
const (
	ThemeBefore1985 = "before_1985"
	ThemeNineties   = "nineties"
	ThemeYear       = "year"
	ThemeCover      = "cover"
	ThemeSamples    = "samples"
	ThemeLive       = "live"
	ThemeProducer   = "producer"
	ThemeFast       = "fast"
	ThemeSlow       = "slow"
	ThemeBuild      = "build"
	ThemeSad        = "sad"
	ThemeGenre      = "genre"
)

// ThemeKinds are every theme, in the order they're offered.
var ThemeKinds = []string{
	ThemeBefore1985, ThemeNineties, ThemeYear, ThemeCover, ThemeSamples, ThemeLive,
	ThemeProducer, ThemeFast, ThemeSlow, ThemeBuild, ThemeSad, ThemeGenre,
}

// Theme is a theme round's prompt.
type Theme struct {
	Kind   string `json:"kind"`
	Prompt string `json:"prompt"`
	// Year is the year a ThemeYear asks for; Name, the producer or genre.
	Year int    `json:"year,omitempty"`
	Name string `json:"name,omitempty"`
}

// ThemePool is what prompts may name: the night's producers and genres.
type ThemePool struct {
	// Producers are credited as producers on the night's songs.
	Producers []string
	// Genres are the night's artists' tags, most common first.
	Genres []string
	// ThisYear caps a year prompt.
	ThisYear int
}

// Whether a song fits a theme.
const (
	FitYes     = "yes"
	FitNo      = "no"
	FitUnknown = "unknown"
)

// Tempos and builds the beat-map themes ask for.
const (
	fastBPM = 140
	slowBPM = 95
	// buildRise is how much louder (energy, 0–1) a later section must be
	// than an earlier one for a quiet-to-loud build.
	buildRise = 0.35
)

// sadTags are tags that mark a sad song.
var sadTags = []string{"sad", "melancholy", "melancholic", "depressing", "heartbreak", "heartbreaking", "breakup", "sad songs", "emo"}

// MakeTheme makes a theme of a kind, naming what the pool has: ok is false
// if it needs a name the pool doesn't have.
func MakeTheme(kind string, p ThemePool, rng *rand.Rand) (Theme, bool) {
	t := Theme{Kind: kind}
	switch kind {
	case ThemeBefore1985:
		t.Prompt = "A song from before 1985"
	case ThemeNineties:
		t.Prompt = "A song from the 90s"
	case ThemeYear:
		top := max(1975, min(p.ThisYear, 2010))
		t.Year = 1965 + rng.IntN(top-1965+1)
		t.Prompt = fmt.Sprintf("A song from %d: the year someone here was born?", t.Year)
	case ThemeCover:
		t.Prompt = "A cover"
	case ThemeSamples:
		t.Prompt = "A song that samples something"
	case ThemeLive:
		t.Prompt = "A live recording"
	case ThemeProducer:
		if len(p.Producers) == 0 {
			return Theme{}, false
		}
		t.Name = p.Producers[rng.IntN(len(p.Producers))]
		t.Prompt = "A song produced by " + t.Name
	case ThemeFast:
		t.Prompt = fmt.Sprintf("Over %d BPM", fastBPM)
	case ThemeSlow:
		t.Prompt = "The slowest song you can stand"
	case ThemeBuild:
		t.Prompt = "A big quiet-to-loud build"
	case ThemeSad:
		t.Prompt = "A sad song"
	case ThemeGenre:
		if len(p.Genres) == 0 {
			return Theme{}, false
		}
		t.Name = p.Genres[rng.IntN(min(len(p.Genres), 5))]
		t.Prompt = "Something " + t.Name
	default:
		return Theme{}, false
	}
	return t, true
}

// PickTheme picks a theme at random, from those the pool can name.
func PickTheme(p ThemePool, rng *rand.Rand) Theme {
	kinds := slices.Clone(ThemeKinds)
	rng.Shuffle(len(kinds), func(i, j int) { kinds[i], kinds[j] = kinds[j], kinds[i] })
	for _, k := range kinds {
		if t, ok := MakeTheme(k, p, rng); ok {
			return t
		}
	}
	t, _ := MakeTheme(ThemeNineties, p, rng)
	return t
}

// Fits says whether a song fits a theme, and a note for the big screen
// when it doesn't, or can't tell ("Not a cover, as far as we know").
func (t Theme) Fits(f Facts) (fit, note string) {
	year := func(ok func(int) bool, want string) (string, string) {
		if f.Year == 0 {
			return FitUnknown, "We don't know when it came out"
		}
		if ok(f.Year) {
			return FitYes, ""
		}
		return FitNo, fmt.Sprintf("From %d, not %s", f.Year, want)
	}
	bpm := func(ok func(float64) bool, miss string) (string, string) {
		if f.BPM == 0 {
			return FitUnknown, "We don't know its tempo yet"
		}
		if ok(f.BPM) {
			return FitYes, fmt.Sprintf("%.0f BPM", f.BPM)
		}
		return FitNo, fmt.Sprintf("%.0f BPM: %s", f.BPM, miss)
	}
	switch t.Kind {
	case ThemeBefore1985:
		return year(func(y int) bool { return y < 1985 }, "before 1985")
	case ThemeNineties:
		return year(func(y int) bool { return y >= 1990 && y < 2000 }, "from the 90s")
	case ThemeYear:
		return year(func(y int) bool { return y == t.Year }, fmt.Sprint(t.Year))
	case ThemeCover:
		switch {
		case f.CoverOf != nil:
			return FitYes, ""
		case f.Notes:
			return FitNo, "Not a cover, as far as we know"
		}
		return FitUnknown, "We can't tell if it's a cover"
	case ThemeSamples:
		switch {
		case len(f.Samples) > 0:
			return FitYes, "Samples " + f.Samples[0].Label()
		case f.Notes:
			return FitNo, "No samples, as far as we know"
		}
		return FitUnknown, "We can't tell if it samples anything"
	case ThemeLive:
		if _, quals := match.Title(f.Title); slices.Contains(quals, "live") || strings.Contains(strings.ToLower(f.Album), "live") {
			return FitYes, ""
		}
		return FitNo, "Not a live recording, as far as we know"
	case ThemeProducer:
		names := credited(f, "Produced by")
		if len(names) == 0 && !f.Notes {
			return FitUnknown, "We don't know who produced it"
		}
		for _, n := range names {
			if Matches(n, t.Name) {
				return FitYes, ""
			}
		}
		if len(names) == 0 {
			return FitNo, "Not produced by " + t.Name + ", as far as we know"
		}
		return FitNo, "Produced by " + list(names)
	case ThemeFast:
		return bpm(func(b float64) bool { return b > fastBPM }, "not fast enough")
	case ThemeSlow:
		return bpm(func(b float64) bool { return b <= slowBPM }, "not that slow")
	case ThemeBuild:
		if len(f.Sections) < 2 {
			return FitUnknown, "We haven't mapped it yet"
		}
		if Build(f) >= buildRise {
			return FitYes, ""
		}
		return FitNo, "No big build, as far as we can hear"
	case ThemeSad, ThemeGenre:
		if len(f.Tags) == 0 {
			return FitUnknown, "We don't know its genre"
		}
		want := []string{match.Simplify(t.Name)}
		if t.Kind == ThemeSad {
			want = sadTags
		}
		for _, tag := range f.Tags {
			if slices.Contains(want, match.Simplify(tag)) {
				return FitYes, ""
			}
		}
		if t.Kind == ThemeSad {
			return FitNo, "Not sad, as far as we know"
		}
		return FitNo, "Not " + t.Name + ", as far as we know"
	}
	return FitUnknown, ""
}

// Build is how much louder a song gets from a quiet part to a later loud
// one: the biggest rise in section energy, 0 to 1.
func Build(f Facts) float64 {
	best, quiet := 0.0, 1.0
	for _, s := range f.Sections {
		quiet = min(quiet, s.Energy)
		best = max(best, s.Energy-quiet)
	}
	return best
}
