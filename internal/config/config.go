// Package config loads the runtime configuration from environment
// variables. Every setting has a sane default: quorum starts with no
// configuration at all.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
)

// Config holds the runtime configuration.
type Config struct {
	// Addr is the listen address (QUORUM_ADDR, default ":8080").
	Addr string
	// BaseURL is the absolute public URL of the instance, used to build
	// links in pages and emails (QUORUM_BASE_URL, default
	// "http://localhost:8080").
	BaseURL string
	// DBPath is the SQLite database file path (QUORUM_DB_PATH, default
	// "quorum.db").
	DBPath string
	// LogLevel is the minimum slog level (QUORUM_LOG_LEVEL, default "info").
	LogLevel slog.Level
	// LogFormat is "json" (default) or "text" (QUORUM_LOG_FORMAT).
	LogFormat string
}

// Load builds a Config from the given environment lookup function
// (usually os.Getenv; injected for tests).
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:      ":8080",
		BaseURL:   "http://localhost:8080",
		DBPath:    "quorum.db",
		LogLevel:  slog.LevelInfo,
		LogFormat: "json",
	}

	if v := getenv("QUORUM_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := getenv("QUORUM_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := getenv("QUORUM_BASE_URL"); v != "" {
		u, err := url.Parse(v)
		if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return Config{}, fmt.Errorf("QUORUM_BASE_URL must be an absolute http(s) URL, got %q", v)
		}
		cfg.BaseURL = v
	}
	if v := getenv("QUORUM_LOG_LEVEL"); v != "" {
		var lvl slog.Level
		if err := lvl.UnmarshalText([]byte(v)); err != nil {
			return Config{}, fmt.Errorf("QUORUM_LOG_LEVEL must be debug, info, warn or error, got %q", v)
		}
		cfg.LogLevel = lvl
	}
	if v := getenv("QUORUM_LOG_FORMAT"); v != "" {
		if v != "json" && v != "text" {
			return Config{}, fmt.Errorf("QUORUM_LOG_FORMAT must be json or text, got %q", v)
		}
		cfg.LogFormat = v
	}
	if v := getenv("DATABASE_URL"); v != "" {
		return Config{}, fmt.Errorf("DATABASE_URL is set but PostgreSQL support is not available yet; unset it to use SQLite")
	}

	return cfg, nil
}
