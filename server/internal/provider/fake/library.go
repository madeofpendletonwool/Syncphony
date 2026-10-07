// SPDX-License-Identifier: AGPL-3.0-only

package fake

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// The library is fixed: three artists, two albums each, three tracks per
// album. Every track is a sine tone at its own pitch, so you can hear which
// one is playing.
var library = buildLibrary([]albumSpec{
	{"The Test Patterns", "Calibration", 2019, []string{"Reference Tone", "Left Channel", "Right Channel"}},
	{"The Test Patterns", "Colour Bars", 2021, []string{"SMPTE", "Pluge", "Ident"}},
	{"Null Island", "Zero Zero", 2018, []string{"Latitude", "Longitude", "Weather Buoy"}},
	{"Null Island", "Prime Meridian", 2022, []string{"Greenwich", "Equator", "Gulf of Guinea"}},
	{"Sine Language", "A440", 2020, []string{"Concert Pitch", "Tuning Fork", "Beat Frequency"}},
	{"Sine Language", "Overtones", 2023, []string{"Fundamental", "Second Harmonic", "Explicit Content"}},
})

type albumSpec struct {
	artist, title string
	year          int
	tracks        []string
}

type lib struct {
	artists []*artist
	albums  []*album
	tracks  []*track
	byID    map[string]any
}

type artist struct {
	id, name string
	albums   []*album
}

type album struct {
	id, title string
	year      int
	artist    *artist
	tracks    []*track
}

type track struct {
	id, title string
	album     *album
	duration  time.Duration
	isrc      string
	explicit  bool
	hz        float64
}

func buildLibrary(specs []albumSpec) *lib {
	l := &lib{byID: map[string]any{}}
	for _, s := range specs {
		var ar *artist
		if n := len(l.artists); n > 0 && l.artists[n-1].name == s.artist {
			ar = l.artists[n-1]
		} else {
			ar = &artist{id: fmt.Sprintf("ar%d", n+1), name: s.artist}
			l.artists = append(l.artists, ar)
			l.byID[ar.id] = ar
		}
		al := &album{id: fmt.Sprintf("al%d", len(l.albums)+1), title: s.title, year: s.year, artist: ar}
		ar.albums = append(ar.albums, al)
		l.albums = append(l.albums, al)
		l.byID[al.id] = al
		for _, title := range s.tracks {
			n := len(l.tracks)
			t := &track{
				id:       fmt.Sprintf("t%02d", n+1),
				title:    title,
				album:    al,
				duration: time.Duration(20+n%4*5) * time.Second,
				isrc:     fmt.Sprintf("XXFAK%07d", n+1),
				explicit: title == "Explicit Content",
				// A chromatic scale up from A3.
				hz: 220 * math.Pow(2, float64(n)/12),
			}
			al.tracks = append(al.tracks, t)
			l.tracks = append(l.tracks, t)
			l.byID[t.id] = t
		}
	}
	return l
}

func get[T any](l *lib, id string) (T, bool) {
	v, ok := l.byID[id].(T)
	return v, ok
}

// playlists are fixed too: every third track, and everything.
var playlists = []playlist{
	{"p1", "Fake Favourites", func() []*track {
		var ts []*track
		for i, t := range library.tracks {
			if i%3 == 0 {
				ts = append(ts, t)
			}
		}
		return ts
	}},
	{"p2", "Everything", func() []*track { return library.tracks }},
}

// publicPlaylists are other people's, as Spotify's public playlists are:
// searching finds them, but the account's own list doesn't hold them.
var publicPlaylists = []playlist{
	{"p3", "Null Island Radio", func() []*track {
		return slices.DeleteFunc(slices.Clone(library.tracks), func(t *track) bool { return t.album.artist.name != "Null Island" })
	}},
}

type playlist struct {
	id, name string
	tracks   func() []*track
}
