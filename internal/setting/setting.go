// Package setting serves the hot instance settings (settings table):
// values an operator changes from the admin page without restarting.
// Reads are cached; writes update the cache and the database together.
package setting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/internal/store/sqlite"
)

const (
	KeyInstanceName      = "instance_name"
	KeyRegistrationsOpen = "registrations_open"
)

// Service reads and writes settings. Safe for concurrent use.
type Service struct {
	store *store.Store

	mu    sync.RWMutex
	cache map[string]string

	// defaults apply when the table has no row for a key (typically
	// the environment values, kept as fallback).
	defaults map[string]string
}

// NewService wires the settings service with its fallback values.
func NewService(st *store.Store, defaultName string, defaultRegistrationsOpen bool) *Service {
	return &Service{
		store: st,
		cache: make(map[string]string),
		defaults: map[string]string{
			KeyInstanceName:      defaultName,
			KeyRegistrationsOpen: strconv.FormatBool(defaultRegistrationsOpen),
		},
	}
}

// get returns the setting value, cached after the first read.
func (s *Service) get(ctx context.Context, key string) string {
	s.mu.RLock()
	if v, ok := s.cache[key]; ok {
		s.mu.RUnlock()
		return v
	}
	s.mu.RUnlock()

	v, err := s.store.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		v = s.defaults[key]
	} else if err != nil {
		return s.defaults[key] // degraded read; do not cache
	}
	s.mu.Lock()
	s.cache[key] = v
	s.mu.Unlock()
	return v
}

// Set persists and caches a value.
func (s *Service) Set(ctx context.Context, key, value string) error {
	if err := s.store.UpsertSetting(ctx, sqlite.UpsertSettingParams{Key: key, Value: value}); err != nil {
		return fmt.Errorf("save setting %s: %w", key, err)
	}
	s.mu.Lock()
	s.cache[key] = value
	s.mu.Unlock()
	return nil
}

// InstanceName is shown in the header and page titles.
func (s *Service) InstanceName(ctx context.Context) string {
	if v := s.get(ctx, KeyInstanceName); v != "" {
		return v
	}
	return "Quorum"
}

// RegistrationsOpen gates new account creation.
func (s *Service) RegistrationsOpen(ctx context.Context) bool {
	v, err := strconv.ParseBool(s.get(ctx, KeyRegistrationsOpen))
	if err != nil {
		return true
	}
	return v
}
