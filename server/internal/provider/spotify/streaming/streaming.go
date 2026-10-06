// SPDX-License-Identifier: AGPL-3.0-only

// Package streaming is the Spotify provider's Audio and Library backend. It speaks
// Spotify's streaming protocol, the one the Spotify apps use, through
// go-librespot's lower-level packages (GPL-3.0): access point login, audio
// keys, spclient metadata and storage, and AES decryption. Audio is passed
// through as Ogg Vorbis, never decoded, so nothing here needs cgo.
//
// Spotify refuses some tracks' decryption keys (ErrNotPlayable). Getting
// around that means defeating its key obfuscation, which this package
// deliberately doesn't do. See docs/adr/0004-spotify-playback.md.
package streaming

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	golibrespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/ap"
	"github.com/devgianlu/go-librespot/apresolve"
	"github.com/devgianlu/go-librespot/audio"
	"github.com/devgianlu/go-librespot/login5"
	pb "github.com/devgianlu/go-librespot/proto/spotify"
	storagepb "github.com/devgianlu/go-librespot/proto/spotify/download"
	extmetadatapb "github.com/devgianlu/go-librespot/proto/spotify/extendedmetadata"
	audiofilespb "github.com/devgianlu/go-librespot/proto/spotify/extendedmetadata/audiofiles"
	credentialspb "github.com/devgianlu/go-librespot/proto/spotify/login5/v3/credentials"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	"github.com/devgianlu/go-librespot/spclient"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify"
)

const (
	// headerSize is the Spotify-specific header before the Ogg stream in
	// every decrypted Ogg Vorbis file. Players can't parse it, so it's cut.
	headerSize = 0xa7
	// connectTimeout bounds logging an account in.
	connectTimeout = 30 * time.Second
	// idleConn is how long an account's connection stays open unused.
	idleConn = 15 * time.Minute
	// Keys are cached so a song checked when it's queued, and played (or
	// seeked) later, costs one key request: Spotify throttles them.
	keyTTL     = 24 * time.Hour
	refusedTTL = time.Hour
	maxKeys    = 4096
)

// Backend implements spotify.Audio. It keeps one connection per account,
// opened on first use and closed when idle. Close it when done.
type Backend struct {
	log      golibrespot.Logger
	client   *http.Client
	resolver *apresolve.ApResolver
	deviceID string
	now      func() time.Time

	mu    sync.Mutex
	conns map[string]*conn // by username
	keys  map[string]keyEntry

	stop     chan struct{}
	stopOnce sync.Once
}

var _ spotify.Audio = (*Backend)(nil)

// New returns a Backend. client is used for Spotify's HTTP endpoints and
// the audio CDN; nil means a default one.
func New(client *http.Client) *Backend {
	if client == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.ResponseHeaderTimeout = 30 * time.Second
		client = &http.Client{Transport: t}
	}
	log := slogLogger{slog.Default().With("component", "spotify-streaming")}
	id := make([]byte, 20)
	_, _ = rand.Read(id)
	b := &Backend{
		log:      log,
		client:   client,
		resolver: apresolve.NewApResolver(log, client, true),
		deviceID: hex.EncodeToString(id),
		now:      time.Now,
		conns:    map[string]*conn{},
		keys:     map[string]keyEntry{},
		stop:     make(chan struct{}),
	}
	go b.reap()
	return b
}

// Close closes every connection.
func (b *Backend) Close() error {
	b.stopOnce.Do(func() { close(b.stop) })
	b.mu.Lock()
	defer b.mu.Unlock()
	for user, c := range b.conns {
		c.close()
		delete(b.conns, user)
	}
	return nil
}

// ClientID implements spotify.Audio: the access point accepts logins from
// Spotify's own desktop client ID, which librespot uses.
func (b *Backend) ClientID() string { return golibrespot.ClientIdHex }

// Pair implements spotify.Audio.
func (b *Backend) Pair(ctx context.Context, accessToken string) (string, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	addr, err := b.resolver.GetAccesspoint(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("spotify streaming: resolving the access point: %w: %w", provider.ErrUnavailable, err)
	}
	a := ap.NewAccesspoint(b.log, addr, b.deviceID)
	defer a.Close()
	if err := a.ConnectSpotifyToken(ctx, "", accessToken); err != nil {
		if errors.Is(loginError(err), provider.ErrAuthExpired) {
			return "", nil, fmt.Errorf("%w: %w", provider.ErrInvalidCredentials, loginError(err))
		}
		return "", nil, loginError(err)
	}
	return a.Username(), a.StoredCredentials(), nil
}

// loginError maps an access point or login5 login failure.
func loginError(err error) error {
	var apErr *ap.AccesspointLoginError
	var l5Err *login5.LoginError
	switch {
	case errors.As(err, &apErr):
		switch apErr.Message.GetErrorCode() {
		case pb.ErrorCode_PremiumAccountRequired:
			return fmt.Errorf("%w: Spotify only streams to Premium accounts (%w)", provider.ErrAuthExpired, err)
		case pb.ErrorCode_BadCredentials:
			return fmt.Errorf("%w: %w", provider.ErrAuthExpired, err)
		}
	case errors.As(err, &l5Err):
		return fmt.Errorf("%w: %w", provider.ErrAuthExpired, err)
	}
	return fmt.Errorf("spotify streaming: %w: %w", provider.ErrUnavailable, err)
}

// --- Connections -----------------------------------------------------------------

// conn is one account's connection. ready is closed once err, or the rest,
// are set.
type conn struct {
	ready chan struct{}
	err   error
	ap    *ap.Accesspoint
	keys  *audio.KeyProvider
	sp    *spclient.Spclient
	used  time.Time // guarded by Backend.mu
}

func (c *conn) alive() bool {
	select {
	case <-c.ready:
	default:
		return true // still connecting
	}
	if c.err != nil {
		return false
	}
	select {
	case <-c.ap.Done():
		return false
	default:
		return true
	}
}

func (c *conn) close() {
	select {
	case <-c.ready:
		if c.ap != nil {
			c.ap.Close()
		}
	default:
		// Still connecting: connect closes it if it's no longer wanted.
	}
}

// conn returns login's connection, logging in if there isn't a live one.
func (b *Backend) conn(ctx context.Context, login spotify.Login) (*conn, error) {
	if login.Username == "" || len(login.Stored) == 0 {
		return nil, fmt.Errorf("spotify streaming: no streaming login: %w", provider.ErrAuthExpired)
	}
	b.mu.Lock()
	c, ok := b.conns[login.Username]
	if !ok || !c.alive() {
		if ok {
			c.close()
		}
		c = &conn{ready: make(chan struct{})}
		b.conns[login.Username] = c
		b.mu.Unlock()
		// Connect for everyone waiting, not just this caller: a cancelled
		// request mustn't fail the others.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), connectTimeout)
		c.err = b.connect(cctx, login, c)
		cancel()
		close(c.ready)
		b.mu.Lock()
	}
	c.used = b.now()
	b.mu.Unlock()
	select {
	case <-c.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if c.err != nil {
		b.drop(login.Username, c)
		return nil, c.err
	}
	return c, nil
}

// drop forgets c if it's still login's connection, and closes it.
func (b *Backend) drop(username string, c *conn) {
	b.mu.Lock()
	if b.conns[username] == c {
		delete(b.conns, username)
	}
	b.mu.Unlock()
	c.close()
}

func (b *Backend) connect(ctx context.Context, login spotify.Login, c *conn) error {
	addr, err := b.resolver.GetAccesspoint(ctx)
	if err != nil {
		return fmt.Errorf("spotify streaming: resolving the access point: %w: %w", provider.ErrUnavailable, err)
	}
	a := ap.NewAccesspoint(b.log, addr, b.deviceID)
	if err := a.ConnectStored(ctx, login.Username, login.Stored); err != nil {
		a.Close()
		return loginError(err)
	}
	clientToken, err := retrieveClientToken(ctx, b.client, b.deviceID)
	if err != nil {
		a.Close()
		return fmt.Errorf("spotify streaming: client token: %w: %w", provider.ErrUnavailable, err)
	}
	l5 := login5.NewLogin5(b.log, b.client, b.deviceID, clientToken)
	if err := l5.Login(ctx, &credentialspb.StoredCredential{Username: login.Username, Data: login.Stored}); err != nil {
		a.Close()
		return loginError(err)
	}
	spAddr, err := b.resolver.GetSpclient(ctx)
	if err != nil {
		a.Close()
		return fmt.Errorf("spotify streaming: resolving spclient: %w: %w", provider.ErrUnavailable, err)
	}
	sp, err := spclient.NewSpclient(ctx, b.log, b.client, spAddr, l5.AccessToken(), b.deviceID, clientToken)
	if err != nil {
		a.Close()
		return fmt.Errorf("spotify streaming: spclient: %w: %w", provider.ErrUnavailable, err)
	}
	c.ap, c.keys, c.sp = a, audio.NewAudioKeyProvider(b.log, a), sp
	return nil
}

// reap closes connections that have been idle for a while.
func (b *Backend) reap() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-t.C:
		}
		b.mu.Lock()
		now := b.now()
		for user, c := range b.conns {
			if now.Sub(c.used) > idleConn {
				select {
				case <-c.ready:
					c.close()
					delete(b.conns, user)
				default:
				}
			}
		}
		b.mu.Unlock()
	}
}

// --- Tracks ----------------------------------------------------------------------

// track finds the file to play for trackID and its decryption key.
func (b *Backend) track(ctx context.Context, login spotify.Login, trackID string, maxBitrate int) (*conn, *metadatapb.AudioFile, []byte, error) {
	id, err := golibrespot.SpotifyIdFromBase62(golibrespot.SpotifyIdTypeTrack, trackID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("spotify streaming: track %q: %w", trackID, provider.ErrNotFound)
	}
	c, err := b.conn(ctx, login)
	if err != nil {
		return nil, nil, nil, err
	}
	var files audiofilespb.AudioFilesExtensionResponse
	if err := c.sp.ExtendedMetadataSimple(ctx, *id, extmetadatapb.ExtensionKind_AUDIO_FILES, &files); err != nil {
		if strings.Contains(err.Error(), "status 404") {
			return nil, nil, nil, fmt.Errorf("spotify streaming: track %s: %w", trackID, provider.ErrNotFound)
		}
		return nil, nil, nil, fmt.Errorf("spotify streaming: audio files of %s: %w: %w", trackID, provider.ErrUnavailable, err)
	}
	var all []*metadatapb.AudioFile
	for _, f := range files.Files {
		if f.GetFile() != nil {
			all = append(all, f.GetFile())
		}
	}
	file := pickFile(all, maxBitrate)
	if file == nil {
		return nil, nil, nil, fmt.Errorf("spotify streaming: track %s has no Ogg Vorbis audio: %w", trackID, provider.ErrNotPlayable)
	}
	key, err := b.key(ctx, c, login.Username, id.Id(), file.GetFileId())
	if err != nil {
		if errors.Is(err, ap.ErrAccesspointClosed) {
			b.drop(login.Username, c)
			return nil, nil, nil, fmt.Errorf("spotify streaming: %w: %w", provider.ErrUnavailable, err)
		}
		return nil, nil, nil, fmt.Errorf("spotify streaming: key for %s: %w", trackID, err)
	}
	return c, file, key, nil
}

// bitrates are the Ogg Vorbis formats, which are all we pass through.
var bitrates = map[metadatapb.AudioFile_Format]int{
	metadatapb.AudioFile_OGG_VORBIS_96:  96,
	metadatapb.AudioFile_OGG_VORBIS_160: 160,
	metadatapb.AudioFile_OGG_VORBIS_320: 320,
}

// pickFile returns the best Ogg Vorbis file within maxBitrate (0 means no
// cap), or the smallest if none is.
func pickFile(files []*metadatapb.AudioFile, maxBitrate int) *metadatapb.AudioFile {
	var best, smallest *metadatapb.AudioFile
	for _, f := range files {
		rate, ok := bitrates[f.GetFormat()]
		if !ok || len(f.GetFileId()) == 0 {
			continue
		}
		if smallest == nil || rate < bitrates[smallest.GetFormat()] {
			smallest = f
		}
		if (maxBitrate <= 0 || rate <= maxBitrate) && (best == nil || rate > bitrates[best.GetFormat()]) {
			best = f
		}
	}
	if best == nil {
		return smallest
	}
	return best
}

type keyEntry struct {
	key     []byte
	err     error // ErrNotPlayable, cached for refusedTTL
	expires time.Time
}

// key returns a file's decryption key, from the cache if it can.
func (b *Backend) key(ctx context.Context, c *conn, username string, gid, fileID []byte) ([]byte, error) {
	k := username + "/" + hex.EncodeToString(fileID)
	b.mu.Lock()
	e, ok := b.keys[k]
	b.mu.Unlock()
	if ok && b.now().Before(e.expires) {
		return e.key, e.err
	}
	key, err := c.keys.Request(ctx, gid, fileID)
	var kpe *audio.KeyProviderError
	switch {
	case err == nil:
		e = keyEntry{key: key, expires: b.now().Add(keyTTL)}
	case errors.As(err, &kpe) && kpe.Code == 1:
		// Spotify won't give this account the key, every time it's asked.
		e = keyEntry{err: fmt.Errorf("%w: %w", provider.ErrNotPlayable, err), expires: b.now().Add(refusedTTL)}
	case errors.As(err, &kpe) && kpe.Code == 2:
		// Too many key requests too fast.
		return nil, fmt.Errorf("%w: %w", &provider.RateLimitError{}, err)
	case errors.Is(err, ap.ErrAccesspointClosed):
		return nil, err
	default:
		return nil, fmt.Errorf("%w: %w", provider.ErrUnavailable, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.keys) >= maxKeys {
		now := b.now()
		for k, old := range b.keys {
			if now.After(old.expires) || len(b.keys) >= maxKeys {
				delete(b.keys, k)
			}
		}
	}
	b.keys[k] = e
	return e.key, e.err
}

// Check implements spotify.Audio. It asks for the key of the file Open
// would play, at any bitrate, and caches it.
func (b *Backend) Check(ctx context.Context, login spotify.Login, trackID string) error {
	_, _, _, err := b.track(ctx, login, trackID, 0)
	return err
}

// Open implements spotify.Audio.
func (b *Backend) Open(ctx context.Context, login spotify.Login, trackID string, opts spotify.AudioOpts) (spotify.AudioFile, error) {
	c, af, key, err := b.track(ctx, login, trackID, opts.MaxBitrate)
	if err != nil {
		return nil, err
	}
	res, err := c.sp.ResolveStorageInteractive(ctx, af.GetFileId(), af.GetFormat().Enum(), false)
	if err != nil {
		return nil, fmt.Errorf("spotify streaming: storage for %s: %w: %w", trackID, provider.ErrUnavailable, err)
	}
	if res.GetResult() != storagepb.StorageResolveResponse_CDN || len(res.GetCdnurl()) == 0 {
		return nil, fmt.Errorf("spotify streaming: storage for %s: result %s: %w", trackID, res.GetResult(), provider.ErrUnavailable)
	}
	raw, err := audio.NewHttpChunkedReader(b.log, b.client, res.GetCdnurl()[0])
	if err != nil {
		return nil, fmt.Errorf("spotify streaming: audio of %s: %w: %w", trackID, provider.ErrUnavailable, err)
	}
	if raw.Size() <= headerSize {
		_ = raw.Close()
		return nil, fmt.Errorf("spotify streaming: audio of %s is only %d bytes: %w", trackID, raw.Size(), provider.ErrUnavailable)
	}
	dec, err := audio.NewAesAudioDecryptor(raw, key)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	return &oggFile{raw: raw, dec: dec}, nil
}

// oggFile is a decrypted Ogg Vorbis file, without Spotify's header.
type oggFile struct {
	raw *audio.HttpChunkedReader
	dec io.ReaderAt
}

func (*oggFile) ContentType() string { return "audio/ogg" }

func (f *oggFile) Size() int64 { return f.raw.Size() - headerSize }

func (f *oggFile) ReadRange(_ context.Context, off, n int64) (io.ReadCloser, error) {
	return io.NopCloser(io.NewSectionReader(f.dec, headerSize+off, n)), nil
}

func (f *oggFile) Close() error { return f.raw.Close() }
