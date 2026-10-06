// SPDX-License-Identifier: AGPL-3.0-only

package lyrics_test

import (
	"slices"
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/lyrics"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

func TestParseLRC(t *testing.T) {
	const lrc = "[ar:The Square Roots]\r\n" +
		"[ti:Rolloff]\n" +
		"[offset:+500]\n" +
		"not timed, dropped\n" +
		"[00:01.00]First\n" +
		"[00:10.5][00:30.250] Chorus <00:10.70>with <00:11.00>words\n" +
		"[00:20] Middle\n" +
		"[01:02:30]Late\n" +
		"[00:00.20]Clamped by the offset\n" +
		"[00:40.00]\n"
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	want := []provider.LyricLine{
		{At: 0, Text: "Clamped by the offset"},
		{At: ms(500), Text: "First"},
		{At: ms(10000), Text: "Chorus with words"},
		{At: ms(19500), Text: "Middle"},
		{At: ms(29750), Text: "Chorus with words"},
		{At: ms(39500), Text: ""},
		{At: ms(61800), Text: "Late"},
	}
	if got := lyrics.ParseLRC(lrc); !slices.Equal(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if got := lyrics.ParseLRC("just words\nno times"); got != nil {
		t.Errorf("plain text: %+v", got)
	}
}
