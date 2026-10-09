// SPDX-License-Identifier: AGPL-3.0-only

// Package transcode is the server's fallback for audio the player can't
// decode, such as Ogg Vorbis on iOS Safari. Stream asks the provider for a
// track and, only if the format isn't acceptable, converts it with ffmpeg.
package transcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// ErrNoTranscoder means the stream needs transcoding but none is configured.
var ErrNoTranscoder = errors.New("transcode: format not accepted and no transcoder available")

// Format is a target format the server can produce.
type Format struct {
	ContentType string
	// codec and container are ffmpeg's -c:a and -f.
	codec, container string
}

// Targets in order of preference. MP3 and AAC play in every browser we
// care about.
var (
	MP3     = Format{"audio/mpeg", "libmp3lame", "mp3"}
	AAC     = Format{"audio/aac", "aac", "adts"}
	Targets = []Format{MP3, AAC}
)

// Transcoder converts audio to another format.
type Transcoder interface {
	// Transcode converts src to f. It takes ownership of src.Body. kbps
	// is the target bitrate, and the output begins start into the song.
	Transcode(ctx context.Context, src *provider.AudioStream, f Format, kbps int, start time.Duration) (*provider.AudioStream, error)
}

// DefaultBitrate is used when StreamOpts.MaxBitrate is 0.
const DefaultBitrate = 192

// Stream gets a track from s. If its content type isn't in opts.Accept, it
// is transcoded with t to the first of Targets that is. Transcoded streams
// are never seekable by bytes, and their length is unknown, so they begin
// at opts.Start instead. A stream that's acceptable but can't be seeked is
// transcoded too when opts.Start asks for somewhere past the beginning;
// one that can be seeked ignores opts.Start, since the player seeks it. t
// may be nil, in which case unacceptable formats fail with ErrNoTranscoder.
func Stream(ctx context.Context, s provider.Streamer, trackID string, opts provider.StreamOpts, t Transcoder) (*provider.AudioStream, error) {
	a, err := s.Stream(ctx, trackID, opts)
	if err != nil {
		return nil, err
	}
	target, ok := pick(opts.Accept)
	if Accepts(opts.Accept, a.ContentType) && (opts.Start <= 0 || a.Seekable || t == nil || !ok) {
		return a, nil
	}
	if t == nil || !ok {
		a.Body.Close()
		return nil, fmt.Errorf("%w: %s", ErrNoTranscoder, a.ContentType)
	}
	if a.Offset != 0 || (a.Length >= 0 && a.Length != a.Size) {
		// A partial file won't decode; start again from the top.
		a.Body.Close()
		opts.Range = nil
		if a, err = s.Stream(ctx, trackID, opts); err != nil {
			return nil, err
		}
	}
	kbps := DefaultBitrate
	if opts.MaxBitrate > 0 {
		kbps = min(kbps, opts.MaxBitrate)
	}
	return t.Transcode(ctx, a, target, kbps, max(opts.Start, 0))
}

// Accepts reports whether contentType matches one of accept, which may
// include wildcards like "audio/*". An empty accept list accepts anything.
func Accepts(accept []string, contentType string) bool {
	if len(accept) == 0 {
		return true
	}
	mt := mediaType(contentType)
	for _, a := range accept {
		a = mediaType(a)
		if a == mt || a == "*/*" || (strings.HasSuffix(a, "/*") && strings.HasPrefix(mt, strings.TrimSuffix(a, "*"))) {
			return true
		}
	}
	return false
}

func mediaType(s string) string {
	if mt, _, err := mime.ParseMediaType(s); err == nil {
		return mt
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func pick(accept []string) (Format, bool) {
	for _, f := range Targets {
		if Accepts(accept, f.ContentType) {
			return f, true
		}
	}
	return Format{}, false
}

// FFmpeg transcodes with an ffmpeg binary.
type FFmpeg struct {
	// Path is the ffmpeg binary. Default "ffmpeg" on $PATH.
	Path string
}

// Available reports whether the ffmpeg binary can be found.
func (f FFmpeg) Available() bool {
	_, err := exec.LookPath(f.path())
	return err == nil
}

func (f FFmpeg) path() string {
	if f.Path == "" {
		return "ffmpeg"
	}
	return f.Path
}

// Transcode implements Transcoder. The ffmpeg process is killed when the
// returned stream is closed or ctx is done.
func (f FFmpeg) Transcode(ctx context.Context, src *provider.AudioStream, to Format, kbps int, start time.Duration) (*provider.AudioStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-i", "pipe:0"}
	if start > 0 {
		// After -i: piped input can't be seeked, so ffmpeg decodes up to
		// start and drops it. That's exact, and quick next to playing it.
		args = append(args, "-ss", fmt.Sprintf("%.3f", start.Seconds()))
	}
	args = append(args,
		"-vn", "-map_metadata", "-1",
		"-c:a", to.codec, "-b:a", fmt.Sprintf("%dk", kbps),
		"-f", to.container, "pipe:1")
	// The binary comes from server config and the arguments from Targets.
	cmd := exec.CommandContext(ctx, f.path(), args...) //nolint:gosec // see above
	cmd.Stdin = src.Body
	stderr := &limitedBuffer{max: 4 << 10}
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		src.Body.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		src.Body.Close()
		return nil, fmt.Errorf("transcode: starting ffmpeg: %w", err)
	}
	return &provider.AudioStream{
		Body:        &process{cmd: cmd, out: out, src: src.Body, cancel: cancel, stderr: stderr},
		ContentType: to.ContentType,
		Length:      -1,
		Size:        -1,
	}, nil
}

// Cut is a clip of a song: Length of it from Start.
type Cut struct {
	Start, Length time.Duration
}

// Clip limits: the longest clip, and the fade at each end so a clip
// doesn't click in or out.
const (
	MaxClip  = 30 * time.Second
	ClipFade = 50 * time.Millisecond
)

// Clipper cuts short clips of a song, for game rounds (MAD-792).
type Clipper interface {
	// Clips cuts each of cuts from src in format f at kbps, faded in and
	// out, with no tags: nothing in a clip names the song. It reads src
	// once and takes ownership of its Body. Cuts must start inside the
	// song; one that runs past its end comes back short.
	Clips(ctx context.Context, src *provider.AudioStream, f Format, kbps int, cuts []Cut) ([][]byte, error)
}

// Clips implements Clipper. Every cut comes from one pass over src: the
// song is split, then each copy trimmed, so it's read once however many
// clips it makes. Each goes to its own file in a temporary directory.
func (f FFmpeg) Clips(ctx context.Context, src *provider.AudioStream, to Format, kbps int, cuts []Cut) ([][]byte, error) {
	defer src.Body.Close()
	if len(cuts) == 0 {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "syncphony-clips-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	var graph strings.Builder
	fmt.Fprintf(&graph, "[0:a]asplit=%d", len(cuts))
	for i := range cuts {
		fmt.Fprintf(&graph, "[s%d]", i)
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-i", "pipe:0"}
	var outs []string
	for i, c := range cuts {
		length := min(max(c.Length, ClipFade*2), MaxClip)
		fade := ClipFade.Seconds()
		// Trimmed in the filter, so the fade out is timed from the clip's
		// own start.
		fmt.Fprintf(&graph, ";[s%d]atrim=start=%.3f:duration=%.3f,asetpts=PTS-STARTPTS,afade=t=in:d=%.3f,afade=t=out:st=%.3f:d=%.3f[o%d]",
			i, max(c.Start, 0).Seconds(), length.Seconds(), fade, length.Seconds()-fade, fade, i)
		out := filepath.Join(dir, fmt.Sprintf("%d.%s", i, to.container))
		outs = append(outs, out)
		args = append(args, "-map", fmt.Sprintf("[o%d]", i),
			"-map_metadata", "-1", "-fflags", "+bitexact", "-c:a", to.codec, "-b:a", fmt.Sprintf("%dk", kbps))
		if to.container == "mp3" {
			args = append(args, "-id3v2_version", "0")
		}
		args = append(args, "-f", to.container, out)
	}
	args = slices.Insert(args, 6, "-filter_complex", graph.String())
	// The binary comes from server config and the arguments from Targets
	// and durations.
	cmd := exec.CommandContext(ctx, f.path(), args...) //nolint:gosec // see above
	cmd.Stdin = src.Body
	stderr := &limitedBuffer{max: 4 << 10}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("transcode: ffmpeg clips: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	clips := make([][]byte, len(outs))
	for i, out := range outs {
		if clips[i], err = os.ReadFile(out); err != nil { //nolint:gosec // our own temporary file
			return nil, err
		}
	}
	return clips, nil
}

// process is ffmpeg's output. Reading to EOF reports ffmpeg's exit status;
// Close stops it early.
type process struct {
	cmd    *exec.Cmd
	out    io.ReadCloser
	src    io.Closer
	cancel context.CancelFunc
	stderr *limitedBuffer

	once    sync.Once
	waitErr error
}

func (p *process) Read(b []byte) (int, error) {
	n, err := p.out.Read(b)
	if errors.Is(err, io.EOF) {
		if werr := p.wait(); werr != nil {
			return n, werr
		}
	}
	return n, err
}

func (p *process) Close() error {
	p.cancel()
	p.src.Close()
	_ = p.wait() // ffmpeg was killed, so its exit status means nothing
	return nil
}

func (p *process) wait() error {
	p.once.Do(func() {
		if err := p.cmd.Wait(); err != nil {
			p.waitErr = fmt.Errorf("transcode: ffmpeg: %w: %s", err, strings.TrimSpace(p.stderr.String()))
		}
		p.cancel()
		p.src.Close()
	})
	return p.waitErr
}

// limitedBuffer keeps the first max bytes written to it.
type limitedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
