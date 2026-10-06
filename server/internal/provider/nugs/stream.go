// SPDX-License-Identifier: AGPL-3.0-only

package nugs

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/transcode"
)

// format is a way nugs.net serves a track. The stream API doesn't say which
// it sent; like nugs.net's own apps, we tell by the URL.
type format struct {
	name        string
	contentType string
	// hls formats are playlists of segments, not files.
	hls bool
}

var (
	flac = format{name: "flac", contentType: "audio/flac"}
	// mqa is MQA-encoded FLAC, which plays as 24-bit FLAC without an MQA
	// decoder.
	mqa  = format{name: "mqa", contentType: "audio/flac"}
	alac = format{name: "alac", contentType: "audio/mp4"}
	aac  = format{name: "aac", contentType: "audio/mp4"}
	hls  = format{name: "hls", hls: true}
)

// markers recognize formats by their URLs, checked in order. 360 Reality
// Audio (".s360/") is left out: it's multichannel, for headphones.
var markers = []struct {
	marker string
	f      format
}{
	{".alac16/", alac},
	{".flac16/", flac},
	{".mqa24/", mqa},
	{".flac?", flac},
	{".aac150/", aac},
	{".m4a?", aac},
	{".m3u8?", hls},
}

func formatOf(u string) (format, bool) {
	for _, m := range markers {
		if strings.Contains(u, m.marker) {
			return m.f, true
		}
	}
	return format{}, false
}

// platforms are the stream API's platform IDs. Each answers with the
// format it would play on that platform, so asking all of them is how to
// learn which formats a track comes in.
var platforms = []string{"1", "4", "7", "10"}

// source is one way to play a track.
type source struct {
	url string
	f   format
}

// Stream plays a track in the best format the player can take: lossless
// unless a bitrate cap rules it out. If the player takes none of them, the
// server's transcoder converts the best.
func (s *session) Stream(ctx context.Context, trackID string, opts provider.StreamOpts) (*provider.AudioStream, error) {
	_, track, ok := splitTrackID(trackID)
	if !ok {
		return nil, fmt.Errorf("nugs: track %q: %w", trackID, provider.ErrNotFound)
	}
	sub, err := s.auth.subscription(ctx)
	if err != nil {
		return nil, err
	}
	if !sub.active(s.p.now()) {
		return nil, errNoSubscription
	}
	srcs, err := s.p.sources(ctx, track, sub, s.auth.userID())
	if err != nil {
		return nil, err
	}
	src, ok := choose(srcs, opts)
	if !ok {
		return nil, fmt.Errorf("nugs: no stream for track %s: %w", track, provider.ErrNotFound)
	}
	if src.f.hls {
		return s.p.hlsStream(ctx, src.url)
	}
	return s.p.file(ctx, src, opts.Range)
}

// sources asks every platform for the track's stream URL.
func (p *Provider) sources(ctx context.Context, track string, sub *subscription, userID string) ([]source, error) {
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		srcs     []source
		firstErr error
	)
	for _, platform := range platforms {
		wg.Go(func() {
			link, err := p.streamLink(ctx, track, platform, sub, userID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			if f, ok := formatOf(link); ok {
				srcs = append(srcs, source{url: link, f: f})
			}
		})
	}
	wg.Wait()
	if len(srcs) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return srcs, nil
}

// streamLink asks the stream API for a signed URL of the track as one
// platform plays it. A track that doesn't exist gets no URL.
func (p *Provider) streamLink(ctx context.Context, track, platform string, sub *subscription, userID string) (string, error) {
	q := url.Values{
		"app":                     {"1"},
		"platformID":              {platform},
		"trackID":                 {track},
		"subscriptionID":          {sub.LegacyID.id()},
		"subCostplanIDAccessList": {sub.planID()},
		"startDateStamp":          {strconv.FormatInt(stamp(sub.StartedAt), 10)},
		"endDateStamp":            {strconv.FormatInt(stamp(sub.EndsAt), 10)},
		"nn_userID":               {userID},
	}
	var r struct {
		StreamLink string `json:"streamLink"`
	}
	if err := p.getJSON(ctx, p.ep.Stream+"/bigriver/subPlayer.aspx?"+q.Encode(), "", "stream", &r); err != nil {
		return "", err
	}
	if u, err := url.Parse(r.StreamLink); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", nil
	}
	return r.StreamLink, nil
}

// choose picks the source to play. It prefers a format the player
// accepts, then the best format whatever the player takes.
func choose(srcs []source, opts provider.StreamOpts) (source, bool) {
	prefer := []format{flac, mqa, aac, alac, hls}
	// Lossless runs to 1,000 kbit/s and more; under a cap, take AAC.
	if opts.MaxBitrate > 0 && opts.MaxBitrate < 1000 {
		prefer = []format{aac, hls, flac, mqa, alac}
	}
	find := func(f format) (source, bool) {
		for _, s := range srcs {
			if s.f == f {
				return s, true
			}
		}
		return source{}, false
	}
	for _, f := range prefer {
		if s, ok := find(f); ok && !f.hls && transcode.Accepts(opts.Accept, f.contentType) {
			return s, true
		}
	}
	for _, f := range prefer {
		if s, ok := find(f); ok {
			return s, true
		}
	}
	return source{}, false
}

// file streams an audio file from nugs.net's CDN, passing a range through.
func (p *Provider) file(ctx context.Context, src source, rng *provider.ByteRange) (*provider.AudioStream, error) {
	req, err := newRequest(ctx, src.url, "")
	if err != nil {
		return nil, err
	}
	if rng != nil {
		if rng.Start < 0 || (rng.End >= 0 && rng.End < rng.Start) {
			return nil, fmt.Errorf("nugs audio: range %d-%d: %w", rng.Start, rng.End, provider.ErrRange)
		}
		end := ""
		if rng.End >= 0 {
			end = strconv.FormatInt(rng.End, 10)
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%s", rng.Start, end))
	}
	resp, err := p.do(req, "audio", false)
	if err != nil {
		return nil, err
	}
	a := &provider.AudioStream{Body: resp.Body, ContentType: src.f.contentType, Length: resp.ContentLength, Size: -1}
	if resp.StatusCode == http.StatusPartialContent {
		start, end, size, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok {
			resp.Body.Close()
			return nil, fmt.Errorf("nugs audio: bad Content-Range %q: %w", resp.Header.Get("Content-Range"), provider.ErrUnavailable)
		}
		a.Offset, a.Length, a.Size, a.Seekable = start, end-start+1, size, true
		return a, nil
	}
	// The whole file: no range was asked for, or it was ignored.
	a.Size = a.Length
	a.Seekable = rng == nil && strings.Contains(resp.Header.Get("Accept-Ranges"), "bytes")
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
