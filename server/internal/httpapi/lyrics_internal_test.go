// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"strings"
	"testing"
)

func TestHideLine(t *testing.T) {
	l := Lyrics{
		Plain: "We can be heroes\nJust for one day\nWe can be heroes",
		Lines: []LyricLine{{AtMs: 0, Text: "We can be heroes"}, {AtMs: 4000, Text: "Just for one day"}, {AtMs: 8000, Text: "We can be heroes "}},
	}
	got := hideLine(l, 8000)
	// Every time the line comes back, not just the one asked about.
	if got.Lines[0].Text != hiddenLine || got.Lines[1].Text != "Just for one day" || got.Lines[2].Text != hiddenLine {
		t.Errorf("lines %+v", got.Lines)
	}
	if strings.Contains(got.Plain, "heroes") || !strings.Contains(got.Plain, "one day") {
		t.Errorf("plain %q", got.Plain)
	}
	if same := hideLine(l, 1234); same.Lines[0].Text != "We can be heroes" {
		t.Error("hid a line nobody asked about")
	}
}
