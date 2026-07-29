// Package maintenance runs the housekeeping loop: warn organizers
// before their polls expire, purge polls past their horizon, and drop
// stale login tokens.
package maintenance

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/lporcheron/quorum/internal/notify"
	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/internal/store"
)

const (
	interval       = time.Hour
	reminderWindow = 7 * 24 * time.Hour
	batch          = 100
)

// Runner executes the periodic housekeeping.
type Runner struct {
	log      *slog.Logger
	polls    *poll.Service
	notifier *notify.Notifier
	store    *store.Store
	now      func() time.Time
}

// New wires a Runner; now is injectable for tests.
func New(log *slog.Logger, polls *poll.Service, notifier *notify.Notifier, st *store.Store, now func() time.Time) *Runner {
	if now == nil {
		now = time.Now
	}
	return &Runner{log: log, polls: polls, notifier: notifier, store: st, now: now}
}

// Run executes once at startup, then hourly, until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			r.log.ErrorContext(ctx, "maintenance pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce performs one housekeeping pass. Exposed for tests.
func (r *Runner) RunOnce(ctx context.Context) error {
	// Warn before deleting: organizers get one email inside the window.
	reminders, err := r.polls.NeedingReminder(ctx, reminderWindow, batch)
	if err != nil {
		return err
	}
	for _, p := range reminders {
		r.notifier.Remind(ctx, p)
		if err := r.polls.MarkReminded(ctx, p); err != nil {
			return err
		}
	}
	if len(reminders) > 0 {
		r.log.InfoContext(ctx, "expiry reminders scheduled", "count", len(reminders))
	}

	// Purge polls past their horizon.
	expired, err := r.polls.Expired(ctx, batch)
	if err != nil {
		return err
	}
	for _, p := range expired {
		if err := r.polls.Delete(ctx, p); err != nil {
			return fmt.Errorf("purge poll %s: %w", p.PublicID, err)
		}
		r.log.InfoContext(ctx, "poll purged", "poll", p.PublicID, "title", p.Title)
	}

	if err := r.store.DeleteExpiredLoginTokens(ctx, store.FormatTime(r.now())); err != nil {
		return fmt.Errorf("clean login tokens: %w", err)
	}
	return nil
}
