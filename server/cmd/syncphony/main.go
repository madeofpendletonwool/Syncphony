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

	"github.com/madeofpendletonwool/syncphony/server/internal/auth"
	"github.com/madeofpendletonwool/syncphony/server/internal/config"
	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/links"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/fake"
	"github.com/madeofpendletonwool/syncphony/server/internal/provider/navidrome"
	"github.com/madeofpendletonwool/syncphony/server/internal/realtime"
	"github.com/madeofpendletonwool/syncphony/server/internal/rooms"
	"github.com/madeofpendletonwool/syncphony/server/internal/store"
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
	reg, err := providers(cfg)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, filepath.Join(cfg.DataDir, "syncphony.db"))
	if err != nil {
		return nil, err
	}
	bus := realtime.NewLocal()
	return &app{
		cfg: cfg, db: db, bus: bus,
		links: links.New(db, v, reg, links.Config{BaseURL: cfg.BaseURL, Notifier: links.BusNotifier{Bus: bus}}),
	}, nil
}

// providers builds the registry of linkable services.
func providers(cfg config.Config) (*provider.Registry, error) {
	ps := []provider.Provider{navidrome.New(navidrome.Options{})}
	if cfg.FakeProvider {
		ps = append(ps, fake.New(fake.Options{}))
	}
	return provider.NewRegistry(ps...)
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

	accounts, err := auth.New(db, auth.Config{BaseURL: cfg.BaseURL})
	if err != nil {
		return err
	}
	if link, err := accounts.Bootstrap(ctx); err != nil {
		return err
	} else if link != "" {
		slog.Warn("no accounts yet: open this one-time link to create the admin account (valid 24h, renewed on restart)", "url", link)
	}
	go sweepSessions(ctx, db)

	api := &httpapi.Server{
		Version: version, Auth: accounts, Links: a.links,
		Rooms: rooms.New(db, a.bus), Bus: a.bus, Presence: realtime.NewPresence(),
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

// sweepSessions deletes expired sessions every hour until ctx is done.
func sweepSessions(ctx context.Context, db *store.Store) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := db.DeleteExpiredSessions(ctx, store.Now()); err != nil {
			slog.Warn("sweeping expired sessions", "err", err)
		} else if n > 0 {
			slog.Debug("swept expired sessions", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
