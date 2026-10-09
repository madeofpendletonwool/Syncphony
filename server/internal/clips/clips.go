// SPDX-License-Identifier: AGPL-3.0-only

// Package clips cuts short clips of songs for game rounds (MAD-792, ADR
// 0015): a second or a few of a song for name that tune, and a little
// longer for its reveal. Clips are cut on the server, from the same
// stream the speaker plays, and kept briefly in memory under opaque IDs,
// so nothing about a clip (its URL, its file, its tags) names the song.
package clips

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/transcode"
)

// ErrNotFound is a clip that isn't the room's, or has expired.
var ErrNotFound = errors.New("there's no such clip")

// Sessions opens provider sessions for links. links.Service is one.
type Sessions interface {
	Open(ctx context.Context, linkID string) (provider.Session, error)
}

// Song is where a song streams from: a link that streams, and its track
// there. Duration, if known, keeps cuts inside it.
type Song struct {
	LinkID, TrackID string
	Duration        time.Duration
}

// Clip is a cut clip.
type Clip struct {
	Data        []byte
	ContentType string
}

// Config tunes the service. Zero values take the defaults.
type Config struct {
	// TTL is how long a clip is kept after it's cut. Default 20m: long
	// enough to cut a round's clips a song ahead.
	TTL time.Duration
	// Timeout bounds cutting one song's clips, from opening its stream.
	// Default 90s: the stream is read up to the last cut.
	Timeout time.Duration
	// Kbps is the clips' bitrate. Default 160.
	Kbps int
}

// Service cuts and keeps clips.
type Service struct {
	cfg      Config
	sessions Sessions
	cutter   transcode.Clipper

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu   sync.Mutex
	byID map[string]*clip
}

// clip is a clip being cut, or cut.
type clip struct {
	roomID string
	// ready closes when it's cut, or failed.
	ready chan struct{}
	data  []byte
	err   error
	at    time.Time
}

// New returns a Service. Call Close when done.
func New(sessions Sessions, cutter transcode.Clipper, cfg Config) *Service {
	if cfg.TTL == 0 {
		cfg.TTL = 20 * time.Minute
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 90 * time.Second
	}
	if cfg.Kbps == 0 {
		cfg.Kbps = 160
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{cfg: cfg, sessions: sessions, cutter: cutter, ctx: ctx, cancel: cancel, byID: map[string]*clip{}}
}

// Close stops cutting. Clips not yet cut fail.
func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

// Cut starts cutting clips of a song for a room, in the background, and
// returns their IDs at once, in the order of cuts. Cuts are kept inside
// the song when its duration is known.
func (s *Service) Cut(roomID string, song Song, cuts []transcode.Cut) []string {
	now := time.Now()
	s.mu.Lock()
	for id, c := range s.byID {
		if !c.at.IsZero() && now.Sub(c.at) > s.cfg.TTL {
			delete(s.byID, id)
		}
	}
	ids := make([]string, len(cuts))
	made := make([]*clip, len(cuts))
	for i := range cuts {
		ids[i], made[i] = store.NewID(), &clip{roomID: roomID, ready: make(chan struct{})}
		s.byID[ids[i]] = made[i]
		cuts[i] = Inside(cuts[i], song.Duration)
	}
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(s.ctx, s.cfg.Timeout)
		defer cancel()
		data, err := s.cut(ctx, song, cuts)
		if err != nil {
			slog.Warn("clips: cutting a song's clips", "link", song.LinkID, "err", err)
		}
		at := time.Now()
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, c := range made {
			if err == nil && i < len(data) && len(data[i]) > 0 {
				c.data = data[i]
			} else {
				c.err = cmpErr(err, ErrNotFound)
			}
			c.at = at
			close(c.ready)
		}
	}()
	return ids
}

func cmpErr(err, or error) error {
	if err != nil {
		return err
	}
	return or
}

func (s *Service) cut(ctx context.Context, song Song, cuts []transcode.Cut) ([][]byte, error) {
	sess, err := s.sessions.Open(ctx, song.LinkID)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	st, ok := sess.(provider.Streamer)
	if !ok {
		return nil, errors.New("clips: the song's service doesn't stream")
	}
	a, err := st.Stream(ctx, song.TrackID, provider.StreamOpts{})
	if err != nil {
		return nil, err
	}
	return s.cutter.Clips(ctx, a, transcode.MP3, s.cfg.Kbps, cuts)
}

// Inside keeps a cut inside a song of duration d (if known), ending a
// little before its last moment.
func Inside(c transcode.Cut, d time.Duration) transcode.Cut {
	c.Length = min(max(c.Length, 2*transcode.ClipFade), transcode.MaxClip)
	if d > 0 {
		c.Start = min(c.Start, d-c.Length-time.Second)
	}
	c.Start = max(c.Start, 0)
	return c
}

// Get returns a room's clip, waiting for it to be cut.
func (s *Service) Get(ctx context.Context, roomID, id string) (Clip, error) {
	s.mu.Lock()
	c := s.byID[id]
	s.mu.Unlock()
	if c == nil || c.roomID != roomID {
		return Clip{}, ErrNotFound
	}
	select {
	case <-c.ready:
	case <-ctx.Done():
		return Clip{}, ctx.Err()
	}
	if c.err != nil {
		return Clip{}, ErrNotFound
	}
	return Clip{Data: c.data, ContentType: transcode.MP3.ContentType}, nil
}

// Ready reports whether all of ids are cut, and cut well. A round waits
// for its clips before it starts.
func (s *Service) Ready(ids []string) (ready, failed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ready = true
	for _, id := range ids {
		c := s.byID[id]
		if c == nil {
			return false, true
		}
		select {
		case <-c.ready:
			if c.err != nil {
				return false, true
			}
		default:
			ready = false
		}
	}
	return ready, false
}
