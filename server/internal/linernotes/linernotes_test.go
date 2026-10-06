// SPDX-License-Identifier: AGPL-3.0-only

package linernotes

import (
	"strings"
	"testing"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
)

func TestWrite(t *testing.T) {
	d := musicbrainz.Details{
		Title: "Song", FirstReleased: "1971-05-01",
		Credits: []musicbrainz.Credit{
			{Type: "mix", Name: "Mixer"},
			{Type: "producer", Name: "Prod A"},
			{Type: "producer", Name: "Prod A"}, // listed twice on MusicBrainz
			{Type: "vocal", Attributes: []string{"lead vocals"}, Name: "Singer"},
			{Type: "instrument", Attributes: []string{"bass guitar"}, Name: "Bassist"},
			{Type: "instrument", Name: "Unknown instrument"},
			{Type: "photography", Name: "Not a credit we show"},
		},
		Works: []musicbrainz.Work{{Title: "Song", Attributes: []string{"live"}, Writers: []musicbrainz.Credit{
			{Type: "composer", Name: "Composer"}, {Type: "lyricist", Name: "Lyricist"},
		}}},
		Samples: []musicbrainz.RecordingRef{{Title: "Break", Artist: "Drummer"}},
		Release: &musicbrainz.Release{Title: "Greatest Hits", Date: "1999-01-01", Type: "Album", FirstReleased: "1999"},
		Artist:  &musicbrainz.Artist{MBID: "a", Name: "Singer", Type: "Person", BeginArea: "Leeds", Begin: "1950-02-03", Disambiguation: "UK singer"},
	}
	n := write(musicbrainz.IDs{Recording: "r"}, d)
	if n.Year != 1971 || n.Release.Title != "Greatest Hits" || n.Artist.About != "Person · UK singer" {
		t.Fatalf("notes: %+v %+v %+v", n, n.Release, n.Artist)
	}
	var credits []string
	for _, c := range n.Credits {
		credits = append(credits, c.Role+"="+strings.Join(c.Names, ","))
	}
	if got, want := strings.Join(credits, "; "), "Music by=Composer; Lyrics by=Lyricist; Produced by=Prod A; Vocals=Singer; Bass guitar=Bassist; Mixed by=Mixer"; got != want {
		t.Errorf("credits:\n got %s\nwant %s", got, want)
	}
	var facts []string
	for _, f := range n.Facts {
		facts = append(facts, f.Kind+"="+f.Text)
	}
	want := "live=A live recording of “Song”; samples=Samples “Break” by Drummer; first_released=First released in 1971; origin=Singer born in Leeds in 1950"
	if got := strings.Join(facts, "; "); got != want {
		t.Errorf("facts:\n got %s\nwant %s", got, want)
	}
}

func TestWritersTogether(t *testing.T) {
	d := musicbrainz.Details{Works: []musicbrainz.Work{{Writers: []musicbrainz.Credit{
		{Type: "composer", Name: "B"}, {Type: "lyricist", Name: "B"}, {Type: "composer", Name: "A"}, {Type: "lyricist", Name: "A"},
	}}}}
	c := credits(d)
	if len(c) != 1 || c[0].Role != "Written by" || strings.Join(c[0].Names, ",") != "A,B" {
		t.Fatalf("credits: %+v", c)
	}
}

func TestClip(t *testing.T) {
	s := "First sentence is here. Second sentence is longer than that. Third."
	if got := clip(s, 40); got != "First sentence is here." {
		t.Errorf("at a sentence: %q", got)
	}
	if got := clip("one two three four five", 12); got != "one two…" {
		t.Errorf("at a word: %q", got)
	}
	if got := clip("short", 40); got != "short" {
		t.Errorf("short: %q", got)
	}
}

func TestSiteKey(t *testing.T) {
	for base, want := range map[string]string{
		"https://en.wikipedia.org":     "enwiki",
		"https://de.wikipedia.org/":    "dewiki",
		"https://zh-yue.wikipedia.org": "zh_yuewiki",
		"http://127.0.0.1:8080":        "enwiki",
	} {
		if got := siteKey(base); got != want {
			t.Errorf("siteKey(%q) = %q, want %q", base, got, want)
		}
	}
}
