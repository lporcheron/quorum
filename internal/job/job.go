// Package job is the database-backed queue for background work —
// today, outgoing email. One worker goroutine polls for due jobs,
// retries failures with exponential backoff, and keeps exhausted jobs
// in the table (attempts ≥ maxAttempts) for inspection.
package job

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/internal/store/sqlite"
)

const (
	maxAttempts  = 10
	maxBatch     = 20
	pollInterval = 5 * time.Second
	baseBackoff  = time.Minute
	maxBackoff   = time.Hour
)

// Queue enqueues background jobs.
type Queue struct {
	store *store.Store
	now   func() time.Time
	wake  chan struct{}
}

// NewQueue wires a Queue; now is injectable for tests.
func NewQueue(st *store.Store, now func() time.Time) *Queue {
	if now == nil {
		now = time.Now
	}
	return &Queue{store: st, now: now, wake: make(chan struct{}, 1)}
}

// Enqueue schedules a job for immediate execution and nudges the
// worker so email latency stays low.
func (q *Queue) Enqueue(ctx context.Context, typ string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", typ, err)
	}
	now := store.FormatTime(q.now())
	_, err = q.store.CreateJob(ctx, sqlite.CreateJobParams{
		Type:      typ,
		Payload:   string(body),
		RunAt:     now,
		CreatedAt: now,
	})
	if err != nil {
		return fmt.Errorf("enqueue %s: %w", typ, err)
	}
	select {
	case q.wake <- struct{}{}:
	default: // a wake-up is already pending
	}
	return nil
}

// Handler executes one job; a non-nil error triggers a retry.
type Handler func(ctx context.Context, payload []byte) error

// Worker drains the queue.
type Worker struct {
	queue    *Queue
	log      *slog.Logger
	handlers map[string]Handler
}

// NewWorker wires a Worker over the queue.
func NewWorker(q *Queue, log *slog.Logger, handlers map[string]Handler) *Worker {
	return &Worker{queue: q, log: log, handlers: handlers}
}

// Run polls until ctx is cancelled. Enqueue wakes it early.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if _, err := w.ProcessDue(ctx); err != nil && ctx.Err() == nil {
			w.log.ErrorContext(ctx, "job worker pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-w.queue.wake:
		}
	}
}

// ProcessDue runs every currently-due job once and reports how many it
// attempted. Exposed for tests, which drive the worker synchronously.
func (w *Worker) ProcessDue(ctx context.Context) (int, error) {
	nowStr := store.FormatTime(w.queue.now())
	rows, err := w.queue.store.DueJobs(ctx, sqlite.DueJobsParams{
		Now:         nowStr,
		MaxAttempts: maxAttempts,
		MaxBatch:    maxBatch,
	})
	if err != nil {
		return 0, fmt.Errorf("list due jobs: %w", err)
	}
	for _, row := range rows {
		w.runOne(ctx, row)
	}
	return len(rows), nil
}

func (w *Worker) runOne(ctx context.Context, row sqlite.Job) {
	handler, ok := w.handlers[row.Type]
	err := fmt.Errorf("no handler for job type %q", row.Type)
	if ok {
		err = handler(ctx, []byte(row.Payload))
	}
	if err == nil {
		if err := w.queue.store.DeleteJob(ctx, row.ID); err != nil {
			w.log.ErrorContext(ctx, "delete completed job", "job", row.ID, "error", err)
		}
		w.log.InfoContext(ctx, "job done", "job", row.ID, "type", row.Type, "attempt", row.Attempts+1)
		return
	}

	attempts := row.Attempts + 1
	delay := backoff(int(attempts))
	if rerr := w.queue.store.RescheduleJob(ctx, sqlite.RescheduleJobParams{
		ID:        row.ID,
		Attempts:  attempts,
		LastError: nullString(err.Error()),
		RunAt:     store.FormatTime(w.queue.now().Add(delay)),
	}); rerr != nil {
		w.log.ErrorContext(ctx, "reschedule job", "job", row.ID, "error", rerr)
	}
	level := slog.LevelWarn
	if attempts >= maxAttempts {
		level = slog.LevelError
	}
	w.log.Log(ctx, level, "job failed",
		"job", row.ID, "type", row.Type, "attempt", attempts,
		"max_attempts", maxAttempts, "retry_in", delay.String(), "error", err.Error(),
	)
}

// backoff doubles from one minute, capped at one hour.
func backoff(attempt int) time.Duration {
	if attempt > 12 { // avoid shift overflow long before it matters
		return maxBackoff
	}
	d := baseBackoff << (attempt - 1)
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
