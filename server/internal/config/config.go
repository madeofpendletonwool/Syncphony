// SPDX-License-Identifier: AGPL-3.0-only

// Package config loads server configuration from SYNCPHONY_* environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/vault"
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
	// TrustedProxies are reverse proxies whose X-Forwarded-For header is
	// believed when working out a client's IP (for login rate limits).
	TrustedProxies []netip.Prefix
	// Vault says where the credential vault's master key comes from. With
	// nothing set, a key file is generated in DataDir.
	Vault vault.KeySource
	// FakeProvider registers the in-memory "fake" provider, for UI
	// development without real services.
	FakeProvider bool
	// SpotifyClientID and SpotifyClientSecret are the Spotify developer
	// app's. Spotify is offered only when the client ID is set.
	SpotifyClientID, SpotifyClientSecret string
	// LRCLIBURL is the LRCLIB instance asked for lyrics a song's own
	// service doesn't have, e.g. a self-hosted one. Empty turns it off.
	LRCLIBURL string
}

// defaultTrustedProxies are loopback and private networks, where a
// self-hosted reverse proxy (Caddy, a Docker network) usually lives.
const defaultTrustedProxies = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7"

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
	if !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
		return Config{}, fmt.Errorf("SYNCPHONY_BASE_URL %q: must start with http:// or https://", c.BaseURL)
	}
	c.Vault = vault.KeySource{Key: os.Getenv("SYNCPHONY_VAULT_KEY"), KeyFile: os.Getenv("SYNCPHONY_VAULT_KEY_FILE")}
	for k := range strings.SplitSeq(os.Getenv("SYNCPHONY_VAULT_OLD_KEYS"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			c.Vault.OldKeys = append(c.Vault.OldKeys, k)
		}
	}
	switch v := env("SYNCPHONY_FAKE_PROVIDER", "false"); v {
	case "true", "1":
		c.FakeProvider = true
	case "false", "0":
	default:
		return Config{}, fmt.Errorf("SYNCPHONY_FAKE_PROVIDER %q: want true or false", v)
	}
	c.SpotifyClientID = strings.TrimSpace(os.Getenv("SYNCPHONY_SPOTIFY_CLIENT_ID"))
	c.SpotifyClientSecret = strings.TrimSpace(os.Getenv("SYNCPHONY_SPOTIFY_CLIENT_SECRET"))
	switch u := strings.TrimRight(strings.TrimSpace(env("SYNCPHONY_LRCLIB_URL", "https://lrclib.net")), "/"); {
	case u == "off" || u == "none":
	case strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://"):
		c.LRCLIBURL = u
	default:
		return Config{}, fmt.Errorf("SYNCPHONY_LRCLIB_URL %q: want an http(s) URL, or off", u)
	}
	proxies := env("SYNCPHONY_TRUSTED_PROXIES", defaultTrustedProxies)
	if proxies != "none" {
		for p := range strings.SplitSeq(proxies, ",") {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(p))
			if err != nil {
				return Config{}, fmt.Errorf("SYNCPHONY_TRUSTED_PROXIES: %w", err)
			}
			c.TrustedProxies = append(c.TrustedProxies, prefix)
		}
	}
	return c, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
