// SPDX-License-Identifier: AGPL-3.0-only

package provider

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Session is an authenticated client for one linked account. Sessions must
// be safe for concurrent use.
//
// Optional features are separate interfaces (Streamer, Remote,
// PlaylistLister, Lyricist, Recommender), discovered with a type assertion and declared in
// Capabilities. PlayChecker is optional too, but needs no capability: the
// core only uses it when it's there.
type Session interface {
	// Search returns matches for q. Asking for a kind the provider can't
	// search is ErrUnsupported.
	Search(ctx context.Context, q SearchQuery) (SearchPage, error)
	// Track looks up a track by provider ID.
	Track(ctx context.Context, id string) (Track, error)
	// Album returns an album and its tracks in order.
	Album(ctx context.Context, id string) (Album, []Track, error)
	// Artist returns an artist and their albums. Providers that can't
	// search artists return ErrUnsupported.
	Artist(ctx context.Context, id string) (Artist, []Album, error)
	// Artwork returns an image and its content type. size is the wanted
	// width in pixels, a hint; 0 means the provider's default. Providers
	// without the Artwork capability return ErrUnsupported.
	Artwork(ctx context.Context, ref ArtworkRef, size int) (io.ReadCloser, string, error)
	Close() error
}

// Streamer is implemented by sessions of Stream providers.
type Streamer interface {
	Stream(ctx context.Context, trackID string, opts StreamOpts) (*AudioStream, error)
}

// StreamOpts shapes a stream request.
type StreamOpts struct {
	// Range requests part of the file. Nil means the whole file.
	Range *ByteRange
	// Accept lists content types the player can decode, as a hint for
	// providers that can choose a format. Empty means any.
	Accept []string
	// MaxBitrate caps the bitrate in kbit/s, if the provider can. 0 means no cap.
	MaxBitrate int
}

// ByteRange is an HTTP-style byte range of the original file.
type ByteRange struct {
	Start int64
	// End is inclusive. -1 means to the end of the file.
	End int64
}

// AudioStream is audio from a provider. The caller must close Body.
type AudioStream struct {
	Body        io.ReadCloser
	ContentType string
	// Offset is the position of Body's first byte in the file. It is 0
	// unless a range was requested and honored.
	Offset int64
	// Length is the number of bytes in Body, or -1 if unknown.
	Length int64
	// Size is the size of the whole file, or -1 if unknown.
	Size int64
	// Seekable means the provider honors StreamOpts.Range. A stream that
	// isn't seekable always starts at Offset 0.
	Seekable bool
}

// ContentRange formats the HTTP Content-Range header for a partial stream.
// It returns "" when the stream is the whole file or the extent is unknown.
func (a *AudioStream) ContentRange() string {
	if a.Length < 0 || (a.Offset == 0 && a.Length == a.Size) {
		return ""
	}
	size := "*"
	if a.Size >= 0 {
		size = fmt.Sprint(a.Size)
	}
	return fmt.Sprintf("bytes %d-%d/%s", a.Offset, a.Offset+a.Length-1, size)
}

// Remote is implemented by sessions of Remote providers. The server drives
// the provider's own player instead of streaming audio.
type Remote interface {
	Play(ctx context.Context, trackID string, at time.Duration) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Seek(ctx context.Context, to time.Duration) error
	State(ctx context.Context) (RemoteState, error)
}

// RemoteState is a remote player's state.
type RemoteState struct {
	// TrackID is the playing track, or "" if nothing is loaded.
	TrackID  string
	Playing  bool
	Position time.Duration
	// Device is the name of the device playing, if known.
	Device string
	// At is when the state was observed, for extrapolating Position.
	At time.Time
}

// PlaylistLister is implemented by sessions of providers with the Playlists capability.
type PlaylistLister interface {
	// Playlists lists the account's playlists. Pass "" for the first page,
	// then the previous page's Next.
	Playlists(ctx context.Context, cursor string) (Page[Playlist], error)
	// PlaylistTracks lists a playlist's tracks in order.
	PlaylistTracks(ctx context.Context, id, cursor string) (Page[Track], error)
}

// Lyricist is implemented by sessions of providers with the Lyrics capability.
type Lyricist interface {
	Lyrics(ctx context.Context, trackID string) (Lyrics, error)
}

// Recommender is implemented by sessions of providers with the
// Recommendations capability. Autopilot uses it to keep a room's music
// going. Every song it returns is in this account's library, so it plays.
//
// Services often recommend from data they fetch elsewhere (Navidrome asks
// Last.fm), so an empty result is common and isn't an error. A track or
// artist ID that doesn't exist is ErrNotFound.
type Recommender interface {
	// SimilarToTrack returns songs like a track, most similar first. They
	// may include the track's own artist, but not the track.
	SimilarToTrack(ctx context.Context, trackID string, limit int) ([]Track, error)
	// SimilarToArtist returns songs by artists like an artist, most
	// similar first.
	SimilarToArtist(ctx context.Context, artistID string, limit int) ([]Track, error)
	// TopTracks returns an artist's most popular songs, most popular first.
	// It goes by name, since that's what popularity data is keyed by.
	TopTracks(ctx context.Context, artist string, limit int) ([]Track, error)
	// RandomTracks returns songs picked at random, for when nothing else
	// turns anything up.
	RandomTracks(ctx context.Context, limit int) ([]Track, error)
}

// PlayChecker is implemented by sessions that can tell ahead of time that a
// track won't play, so the queue can refuse it instead of skipping it later.
type PlayChecker interface {
	// CheckPlayable returns ErrNotPlayable for a track the service won't
	// play. Other errors, ErrUnsupported included, mean it couldn't tell.
	CheckPlayable(ctx context.Context, trackID string) error
}
