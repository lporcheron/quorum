package auth

import (
	"context"

	"errors"
	"testing"
	"time"

	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/internal/space"
	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/internal/store/storetest"
)

// accountFixture builds a user with: a poll in their personal space, a
// poll they created in a shared space owned by someone else, and a
// participation (with votes) on a third party's poll.
type accountFixture struct {
	users  *Service
	polls  *poll.Service
	spaces *space.Service
	st     *store.Store

	victim     User
	other      User
	personal   poll.Poll // victim's poll in their personal space
	teamPoll   poll.Poll // victim's poll in other's shared space
	otherPoll  poll.Poll // other's poll where victim voted
	victimPart poll.Participant
}

func newAccountFixture(t *testing.T) (context.Context, *accountFixture) {
	t.Helper()
	ctx := context.Background()
	_, st := storetest.Open(t)
	now := func() time.Time { return testNow }
	f := &accountFixture{
		users:  NewService(st, now, nil, nil),
		polls:  poll.NewService(st, now),
		spaces: space.NewService(st, now),
		st:     st,
	}

	mkUser := func(sub, email string) User {
		u, err := f.users.Complete(ctx, Login{Provider: "test", Subject: sub, Email: email, EmailVerified: true, Name: sub}, Defaults{})
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		return u
	}
	f.victim = mkUser("victim", "victim@example.com")
	f.other = mkUser("other", "other@example.com")

	mkPoll := func(title string, spaceID, creatorID int64) poll.Poll {
		p, _, err := f.polls.Create(ctx, poll.NewPoll{
			Title: title, Kind: poll.KindAllDay, AllowComments: true,
			Dates:   []poll.Date{{Year: 2026, Month: time.October, Day: 1}},
			SpaceID: spaceID, CreatedByUserID: creatorID,
		})
		if err != nil {
			t.Fatalf("create poll %s: %v", title, err)
		}
		return p
	}
	f.personal = mkPoll("Personal", f.victim.PersonalSpaceID, f.victim.ID)

	// A shared space owned by other, victim is a member and created a poll there.
	shared, err := f.spaces.Create(ctx, f.other.ID, "Team")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := f.spaces.Invite(ctx, shared, f.other.ID, f.victim.Email, space.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.spaces.Accept(ctx, tok, f.victim.ID); err != nil {
		t.Fatal(err)
	}
	f.teamPoll = mkPoll("Team poll", shared.ID, f.victim.ID)

	// Victim votes and comments on other's poll.
	f.otherPoll = mkPoll("Other poll", f.other.PersonalSpaceID, f.other.ID)
	v, err := f.polls.View(ctx, f.otherPoll)
	if err != nil {
		t.Fatal(err)
	}
	f.victimPart, _, err = f.polls.Join(ctx, f.otherPoll, "Victim", "victim@example.com", f.victim.ID,
		map[int64]poll.VoteValue{v.Options[0].ID: poll.VoteYes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.polls.AddComment(ctx, f.otherPoll, &f.victimPart, "", "see you there"); err != nil {
		t.Fatal(err)
	}
	return ctx, f
}

func TestDeleteAccountErasesPersonalData(t *testing.T) {
	ctx, f := newAccountFixture(t)

	if err := f.users.DeleteAccount(ctx, f.victim.ID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}

	// The account, its identity and personal-space poll are gone.
	if _, err := f.users.UserByID(ctx, f.victim.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("user still there: %v", err)
	}
	if _, err := f.polls.ByPublicID(ctx, f.personal.PublicID); !errors.Is(err, poll.ErrNotFound) {
		t.Errorf("personal poll survived: %v", err)
	}
	if _, err := f.spaces.ByID(ctx, f.victim.PersonalSpaceID); !errors.Is(err, space.ErrNotFound) {
		t.Errorf("personal space survived: %v", err)
	}
	// Signing in again with the same identity creates a fresh account.
	fresh, err := f.users.Complete(ctx, Login{Provider: "test", Subject: "victim", Email: "victim@example.com", EmailVerified: true, Name: "victim"}, Defaults{})
	if err != nil || fresh.ID == f.victim.ID {
		t.Errorf("re-registration after deletion: %v (id %d)", err, fresh.ID)
	}

	// The team keeps the shared-space poll, detached from the account.
	kept, err := f.polls.ByPublicID(ctx, f.teamPoll.PublicID)
	if err != nil {
		t.Fatalf("team poll gone: %v", err)
	}
	if kept.CreatedByUserID != 0 || kept.SpaceID != f.teamPoll.SpaceID {
		t.Errorf("team poll not detached: %+v", kept)
	}

	// Participation, votes and comments on other people's polls are erased.
	v, err := f.polls.View(ctx, f.otherPoll)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Participants) != 0 || len(v.Comments) != 0 {
		t.Errorf("participation survived: %d participants, %d comments", len(v.Participants), len(v.Comments))
	}
	// Other's data is untouched.
	if _, err := f.users.UserByID(ctx, f.other.ID); err != nil {
		t.Errorf("bystander account touched: %v", err)
	}
}

func TestDeleteAccountBlockedBySharedOwnership(t *testing.T) {
	ctx, f := newAccountFixture(t)

	// Victim now owns a shared space with a member in it.
	owned, err := f.spaces.Create(ctx, f.victim.ID, "Mine")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := f.spaces.Invite(ctx, owned, f.victim.ID, f.other.Email, space.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.spaces.Accept(ctx, tok, f.other.ID); err != nil {
		t.Fatal(err)
	}

	if err := f.users.DeleteAccount(ctx, f.victim.ID); !errors.Is(err, ErrOwnsSharedSpace) {
		t.Fatalf("deletion allowed while owning a shared space: %v", err)
	}
	// After transferring, deletion goes through.
	if err := f.spaces.Transfer(ctx, owned, f.victim.ID, f.other.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.users.DeleteAccount(ctx, f.victim.ID); err != nil {
		t.Fatalf("DeleteAccount after transfer: %v", err)
	}
	// The transferred space and its new owner are intact.
	if _, err := f.spaces.ByID(ctx, owned.ID); err != nil {
		t.Errorf("transferred space gone: %v", err)
	}
}

func TestDeleteAccountUnknownUser(t *testing.T) {
	ctx, f := newAccountFixture(t)
	if err := f.users.DeleteAccount(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
