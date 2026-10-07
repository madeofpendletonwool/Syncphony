// SPDX-License-Identifier: AGPL-3.0-only

// Package match finds the same recording on another service: when a
// song's own service can't play it, the room plays it through someone
// else's. See docs/adr/0006-cross-service-matching.md.
//
// Matching is strict on purpose. Playing nothing (skipping) beats playing
// the live version, a remix, or a different song with the same name.
package match

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Score says how well got matches want, from 0 to 1, and whether it's a
// match at all. The same ISRC is a perfect match. Otherwise titles must
// agree (ignoring remaster notes and featured artists), an artist must
// overlap, both must be the same kind of version (live, remix, acoustic,
// ...), and durations must be within 10 seconds when both are known.
func Score(want, got provider.Track) (float64, bool) {
	if want.ISRC != "" && strings.EqualFold(want.ISRC, got.ISRC) {
		return 1, true
	}
	wt, wv := title(want.Title)
	gt, gv := title(got.Title)
	if wt == "" || !slices.Equal(wv, gv) {
		return 0, false
	}
	ts := 1.0
	if wt != gt {
		ts = jaccard(strings.Fields(wt), strings.Fields(gt))
		if ts < 0.8 {
			return 0, false
		}
	}
	if !artistsOverlap(want.Artists, got.Artists) {
		return 0, false
	}
	ds := 1.0
	if want.Duration > 0 && got.Duration > 0 {
		switch d := (want.Duration - got.Duration).Abs(); {
		case d <= 3*time.Second:
		case d <= 10*time.Second:
			ds = 0.5
		default:
			return 0, false
		}
	}
	// Short of an ISRC, even a perfect fuzzy match scores below 1.
	return 0.95 * (0.6*ts + 0.4*ds), true
}

// Qualifiers in brackets or after a dash that don't change the recording.
var harmless = regexp.MustCompile(`(?i)\b(remaster(ed)?|\d{4} remaster(ed)?|remastered \d{4}|mono|stereo|single version|album version|original mix|explicit|clean|deluxe( edition)?|bonus track)\b`)

// Words that mark a different recording.
var variants = []string{"live", "remix", "mix", "acoustic", "instrumental", "demo", "karaoke", "cover", "edit", "sped up", "slowed", "reprise", "unplugged", "orchestral"}

var (
	brackets = regexp.MustCompile(`\s*[(\[]([^)\]]*)[)\]]`)
	dashTail = regexp.MustCompile(`\s+-\s+(.*)$`)
	feat     = regexp.MustCompile(`(?i)\s*\b(feat\.?|ft\.?|featuring)\s.*$`)
)

// Title normalizes a title, without its qualifiers, and returns the words
// in them that mark a different recording ("Song (Live at Wembley)" is
// "song" with ["live"]; "Song - 2011 Remaster" is just "song").
func Title(s string) (string, []string) { return title(s) }

// title normalizes a title and pulls out the variant words in its
// qualifiers ("Song (Live at Wembley)" is "song" with ["live"]).
func title(s string) (string, []string) {
	var quals []string
	s = brackets.ReplaceAllStringFunc(s, func(m string) string {
		quals = append(quals, brackets.FindStringSubmatch(m)[1])
		return " "
	})
	if m := dashTail.FindStringSubmatch(s); m != nil {
		quals = append(quals, m[1])
		s = strings.TrimSuffix(s, m[0])
	}
	s = feat.ReplaceAllString(s, "")
	var found []string
	for _, q := range quals {
		q = " " + Simplify(harmless.ReplaceAllString(q, " ")) + " "
		for _, v := range variants {
			if strings.Contains(q, " "+v+" ") && !slices.Contains(found, v) {
				found = append(found, v)
			}
		}
	}
	slices.Sort(found)
	return Simplify(s), found
}

// Simplify lowercases, strips accents and punctuation, and collapses
// spaces, so "Beyoncé – Halo!" and "beyonce halo" compare equal.
func Simplify(s string) string {
	var b strings.Builder
	space := true
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// accents, after NFD
		case r == '&':
			if !space {
				b.WriteByte(' ')
			}
			b.WriteString("and ")
			space = true
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			space = false
		case r == '\'' || r == '’':
			// "don't" and "dont" are the same word
		default:
			if !space {
				b.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func jaccard(a, b []string) float64 {
	set := map[string]int{}
	for _, w := range a {
		set[w] |= 1
	}
	for _, w := range b {
		set[w] |= 2
	}
	both := 0
	for _, v := range set {
		if v == 3 {
			both++
		}
	}
	return float64(both) / float64(len(set))
}

var artistSplit = regexp.MustCompile(`(?i)\s*(,|&|\band\b|\bx\b|\bfeat\.?|\bft\.?|\bfeaturing|\bwith\b|/|;)\s*`)

// artistsOverlap reports whether any artist credited on a is credited on
// b. Credits like "A & B" count as both A and B; "The" is ignored.
func artistsOverlap(a, b []provider.ArtistCredit) bool {
	names := func(cs []provider.ArtistCredit) map[string]bool {
		out := map[string]bool{}
		for _, c := range cs {
			out[artistKey(c.Name)] = true
			for _, part := range artistSplit.Split(c.Name, -1) {
				out[artistKey(part)] = true
			}
		}
		delete(out, "")
		return out
	}
	an := names(a)
	for n := range names(b) {
		if an[n] {
			return true
		}
	}
	return false
}

func artistKey(s string) string {
	s = Simplify(s)
	return strings.TrimPrefix(s, "the ")
}
