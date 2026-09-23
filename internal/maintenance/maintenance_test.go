package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/internal/i18n"
	"github.com/lporcheron/quorum/internal/ids"
	"github.com/lporcheron/quorum/internal/job"
	"github.com/lporcheron/quorum/internal/mail"
	"github.com/lporcheron/quorum/internal/notify"
	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/internal/store/sqlite"
	"github.com/lporcheron/quorum/internal/store/storetest"
)

var start = time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC)

// enabledMailer turns notifications on; nothing is sent because no
// worker drains the queue in these tests.
type enabledMailer struct{}

func (enabledMailer) Enabled() bool                            { return true }
func (enabledMailer) Send(context.Context, mail.Message) error { return nil }

type fixture struct {
	ctx    context.Context
	store  *store.Store
	polls  *poll.Service
	runner *Runner
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, st := storetest.Open(t)
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	f := &fixture{ctx: context.Background(), store: st, now: start}
	clock := func() time.Time { return f.now }
	f.polls = poll.NewService(st, clock)
	users := auth.NewService(st, clock, auth.Policy{})
	notifier := notify.New(log, job.NewQueue(st, clock), enabledMailer{}, f.polls, users, tr,
		"https://quorum.example", "quorum@example.com", clock)
	f.runner = New(log, f.polls, notifier, st, clock)
	return f
}

func (f *fixture) createPoll(t *testing.T, userID, spaceID int64) poll.Poll {
	t.Helper()
	p, _, err := f.polls.Create(f.ctx, poll.NewPoll{
		Title:           "Team dinner",
		Kind:            poll.KindAllDay,
		Dates:           []poll.Date{{Year: 2026, Month: time.September, Day: 12}},
		SpaceID:         spaceID,
		CreatedByUserID: userID,
	})
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}
	return p
}

func (f *fixture) runOnce(t *testing.T) {
	t.Helper()
	if err := f.runner.RunOnce(f.ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}

// reminderJobs counts the expiry reminders queued for the poll.
func (f *fixture) reminderJobs(t *testing.T, p poll.Poll) int {
	t.Helper()
	jobs, err := f.store.DueJobs(f.ctx, sqlite.DueJobsParams{
		Now: store.FormatTime(f.now), MaxAttempts: 1, MaxBatch: 1000,
	})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	n := 0
	for _, j := range jobs {
		var payload struct {
			PollID string `json:"poll_id"`
			Kind   string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(j.Payload), &payload); err != nil {
			t.Fatalf("decode job payload: %v", err)
		}
		if j.Type == "email.notify" && payload.PollID == p.PublicID && payload.Kind == "reminder" {
			n++
		}
	}
	return n
}

func (f *fixture) exists(t *testing.T, p poll.Poll) bool {
	t.Helper()
	_, err := f.polls.ByPublicID(f.ctx, p.PublicID)
	if errors.Is(err, poll.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("ByPublicID: %v", err)
	}
	return true
}

func TestRunOncePollLifecycle(t *testing.T) {
	f := newFixture(t)
	users := auth.NewService(f.store, func() time.Time { return f.now }, auth.Policy{})
	alice, err := users.Complete(f.ctx, auth.Login{
		Provider: "google", Subject: "alice", Email: "alice@example.com", EmailVerified: true, Name: "Alice",
	}, auth.Defaults{})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	claimed := f.createPoll(t, alice.ID, alice.PersonalSpaceID)
	guest := f.createPoll(t, 0, 0)
	if !claimed.DeletesAt.Equal(guest.DeletesAt) {
		t.Fatalf("fixture polls must share a horizon: %v vs %v", claimed.DeletesAt, guest.DeletesAt)
	}
	horizon := claimed.DeletesAt

	// Far from the horizon: nothing to do.
	f.runOnce(t)
	if f.reminderJobs(t, claimed) != 0 || !f.exists(t, claimed) || !f.exists(t, guest) {
		t.Fatal("a fresh poll was reminded or purged")
	}

	// Inside the reminder window: the organizer is warned, once. A
	// guest poll has nobody to warn.
	f.now = horizon.Add(-reminderWindow + time.Hour)
	f.runOnce(t)
	f.runOnce(t)
	if n := f.reminderJobs(t, claimed); n != 1 {
		t.Errorf("claimed poll reminders = %d, want 1", n)
	}
	if n := f.reminderJobs(t, guest); n != 0 {
		t.Errorf("guest poll reminders = %d, want 0", n)
	}
	if !f.exists(t, claimed) || !f.exists(t, guest) {
		t.Fatal("a poll was purged before its horizon")
	}

	// Past the horizon: both polls are purged.
	f.now = horizon.Add(time.Second)
	f.runOnce(t)
	if f.exists(t, claimed) || f.exists(t, guest) {
		t.Error("expired polls survived the purge")
	}
}

func TestRunOncePurgesInBatches(t *testing.T) {
	f := newFixture(t)
	var polls []poll.Poll
	for range batch + 1 {
		polls = append(polls, f.createPoll(t, 0, 0))
	}
	f.now = polls[0].DeletesAt.Add(time.Second)

	f.runOnce(t)
	left := 0
	for _, p := range polls {
		if f.exists(t, p) {
			left++
		}
	}
	if left != 1 {
		t.Fatalf("after one pass %d polls left, want 1 (batch of %d)", left, batch)
	}
	f.runOnce(t)
	for _, p := range polls {
		if f.exists(t, p) {
			t.Fatal("the second pass did not finish the purge")
		}
	}
}

func TestRunOnceDropsExpiredLoginTokens(t *testing.T) {
	f := newFixture(t)
	create := func(expires time.Time) string {
		hash := ids.HashToken(ids.Token())
		_, err := f.store.CreateLoginToken(f.ctx, sqlite.CreateLoginTokenParams{
			Email: "alice@example.com", TokenHash: hash,
			ExpiresAt: store.FormatTime(expires), CreatedAt: store.FormatTime(start),
		})
		if err != nil {
			t.Fatalf("create login token: %v", err)
		}
		return hash
	}
	expired := create(start.Add(-time.Minute))
	live := create(start.Add(time.Minute))

	f.runOnce(t)
	if _, err := f.store.GetLoginTokenByHash(f.ctx, expired); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expired token: err = %v, want sql.ErrNoRows", err)
	}
	if _, err := f.store.GetLoginTokenByHash(f.ctx, live); err != nil {
		t.Errorf("live token was dropped: %v", err)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	f := newFixture(t)
	p := f.createPoll(t, 0, 0)
	f.now = p.DeletesAt.Add(time.Second)

	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- f.runner.Run(ctx) }()

	// Run makes a first pass immediately.
	deadline := time.Now().Add(5 * time.Second)
	for f.exists(t, p) {
		if time.Now().After(deadline) {
			t.Fatal("Run did not make its startup pass")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil on cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
