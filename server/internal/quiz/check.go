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
		switch {
		case len(q.Blanks) > 0:
			return blanksRight(r.Text, q.Blanks)
		case q.Topic == TopicNextLine:
			return lineRight(r.Text, q.Correct)
		}
	}
	return false, 0
}

// blanksRight marks the words typed for a line's blanks: each one right,
// in any order, scores its share.
func blanksRight(got string, blanks []string) (bool, float64) {
	typed := canon(got)
	used := make([]bool, len(typed))
	right := 0
	for _, b := range blanks {
		want := strings.Join(canon(b), "")
		for i, w := range typed {
			if !used[i] && sameWord(w, want) {
				used[i] = true
				right++
				break
			}
		}
	}
	if right == len(blanks) {
		return true, 1
	}
	return false, float64(right) / float64(len(blanks))
}

// lineRight marks a typed lyric line by how many of its words came in
// order: nearly all is right, half or more scores a share.
func lineRight(got, want string) (bool, float64) {
	g, w := canon(got), canon(want)
	if len(w) == 0 {
		return false, 0
	}
	// The longest run of words in common, in order.
	prev := make([]int, len(w)+1)
	cur := make([]int, len(w)+1)
	for i := range g {
		for j := range w {
			if sameWord(g[i], w[j]) {
				cur[j+1] = prev[j] + 1
			} else {
				cur[j+1] = max(prev[j+1], cur[j])
			}
		}
		prev, cur = cur, prev
	}
	share := float64(prev[len(w)]) / float64(len(w))
	switch {
	case share >= 0.85:
		return true, 1
	case share >= 0.5:
		return false, share
	}
	return false, 0
}

// sameWord reports whether a typed word is the wanted one, allowing a typo
// in longer words.
func sameWord(got, want string) bool {
	allow := 0
	if len([]rune(want)) >= 6 {
		allow = 1
	}
	return got == want || levenshtein(got, want, allow) <= allow
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
	return strings.Join(canon(parens.ReplaceAllString(s, "")), "")
}

// canon is text's words, simplified and with how people sing and type
// them evened out: "do not" and "don't", "runnin'" and "running",
// "'cause" and "because" are the same.
func canon(s string) []string {
	ws := strings.Fields(key(s))
	out := make([]string, 0, len(ws))
	for i := 0; i < len(ws); i++ {
		if i+1 < len(ws) {
			if c, ok := contractions[ws[i]+" "+ws[i+1]]; ok {
				out = append(out, c)
				i++
				continue
			}
		}
		w := ws[i]
		if c, ok := spellings[w]; ok {
			w = c
		}
		if len(w) > 4 && strings.HasSuffix(w, "ing") {
			w = strings.TrimSuffix(w, "g")
		}
		out = append(out, w)
	}
	return out
}

// contractions are two words sung as one, after punctuation's gone.
var contractions = map[string]string{
	"do not": "dont", "does not": "doesnt", "did not": "didnt", "is not": "isnt", "are not": "arent",
	"was not": "wasnt", "can not": "cant", "will not": "wont", "would not": "wouldnt", "could not": "couldnt",
	"should not": "shouldnt", "i am": "im", "you are": "youre", "they are": "theyre", "it is": "its",
	"that is": "thats", "i will": "ill", "you will": "youll", "i have": "ive", "you have": "youve",
	"i would": "id", "let us": "lets", "going to": "gonna", "want to": "wanna", "got to": "gotta",
}

// spellings are words with another way of writing them.
var spellings = map[string]string{
	"cannot": "cant", "because": "cause", "cuz": "cause", "cos": "cause", "coz": "cause",
	"until": "til", "till": "til", "them": "em", "okay": "ok", "though": "tho",
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
