// SPDX-License-Identifier: AGPL-3.0-only

package spotify

import (
	"context"
	"fmt"
	"io"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Audio fetches tracks' audio over Spotify's streaming protocol, the one
// the Spotify apps use, and decrypts it. The Web API has no audio, so this
// is the only way to play a track through the server.
//
// Implementations must be safe for concurrent use. They map failures to
// the provider package's errors: ErrNotFound for a track with no audio
// file, ErrNotPlayable when Spotify won't give the account a track's
// decryption key, ErrAuthExpired when Spotify refuses the login (including
// for accounts without Premium), and ErrUnavailable or ErrRateLimited for
// service trouble.
type Audio interface {
	// ClientID is the client ID device pairings are approved for. The
	// streaming protocol only accepts logins from some clients, so the
	// backend chooses.
	ClientID() string
	// Pair logs in with the access token of an approved device pairing, and
	// returns the account's username and reusable credentials.
	Pair(ctx context.Context, accessToken string) (username string, stored []byte, err error)
	// Open opens a track's audio.
	Open(ctx context.Context, login Login, trackID string, opts AudioOpts) (AudioFile, error)
	// Check returns ErrNotPlayable if Spotify won't give login the track's
	// decryption key. A key it does give is kept for Open.
	Check(ctx context.Context, login Login, trackID string) error
}

// Login is the account an Audio backend logs in as, from Pair.
type Login struct {
	Username string
	Stored   []byte
}

// AudioOpts shapes the choice of file, when a track has several.
type AudioOpts struct {
	// Accept lists content types the player can decode. Empty means any.
	Accept []string
	// MaxBitrate caps the bitrate in kbit/s. 0 means no cap.
	MaxBitrate int
}

// AudioFile is one decrypted audio file of a track.
type AudioFile interface {
	ContentType() string
	// Size is the file's length in bytes.
	Size() int64
	// ReadRange returns n bytes starting at off. The range is within Size.
	ReadRange(ctx context.Context, off, n int64) (io.ReadCloser, error)
	Close() error
}

// Stream streams a track's audio from the Audio backend. Files are
// decrypted as they're read, from any offset, so every stream is seekable.
func (s *session) Stream(ctx context.Context, trackID string, opts provider.StreamOpts) (*provider.AudioStream, error) {
	if err := checkID("track", trackID); err != nil {
		return nil, err
	}
	f, err := s.p.audio.Open(ctx, s.login(), trackID, AudioOpts{Accept: opts.Accept, MaxBitrate: opts.MaxBitrate})
	if err != nil {
		return nil, err
	}
	size := f.Size()
	start, end := int64(0), size-1
	if r := opts.Range; r != nil {
		if r.Start < 0 || r.Start >= size || (r.End >= 0 && r.End < r.Start) {
			f.Close()
			return nil, fmt.Errorf("spotify stream: range %d-%d of %d bytes: %w", r.Start, r.End, size, provider.ErrRange)
		}
		start = r.Start
		if r.End >= 0 {
			end = min(r.End, size-1)
		}
	}
	body, err := f.ReadRange(ctx, start, end-start+1)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &provider.AudioStream{
		Body:        &fileBody{ReadCloser: body, file: f},
		ContentType: f.ContentType(),
		Offset:      start,
		Length:      end - start + 1,
		Size:        size,
		Seekable:    true,
	}, nil
}

// CheckPlayable implements provider.PlayChecker: Spotify refuses the
// decryption keys of some tracks, for reasons it doesn't give.
func (s *session) CheckPlayable(ctx context.Context, trackID string) error {
	if err := checkID("track", trackID); err != nil {
		return err
	}
	return s.p.audio.Check(ctx, s.login(), trackID)
}

// fileBody closes the file along with the range being read from it.
type fileBody struct {
	io.ReadCloser
	file AudioFile
}

func (b *fileBody) Close() error {
	err := b.ReadCloser.Close()
	if ferr := b.file.Close(); err == nil {
		err = ferr
	}
	return err
}
