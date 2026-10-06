// SPDX-License-Identifier: AGPL-3.0-only

// Command syncphony runs the Syncphony server: API, realtime, and web UI.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/artwork"
	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/config"
	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/linernotes"
	"github.com/madeofpendletonwool/syncphony/server/internal/links"
	"github.com/madeofpendletonwool/syncphony/server/internal/lyrics"
	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/palette"
	"github.com/madeofpendletonwool/syncphony/server/internal/playback"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/navidrome"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify/streaming"
	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/transcode"
	"github.com/madeofpendletonwool/syncphony/server/internal/vault"
	"github.com/madeofpendletonwool/syncphony/server/internal/webui"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var err error
	if len(os.Args) > 1 && os.Args[1] == "vault" {
		err = vaultCommand(os.Args[2:])
	} else {
		err = run()
	}
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// app is what both the server and the CLI commands need.
type app struct {
	cfg   config.Config
	db    *store.Store
	bus   realtime.Bus
	links *links.Service
	// closeProviders releases providers' connections.
	closeProviders func()
}

func setup(ctx context.Context) (*app, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel})))
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	keyFile := filepath.Join(cfg.DataDir, "vault.key")
	v, created, err := vault.Load(cfg.Vault, keyFile)
	if err != nil {
		return nil, err
	}
	if created {
		slog.Warn("generated a vault key for linked-service credentials; back it up, and for better protection move it out of the data directory (SYNCPHONY_VAULT_KEY or SYNCPHONY_VAULT_KEY_FILE)", "file", keyFile)
	}
	reg, closeProviders, err := providers(cfg)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, filepath.Join(cfg.DataDir, "syncphony.db"))
	if err != nil {
		closeProviders()
		return nil, err
	}
	bus := realtime.NewLocal()
	return &app{
		cfg: cfg, db: db, bus: bus, closeProviders: closeProviders,
		links: links.New(db, v, reg, links.Config{BaseURL: cfg.BaseURL, Notifier: links.BusNotifier{Bus: bus}}),
	}, nil
}

// providers builds the registry of linkable services, and returns a func
// that closes their connections.
func providers(cfg config.Config) (*provider.Registry, func(), error) {
	ps := []provider.Provider{navidrome.New(navidrome.Options{})}
	closeAll := func() {}
	if cfg.SpotifyClientID != "" {
		audio := streaming.New(nil)
		sp, err := spotify.New(spotify.Options{
			ClientID: cfg.SpotifyClientID, ClientSecret: cfg.SpotifyClientSecret,
			Audio: audio, Library: audio,
		})
		if err != nil {
			audio.Close()
			return nil, nil, err
		}
		ps = append(ps, sp)
		closeAll = func() { audio.Close() }
	} else {
		slog.Info("Spotify is off: set SYNCPHONY_SPOTIFY_CLIENT_ID and SYNCPHONY_SPOTIFY_CLIENT_SECRET to offer it")
	}
	if cfg.FakeProvider {
		ps = append(ps, fake.New(fake.Options{}))
	}
	reg, err := provider.NewRegistry(ps...)
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	return reg, closeAll, nil
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := setup(ctx)
	if err != nil {
		return err
	}
	cfg, db := a.cfg, a.db
	defer db.Close()
	defer a.closeProviders()

	accounts, err := auth.New(db, auth.Config{BaseURL: cfg.BaseURL})
	if err != nil {
		return err
	}
	if link, err := accounts.Bootstrap(ctx); err != nil {
		return err
	} else if link != "" {
		slog.Warn("no accounts yet: open this one-time link to create the admin account (valid 24h, renewed on restart)", "url", link)
	}
	// What MusicBrainz and LRCLIB ask clients to send: name/version (contact).
	userAgent := "Syncphony/" + version + " ( https://github.com/madeofpendletonwool/syncphony )"
	lyricsOpts := lyrics.Options{}
	if cfg.LRCLIBURL != "" {
		lyricsOpts.LRCLIB = &lyrics.LRCLIB{
			BaseURL:   cfg.LRCLIBURL,
			UserAgent: userAgent,
		}
	} else {
		slog.Info("LRCLIB is off: only lyrics from linked services are shown")
	}
	lyricsSvc := lyrics.New(db, lyricsOpts)
	var mb *musicbrainz.Service
	var notes *linernotes.Service
	if cfg.MusicBrainzURL != "" {
		mb = musicbrainz.New(db, musicbrainz.Options{BaseURL: cfg.MusicBrainzURL, UserAgent: userAgent})
		go mb.Run(ctx)
		notes = linernotes.New(db, mb, linernotes.Options{WikipediaURL: cfg.WikipediaURL, UserAgent: userAgent})
		if cfg.WikipediaURL == "" {
			slog.Info("Wikipedia is off: liner notes have no artist bios")
		}
	} else {
		slog.Info("MusicBrainz is off: artwork comes only from linked services, and there are no liner notes")
	}
	go sweep(ctx, db, accounts, lyricsSvc, mb, notes)

	roomSvc := rooms.New(db, a.bus)
	queueSvc := queue.New(db, roomSvc, a.links)
	art := artwork.New(a.links, mb)
	palettes := palette.New(db, art)
	go palettes.Run(ctx)
	queueSvc.OnAdd = func(ts []provider.Track) {
		if mb != nil {
			mb.Enqueue(ts...)
		}
		palettes.Enqueue(ts...)
	}
	var transcoder transcode.Transcoder
	if ff := (transcode.FFmpeg{}); ff.Available() {
		transcoder = ff
	} else {
		slog.Warn("ffmpeg not found: songs in formats the player can't decode won't play")
	}
	presence := realtime.NewPresence()
	player := playback.New(db, roomSvc, queueSvc, a.links, playback.Config{
		Transcoder: transcoder, Presence: presence, Matcher: match.New(db, a.links, presence),
	})
	defer player.Close()
	go player.Run(ctx)
	api := &httpapi.Server{
		Version: version, Auth: accounts, Links: a.links, Lyrics: lyricsSvc, LinerNotes: notes, Artwork: art, Palettes: palettes,
		Rooms: roomSvc, Queue: queueSvc, Playback: player, Bus: a.bus, Presence: presence,
		BaseURL: cfg.BaseURL, TrustedProxies: cfg.TrustedProxies,
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler())
	mux.Handle("GET /ws/rooms/{id}", api.RoomSocket())
	mux.Handle("/", webui.Handler())

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	slog.Info("listening", "addr", ln.Addr().String(), "base_url", cfg.BaseURL, "version", version)

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// sweep deletes expired sessions and displays, cached lyrics, MusicBrainz
// matches and liner notes every hour until ctx is done.
func sweep(ctx context.Context, db *store.Store, accounts *auth.Service, ly *lyrics.Service, mb *musicbrainz.Service, notes *linernotes.Service) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := db.DeleteExpiredSessions(ctx, store.Now()); err != nil {
			slog.Warn("sweeping expired sessions", "err", err)
		} else if n > 0 {
			slog.Debug("swept expired sessions", "count", n)
		}
		if err := accounts.SweepDisplays(ctx); err != nil {
			slog.Warn("sweeping expired displays", "err", err)
		}
		if err := ly.Sweep(ctx); err != nil {
			slog.Warn("sweeping expired lyrics", "err", err)
		}
		if mb != nil {
			if err := mb.Sweep(ctx); err != nil {
				slog.Warn("sweeping expired MusicBrainz matches", "err", err)
			}
			if err := notes.Sweep(ctx); err != nil {
				slog.Warn("sweeping expired liner notes", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
