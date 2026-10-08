// SPDX-License-Identifier: AGPL-3.0-only

package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

const (
	// analyzeTimeout bounds downloading, decoding and analysing one song.
	analyzeTimeout = 3 * time.Minute
	// maxDuration skips DJ mixes and audiobooks: hours of beats nobody needs.
	maxDuration = 30 * time.Minute
	queueSize   = 256
	// retryMisses is how long a song that couldn't be analysed is left alone.
	retryMisses = 7 * 24 * time.Hour
	// keepUnused is how long a map nobody plays is kept.
	keepUnused = 180 * 24 * time.Hour
)

// ErrUnavailable means a song has no beat map and won't get one: its
// service has no audio to analyse, or it can't be decoded.
var ErrUnavailable = errors.New("analysis: no beat map for this song")

// Sessions opens a service link, for its audio.
type Sessions interface {
	Open(ctx context.Context, linkID string) (provider.Session, error)
}

// Decoder turns a song's file into mono 32-bit float samples at SampleRate.
type Decoder interface {
	Decode(ctx context.Context, path string) (io.ReadCloser, error)
}

// Service works out songs' beat maps and keeps them. Call Run to analyse
// songs in the background as they're queued, so the map is ready when the
// song starts.
type Service struct {
	db       *store.Store
	sessions Sessions
	decoder  Decoder

	jobs    chan source
	mu      sync.Mutex
	pending map[string]bool
	group   singleflight.Group
}

// source is where a song's audio comes from.
type source struct {
	provider, linkID, trackID string
	duration                  time.Duration
}

func (s source) key() string { return s.provider + "\x00" + s.trackID }

// New returns a Service.
func New(db *store.Store, sessions Sessions, decoder Decoder) *Service {
	return &Service{db: db, sessions: sessions, decoder: decoder, jobs: make(chan source, queueSize), pending: map[string]bool{}}
}

// Enqueue asks for newly queued songs to be analysed.
func (s *Service) Enqueue(ts ...provider.Track) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range ts {
		src := source{provider: t.Ref.Provider, linkID: t.Ref.LinkID, trackID: t.Ref.ID, duration: t.Duration}
		k := src.key()
		if src.trackID == "" || src.linkID == "" || s.pending[k] {
			continue
		}
		select {
		case s.jobs <- src:
			s.pending[k] = true
		default:
			slog.Debug("analysis: queue full, dropping a track", "provider", src.provider, "track", src.trackID)
		}
	}
}

// Run analyses queued songs, one at a time, until ctx is done.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case src := <-s.jobs:
			if _, err := s.get(ctx, src); err != nil && !errors.Is(err, ErrUnavailable) && ctx.Err() == nil {
				slog.Debug("analysis", "provider", src.provider, "track", src.trackID, "err", err)
			}
			s.mu.Lock()
			delete(s.pending, src.key())
			s.mu.Unlock()
		}
	}
}

// ForItem returns the beat map of the song a queue item plays (from the
// service standing in for its own, if one is), analysing it now if it
// hasn't been. It's ErrUnavailable if the song can't have one.
func (s *Service) ForItem(ctx context.Context, it store.QueueItem, duration time.Duration) (Map, error) {
	src := source{provider: it.Provider, linkID: it.LinkID.String, trackID: it.TrackID, duration: duration}
	if it.ViaLinkID.Valid {
		src = source{provider: it.ViaProvider.String, linkID: it.ViaLinkID.String, trackID: it.ViaTrackID.String, duration: duration}
	}
	return s.get(ctx, src)
}

// Sweep forgets maps nobody has played in a long while.
func (s *Service) Sweep(ctx context.Context) error {
	now := store.Now()
	_, err := s.db.SweepBeatMaps(ctx, store.SweepBeatMapsParams{UnusedSince: now.Add(-keepUnused), MissedBefore: now.Add(-retryMisses)})
	return err
}

// get returns src's map from the cache, or analyses it.
func (s *Service) get(ctx context.Context, src source) (Map, error) {
	row, err := s.db.GetBeatMap(ctx, store.GetBeatMapParams{Provider: src.provider, TrackID: src.trackID})
	switch {
	case err == nil && row.Version == Version:
		now := store.Now()
		_ = s.db.TouchBeatMap(ctx, store.TouchBeatMapParams{Now: now, Provider: src.provider, TrackID: src.trackID, Since: now.Add(-24 * time.Hour)})
		if !row.Found {
			if now.Sub(row.AnalyzedAt) < retryMisses {
				return Map{}, ErrUnavailable
			}
			break
		}
		var m Map
		if err := json.Unmarshal([]byte(row.Map), &m); err == nil {
			return m, nil
		}
	case err != nil && !store.IsNotFound(err):
		return Map{}, err
	}
	if src.linkID == "" {
		return Map{}, ErrUnavailable
	}
	v, err, _ := s.group.Do(src.key(), func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), analyzeTimeout)
		defer cancel()
		start := time.Now()
		m, err := s.analyze(ctx, src)
		if errors.Is(err, ErrUnavailable) {
			return Map{}, s.save(ctx, src, nil)
		}
		if err != nil {
			return Map{}, err
		}
		slog.Debug("analysed a song", "provider", src.provider, "track", src.trackID, "bpm", m.BPM, "took", time.Since(start).Round(time.Millisecond))
		return m, s.save(ctx, src, &m)
	})
	if err != nil {
		return Map{}, err
	}
	m := v.(Map)
	if m.Version == 0 {
		return Map{}, ErrUnavailable
	}
	return m, nil
}

// save keeps src's map, or that it has none.
func (s *Service) save(ctx context.Context, src source, m *Map) error {
	body := "{}"
	if m != nil {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		body = string(b)
	}
	now := store.Now()
	if err := s.db.PutBeatMap(ctx, store.PutBeatMapParams{
		Provider: src.provider, TrackID: src.trackID, Version: Version, Found: m != nil, Map: body, AnalyzedAt: now, UsedAt: now,
	}); err != nil {
		return err
	}
	if m == nil {
		return ErrUnavailable
	}
	return nil
}

// analyze downloads src's audio to a temporary file (decoders can't read
// some files front to back, an MP4 with its index at the end), decodes
// and analyses it.
func (s *Service) analyze(ctx context.Context, src source) (Map, error) {
	if s.decoder == nil || src.duration > maxDuration {
		return Map{}, ErrUnavailable
	}
	sess, err := s.sessions.Open(ctx, src.linkID)
	if err != nil {
		return Map{}, err
	}
	defer sess.Close()
	st, ok := sess.(provider.Streamer)
	if !ok {
		return Map{}, ErrUnavailable
	}
	a, err := st.Stream(ctx, src.trackID, provider.StreamOpts{})
	if errors.Is(err, provider.ErrNotFound) || errors.Is(err, provider.ErrNotPlayable) {
		return Map{}, ErrUnavailable
	} else if err != nil {
		return Map{}, err
	}
	f, err := os.CreateTemp("", "syncphony-analysis-*")
	if err != nil {
		a.Body.Close()
		return Map{}, err
	}
	defer os.Remove(f.Name())
	_, err = io.Copy(f, a.Body)
	a.Body.Close()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Map{}, fmt.Errorf("analysis: downloading: %w", err)
	}
	pcm, err := s.decoder.Decode(ctx, f.Name())
	if err != nil {
		return Map{}, err
	}
	defer pcm.Close()
	m, err := Analyze(pcm)
	if err != nil {
		if ctx.Err() != nil {
			return Map{}, ctx.Err()
		}
		// ffmpeg couldn't read it: it never will.
		slog.Debug("analysis: decoding", "provider", src.provider, "track", src.trackID, "err", err)
		return Map{}, ErrUnavailable
	}
	return m, nil
}

// FFmpeg decodes with an ffmpeg binary.
type FFmpeg struct {
	// Path is the ffmpeg binary. Default "ffmpeg" on $PATH.
	Path string
}

// Decode implements Decoder. ffmpeg is stopped when the reader is closed
// or ctx is done; reading to the end reports how it exited.
func (f FFmpeg) Decode(ctx context.Context, path string) (io.ReadCloser, error) {
	bin := f.Path
	if bin == "" {
		bin = "ffmpeg"
	}
	ctx, cancel := context.WithCancel(ctx)
	// The binary comes from server config, the path from os.CreateTemp.
	cmd := exec.CommandContext(ctx, bin, //nolint:gosec // see above
		"-hide_banner", "-loglevel", "error", "-nostdin", "-i", path,
		"-vn", "-ac", "1", "-ar", fmt.Sprint(SampleRate), "-f", "f32le", "pipe:1")
	var stderr strings.Builder
	cmd.Stderr = &limited{w: &stderr, n: 2 << 10}
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("analysis: starting ffmpeg: %w", err)
	}
	return &decoding{out: out, cmd: cmd, cancel: cancel, stderr: &stderr}, nil
}

type decoding struct {
	out    io.ReadCloser
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stderr *strings.Builder
	once   sync.Once
	err    error
}

func (d *decoding) Read(p []byte) (int, error) {
	n, err := d.out.Read(p)
	if errors.Is(err, io.EOF) {
		if werr := d.wait(); werr != nil {
			return n, werr
		}
	}
	return n, err
}

func (d *decoding) Close() error {
	d.cancel()
	_ = d.wait()
	return nil
}

func (d *decoding) wait() error {
	d.once.Do(func() {
		if err := d.cmd.Wait(); err != nil {
			d.err = fmt.Errorf("analysis: ffmpeg: %w: %s", err, strings.TrimSpace(d.stderr.String()))
		}
		d.cancel()
	})
	return d.err
}

// limited writes the first n bytes to w and drops the rest.
type limited struct {
	w io.Writer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		_, _ = l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}
