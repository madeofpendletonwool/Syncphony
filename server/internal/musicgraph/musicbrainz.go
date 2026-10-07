// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// MusicBrainzLookup is what the MusicBrainz source asks.
// musicbrainz.Service is one.
type MusicBrainzLookup interface {
	ArtistTags(ctx context.Context, mbid string) ([]musicbrainz.Tag, error)
	FirstReleased(ctx context.Context, recordingMBID string) (string, error)
}

// genreBoost is how much more a curated genre counts than a free tag.
const genreBoost = 1.5

// MusicBrainz asks MusicBrainz for an artist's genres and tags, and when a
// song first came out. It only answers about artists and songs with an
// MBID, and shares MusicBrainz's one request a second with the rest of the
// server.
type MusicBrainz struct {
	mb MusicBrainzLookup
}

// NewMusicBrainz returns a MusicBrainz.
func NewMusicBrainz(mb MusicBrainzLookup) *MusicBrainz { return &MusicBrainz{mb: mb} }

// Name implements Source.
func (*MusicBrainz) Name() string { return "musicbrainz" }

// Artist implements Source with the artist's genres and tags.
func (m *MusicBrainz) Artist(ctx context.Context, a ArtistRef) (Artist, error) {
	if a.MBID == "" {
		return Artist{}, provider.ErrUnsupported
	}
	tags, err := m.mb.ArtistTags(ctx, a.MBID)
	if err != nil {
		return Artist{}, err
	}
	most := 0.0
	for _, t := range tags {
		most = max(most, m.count(t))
	}
	out := Artist{Ref: a}
	for _, t := range tags {
		if most > 0 {
			out.Tags = append(out.Tags, Tag{Name: t.Name, Weight: min(m.count(t)/most, 1)})
		}
	}
	if len(out.Tags) == 0 {
		return Artist{}, fmt.Errorf("musicbrainz: artist %s has no tags: %w", a.MBID, provider.ErrNotFound)
	}
	return out, nil
}

func (*MusicBrainz) count(t musicbrainz.Tag) float64 {
	if t.Genre {
		return float64(t.Count) * genreBoost
	}
	return float64(t.Count)
}

// Track implements Source with the year the recording first came out.
func (m *MusicBrainz) Track(ctx context.Context, s SongRef) (Track, error) {
	if s.MBID == "" {
		return Track{}, provider.ErrUnsupported
	}
	date, err := m.mb.FirstReleased(ctx, s.MBID)
	if err != nil {
		return Track{}, err
	}
	year := 0
	if len(date) >= 4 {
		year, _ = strconv.Atoi(date[:4])
	}
	if year == 0 {
		return Track{}, fmt.Errorf("musicbrainz: recording %s has no date: %w", s.MBID, provider.ErrNotFound)
	}
	return Track{Ref: s, Year: year}, nil
}

var _ Source = (*MusicBrainz)(nil)

func isNotFound(err error) bool { return errors.Is(err, provider.ErrNotFound) }

func trimSlash(s string) string { return strings.TrimRight(s, "/") }
