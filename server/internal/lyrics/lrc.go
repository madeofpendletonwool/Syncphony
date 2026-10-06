// SPDX-License-Identifier: AGPL-3.0-only

package lyrics

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

var (
	// timeTag is [mm:ss], [mm:ss.xx] or [mm:ss:xx] at the start of a line.
	timeTag = regexp.MustCompile(`^\[(\d+):(\d{1,2})(?:[.:](\d{1,3}))?\]`)
	// metaTag is an ID tag, like [ar:Artist] or [offset:+250].
	metaTag = regexp.MustCompile(`^\[([a-zA-Z#]+):(.*)\]\s*$`)
	// wordTag is an enhanced-LRC word timing, <mm:ss.xx>.
	wordTag = regexp.MustCompile(`<\d+:\d{1,2}(?:[.:]\d{1,3})?>`)
)

// ParseLRC parses LRC lyrics into lines in time order. A line with several
// time tags (a repeated chorus) appears once per tag. Untimed text, ID
// tags and word timings are dropped; [offset:] is applied.
func ParseLRC(lrc string) []provider.LyricLine {
	var lines []provider.LyricLine
	var offset time.Duration
	for raw := range strings.Lines(lrc) {
		raw = strings.TrimSpace(raw)
		if m := metaTag.FindStringSubmatch(raw); m != nil {
			if strings.EqualFold(m[1], "offset") {
				if ms, err := strconv.Atoi(strings.TrimSpace(m[2])); err == nil {
					offset = time.Duration(ms) * time.Millisecond
				}
			}
			continue
		}
		var ats []time.Duration
		for {
			m := timeTag.FindStringSubmatch(raw)
			if m == nil {
				break
			}
			ats = append(ats, tagTime(m[1], m[2], m[3]))
			raw = raw[len(m[0]):]
		}
		text := strings.TrimSpace(wordTag.ReplaceAllString(raw, ""))
		for _, at := range ats {
			lines = append(lines, provider.LyricLine{At: at, Text: text})
		}
	}
	// A positive offset shows lines sooner.
	for i := range lines {
		lines[i].At = max(lines[i].At-offset, 0)
	}
	slices.SortStableFunc(lines, func(a, b provider.LyricLine) int { return cmp.Compare(a.At, b.At) })
	return lines
}

func tagTime(mins, secs, frac string) time.Duration {
	m, _ := strconv.Atoi(mins)
	s, _ := strconv.Atoi(secs)
	d := time.Duration(m)*time.Minute + time.Duration(s)*time.Second
	if frac != "" {
		// Hundredths usually, but some files use tenths or thousandths.
		f, _ := strconv.Atoi(frac)
		for range 3 - len(frac) {
			f *= 10
		}
		d += time.Duration(f) * time.Millisecond
	}
	return d
}
