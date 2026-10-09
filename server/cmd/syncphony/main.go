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
	"strings"
	"syscall"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/admin"
	"github.com/madeofpendletonwool/syncphony/server/internal/analysis"
	"github.com/madeofpendletonwool/syncphony/server/internal/artwork"
	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/autopilot"
	"github.com/madeofpendletonwool/syncphony/server/internal/backup"
	"github.com/madeofpendletonwool/syncphony/server/internal/clips"
	"github.com/madeofpendletonwool/syncphony/server/internal/config"
	"github.com/madeofpendletonwool/syncphony/server/internal/dj"
	"github.com/madeofpendletonwool/syncphony/server/internal/games"
	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/linernotes"
	"github.com/madeofpendletonwool/syncphony/server/internal/links"
	"github.com/madeofpendletonwool/syncphony/server/internal/lyrics"
	"github.com/madeofpendletonwool/syncphony/server/internal/match"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicbrainz"
	"github.com/madeofpendletonwool/syncphony/server/internal/musicgraph"
	"github.com/madeofpendletonwool/syncphony/server/internal/nights"
	"github.com/madeofpendletonwool/syncphony/server/internal/palette"
	"github.com/madeofpendletonwool/syncphony/server/internal/playback"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/navidrome"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/nugs"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/spotify/streaming"
	"github.com/madeofpendletonwool/syncphony/server/internal/queue"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
	"github.com/madeofpendletonwool/syncphony/server/internal/suggest"
	"github.com/madeofpendletonwool/syncphony/server/internal/transcode"
	"github.com/madeofpendletonwool/syncphony/server/internal/vault"
	"github.com/madeofpendletonwool/syncphony/server/internal/webui"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var err error
	switch {
	case len(os.Args) > 1 && os.Args[1] == "vault":
		err = vaultCommand(os.Args[2:])
	case len(os.Args) > 1 && os.Args[1] == "backup":
		err = backupCommand(os.Args[2:])
	case len(os.Args) > 1 && os.Args[1] == "admin":
		err = adminCommand(os.Args[2:])
	default:
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
	// backups makes, rotates and restores database backups.
	backups *backup.Service
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
	backups, err := openBackups(cfg, keyFile)
	if err != nil {
		return nil, err
	}
	if p, err := backups.ApplyStaged(ctx); err != nil {
		return nil, err
	} else if p != nil {
		slog.Warn("restored the database from a backup", "from", p.From, "staged_at", p.StagedAt)
	}
	reg, closeProviders, err := providers(cfg)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, filepath.Join(cfg.DataDir, backup.DBFile), store.BeforeMigrate(func(ctx context.Context, s *store.Store, from, to int64) error {
		// A new version is about to change the database: keep it as it was.
		b, err := backups.BackupFrom(ctx, s, backup.PreUpgrade)
		if err != nil {
			slog.Error("couldn't back up the database before upgrading it; carrying on", "err", err)
			return nil
		}
		slog.Info("backed up the database before upgrading it", "file", b.Name, "schema", from, "to", to)
		return nil
	}))
	if err != nil {
		closeProviders()
		return nil, err
	}
	backups.DB = db
	backups.Schedule = admin.New(db).BackupSchedule
	bus := realtime.NewLocal()
	return &app{
		cfg: cfg, db: db, bus: bus, closeProviders: closeProviders, backups: backups,
		links: links.New(db, v, reg, links.Config{BaseURL: cfg.BaseURL, Notifier: links.BusNotifier{Bus: bus}}),
	}, nil
}

// openBackups sets up backups in cfg.BackupDir, warning if they can't be
// written or would sit next to the vault key that unlocks them.
func openBackups(cfg config.Config, keyFile string) (*backup.Service, error) {
	dir, err := filepath.Abs(cfg.BackupDir)
	if err != nil {
		return nil, err
	}
	b := backup.New(dir, cfg.DataDir)
	if err := b.Check(); err != nil {
		slog.Error("backups can't be written", "err", err)
	}
	b.CleanUp()
	if cfg.Vault.Key == "" {
		if cfg.Vault.KeyFile != "" {
			keyFile = cfg.Vault.KeyFile
		}
		if k, err := filepath.Abs(keyFile); err == nil && strings.HasPrefix(k, dir+string(filepath.Separator)) {
			slog.Warn("the vault key is in the backup folder: anyone with the backups can read linked services' credentials; keep it somewhere else", "key", k, "backups", dir)
		}
	}
	return b, nil
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
	if cfg.Nugs {
		ps = append(ps, nugs.New(nugs.Options{}))
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
	graph := newMusicGraph(cfg, db, mb, userAgent)
	if len(graph.Sources()) > 0 {
		go graph.Run(ctx)
		go repairMusicGraph(ctx, graph)
	}
	roomSvc := rooms.New(db, a.bus)
	queueSvc := queue.New(db, roomSvc, a.links)
	art := artwork.New(a.links, mb)
	palettes := palette.New(db, art)
	go palettes.Run(ctx)
	// Beat maps need ffmpeg to decode the songs.
	var beatMaps *analysis.Service
	if ff := (analysis.FFmpeg{}); ffmpegAvailable() {
		beatMaps = analysis.New(db, a.links, ff)
		go beatMaps.Run(ctx)
	} else {
		slog.Warn("ffmpeg not found: songs get no beat maps, so the app can't move with the music")
	}
	queueSvc.OnAdd = func(ts []provider.Track) {
		if mb != nil {
			mb.Enqueue(ts...)
		}
		palettes.Enqueue(ts...)
		if beatMaps != nil {
			beatMaps.Enqueue(ts...)
		}
		if len(graph.Sources()) > 0 {
			graph.Warm(ts...)
		}
	}
	var transcoder transcode.Transcoder
	if ff := (transcode.FFmpeg{}); ffmpegAvailable() {
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
	// Autopilot tops up rooms whose queue runs dry. It follows the same
	// queue and room changes as the player, after it.
	pilot := autopilot.New(db, roomSvc, queueSvc, a.links, presence)
	pilot.Player = player
	pilot.Memory = &dj.Memory{DB: db}
	// Suggestions (ADR 0010) ask the same DJ.
	suggestions := suggest.New(db, roomSvc, a.links)
	if len(graph.Sources()) > 0 {
		pilot.Graph = graph
		pilot.Memory.Graph = graph
		suggestions.Graph = graph
	}
	defer pilot.Close()
	onChange, onUpdate := queueSvc.OnChange, roomSvc.OnUpdate
	queueSvc.OnChange = func(roomID string) {
		onChange(roomID)
		pilot.Kick(roomID)
	}
	roomSvc.OnUpdate = func(r store.Room) {
		onUpdate(r)
		pilot.RoomUpdated(r)
	}
	// Party games (ADR 0015) follow the same playback and queue changes.
	gameSvc := games.New(db, a.bus, roomSvc, games.Config{})
	gameSources := &games.Sources{
		DB: db, LinerNotes: notes, Lyrics: lyricsSvc, Graph: graph, Providers: a.links,
		// Name that tune's new songs are the DJ's picks for the whole room.
		New: func(ctx context.Context, roomID string) ([]provider.Track, error) {
			row, err := roomSvc.Get(ctx, roomID)
			if err != nil {
				return nil, err
			}
			picks, err := suggestions.Suggest(ctx, suggest.Query{RoomID: roomID, UserID: row.OwnerID, Scope: suggest.ScopeGroup, Limit: 12})
			ts := make([]provider.Track, 0, len(picks))
			for _, p := range picks {
				ts = append(ts, p.Track)
			}
			return ts, err
		},
	}
	gameSvc.Facts = gameSources
	gameSvc.Music = player
	// Queue games (MAD-794..796) hold songs and put them at the front, and
	// connect the artists walks the music graph.
	gameSvc.Queue, gameSvc.Artists, gameSvc.Trim = queueSvc, gameSources, player
	// Games live in memory: nothing holds a song from before a restart.
	if err := queueSvc.ReleaseHolds(ctx); err != nil {
		slog.Warn("games: letting held songs back into the queue", "err", err)
	}
	// Name that tune's clips are cut with ffmpeg (MAD-792).
	var clipSvc *clips.Service
	if ffmpegAvailable() {
		clipSvc = clips.New(a.links, transcode.FFmpeg{}, clips.Config{})
		defer clipSvc.Close()
		gameSvc.Clips, gameSvc.Tunes = clipSvc, gameSources
	} else {
		slog.Warn("ffmpeg not found: no name that tune, which needs song clips")
	}
	gameSvc.OnHide = func(ctx context.Context, roomID string) {
		// Everyone gets the song again, with it left out or put back.
		if np, err := player.NowPlaying(ctx, roomID); err == nil {
			roomSvc.PublishNowPlaying(np)
		}
		if _, err := roomSvc.QueueChanged(ctx, roomID); err != nil {
			slog.Warn("games: resending the queue", "room", roomID, "err", err)
		}
	}
	defer gameSvc.Close()
	onChange2, onDelete := queueSvc.OnChange, roomSvc.OnDelete
	queueSvc.OnChange = func(roomID string) {
		onChange2(roomID)
		gameSvc.QueueChanged(roomID)
	}
	roomSvc.OnDelete = func(roomID string) {
		onDelete(roomID)
		gameSvc.RoomDeleted(roomID)
	}
	nightSvc := nights.New(db, a.bus)
	nightSvc.Awards = gameSvc.Awards
	nightSvc.Champion, nightSvc.Bracket = gameSvc.Champion, gameSvc.NightBracket
	go a.backups.Run(ctx)
	api := &httpapi.Server{
		Version: version, StartedAt: time.Now().UTC(), Admin: admin.New(db), Backups: a.backups, Auth: accounts, Links: a.links, Lyrics: lyricsSvc, LinerNotes: notes, Artwork: art, Palettes: palettes, BeatMaps: beatMaps,
		Rooms: roomSvc, Queue: queueSvc, Playback: player, Nights: nightSvc, Games: gameSvc, Clips: clipSvc, Suggest: suggestions, Autopilot: pilot,
		Bus: a.bus, Presence: presence,
		BaseURL: cfg.BaseURL, TrustedProxies: cfg.TrustedProxies,
	}
	if len(graph.Sources()) > 0 {
		api.Graph = graph
	}
	go sweep(ctx, db, accounts, roomSvc, lyricsSvc, mb, notes, graph, beatMaps)
	go tick(ctx, api, nightSvc)
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

// ffmpegAvailable reports whether ffmpeg is on $PATH, for transcoding and beat maps.
func ffmpegAvailable() bool { return (transcode.FFmpeg{}).Available() }

// tick ends guests whose time is up, and the night in rooms that went
// quiet, every minute until ctx is done.
func tick(ctx context.Context, api *httpapi.Server, ns *nights.Service) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if err := api.EndExpiredGuests(ctx); err != nil {
			slog.Warn("ending expired guests", "err", err)
		}
		if err := ns.Sweep(ctx); err != nil {
			slog.Warn("ending quiet rooms' nights", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// newMusicGraph sets up the DJ's music knowledge (ADR 0012) from the
// sources that are configured.
func newMusicGraph(cfg config.Config, db *store.Store, mb *musicbrainz.Service, userAgent string) *musicgraph.Service {
	var sources []musicgraph.Source
	if cfg.LastFMKey != "" {
		sources = append(sources, musicgraph.NewLastFM(musicgraph.LastFMOptions{Key: cfg.LastFMKey, UserAgent: userAgent}))
	} else {
		slog.Info("Last.fm is off: set SYNCPHONY_LASTFM_KEY for the best similar artists and songs")
	}
	if cfg.ListenBrainzURL != "" {
		sources = append(sources, musicgraph.NewListenBrainz(musicgraph.ListenBrainzOptions{
			BaseURL: cfg.ListenBrainzURL, Token: cfg.ListenBrainzToken, UserAgent: userAgent,
		}))
	}
	if cfg.DeezerURL != "" {
		sources = append(sources, musicgraph.NewDeezer(musicgraph.DeezerOptions{BaseURL: cfg.DeezerURL, UserAgent: userAgent}))
	}
	opts := musicgraph.Options{}
	if mb != nil {
		sources = append(sources, musicgraph.NewMusicBrainz(mb))
		opts.Finder = mb
	}
	opts.Sources = sources
	graph := musicgraph.New(db, opts)
	if len(sources) > 0 {
		slog.Info("music knowledge for the DJ", "sources", strings.Join(graph.Sources(), ", "))
	} else {
		slog.Info("music knowledge is off: no sources are configured")
	}
	return graph
}

// repairMusicGraph fetches again the music knowledge cached while a source
// was failing, so a fix or an outage that's over fills it in now.
func repairMusicGraph(ctx context.Context, graph *musicgraph.Service) {
	r, err := graph.Repair(ctx)
	switch {
	case err != nil && ctx.Err() == nil:
		slog.Warn("repairing music knowledge", "err", err)
	case r.Artists+r.Tracks > 0:
		slog.Info("repaired music knowledge", "artists", r.Artists, "tracks", r.Tracks, "complete", r.Complete)
	}
}

// sweep deletes expired sessions, displays and room invites, cached lyrics,
// MusicBrainz matches, liner notes and music knowledge every hour until ctx
// is done.
func sweep(ctx context.Context, db *store.Store, accounts *auth.Service, rs *rooms.Service, ly *lyrics.Service, mb *musicbrainz.Service, notes *linernotes.Service, graph *musicgraph.Service, beatMaps *analysis.Service) {
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
		if err := accounts.SweepGuestPasses(ctx); err != nil {
			slog.Warn("sweeping old guest passes", "err", err)
		}
		if err := rs.DeleteSpentInvites(ctx); err != nil {
			slog.Warn("sweeping spent room invites", "err", err)
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
		if err := graph.Sweep(ctx); err != nil {
			slog.Warn("sweeping expired music knowledge", "err", err)
		}
		if beatMaps != nil {
			if err := beatMaps.Sweep(ctx); err != nil {
				slog.Warn("sweeping unused beat maps", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
