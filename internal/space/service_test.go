package space

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/internal/store"
)

var testNow = time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC)

// fixture: a space owned by owner, with admin and member users, plus an
// outsider with their own account.
type fixture struct {
	svc     *Service
	sp      Space
	owner   auth.User
	admin   auth.User
	member  auth.User
	outside auth.User
}

func newFixture(t *testing.T) (context.Context, *fixture) {
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
	now := func() time.Time { return testNow }
	users := auth.NewService(st, now, true, nil)
	svc := NewService(st, now)

	newUser := func(sub, email string) auth.User {
		u, err := users.Complete(ctx, auth.Login{Provider: "test", Subject: sub, Email: email, EmailVerified: true, Name: sub}, auth.Defaults{})
		if err != nil {
			t.Fatalf("create user %s: %v", email, err)
		}
		return u
	}

	f := &fixture{
		svc:     svc,
		owner:   newUser("owner", "owner@example.com"),
		admin:   newUser("admin", "admin@example.com"),
		member:  newUser("member", "member@example.com"),
		outside: newUser("outside", "outside@example.com"),
	}
	f.sp, err = svc.Create(ctx, f.owner.ID, "Bleemeo")
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	join := func(u auth.User, role Role) {
		token, err := svc.Invite(ctx, f.sp, f.owner.ID, u.Email, role)
		if err != nil {
			t.Fatalf("invite %s: %v", u.Email, err)
		}
		if _, err := svc.Accept(ctx, token, u.ID); err != nil {
			t.Fatalf("accept %s: %v", u.Email, err)
		}
	}
	join(f.admin, RoleAdmin)
	join(f.member, RoleMember)
	return ctx, f
}

func TestMembershipIsTheGate(t *testing.T) {
	ctx, f := newFixture(t)

	for _, tc := range []struct {
		user auth.User
		want Role
	}{
		{f.owner, RoleOwner}, {f.admin, RoleAdmin}, {f.member, RoleMember},
	} {
		role, err := f.svc.Membership(ctx, f.sp.ID, tc.user.ID)
		if err != nil || role != tc.want {
			t.Errorf("Membership(%s) = %v, %v; want %v", tc.user.Name, role, err, tc.want)
		}
	}
	if _, err := f.svc.Membership(ctx, f.sp.ID, f.outside.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("outsider membership: %v, want ErrForbidden", err)
	}
	if _, err := f.svc.Membership(ctx, f.sp.ID, 0); !errors.Is(err, ErrForbidden) {
		t.Errorf("anonymous membership: %v, want ErrForbidden", err)
	}

	// Require honours the role order.
	if _, err := f.svc.Require(ctx, f.sp.ID, f.member.ID, RoleAdmin); !errors.Is(err, ErrForbidden) {
		t.Errorf("member passing an admin gate: %v", err)
	}
	if _, err := f.svc.Require(ctx, f.sp.ID, f.owner.ID, RoleAdmin); err != nil {
		t.Errorf("owner failing an admin gate: %v", err)
	}
}

func TestInvitationRules(t *testing.T) {
	ctx, f := newFixture(t)

	// Members cannot invite.
	if _, err := f.svc.Invite(ctx, f.sp, f.member.ID, "x@example.com", RoleMember); !errors.Is(err, ErrForbidden) {
		t.Errorf("member invited someone: %v", err)
	}
	// Nobody invites an owner role.
	if _, err := f.svc.Invite(ctx, f.sp, f.owner.ID, "x@example.com", RoleOwner); !errors.Is(err, ErrForbidden) {
		t.Errorf("owner-role invitation accepted: %v", err)
	}
	// Existing members are not re-invitable.
	if _, err := f.svc.Invite(ctx, f.sp, f.owner.ID, f.member.Email, RoleMember); !errors.Is(err, ErrAlreadyMember) {
		t.Errorf("re-inviting a member: %v", err)
	}
	// Garbage email.
	if _, err := f.svc.Invite(ctx, f.sp, f.owner.ID, "not-an-email", RoleMember); !errors.Is(err, ErrBadEmail) {
		t.Errorf("bad email: %v", err)
	}

	// Re-inviting the same address replaces the pending invitation.
	tok1, err := f.svc.Invite(ctx, f.sp, f.admin.ID, "new@example.com", RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	tok2, err := f.svc.Invite(ctx, f.sp, f.admin.ID, "New@Example.com", RoleAdmin)
	if err != nil {
		t.Fatalf("re-invite: %v", err)
	}
	if _, _, err := f.svc.InvitationByToken(ctx, tok1); !errors.Is(err, ErrInvalidInvitation) {
		t.Errorf("replaced invitation still valid: %v", err)
	}
	invs, err := f.svc.Invitations(ctx, f.sp, f.owner.ID)
	if err != nil || len(invs) != 1 || invs[0].Role != RoleAdmin || invs[0].Email != "new@example.com" {
		t.Errorf("invitations = %+v, %v", invs, err)
	}

	// Accept consumes the token, once.
	if _, err := f.svc.Accept(ctx, tok2, f.outside.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if role, err := f.svc.Membership(ctx, f.sp.ID, f.outside.ID); err != nil || role != RoleAdmin {
		t.Errorf("accepted role = %v, %v", role, err)
	}
	if _, err := f.svc.Accept(ctx, tok2, f.outside.ID); !errors.Is(err, ErrInvalidInvitation) {
		t.Errorf("token reused: %v", err)
	}
}

func TestInvitationExpiry(t *testing.T) {
	ctx, f := newFixture(t)
	token, err := f.svc.Invite(ctx, f.sp, f.owner.ID, "late@example.com", RoleMember)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	f.svc.now = func() time.Time { return testNow.Add(8 * 24 * time.Hour) }
	if _, err := f.svc.Accept(ctx, token, f.outside.ID); !errors.Is(err, ErrInvalidInvitation) {
		t.Errorf("expired invitation accepted: %v", err)
	}
}

func TestRemovalRules(t *testing.T) {
	ctx, f := newFixture(t)

	// Member cannot remove anyone else.
	if err := f.svc.RemoveMember(ctx, f.sp, f.member.ID, f.admin.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("member removed an admin: %v", err)
	}
	// Admin cannot remove another admin...
	tok, _ := f.svc.Invite(ctx, f.sp, f.owner.ID, f.outside.Email, RoleAdmin)
	if _, err := f.svc.Accept(ctx, tok, f.outside.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RemoveMember(ctx, f.sp, f.admin.ID, f.outside.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("admin removed an admin: %v", err)
	}
	// ...nor the owner.
	if err := f.svc.RemoveMember(ctx, f.sp, f.admin.ID, f.owner.ID); !errors.Is(err, ErrOwnerImmovable) {
		t.Errorf("admin removed the owner: %v", err)
	}
	// The owner cannot leave either — transfer first.
	if err := f.svc.RemoveMember(ctx, f.sp, f.owner.ID, f.owner.ID); !errors.Is(err, ErrOwnerImmovable) {
		t.Errorf("owner left without transfer: %v", err)
	}
	// Admin removes a member; a member removes themself.
	if err := f.svc.RemoveMember(ctx, f.sp, f.admin.ID, f.member.ID); err != nil {
		t.Errorf("admin removing a member: %v", err)
	}
	if err := f.svc.RemoveMember(ctx, f.sp, f.outside.ID, f.outside.ID); err != nil {
		t.Errorf("self-removal: %v", err)
	}
}

func TestRolesAndTransfer(t *testing.T) {
	ctx, f := newFixture(t)

	// Only the owner changes roles.
	if err := f.svc.ChangeRole(ctx, f.sp, f.admin.ID, f.member.ID, RoleAdmin); !errors.Is(err, ErrForbidden) {
		t.Errorf("admin changed a role: %v", err)
	}
	if err := f.svc.ChangeRole(ctx, f.sp, f.owner.ID, f.member.ID, RoleAdmin); err != nil {
		t.Fatalf("promote member: %v", err)
	}
	if role, _ := f.svc.Membership(ctx, f.sp.ID, f.member.ID); role != RoleAdmin {
		t.Errorf("member role after promotion = %v", role)
	}
	// Nobody grants owner via ChangeRole.
	if err := f.svc.ChangeRole(ctx, f.sp, f.owner.ID, f.member.ID, RoleOwner); !errors.Is(err, ErrForbidden) {
		t.Errorf("owner role granted by ChangeRole: %v", err)
	}

	// Transfer: only the owner, only to a member.
	if err := f.svc.Transfer(ctx, f.sp, f.admin.ID, f.member.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("admin transferred ownership: %v", err)
	}
	if err := f.svc.Transfer(ctx, f.sp, f.owner.ID, f.outside.ID); !errors.Is(err, ErrNotAMember) {
		t.Errorf("transfer to outsider: %v", err)
	}
	if err := f.svc.Transfer(ctx, f.sp, f.owner.ID, f.admin.ID); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if role, _ := f.svc.Membership(ctx, f.sp.ID, f.admin.ID); role != RoleOwner {
		t.Errorf("new owner role = %v", role)
	}
	if role, _ := f.svc.Membership(ctx, f.sp.ID, f.owner.ID); role != RoleAdmin {
		t.Errorf("previous owner role = %v", role)
	}
	sp, _ := f.svc.ByID(ctx, f.sp.ID)
	if sp.OwnerUserID != f.admin.ID {
		t.Errorf("spaces.owner_user_id = %d", sp.OwnerUserID)
	}
	// And the old owner can now leave.
	if err := f.svc.RemoveMember(ctx, sp, f.owner.ID, f.owner.ID); err != nil {
		t.Errorf("previous owner leaving: %v", err)
	}
}

func TestSettings(t *testing.T) {
	ctx, f := newFixture(t)

	if err := f.svc.UpdateSettings(ctx, f.sp, f.member.ID, "X", "", 0); !errors.Is(err, ErrForbidden) {
		t.Errorf("member updated settings: %v", err)
	}
	if err := f.svc.UpdateSettings(ctx, f.sp, f.admin.ID, "Bleemeo Team", "Europe/Paris", 30); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	sp, _ := f.svc.ByID(ctx, f.sp.ID)
	if sp.Name != "Bleemeo Team" || sp.DefaultTimezone != "Europe/Paris" || sp.RetentionDays != 30 {
		t.Errorf("settings = %+v", sp)
	}
	if err := f.svc.UpdateSettings(ctx, f.sp, f.owner.ID, "X", "Mars/Olympus", 0); !errors.Is(err, ErrBadTimezone) {
		t.Errorf("bad timezone: %v", err)
	}
	if err := f.svc.UpdateSettings(ctx, f.sp, f.owner.ID, "X", "", 100000); !errors.Is(err, ErrBadRetention) {
		t.Errorf("bad retention: %v", err)
	}
	if err := f.svc.UpdateSettings(ctx, f.sp, f.owner.ID, "  ", "", 0); !errors.Is(err, ErrNameRequired) {
		t.Errorf("blank name: %v", err)
	}
}
