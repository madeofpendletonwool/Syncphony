// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// ListenBrainz's APIs.
const (
	DefaultListenBrainzURL     = "https://api.listenbrainz.org"
	DefaultListenBrainzLabsURL = "https://labs.api.listenbrainz.org"
)

// listenBrainzAlgorithm is the similar-artists dataset LB Radio uses:
// artists listened to in the same sessions, over about 20 years of
// listens.
const listenBrainzAlgorithm = "session_based_days_7500_session_300_contribution_5_threshold_10_limit_100_filter_True_skip_30"

// ListenBrainz asks ListenBrainz: artists listened to alongside an
// artist, and an artist's songs by listens. Everything is by MusicBrainz
// ID, so it only answers about artists with one. No account is needed;
// a token (from https://listenbrainz.org/settings/) is sent if set, as
// some endpoints ask for one.
type ListenBrainz struct {
	base, labs string
	c          *client
}

// ListenBrainzOptions configure a ListenBrainz.
type ListenBrainzOptions struct {
	UserAgent string
	// Token is a ListenBrainz user token. Optional.
	Token string
	// BaseURL and LabsURL are the APIs. Default DefaultListenBrainzURL
	// and DefaultListenBrainzLabsURL.
	BaseURL, LabsURL string
	// Client makes the requests. Default http.DefaultClient.
	Client *http.Client
	// Interval is the least time between requests. Default 250ms;
	// negative means none, for tests.
	Interval time.Duration
}

// NewListenBrainz returns a ListenBrainz.
func NewListenBrainz(opts ListenBrainzOptions) *ListenBrainz {
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultListenBrainzURL
	}
	if opts.LabsURL == "" {
		opts.LabsURL = DefaultListenBrainzLabsURL
	}
	if opts.Interval == 0 {
		opts.Interval = 250 * time.Millisecond
	}
	c := newClient("listenbrainz", opts.UserAgent, opts.Client, opts.Interval)
	if opts.Token != "" {
		c.header.Set("Authorization", "Token "+opts.Token)
	}
	return &ListenBrainz{base: trimSlash(opts.BaseURL), labs: trimSlash(opts.LabsURL), c: c}
}

// Name implements Source.
func (*ListenBrainz) Name() string { return "listenbrainz" }

// Artist implements Source with the similar-artists dataset and
// popularity/top-recordings-for-artist.
func (l *ListenBrainz) Artist(ctx context.Context, a ArtistRef) (Artist, error) {
	if a.MBID == "" {
		return Artist{}, provider.ErrUnsupported
	}
	out := Artist{Ref: a}
	var sim []struct {
		MBID  string  `json:"artist_mbid"`
		Name  string  `json:"name"`
		Score float64 `json:"score"`
	}
	err := l.c.getJSON(ctx, l.labs+"/similar-artists/json", url.Values{"artist_mbids": {a.MBID}, "algorithm": {listenBrainzAlgorithm}}, &sim)
	if err != nil {
		return Artist{}, err
	}
	most := 0.0
	for _, s := range sim {
		most = max(most, s.Score)
	}
	for _, s := range sim {
		if s.MBID != a.MBID && most > 0 {
			out.Similar = append(out.Similar, Similar{Artist: ArtistRef{Name: s.Name, MBID: s.MBID}, Score: s.Score / most})
		}
	}

	top, err := l.topRecordings(ctx, a.MBID)
	if err != nil {
		return Artist{}, err
	}
	most = 0
	for _, r := range top {
		most = max(most, r.Listens)
	}
	for _, r := range top {
		out.Top = append(out.Top, Song{SongRef: SongRef{Title: r.Name, MBID: r.MBID, Artist: a}, Score: share(r.Listens, most)})
	}
	if len(out.Similar)+len(out.Top) == 0 {
		return Artist{}, fmt.Errorf("listenbrainz: artist %s: %w", a.MBID, provider.ErrNotFound)
	}
	return out, nil
}

type lbRecording struct {
	MBID    string  `json:"recording_mbid"`
	Name    string  `json:"recording_name"`
	Listens float64 `json:"total_listen_count"`
}

// topRecordings reads an artist's most listened songs. The endpoint sends
// all of them (thousands, for a big artist) and takes no limit without a
// token, so it reads the first maxTop and stops.
func (l *ListenBrainz) topRecordings(ctx context.Context, mbid string) ([]lbRecording, error) {
	var out []lbRecording
	err := l.c.get(ctx, l.base+"/1/popularity/top-recordings-for-artist/"+url.PathEscape(mbid), nil, func(status int, body io.Reader) error {
		switch status {
		case http.StatusOK:
		case http.StatusNotFound:
			return fmt.Errorf("listenbrainz: %w", provider.ErrNotFound)
		default:
			return fmt.Errorf("listenbrainz: top recordings: HTTP %d: %w", status, provider.ErrUnavailable)
		}
		d := json.NewDecoder(body)
		if tok, err := d.Token(); err != nil || tok != json.Delim('[') {
			return fmt.Errorf("listenbrainz: top recordings: not a list: %w", provider.ErrUnavailable)
		}
		for len(out) < maxTop && d.More() {
			var r lbRecording
			if err := d.Decode(&r); err != nil {
				return fmt.Errorf("listenbrainz: top recordings: %w: %w", provider.ErrUnavailable, err)
			}
			if r.Name != "" {
				out = append(out, r)
			}
		}
		return nil
	})
	return out, err
}

// Track implements Source. ListenBrainz isn't asked about songs yet:
// Last.fm gives similar songs, Deezer and MusicBrainz the rest.
func (*ListenBrainz) Track(context.Context, SongRef) (Track, error) {
	return Track{}, provider.ErrUnsupported
}

var _ Source = (*ListenBrainz)(nil)
