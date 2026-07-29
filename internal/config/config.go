// Package config loads the runtime configuration from environment
// variables. Every setting has a sane default: quorum starts with no
// configuration at all.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
)

// OAuthClient is one OAuth application registration.
type OAuthClient struct {
	ClientID     string
	ClientSecret string
}

func (c OAuthClient) Enabled() bool { return c.ClientID != "" && c.ClientSecret != "" }

// OIDC is a generic OpenID Connect provider configured by discovery,
// the option that matters most for self-hosting.
type OIDC struct {
	IssuerURL string // QUORUM_OIDC_ISSUER_URL
	Name      string // QUORUM_OIDC_NAME, shown on the login button
	OAuthClient
}

// SMTP configures outgoing email. Host empty = email features off.
type SMTP struct {
	Host     string // QUORUM_SMTP_HOST
	Port     int    // QUORUM_SMTP_PORT, default 587
	Username string // QUORUM_SMTP_USERNAME
	Password string // QUORUM_SMTP_PASSWORD
	From     string // QUORUM_SMTP_FROM
}

func (s SMTP) Enabled() bool { return s.Host != "" }

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

	// OAuth sign-in providers (QUORUM_OAUTH_<NAME>_CLIENT_ID/_CLIENT_SECRET).
	Google    OAuthClient
	GitHub    OAuthClient
	Microsoft OAuthClient
	// MicrosoftTenant scopes Microsoft sign-in
	// (QUORUM_OAUTH_MICROSOFT_TENANT, default "common").
	MicrosoftTenant string
	OIDC            OIDC
	SMTP            SMTP

	// RegistrationsOpen gates new account creation
	// (QUORUM_REGISTRATIONS_OPEN, default true). Existing users always
	// sign in.
	RegistrationsOpen bool
	// EmailAllowedDomains restricts sign-up emails when non-empty
	// (QUORUM_EMAIL_ALLOWED_DOMAINS, comma-separated).
	EmailAllowedDomains []string
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

	cfg.Google = oauthClient(getenv, "GOOGLE")
	cfg.GitHub = oauthClient(getenv, "GITHUB")
	cfg.Microsoft = oauthClient(getenv, "MICROSOFT")
	cfg.MicrosoftTenant = "common"
	if v := getenv("QUORUM_OAUTH_MICROSOFT_TENANT"); v != "" {
		cfg.MicrosoftTenant = v
	}
	cfg.OIDC = OIDC{
		IssuerURL:   getenv("QUORUM_OIDC_ISSUER_URL"),
		Name:        getenv("QUORUM_OIDC_NAME"),
		OAuthClient: oauthClient(getenv, "OIDC"),
	}
	if cfg.OIDC.IssuerURL != "" && !cfg.OIDC.Enabled() {
		return Config{}, fmt.Errorf("QUORUM_OIDC_ISSUER_URL is set but QUORUM_OAUTH_OIDC_CLIENT_ID/_CLIENT_SECRET are missing")
	}
	if cfg.OIDC.Name == "" {
		cfg.OIDC.Name = "SSO"
	}

	cfg.SMTP = SMTP{
		Host:     getenv("QUORUM_SMTP_HOST"),
		Port:     587,
		Username: getenv("QUORUM_SMTP_USERNAME"),
		Password: getenv("QUORUM_SMTP_PASSWORD"),
		From:     getenv("QUORUM_SMTP_FROM"),
	}
	if v := getenv("QUORUM_SMTP_PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil || port <= 0 || port > 65535 {
			return Config{}, fmt.Errorf("QUORUM_SMTP_PORT must be a port number, got %q", v)
		}
		cfg.SMTP.Port = port
	}
	if cfg.SMTP.Enabled() && cfg.SMTP.From == "" {
		return Config{}, fmt.Errorf("QUORUM_SMTP_FROM is required when QUORUM_SMTP_HOST is set")
	}

	cfg.RegistrationsOpen = true
	if v := getenv("QUORUM_REGISTRATIONS_OPEN"); v != "" {
		open, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("QUORUM_REGISTRATIONS_OPEN must be a boolean, got %q", v)
		}
		cfg.RegistrationsOpen = open
	}
	if v := getenv("QUORUM_EMAIL_ALLOWED_DOMAINS"); v != "" {
		for _, d := range strings.Split(v, ",") {
			if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
				cfg.EmailAllowedDomains = append(cfg.EmailAllowedDomains, d)
			}
		}
	}

	return cfg, nil
}

func oauthClient(getenv func(string) string, name string) OAuthClient {
	return OAuthClient{
		ClientID:     getenv("QUORUM_OAUTH_" + name + "_CLIENT_ID"),
		ClientSecret: getenv("QUORUM_OAUTH_" + name + "_CLIENT_SECRET"),
	}
}
