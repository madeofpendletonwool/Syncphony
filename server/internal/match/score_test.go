// SPDX-License-Identifier: AGPL-3.0-only

package match

import (
	"testing"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

func tr(title string, secs int, artists ...string) provider.Track {
	t := provider.Track{Title: title, Duration: time.Duration(secs) * time.Second}
	for _, a := range artists {
		t.Artists = append(t.Artists, provider.ArtistCredit{Name: a})
	}
	return t
}

func TestScore(t *testing.T) {
	for _, tc := range []struct {
		name      string
		want, got provider.Track
		ok        bool
	}{
		{"identical", tr("Heroes", 371, "David Bowie"), tr("Heroes", 371, "David Bowie"), true},
		{"remaster note", tr("Heroes", 371, "David Bowie"), tr("Heroes - 2017 Remaster", 372, "David Bowie"), true},
		{"bracketed remaster", tr("Heroes", 371, "David Bowie"), tr("Heroes (Remastered 2017)", 371, "David Bowie"), true},
		{"accents and punctuation", tr("Déjà Vu!", 200, "Beyoncé"), tr("deja vu", 201, "Beyonce"), true},
		{"featured artist in the title", tr("Umbrella (feat. JAY-Z)", 275, "Rihanna"), tr("Umbrella", 276, "Rihanna", "JAY-Z"), true},
		{"joint credit", tr("Under Pressure", 248, "Queen & David Bowie"), tr("Under Pressure", 248, "David Bowie"), true},
		{"the", tr("Yellow Submarine", 160, "The Beatles"), tr("Yellow Submarine", 160, "Beatles"), true},
		{"with in a title is the title", tr("Dancing with Myself", 200, "Billy Idol"), tr("Dancing", 200, "Billy Idol"), false},
		{"live is another recording", tr("Heroes", 371, "David Bowie"), tr("Heroes (Live)", 380, "David Bowie"), false},
		{"remix is another recording", tr("Song", 200, "A"), tr("Song - Remix", 200, "A"), false},
		{"both live", tr("Song (Live)", 200, "A"), tr("Song - Live at Wembley", 202, "A"), true},
		{"different artist", tr("Hurt", 218, "Nine Inch Nails"), tr("Hurt", 218, "Johnny Cash"), false},
		{"too long", tr("Song", 200, "A"), tr("Song", 260, "A"), false},
		{"unknown duration", tr("Song", 0, "A"), tr("Song", 260, "A"), true},
		{"different title", tr("Heroes", 371, "David Bowie"), tr("Changes", 371, "David Bowie"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := Score(tc.want, tc.got); ok != tc.ok {
				t.Errorf("Score(%q, %q) ok = %v", tc.want.Title, tc.got.Title, ok)
			}
		})
	}
}

func TestScoreISRC(t *testing.T) {
	want := tr("Heroes", 371, "David Bowie")
	want.ISRC = "GBAYE7700005"
	got := tr("“Heroes” – 2017 Remaster", 371, "Bowie")
	got.ISRC = "gbaye7700005"
	if s, ok := Score(want, got); !ok || s != 1 {
		t.Errorf("same ISRC: %v %v", s, ok)
	}
	exact, _ := Score(want, tr("Heroes", 371, "David Bowie"))
	fuzzy, _ := Score(want, tr("Heroes", 378, "David Bowie"))
	if !(exact < 1 && fuzzy < exact) {
		t.Errorf("scores: exact title %v, off by 7s %v", exact, fuzzy)
	}
}

func TestSimplify(t *testing.T) {
	for in, want := range map[string]string{
		"Don't Stop Me Now": "dont stop me now",
		"Simon & Garfunkel": "simon and garfunkel",
		"  AC/DC  ":         "ac dc",
		"Sigur Rós":         "sigur ros",
	} {
		if got := Simplify(in); got != want {
			t.Errorf("Simplify(%q) = %q, want %q", in, got, want)
		}
	}
}
