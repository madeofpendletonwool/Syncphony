// SPDX-License-Identifier: AGPL-3.0-only

// Package config loads server configuration from SYNCPHONY_* environment variables.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Config is the server configuration.
type Config struct {
	// Addr is the listen address, e.g. ":8080".
	Addr string
	// BaseURL is the public URL users reach the server at. Used for OAuth
	// redirects and links, e.g. "https://syncphony.example.com".
	BaseURL string
	// DataDir holds the SQLite database and other persistent state.
	DataDir string
	// LogLevel is one of debug, info, warn, error.
	LogLevel slog.Level
}

// Load reads configuration from the environment, applying defaults.
func Load() (Config, error) {
	c := Config{
		Addr:    env("SYNCPHONY_ADDR", ":8080"),
		BaseURL: strings.TrimRight(env("SYNCPHONY_BASE_URL", "http://localhost:8080"), "/"),
		DataDir: env("SYNCPHONY_DATA_DIR", "./data"),
	}
	if err := c.LogLevel.UnmarshalText([]byte(env("SYNCPHONY_LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("SYNCPHONY_LOG_LEVEL: %w", err)
	}
	return c, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
