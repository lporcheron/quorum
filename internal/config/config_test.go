package config

import (
	"log/slog"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.BaseURL != "http://localhost:8080" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.DBPath != "quorum.db" {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q", cfg.LogFormat)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"QUORUM_ADDR":       "127.0.0.1:9999",
		"QUORUM_BASE_URL":   "https://polls.example.com",
		"QUORUM_DB_PATH":    "/data/q.db",
		"QUORUM_LOG_LEVEL":  "debug",
		"QUORUM_LOG_FORMAT": "text",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9999" || cfg.BaseURL != "https://polls.example.com" ||
		cfg.DBPath != "/data/q.db" || cfg.LogLevel != slog.LevelDebug || cfg.LogFormat != "text" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"bad base URL", map[string]string{"QUORUM_BASE_URL": "not-a-url"}, "QUORUM_BASE_URL"},
		{"relative base URL", map[string]string{"QUORUM_BASE_URL": "/polls"}, "QUORUM_BASE_URL"},
		{"bad log level", map[string]string{"QUORUM_LOG_LEVEL": "verbose"}, "QUORUM_LOG_LEVEL"},
		{"bad log format", map[string]string{"QUORUM_LOG_FORMAT": "xml"}, "QUORUM_LOG_FORMAT"},
		{"postgres not ready", map[string]string{"DATABASE_URL": "postgres://x"}, "DATABASE_URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(env(tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want mention of %s", err, tc.want)
			}
		})
	}
}
