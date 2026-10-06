// SPDX-License-Identifier: AGPL-3.0-only

package navidrome

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/artcache"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/transcode"
)

type session struct {
	p    *Provider
	link provider.Link
	api  *api
}

var (
	_ provider.Session  = (*session)(nil)
	_ provider.Streamer = (*session)(nil)
	_ provider.Lyricist = (*session)(nil)
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 500
	// defaultArtworkSize is used when the caller has no preference.
	// Originals can be several megabytes.
	defaultArtworkSize = 600
	// maxArtworkSize is the API's own limit, for big screens. Navidrome
	// doesn't scale images up, so asking for more than the original is safe.
	maxArtworkSize = 2048
)

func (s *session) Search(ctx context.Context, q provider.SearchQuery) (provider.SearchPage, error) {
	kinds := q.Kinds
	if len(kinds) == 0 {
		kinds = s.p.Info().Capabilities.Search
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	limit = min(limit, maxSearchLimit)
	offset := 0
	if q.Cursor != "" {
		n, err := strconv.Atoi(q.Cursor)
		if err != nil || n < 0 {
			return provider.SearchPage{}, fmt.Errorf("navidrome: bad search cursor %q", q.Cursor)
		}
		offset = n
	}
	// Ask for one extra of each kind to learn whether another page follows.
	params := url.Values{"query": {q.Text}, "songCount": {"0"}, "albumCount": {"0"}, "artistCount": {"0"}}
	for _, k := range kinds {
		var prefix string
		switch k {
		case provider.KindTrack:
			prefix = "song"
		case provider.KindAlbum:
			prefix = "album"
		case provider.KindArtist:
			prefix = "artist"
		default:
			return provider.SearchPage{}, fmt.Errorf("navidrome: search %q: %w", k, provider.ErrUnsupported)
		}
		params.Set(prefix+"Count", strconv.Itoa(limit+1))
		params.Set(prefix+"Offset", strconv.Itoa(offset))
	}
	r, err := s.api.call(ctx, "search3", params)
	if err != nil {
		return provider.SearchPage{}, err
	}
	var page provider.SearchPage
	if r.SearchResult3 == nil {
		return page, nil
	}
	res := r.SearchResult3
	more := len(res.Song) > limit || len(res.Album) > limit || len(res.Artist) > limit
	for _, so := range res.Song[:min(len(res.Song), limit)] {
		page.Tracks = append(page.Tracks, s.track(so))
	}
	for _, al := range res.Album[:min(len(res.Album), limit)] {
		page.Albums = append(page.Albums, toAlbum(al))
	}
	for _, ar := range res.Artist[:min(len(res.Artist), limit)] {
		page.Artists = append(page.Artists, toArtist(ar))
	}
	if more {
		page.Next = strconv.Itoa(offset + limit)
	}
	return page, nil
}

func (s *session) Track(ctx context.Context, id string) (provider.Track, error) {
	so, err := s.song(ctx, id)
	if err != nil {
		return provider.Track{}, err
	}
	return s.track(*so), nil
}

func (s *session) song(ctx context.Context, id string) (*song, error) {
	r, err := s.api.call(ctx, "getSong", url.Values{"id": {id}})
	if err != nil {
		return nil, err
	}
	if r.Song == nil {
		return nil, fmt.Errorf("navidrome getSong %q: %w", id, provider.ErrNotFound)
	}
	return r.Song, nil
}

func (s *session) Album(ctx context.Context, id string) (provider.Album, []provider.Track, error) {
	r, err := s.api.call(ctx, "getAlbum", url.Values{"id": {id}})
	if err != nil {
		return provider.Album{}, nil, err
	}
	if r.Album == nil {
		return provider.Album{}, nil, fmt.Errorf("navidrome getAlbum %q: %w", id, provider.ErrNotFound)
	}
	ts := make([]provider.Track, len(r.Album.Song))
	for i, so := range r.Album.Song {
		ts[i] = s.track(so)
	}
	return toAlbum(*r.Album), ts, nil
}

func (s *session) Artist(ctx context.Context, id string) (provider.Artist, []provider.Album, error) {
	r, err := s.api.call(ctx, "getArtist", url.Values{"id": {id}})
	if err != nil {
		return provider.Artist{}, nil, err
	}
	if r.Artist == nil {
		return provider.Artist{}, nil, fmt.Errorf("navidrome getArtist %q: %w", id, provider.ErrNotFound)
	}
	as := make([]provider.Album, len(r.Artist.Album))
	for i, al := range r.Artist.Album {
		as[i] = toAlbum(al)
	}
	return toArtist(*r.Artist), as, nil
}

func (s *session) track(so song) provider.Track {
	t := provider.Track{
		Ref:      provider.TrackRef{Provider: ID, LinkID: s.link.ID, ID: so.ID},
		Title:    so.Title,
		Artists:  credits(so.Artists, so.ArtistID, so.Artist),
		Album:    provider.AlbumCredit{ID: so.AlbumID, Title: so.Album},
		Duration: time.Duration(so.Duration) * time.Second,
		Explicit: so.ExplicitStatus == "explicit",
		MBID:     so.MusicBrainzID,
		Artwork:  provider.ArtworkRef(so.CoverArt),
	}
	if len(so.ISRC) > 0 {
		t.ISRC = so.ISRC[0]
	}
	return t
}

func toAlbum(al album) provider.Album {
	title := al.Name
	if title == "" {
		title = al.Title
	}
	return provider.Album{
		ID:         al.ID,
		Title:      title,
		Artists:    credits(al.Artists, al.ArtistID, al.Artist),
		Year:       al.Year,
		TrackCount: al.SongCount,
		Artwork:    provider.ArtworkRef(al.CoverArt),
	}
}

func toArtist(ar artist) provider.Artist {
	art := ar.CoverArt
	if art == "" && ar.ID != "" {
		// Navidrome leaves coverArt off artists but serves their images
		// (from the folder, or fetched from Last.fm and the like) by ID.
		art = "ar-" + ar.ID
	}
	return provider.Artist{ID: ar.ID, Name: ar.Name, Artwork: provider.ArtworkRef(art)}
}

// credits prefers OpenSubsonic's list of artists, falling back to the
// single (often joined, e.g. "A feat. B") artist of plain Subsonic.
func credits(list []credit, id, name string) []provider.ArtistCredit {
	var out []provider.ArtistCredit
	for _, c := range list {
		if c.Name != "" {
			out = append(out, provider.ArtistCredit{ID: c.ID, Name: c.Name})
		}
	}
	if len(out) == 0 && name != "" {
		out = append(out, provider.ArtistCredit{ID: id, Name: name})
	}
	return out
}

// Artwork fetches cover art through getCoverArt. Images are cached per
// account, since a room shows the same few covers over and over.
func (s *session) Artwork(ctx context.Context, ref provider.ArtworkRef, size int) (io.ReadCloser, string, error) {
	if ref == "" {
		return nil, "", fmt.Errorf("navidrome: empty artwork ref: %w", provider.ErrNotFound)
	}
	if size <= 0 {
		size = defaultArtworkSize
	}
	size = min(size, maxArtworkSize)
	key := artKey{account: s.link.Account.ID, ref: string(ref), size: size}
	if img, ok := s.p.art.Get(key); ok {
		return io.NopCloser(bytes.NewReader(img.Data)), img.ContentType, nil
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := s.api.request(ctx, "getCoverArt", url.Values{"id": {string(ref)}, "size": {strconv.Itoa(size)}})
	if err != nil {
		return nil, "", err
	}
	resp, err := s.api.do(req, "getCoverArt")
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(mediaType(ct), "image/") {
		if err := decodeError(resp.Body, "getCoverArt"); err != nil {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("navidrome getCoverArt: unexpected content type %q: %w", ct, provider.ErrUnavailable)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtwork+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, "", ctxErr
		}
		return nil, "", fmt.Errorf("navidrome getCoverArt: %w: %w", provider.ErrUnavailable, err)
	}
	if len(data) > maxArtwork {
		return nil, "", fmt.Errorf("navidrome getCoverArt: image over %d bytes", maxArtwork)
	}
	s.p.art.Put(key, artcache.Image{Data: data, ContentType: ct})
	return io.NopCloser(bytes.NewReader(data)), ct, nil
}

// Formats Navidrome transcodes to out of the box, in order of preference,
// with the content type it serves each as.
var targets = []struct{ format, contentType string }{
	{"mp3", "audio/mpeg"},
	{"aac", "audio/aac"},
	{"opus", "audio/ogg"},
}

// Stream streams a track through the stream endpoint. The original file is
// passed through when the player accepts its format and it's within the
// bitrate cap; otherwise Navidrome transcodes to a format the player
// accepts. Range requests are passed through, and are honored for original
// files (and transcodes Navidrome has cached).
func (s *session) Stream(ctx context.Context, trackID string, opts provider.StreamOpts) (*provider.AudioStream, error) {
	params := url.Values{"id": {trackID}, "format": {"raw"}}
	if len(opts.Accept) > 0 || opts.MaxBitrate > 0 {
		so, err := s.song(ctx, trackID)
		if err != nil {
			return nil, err
		}
		fits := opts.MaxBitrate <= 0 || so.BitRate <= 0 || so.BitRate <= opts.MaxBitrate
		if !transcode.Accepts(opts.Accept, so.ContentType) || !fits {
			for _, t := range targets {
				if transcode.Accepts(opts.Accept, t.contentType) {
					kbps := transcode.DefaultBitrate
					if opts.MaxBitrate > 0 {
						kbps = min(kbps, opts.MaxBitrate)
					}
					params.Set("format", t.format)
					params.Set("maxBitRate", strconv.Itoa(kbps))
					break
				}
			}
			// If none is acceptable we send the original, and the server's
			// own transcoder can have a go at it.
		}
	}
	req, err := s.api.request(ctx, "stream", params)
	if err != nil {
		return nil, err
	}
	if r := opts.Range; r != nil {
		if r.Start < 0 || (r.End >= 0 && r.End < r.Start) {
			return nil, fmt.Errorf("navidrome stream: range %d-%d: %w", r.Start, r.End, provider.ErrRange)
		}
		end := ""
		if r.End >= 0 {
			end = strconv.FormatInt(r.End, 10)
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%s", r.Start, end))
	}
	resp, err := s.api.do(req, "stream")
	if err != nil {
		return nil, err
	}
	ct := resp.Header.Get("Content-Type")
	if isJSON(ct) || strings.HasPrefix(mediaType(ct), "text/") {
		defer resp.Body.Close()
		if err := decodeError(resp.Body, "stream"); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("navidrome stream: unexpected content type %q: %w", ct, provider.ErrUnavailable)
	}
	a := &provider.AudioStream{Body: resp.Body, ContentType: ct, Length: resp.ContentLength, Size: -1}
	if resp.StatusCode == http.StatusPartialContent {
		start, end, size, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok {
			resp.Body.Close()
			return nil, fmt.Errorf("navidrome stream: bad Content-Range %q: %w", resp.Header.Get("Content-Range"), provider.ErrUnavailable)
		}
		a.Offset, a.Length, a.Size, a.Seekable = start, end-start+1, size, true
		return a, nil
	}
	// A whole file: either no range was asked for, or it was ignored (a
	// live transcode), in which case the stream isn't seekable.
	a.Size = a.Length
	a.Seekable = opts.Range == nil && strings.Contains(resp.Header.Get("Accept-Ranges"), "bytes")
	return a, nil
}

// parseContentRange parses "bytes start-end/size", where size may be "*".
func parseContentRange(h string) (start, end, size int64, ok bool) {
	spec, found := strings.CutPrefix(h, "bytes ")
	if !found {
		return 0, 0, 0, false
	}
	rng, total, found := strings.Cut(spec, "/")
	if !found {
		return 0, 0, 0, false
	}
	first, last, found := strings.Cut(rng, "-")
	if !found {
		return 0, 0, 0, false
	}
	var err1, err2, err3 error
	start, err1 = strconv.ParseInt(first, 10, 64)
	end, err2 = strconv.ParseInt(last, 10, 64)
	size = -1
	if total != "*" {
		size, err3 = strconv.ParseInt(total, 10, 64)
	}
	if err1 != nil || err2 != nil || err3 != nil || start < 0 || end < start {
		return 0, 0, 0, false
	}
	return start, end, size, true
}

// Lyrics reads a song's lyrics with OpenSubsonic's getLyricsBySongId,
// which Navidrome answers from embedded tags and .lrc sidecar files.
// Servers without it get plain Subsonic's getLyrics, which has no timings.
func (s *session) Lyrics(ctx context.Context, trackID string) (provider.Lyrics, error) {
	r, err := s.api.call(ctx, "getLyricsBySongId", url.Values{"id": {trackID}})
	switch {
	case err == nil:
		return toLyrics(r.LyricsList, trackID)
	case ctx.Err() != nil, errors.Is(err, provider.ErrNotFound), errors.Is(err, provider.ErrAuthExpired),
		errors.Is(err, provider.ErrRateLimited):
		return provider.Lyrics{}, err
	}
	// Most likely not an OpenSubsonic server. getLyrics goes by artist and
	// title, not ID.
	so, err := s.song(ctx, trackID)
	if err != nil {
		return provider.Lyrics{}, err
	}
	r, err = s.api.call(ctx, "getLyrics", url.Values{"artist": {so.Artist}, "title": {so.Title}})
	if err != nil {
		return provider.Lyrics{}, err
	}
	if r.Lyrics == nil || strings.TrimSpace(r.Lyrics.Value) == "" {
		return provider.Lyrics{}, fmt.Errorf("navidrome: no lyrics for %q: %w", trackID, provider.ErrNotFound)
	}
	return provider.Lyrics{Plain: normalizeNewlines(r.Lyrics.Value)}, nil
}

// toLyrics picks the first synced entry, else the first plain one. Plain
// is filled either way.
func toLyrics(list *lyricsList, trackID string) (provider.Lyrics, error) {
	var pick *structuredLyrics
	if list != nil {
		for i, sl := range list.StructuredLyrics {
			if len(sl.Line) == 0 {
				continue
			}
			if sl.Synced {
				pick = &list.StructuredLyrics[i]
				break
			}
			if pick == nil {
				pick = &list.StructuredLyrics[i]
			}
		}
	}
	if pick == nil {
		return provider.Lyrics{}, fmt.Errorf("navidrome: no lyrics for %q: %w", trackID, provider.ErrNotFound)
	}
	var l provider.Lyrics
	plain := make([]string, len(pick.Line))
	for i, ln := range pick.Line {
		plain[i] = ln.Value
		if pick.Synced {
			at := max(time.Duration(ln.Start-pick.Offset)*time.Millisecond, 0)
			l.Synced = append(l.Synced, provider.LyricLine{At: at, Text: ln.Value})
		}
	}
	l.Plain = strings.Join(plain, "\n")
	return l, nil
}

func normalizeNewlines(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"))
}

func (s *session) Close() error { return nil }
