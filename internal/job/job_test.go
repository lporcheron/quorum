package job

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/internal/store/sqlite"
)

var testNow = time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC)

type clock struct{ t time.Time }

func newTestQueue(t *testing.T) (context.Context, *Queue, *clock, *store.Store) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	st := store.New(db)
	c := &clock{t: testNow}
	return ctx, NewQueue(st, func() time.Time { return c.t }), c, st
}

func worker(q *Queue, handlers map[string]Handler) *Worker {
	return NewWorker(q, slog.New(slog.NewTextHandler(io.Discard, nil)), handlers)
}

func TestEnqueueProcessDelete(t *testing.T) {
	ctx, q, _, st := newTestQueue(t)
	var got atomic.Int32
	w := worker(q, map[string]Handler{
		"ping": func(_ context.Context, payload []byte) error {
			if string(payload) != `{"n":1}` {
				t.Errorf("payload = %s", payload)
			}
			got.Add(1)
			return nil
		},
	})

	if err := q.Enqueue(ctx, "ping", map[string]int{"n": 1}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	n, err := w.ProcessDue(ctx)
	if err != nil || n != 1 || got.Load() != 1 {
		t.Fatalf("ProcessDue: n=%d err=%v handled=%d", n, err, got.Load())
	}
	// Success deletes the job.
	if n, _ := w.ProcessDue(ctx); n != 0 {
		t.Errorf("job survived success: %d due", n)
	}
	if dead, _ := st.CountDeadJobs(ctx, maxAttempts); dead != 0 {
		t.Errorf("dead jobs = %d", dead)
	}
}

func TestRetryWithBackoffThenDead(t *testing.T) {
	ctx, q, c, st := newTestQueue(t)
	fails := 0
	w := worker(q, map[string]Handler{
		"boom": func(context.Context, []byte) error {
			fails++
			return errors.New("smtp down")
		},
	})
	if err := q.Enqueue(ctx, "boom", nil); err != nil {
		t.Fatal(err)
	}

	// First attempt fails; the job is rescheduled a minute later, so an
	// immediate second pass finds nothing.
	if n, _ := w.ProcessDue(ctx); n != 1 {
		t.Fatalf("first pass: %d", n)
	}
	if n, _ := w.ProcessDue(ctx); n != 0 {
		t.Fatalf("job due again immediately after failure")
	}
	c.t = c.t.Add(time.Minute)
	if n, _ := w.ProcessDue(ctx); n != 1 {
		t.Fatalf("job not due after backoff")
	}

	// Burn the remaining budget: each retry doubles, capped at 1 h.
	for i := 0; i < maxAttempts; i++ {
		c.t = c.t.Add(2 * time.Hour)
		if _, err := w.ProcessDue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if fails != maxAttempts {
		t.Errorf("handler ran %d times, want %d", fails, maxAttempts)
	}
	// The job is dead but kept, with its error, for inspection.
	dead, err := st.CountDeadJobs(ctx, maxAttempts)
	if err != nil || dead != 1 {
		t.Fatalf("dead = %d, %v", dead, err)
	}
	rows, err := st.DueJobs(ctx, sqlite.DueJobsParams{Now: store.FormatTime(c.t.Add(100 * time.Hour)), MaxAttempts: maxAttempts, MaxBatch: 10})
	if err != nil || len(rows) != 0 {
		t.Errorf("dead job still served to the worker")
	}
}

func TestUnknownTypeGoesDead(t *testing.T) {
	ctx, q, c, st := newTestQueue(t)
	w := worker(q, nil)
	if err := q.Enqueue(ctx, "mystery", nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxAttempts; i++ {
		if _, err := w.ProcessDue(ctx); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(2 * time.Hour)
	}
	if dead, _ := st.CountDeadJobs(ctx, maxAttempts); dead != 1 {
		t.Errorf("unknown-type job not parked as dead")
	}
}

func TestBackoffShape(t *testing.T) {
	if backoff(1) != time.Minute {
		t.Errorf("backoff(1) = %v", backoff(1))
	}
	if backoff(4) != 8*time.Minute {
		t.Errorf("backoff(4) = %v", backoff(4))
	}
	if backoff(7) != time.Hour || backoff(30) != time.Hour {
		t.Errorf("backoff cap broken: %v %v", backoff(7), backoff(30))
	}
}
