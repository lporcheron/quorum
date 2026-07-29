package poll

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lporcheron/quorum/internal/store"
)

var testNow = time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC)

func newTestService(t *testing.T) (context.Context, *Service) {
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
	return ctx, NewService(store.New(db), func() time.Time { return testNow })
}

func createTimedPoll(t *testing.T, ctx context.Context, s *Service) (Poll, string) {
	t.Helper()
	p, adminToken, err := s.Create(ctx, NewPoll{
		Title:         "Team dinner",
		Kind:          KindTimed,
		Timezone:      "Europe/Paris",
		AllowComments: true,
		Slots: []TimedSlot{
			{Date: Date{2026, time.September, 12}, Hour: 19, Duration: 2 * time.Hour},
			{Date: Date{2026, time.September, 13}, Hour: 19, Duration: 2 * time.Hour},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return p, adminToken
}

func TestCreateValidation(t *testing.T) {
	ctx, s := newTestService(t)

	cases := []struct {
		name string
		in   NewPoll
		want error
	}{
		{"empty title", NewPoll{Kind: KindAllDay, Dates: []Date{{2026, 9, 1}}}, ErrTitleRequired},
		{"no options", NewPoll{Title: "x", Kind: KindAllDay}, ErrNoOptions},
		{"no slots", NewPoll{Title: "x", Kind: KindTimed, Timezone: "Europe/Paris"}, ErrNoOptions},
		{"bad tz", NewPoll{Title: "x", Kind: KindTimed, Timezone: "Mars/Olympus", Slots: []TimedSlot{{Date: Date{2026, 9, 1}, Hour: 10, Duration: time.Hour}}}, ErrBadTimezone},
		{"local tz", NewPoll{Title: "x", Kind: KindTimed, Timezone: "Local", Slots: []TimedSlot{{Date: Date{2026, 9, 1}, Hour: 10, Duration: time.Hour}}}, ErrBadTimezone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := s.Create(ctx, tc.in); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCreateStoresUTCAndDeduplicates(t *testing.T) {
	ctx, s := newTestService(t)
	p, _, err := s.Create(ctx, NewPoll{
		Title:    "Standup",
		Kind:     KindTimed,
		Timezone: "Europe/Paris",
		Slots: []TimedSlot{
			{Date: Date{2026, time.September, 12}, Hour: 19, Duration: time.Hour},
			{Date: Date{2026, time.September, 12}, Hour: 19, Duration: time.Hour}, // duplicate
			{Date: Date{2026, time.September, 11}, Hour: 19, Duration: time.Hour},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	v, err := s.View(ctx, p)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if len(v.Options) != 2 {
		t.Fatalf("len(options) = %d, want 2 (deduplicated)", len(v.Options))
	}
	// Chronological order, and 19:00 CEST == 17:00 UTC.
	if got := v.Options[0].StartsAt.Format(time.RFC3339); got != "2026-09-11T17:00:00Z" {
		t.Errorf("first option starts %s, want 2026-09-11T17:00:00Z", got)
	}
	if p.Timezone != "Europe/Paris" || v.Options[0].AllDay() {
		t.Errorf("poll shape wrong: tz=%q allday=%v", p.Timezone, v.Options[0].AllDay())
	}
}

func TestAdminToken(t *testing.T) {
	ctx, s := newTestService(t)
	p, adminToken := createTimedPoll(t, ctx, s)

	if _, err := s.Admin(ctx, p.PublicID, adminToken); err != nil {
		t.Fatalf("Admin with valid token: %v", err)
	}
	if _, err := s.Admin(ctx, p.PublicID, "wrong-token"); !errors.Is(err, ErrForbidden) {
		t.Errorf("wrong token: err = %v, want ErrForbidden", err)
	}
	if _, err := s.Admin(ctx, "nonexistent", adminToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown poll: err = %v, want ErrNotFound", err)
	}

	fresh, err := s.RegenerateAdminToken(ctx, p)
	if err != nil {
		t.Fatalf("RegenerateAdminToken: %v", err)
	}
	if _, err := s.Admin(ctx, p.PublicID, adminToken); !errors.Is(err, ErrForbidden) {
		t.Errorf("old token still works after rotation")
	}
	if _, err := s.Admin(ctx, p.PublicID, fresh); err != nil {
		t.Errorf("fresh token rejected: %v", err)
	}
}

func TestJoinVoteEditFlow(t *testing.T) {
	ctx, s := newTestService(t)
	p, _ := createTimedPoll(t, ctx, s)
	v, _ := s.View(ctx, p)
	opt1, opt2 := v.Options[0].ID, v.Options[1].ID

	alice, aliceToken, err := s.Join(ctx, p, "Alice", "alice@example.com", map[int64]VoteValue{opt1: VoteYes, opt2: VoteNo})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	_, _, err = s.Join(ctx, p, "Bob", "", map[int64]VoteValue{opt1: VoteIfNeedBe, opt2: VoteYes, 99999: VoteYes})
	if err != nil {
		t.Fatalf("Join bob: %v", err)
	}

	v, err = s.View(ctx, p)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if len(v.Participants) != 2 || len(v.Votes) != 2 {
		t.Fatalf("participants = %d votes = %d", len(v.Participants), len(v.Votes))
	}
	if v.Votes[alice.ID][opt1] != VoteYes {
		t.Errorf("alice vote = %v", v.Votes[alice.ID][opt1])
	}
	if _, ok := v.Votes[v.Participants[1].ID][99999]; ok {
		t.Error("vote on foreign option id was stored")
	}
	// opt1: 1 yes + 1 ifneedbe; opt2: 1 yes + 1 no → opt1 wins on tiebreak.
	if !v.Tallies[0].Winner || v.Tallies[1].Winner {
		t.Errorf("tallies = %+v", v.Tallies)
	}

	// Alice comes back through her personal link and flips her votes.
	got, err := s.ParticipantByToken(ctx, p, aliceToken)
	if err != nil || got.ID != alice.ID {
		t.Fatalf("ParticipantByToken: %v (got %+v)", err, got)
	}
	if err := s.UpdateVotes(ctx, p, got, "Alice L.", "alice@example.com", map[int64]VoteValue{opt2: VoteYes}); err != nil {
		t.Fatalf("UpdateVotes: %v", err)
	}
	v, _ = s.View(ctx, p)
	if v.Votes[alice.ID][opt2] != VoteYes {
		t.Errorf("updated vote = %v", v.Votes[alice.ID][opt2])
	}
	if _, ok := v.Votes[alice.ID][opt1]; ok {
		t.Error("opt1 vote should be gone (no answer) after update")
	}
	if v.Participants[0].Name != "Alice L." {
		t.Errorf("name = %q", v.Participants[0].Name)
	}

	if _, err := s.ParticipantByToken(ctx, p, "bogus"); !errors.Is(err, ErrNotFound) {
		t.Errorf("bogus token: %v", err)
	}
}

func TestJoinValidation(t *testing.T) {
	ctx, s := newTestService(t)
	p, _ := createTimedPoll(t, ctx, s)

	if _, _, err := s.Join(ctx, p, "  ", "", nil); !errors.Is(err, ErrNameRequired) {
		t.Errorf("blank name: %v", err)
	}

	strict, _, err := s.Create(ctx, NewPoll{
		Title: "Strict", Kind: KindAllDay, RequireVoterEmail: true,
		Dates: []Date{{2026, time.September, 1}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := s.Join(ctx, strict, "Carol", "", nil); !errors.Is(err, ErrEmailRequired) {
		t.Errorf("missing required email: %v", err)
	}
	if _, _, err := s.Join(ctx, strict, "Carol", "not-an-email", nil); !errors.Is(err, ErrEmailRequired) {
		t.Errorf("malformed required email: %v", err)
	}

	if err := s.SetPaused(ctx, p, true); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	paused, _ := s.ByPublicID(ctx, p.PublicID)
	if _, _, err := s.Join(ctx, paused, "Dave", "", nil); !errors.Is(err, ErrPollClosed) {
		t.Errorf("paused poll accepted a vote: %v", err)
	}
}

func TestOptionManagement(t *testing.T) {
	ctx, s := newTestService(t)
	p, _ := createTimedPoll(t, ctx, s)
	v, _ := s.View(ctx, p)

	// Add an option later; existing participants get "no answer" on it.
	if _, _, err := s.Join(ctx, p, "Alice", "", map[int64]VoteValue{v.Options[0].ID: VoteYes}); err != nil {
		t.Fatalf("Join: %v", err)
	}
	err := s.AddOptions(ctx, p, []TimedSlot{{Date: Date{2026, time.September, 14}, Hour: 19, Duration: 2 * time.Hour}}, nil)
	if err != nil {
		t.Fatalf("AddOptions: %v", err)
	}
	v, _ = s.View(ctx, p)
	if len(v.Options) != 3 {
		t.Fatalf("len(options) = %d, want 3", len(v.Options))
	}
	if v.Tallies[2].NoAnswer != 1 {
		t.Errorf("new option NoAnswer = %d, want 1", v.Tallies[2].NoAnswer)
	}
	// Kind mismatch is rejected.
	if err := s.AddOptions(ctx, p, nil, []Date{{2026, time.September, 20}}); err == nil {
		t.Error("all-day date accepted on a timed poll")
	}

	// Removing an option cascades its votes away.
	if err := s.RemoveOption(ctx, p, v.Options[0].ID); err != nil {
		t.Fatalf("RemoveOption: %v", err)
	}
	v, _ = s.View(ctx, p)
	if len(v.Options) != 2 {
		t.Fatalf("len(options) = %d after removal", len(v.Options))
	}
	for _, m := range v.Votes {
		if len(m) != 0 {
			t.Errorf("votes survived option removal: %v", m)
		}
	}
}

func TestComments(t *testing.T) {
	ctx, s := newTestService(t)
	p, _ := createTimedPoll(t, ctx, s)

	alice, _, err := s.Join(ctx, p, "Alice", "", nil)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	c1, err := s.AddComment(ctx, p, &alice, "ignored", "I prefer Saturday")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if c1.AuthorName != "Alice" || c1.ParticipantID != alice.ID {
		t.Errorf("participant comment attribution: %+v", c1)
	}
	c2, err := s.AddComment(ctx, p, nil, "Drive-by", "Either works")
	if err != nil {
		t.Fatalf("AddComment anonymous: %v", err)
	}

	if _, err := s.AddComment(ctx, p, nil, "", "hello"); !errors.Is(err, ErrNameRequired) {
		t.Errorf("anonymous comment without name: %v", err)
	}
	if _, err := s.AddComment(ctx, p, &alice, "", "  "); !errors.Is(err, ErrBodyRequired) {
		t.Errorf("empty body: %v", err)
	}

	// Alice deletes her own comment but not the other one.
	if err := s.RemoveOwnComment(ctx, p, alice, c2.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("deleting someone else's comment: %v", err)
	}
	if err := s.RemoveOwnComment(ctx, p, alice, c1.ID); err != nil {
		t.Errorf("deleting own comment: %v", err)
	}

	noComments, _, err := s.Create(ctx, NewPoll{
		Title: "Silent", Kind: KindAllDay, Dates: []Date{{2026, time.October, 1}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.AddComment(ctx, noComments, nil, "X", "hi"); !errors.Is(err, ErrCommentsDisabled) {
		t.Errorf("comment on disabled poll: %v", err)
	}
}

func TestDeletePollCascades(t *testing.T) {
	ctx, s := newTestService(t)
	p, _ := createTimedPoll(t, ctx, s)
	v, _ := s.View(ctx, p)
	if _, _, err := s.Join(ctx, p, "Alice", "", map[int64]VoteValue{v.Options[0].ID: VoteYes}); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if err := s.Delete(ctx, p); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.ByPublicID(ctx, p.PublicID); !errors.Is(err, ErrNotFound) {
		t.Errorf("poll still there: %v", err)
	}
}
