// SPDX-License-Identifier: AGPL-3.0-only

package musicbrainz

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Details are what MusicBrainz knows about a resolved track beyond its
// IDs: who made it, where it came out, and how it relates to other
// recordings. Liner notes are built from them.
type Details struct {
	Title string
	// FirstReleased is the recording's earliest release date: YYYY,
	// YYYY-MM or YYYY-MM-DD. Empty if unknown.
	FirstReleased string
	// Credits are the recording's own: producers, engineers, performers.
	Credits []Credit
	// Works are the compositions it's a recording of, with their writers.
	Works []Work
	// Samples are recordings it samples; SampledBy, those that sample it.
	Samples, SampledBy []RecordingRef
	// Release is the release the track was matched to. Nil if none.
	Release *Release
	// Artist is the track's first credited artist. Nil if unknown.
	Artist *Artist
}

// Credit is an artist's part in a recording or work.
type Credit struct {
	// Type is the MusicBrainz relationship type: "producer", "mix",
	// "engineer", "vocal", "instrument", "composer", "lyricist", "writer"...
	Type string
	// Attributes refine it: the instrument ("guitar"), "lead vocals".
	Attributes []string
	Name       string
	ArtistMBID string
}

// Work is a composition a recording performs.
type Work struct {
	Title string
	// Attributes of the performance: "cover", "live", "instrumental", "partial".
	Attributes []string
	Writers    []Credit
}

// RecordingRef names another recording.
type RecordingRef struct {
	MBID   string
	Title  string
	Artist string
}

// Release is where a track came out.
type Release struct {
	Title   string
	Date    string
	Country string
	Labels  []string
	// Type is the release group's: "Album", "Single", "EP"...
	Type string
	// FirstReleased is the release group's first release date.
	FirstReleased string
}

// Artist is a track's artist.
type Artist struct {
	MBID string
	Name string
	// Type is "Person", "Group", "Orchestra", "Choir"...
	Type           string
	Disambiguation string
	Country        string
	// Area is where they're from; BeginArea where they were born or formed.
	Area, BeginArea string
	// Begin and End are dates, as precise as known.
	Begin, End string
	Ended      bool
	// WikidataID is the artist's Wikidata item ("Q11649"), and
	// WikipediaURL a Wikipedia article MusicBrainz links directly.
	WikidataID   string
	WikipediaURL string
}

// Details looks up what MusicBrainz knows about resolved IDs: up to three
// requests, at its rate limit. Parts that can't be found are left out; it
// fails only if the recording can't be read.
func (s *Service) Details(ctx context.Context, ids IDs) (Details, error) {
	if ids.Recording == "" {
		return Details{}, fmt.Errorf("musicbrainz details: no recording: %w", provider.ErrNotFound)
	}
	var r recordingDetails
	err := s.mb.get(ctx, "recording/"+url.PathEscape(ids.Recording),
		url.Values{"inc": {"artist-credits+artist-rels+work-rels+work-level-rels+recording-rels"}}, &r)
	if errors.Is(err, errNotFound) {
		return Details{}, fmt.Errorf("musicbrainz recording %s: %w", ids.Recording, provider.ErrNotFound)
	}
	if err != nil {
		return Details{}, err
	}
	d := r.details()

	if ids.Release != "" {
		var rel releaseDetails
		switch err := s.mb.get(ctx, "release/"+url.PathEscape(ids.Release), url.Values{"inc": {"labels+release-groups"}}, &rel); {
		case err == nil:
			d.Release = rel.release()
		case !errors.Is(err, errNotFound):
			return d, err
		}
	}
	if ids.Artist != "" {
		var a artistDetails
		switch err := s.mb.get(ctx, "artist/"+url.PathEscape(ids.Artist), url.Values{"inc": {"url-rels"}}, &a); {
		case err == nil:
			d.Artist = a.artist()
		case !errors.Is(err, errNotFound):
			return d, err
		}
	}
	return d, nil
}

// The JSON shapes Details reads.

type relation struct {
	Type         string   `json:"type"`
	TargetType   string   `json:"target-type"`
	Direction    string   `json:"direction"`
	Attributes   []string `json:"attributes"`
	TargetCredit string   `json:"target-credit"`
	Artist       *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
	Work *struct {
		Title     string     `json:"title"`
		Relations []relation `json:"relations"`
	} `json:"work"`
	Recording *struct {
		ID           string         `json:"id"`
		Title        string         `json:"title"`
		ArtistCredit []artistCredit `json:"artist-credit"`
	} `json:"recording"`
	URL *struct {
		Resource string `json:"resource"`
	} `json:"url"`
}

func (r relation) credit() Credit {
	return Credit{Type: r.Type, Attributes: r.Attributes, Name: cmp.Or(r.TargetCredit, r.Artist.Name), ArtistMBID: r.Artist.ID}
}

type recordingDetails struct {
	Title         string     `json:"title"`
	FirstReleased string     `json:"first-release-date"`
	Relations     []relation `json:"relations"`
}

func (r recordingDetails) details() Details {
	d := Details{Title: r.Title, FirstReleased: r.FirstReleased}
	for _, rel := range r.Relations {
		switch {
		case rel.TargetType == "artist" && rel.Artist != nil:
			d.Credits = append(d.Credits, rel.credit())
		case rel.TargetType == "work" && rel.Work != nil && rel.Type == "performance":
			w := Work{Title: rel.Work.Title, Attributes: rel.Attributes}
			for _, wr := range rel.Work.Relations {
				if wr.TargetType == "artist" && wr.Artist != nil {
					w.Writers = append(w.Writers, wr.credit())
				}
			}
			d.Works = append(d.Works, w)
		case rel.TargetType == "recording" && rel.Recording != nil && rel.Type == "samples material":
			ref := RecordingRef{MBID: rel.Recording.ID, Title: rel.Recording.Title, Artist: creditName(rel.Recording.ArtistCredit)}
			// Forward: this recording samples the other one.
			if rel.Direction == "backward" {
				d.SampledBy = append(d.SampledBy, ref)
			} else {
				d.Samples = append(d.Samples, ref)
			}
		}
	}
	return d
}

// creditName joins an artist credit the way MusicBrainz shows it.
func creditName(acs []artistCredit) string {
	var b strings.Builder
	for _, ac := range acs {
		b.WriteString(cmp.Or(ac.Name, ac.Artist.Name))
		b.WriteString(ac.JoinPhrase)
	}
	return strings.TrimSpace(b.String())
}

type releaseDetails struct {
	Title     string `json:"title"`
	Date      string `json:"date"`
	Country   string `json:"country"`
	LabelInfo []struct {
		Label *struct {
			Name string `json:"name"`
		} `json:"label"`
	} `json:"label-info"`
	ReleaseGroup struct {
		PrimaryType   string `json:"primary-type"`
		FirstReleased string `json:"first-release-date"`
	} `json:"release-group"`
}

func (r releaseDetails) release() *Release {
	out := &Release{Title: r.Title, Date: r.Date, Country: r.Country, Type: r.ReleaseGroup.PrimaryType, FirstReleased: r.ReleaseGroup.FirstReleased}
	for _, li := range r.LabelInfo {
		// "[no label]" is MusicBrainz's way of saying self-released.
		if li.Label != nil && li.Label.Name != "" && li.Label.Name != "[no label]" && !slices.Contains(out.Labels, li.Label.Name) {
			out.Labels = append(out.Labels, li.Label.Name)
		}
	}
	return out
}

type area struct {
	Name string `json:"name"`
}

type artistDetails struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Disambiguation string `json:"disambiguation"`
	Country        string `json:"country"`
	Area           *area  `json:"area"`
	BeginArea      *area  `json:"begin-area"`
	LifeSpan       struct {
		Begin string `json:"begin"`
		End   string `json:"end"`
		Ended bool   `json:"ended"`
	} `json:"life-span"`
	Relations []relation `json:"relations"`
}

func (a artistDetails) artist() *Artist {
	out := &Artist{
		MBID: a.ID, Name: a.Name, Type: a.Type, Disambiguation: a.Disambiguation, Country: a.Country,
		Begin: a.LifeSpan.Begin, End: a.LifeSpan.End, Ended: a.LifeSpan.Ended,
	}
	if a.Area != nil {
		out.Area = a.Area.Name
	}
	if a.BeginArea != nil {
		out.BeginArea = a.BeginArea.Name
	}
	for _, r := range a.Relations {
		if r.URL == nil {
			continue
		}
		switch r.Type {
		case "wikidata":
			if i := strings.LastIndex(r.URL.Resource, "/"); i >= 0 && out.WikidataID == "" {
				out.WikidataID = r.URL.Resource[i+1:]
			}
		case "wikipedia":
			if out.WikipediaURL == "" {
				out.WikipediaURL = r.URL.Resource
			}
		}
	}
	return out
}
