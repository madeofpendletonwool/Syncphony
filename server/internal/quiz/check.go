// SPDX-License-Identifier: AGPL-3.0-only

package quiz

import (
	"regexp"
	"strconv"
	"strings"
)

// Response is someone's answer to a question: a choice's index, a number
// or some text, whichever the question takes. A number question shown as
// choices takes a choice too.
type Response struct {
	Choice *int
	Number *int
	Text   string
}

// Check marks a response. Closeness is 1 for a right answer, and for a
// number within the question's tolerance, how near it came (0–1); 0 for
// anything else.
func Check(q Question, r Response) (correct bool, closeness float64) {
	switch q.Answer {
	case AnswerChoice:
		if r.Choice != nil && *r.Choice == q.CorrectIndex {
			return true, 1
		}
	case AnswerNumber:
		want, err := strconv.Atoi(q.Correct)
		if err != nil {
			return false, 0
		}
		got, ok := number(q, r)
		if !ok {
			return false, 0
		}
		off := abs(got - want)
		if off == 0 {
			return true, 1
		}
		if off <= q.Tolerance {
			return false, 1 - float64(off)/float64(q.Tolerance+1)
		}
	case AnswerText, AnswerSong:
		for _, want := range append([]string{q.Correct}, q.Accept...) {
			if Matches(r.Text, want) {
				return true, 1
			}
		}
	}
	return false, 0
}

func number(q Question, r Response) (int, bool) {
	if r.Number != nil {
		return *r.Number, true
	}
	if r.Choice != nil && *r.Choice >= 0 && *r.Choice < len(q.Choices) {
		n, err := strconv.Atoi(q.Choices[*r.Choice])
		return n, err == nil
	}
	return 0, false
}

// parens are asides in titles: "(Remastered 2011)", "[Live]".
var parens = regexp.MustCompile(`\s*[(\[][^)\]]*[)\]]`)

// Matches reports whether typed text is the wanted title, name or lyric:
// case, punctuation, accents, a leading "the", asides in brackets and a
// few typos don't matter. Longer answers forgive more typos.
func Matches(got, want string) bool {
	g, w := simple(got), simple(want)
	if g == "" || w == "" {
		return false
	}
	if g == w {
		return true
	}
	allow := len([]rune(w)) / 6
	if len([]rune(w)) < 4 {
		allow = 0
	}
	return levenshtein(g, w, allow) <= allow
}

func simple(s string) string {
	s = parens.ReplaceAllString(s, "")
	return strings.ReplaceAll(key(s), " ", "")
}

// levenshtein is the edit distance between a and b, or more than limit
// once it's sure to be.
func levenshtein(a, b string, limit int) int {
	ra, rb := []rune(a), []rune(b)
	if abs(len(ra)-len(rb)) > limit {
		return limit + 1
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		best := cur[0]
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			best = min(best, cur[j])
		}
		if best > limit {
			return limit + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
