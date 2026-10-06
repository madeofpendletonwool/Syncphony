// SPDX-License-Identifier: AGPL-3.0-only

package links

import (
	"context"
	"io"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
)

// watcher wraps a provider session so every call's outcome feeds the link's
// health. Errors other than ErrAuthExpired (not found, unavailable, ...)
// say nothing about the credentials and leave health alone.
type watcher struct {
	s     *Service
	row   store.ServiceLink
	inner provider.Session
}

func (w *watcher) see(ctx context.Context, err error) error { return w.s.observe(ctx, w.row, err) }

func (w *watcher) Search(ctx context.Context, q provider.SearchQuery) (provider.SearchPage, error) {
	r, err := w.inner.Search(ctx, q)
	return r, w.see(ctx, err)
}

func (w *watcher) Track(ctx context.Context, id string) (provider.Track, error) {
	r, err := w.inner.Track(ctx, id)
	return r, w.see(ctx, err)
}

func (w *watcher) Album(ctx context.Context, id string) (provider.Album, []provider.Track, error) {
	a, ts, err := w.inner.Album(ctx, id)
	return a, ts, w.see(ctx, err)
}

func (w *watcher) Artist(ctx context.Context, id string) (provider.Artist, []provider.Album, error) {
	a, as, err := w.inner.Artist(ctx, id)
	return a, as, w.see(ctx, err)
}

func (w *watcher) Artwork(ctx context.Context, ref provider.ArtworkRef, size int) (io.ReadCloser, string, error) {
	r, ct, err := w.inner.Artwork(ctx, ref, size)
	return r, ct, w.see(ctx, err)
}

func (w *watcher) Close() error { return w.inner.Close() }

// CheckPlayable implements provider.PlayChecker on every wrapped session, so
// wrap needn't double its variants. Sessions that can't check return
// ErrUnsupported, which callers already treat as "couldn't tell".
func (w *watcher) CheckPlayable(ctx context.Context, trackID string) error {
	pc, ok := w.inner.(provider.PlayChecker)
	if !ok {
		return provider.ErrUnsupported
	}
	return w.see(ctx, pc.CheckPlayable(ctx, trackID))
}

type streamer struct{ *watcher }

func (w streamer) Stream(ctx context.Context, trackID string, opts provider.StreamOpts) (*provider.AudioStream, error) {
	a, err := w.inner.(provider.Streamer).Stream(ctx, trackID, opts)
	return a, w.see(ctx, err)
}

type remote struct{ *watcher }

func (w remote) r() provider.Remote { return w.inner.(provider.Remote) }

func (w remote) Play(ctx context.Context, trackID string, at time.Duration) error {
	return w.see(ctx, w.r().Play(ctx, trackID, at))
}
func (w remote) Pause(ctx context.Context) error  { return w.see(ctx, w.r().Pause(ctx)) }
func (w remote) Resume(ctx context.Context) error { return w.see(ctx, w.r().Resume(ctx)) }
func (w remote) Seek(ctx context.Context, to time.Duration) error {
	return w.see(ctx, w.r().Seek(ctx, to))
}

func (w remote) State(ctx context.Context) (provider.RemoteState, error) {
	st, err := w.r().State(ctx)
	return st, w.see(ctx, err)
}

type playlists struct{ *watcher }

func (w playlists) Playlists(ctx context.Context, cursor string) (provider.Page[provider.Playlist], error) {
	p, err := w.inner.(provider.PlaylistLister).Playlists(ctx, cursor)
	return p, w.see(ctx, err)
}

func (w playlists) PlaylistTracks(ctx context.Context, id, cursor string) (provider.Page[provider.Track], error) {
	p, err := w.inner.(provider.PlaylistLister).PlaylistTracks(ctx, id, cursor)
	return p, w.see(ctx, err)
}

type lyrics struct{ *watcher }

func (w lyrics) Lyrics(ctx context.Context, trackID string) (provider.Lyrics, error) {
	l, err := w.inner.(provider.Lyricist).Lyrics(ctx, trackID)
	return l, w.see(ctx, err)
}

// wrap returns a session with exactly the optional interfaces the inner
// session has, since callers discover features by type assertion.
func wrap(w *watcher) provider.Session {
	_, isStream := w.inner.(provider.Streamer)
	_, isRemote := w.inner.(provider.Remote)
	_, hasPL := w.inner.(provider.PlaylistLister)
	_, hasLy := w.inner.(provider.Lyricist)
	S, R, P, L := streamer{w}, remote{w}, playlists{w}, lyrics{w}

	switch {
	case isStream && hasPL && hasLy:
		return struct {
			*watcher
			streamer
			playlists
			lyrics
		}{w, S, P, L}
	case isStream && hasPL:
		return struct {
			*watcher
			streamer
			playlists
		}{w, S, P}
	case isStream && hasLy:
		return struct {
			*watcher
			streamer
			lyrics
		}{w, S, L}
	case isStream:
		return struct {
			*watcher
			streamer
		}{w, S}
	case isRemote && hasPL && hasLy:
		return struct {
			*watcher
			remote
			playlists
			lyrics
		}{w, R, P, L}
	case isRemote && hasPL:
		return struct {
			*watcher
			remote
			playlists
		}{w, R, P}
	case isRemote && hasLy:
		return struct {
			*watcher
			remote
			lyrics
		}{w, R, L}
	case isRemote:
		return struct {
			*watcher
			remote
		}{w, R}
	case hasPL && hasLy:
		return struct {
			*watcher
			playlists
			lyrics
		}{w, P, L}
	case hasPL:
		return struct {
			*watcher
			playlists
		}{w, P}
	case hasLy:
		return struct {
			*watcher
			lyrics
		}{w, L}
	default:
		return w
	}
}
