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
	"syscall"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/config"
	"github.com/madeofpendletonwool/syncphony/server/internal/httpapi"
	"github.com/madeofpendletonwool/syncphony/server/internal/webui"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel})))

	api := &httpapi.Server{Version: version}
	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler())
	mux.Handle("/", webui.Handler())

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
