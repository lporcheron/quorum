package space

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/lporcheron/quorum/internal/ids"
	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/internal/store/sqlite"
)

const invitationTTL = 7 * 24 * time.Hour

// Service owns all space operations.
type Service struct {
	store *store.Store
	now   func() time.Time
}

// NewService wires a Service; now is injectable for tests.
func NewService(st *store.Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: st, now: now}
}

// Membership is THE access check for space-scoped data: the caller's
// role, or ErrForbidden. Nothing else may decide access to a space.
func (s *Service) Membership(ctx context.Context, spaceID, userID int64) (Role, error) {
	if userID == 0 {
		return "", ErrForbidden
	}
	role, err := s.store.GetMembership(ctx, sqlite.GetMembershipParams{SpaceID: spaceID, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrForbidden
	}
	if err != nil {
		return "", fmt.Errorf("get membership: %w", err)
	}
	return Role(role), nil
}

// Require returns the caller's role iff it grants at least min.
func (s *Service) Require(ctx context.Context, spaceID, userID int64, min Role) (Role, error) {
	role, err := s.Membership(ctx, spaceID, userID)
	if err != nil {
		return "", err
	}
	if !role.AtLeast(min) {
		return "", ErrForbidden
	}
	return role, nil
}

// ByID loads a space by internal id.
func (s *Service) ByID(ctx context.Context, id int64) (Space, error) {
	row, err := s.store.GetSpaceByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Space{}, ErrNotFound
	}
	if err != nil {
		return Space{}, fmt.Errorf("get space: %w", err)
	}
	return spaceFromRow(row)
}

// BySlug loads a space by its URL slug.
func (s *Service) BySlug(ctx context.Context, slug string) (Space, error) {
	row, err := s.store.GetSpaceBySlug(ctx, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return Space{}, ErrNotFound
	}
	if err != nil {
		return Space{}, fmt.Errorf("get space by slug: %w", err)
	}
	return spaceFromRow(row)
}

// ForUser lists the spaces the user belongs to, with their role.
func (s *Service) ForUser(ctx context.Context, userID int64) ([]Membership, error) {
	rows, err := s.store.ListSpacesForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list spaces: %w", err)
	}
	out := make([]Membership, 0, len(rows))
	for _, r := range rows {
		sp, err := spaceFromRow(r.Space)
		if err != nil {
			return nil, err
		}
		out = append(out, Membership{Space: sp, Role: Role(r.Role)})
	}
	return out, nil
}

// Create makes a new space owned by userID.
func (s *Service) Create(ctx context.Context, userID int64, name string) (Space, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Space{}, ErrNameRequired
	}
	now := store.FormatTime(s.now())
	var out Space
	err := s.store.Tx(ctx, func(q *sqlite.Queries) error {
		row, err := q.CreateSpace(ctx, sqlite.CreateSpaceParams{
			PublicID:    ids.PublicID(),
			Slug:        strings.ToLower(ids.PublicID()),
			Name:        name,
			OwnerUserID: userID,
			CreatedAt:   now,
		})
		if err != nil {
			return fmt.Errorf("create space: %w", err)
		}
		if err := q.CreateSpaceMember(ctx, sqlite.CreateSpaceMemberParams{
			SpaceID: row.ID, UserID: userID, Role: string(RoleOwner), CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("create membership: %w", err)
		}
		out, err = spaceFromRow(row)
		return err
	})
	return out, err
}

// UpdateSettings edits name, default timezone and retention (admin+).
func (s *Service) UpdateSettings(ctx context.Context, sp Space, actorID int64, name, defaultTZ string, retentionDays int) error {
	if _, err := s.Require(ctx, sp.ID, actorID, RoleAdmin); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrNameRequired
	}
	if defaultTZ != "" && !validTimezone(defaultTZ) {
		return ErrBadTimezone
	}
	if retentionDays < 0 || retentionDays > 3650 {
		return ErrBadRetention
	}
	err := s.store.UpdateSpaceSettings(ctx, sqlite.UpdateSpaceSettingsParams{
		ID:              sp.ID,
		Name:            name,
		DefaultTimezone: nullString(defaultTZ),
		RetentionDays:   nullInt64(int64(retentionDays)),
	})
	if err != nil {
		return fmt.Errorf("update space settings: %w", err)
	}
	return nil
}

// Members lists the space's members (any member may look).
func (s *Service) Members(ctx context.Context, sp Space, actorID int64) ([]Member, error) {
	if _, err := s.Membership(ctx, sp.ID, actorID); err != nil {
		return nil, err
	}
	rows, err := s.store.ListSpaceMembers(ctx, sp.ID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	out := make([]Member, 0, len(rows))
	for _, r := range rows {
		since, err := store.ParseTime(r.MemberSince)
		if err != nil {
			return nil, err
		}
		out = append(out, Member{
			UserID: r.User.ID,
			Name:   r.User.Name,
			Email:  r.User.Email,
			Role:   Role(r.Role),
			Since:  since,
		})
	}
	return out, nil
}

// Invitations lists pending invitations (admin+).
func (s *Service) Invitations(ctx context.Context, sp Space, actorID int64) ([]Invitation, error) {
	if _, err := s.Require(ctx, sp.ID, actorID, RoleAdmin); err != nil {
		return nil, err
	}
	rows, err := s.store.ListSpaceInvitations(ctx, sp.ID)
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	out := make([]Invitation, 0, len(rows))
	for _, r := range rows {
		inv, err := invitationFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, nil
}

// Invite creates (or replaces) a pending invitation and returns its
// token — shown or emailed once, stored hashed.
func (s *Service) Invite(ctx context.Context, sp Space, actorID int64, email string, role Role) (string, error) {
	if _, err := s.Require(ctx, sp.ID, actorID, RoleAdmin); err != nil {
		return "", err
	}
	if role != RoleAdmin && role != RoleMember {
		return "", ErrForbidden
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return "", ErrBadEmail
	}
	// Refuse inviting someone already in.
	if urow, err := s.store.GetUserByEmail(ctx, email); err == nil {
		if _, err := s.Membership(ctx, sp.ID, urow.ID); err == nil {
			return "", ErrAlreadyMember
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("get user by email: %w", err)
	}

	token := ids.Token()
	now := s.now()
	err := s.store.Tx(ctx, func(q *sqlite.Queries) error {
		// Re-inviting replaces the pending invitation.
		if err := q.DeleteSpaceInvitationByEmail(ctx, sqlite.DeleteSpaceInvitationByEmailParams{
			SpaceID: sp.ID, Email: email,
		}); err != nil {
			return fmt.Errorf("replace invitation: %w", err)
		}
		_, err := q.CreateSpaceInvitation(ctx, sqlite.CreateSpaceInvitationParams{
			SpaceID:         sp.ID,
			Email:           email,
			Role:            string(role),
			TokenHash:       ids.HashToken(token),
			InvitedByUserID: actorID,
			ExpiresAt:       store.FormatTime(now.Add(invitationTTL)),
			CreatedAt:       store.FormatTime(now),
		})
		if err != nil {
			return fmt.Errorf("create invitation: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// CancelInvitation withdraws a pending invitation (admin+).
func (s *Service) CancelInvitation(ctx context.Context, sp Space, actorID, invitationID int64) error {
	if _, err := s.Require(ctx, sp.ID, actorID, RoleAdmin); err != nil {
		return err
	}
	if err := s.store.DeleteSpaceInvitation(ctx, sqlite.DeleteSpaceInvitationParams{ID: invitationID, SpaceID: sp.ID}); err != nil {
		return fmt.Errorf("delete invitation: %w", err)
	}
	return nil
}

// InvitationByToken resolves an invitation link for the accept page.
func (s *Service) InvitationByToken(ctx context.Context, token string) (Invitation, Space, error) {
	row, err := s.store.GetSpaceInvitationByTokenHash(ctx, ids.HashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, Space{}, ErrInvalidInvitation
	}
	if err != nil {
		return Invitation{}, Space{}, fmt.Errorf("get invitation: %w", err)
	}
	inv, err := invitationFromRow(row)
	if err != nil {
		return Invitation{}, Space{}, err
	}
	if inv.Expired(s.now()) {
		return Invitation{}, Space{}, ErrInvalidInvitation
	}
	sp, err := s.ByID(ctx, inv.SpaceID)
	if err != nil {
		return Invitation{}, Space{}, err
	}
	return inv, sp, nil
}

// Accept consumes an invitation for the signed-in user. Possession of
// the token is the authorization: it was delivered to the invited
// mailbox.
func (s *Service) Accept(ctx context.Context, token string, userID int64) (Space, error) {
	inv, sp, err := s.InvitationByToken(ctx, token)
	if err != nil {
		return Space{}, err
	}
	err = s.store.Tx(ctx, func(q *sqlite.Queries) error {
		if err := q.DeleteSpaceInvitation(ctx, sqlite.DeleteSpaceInvitationParams{ID: inv.ID, SpaceID: sp.ID}); err != nil {
			return fmt.Errorf("consume invitation: %w", err)
		}
		// Already a member (e.g. invited twice through different
		// addresses): consuming the invitation is enough.
		if _, err := q.GetMembership(ctx, sqlite.GetMembershipParams{SpaceID: sp.ID, UserID: userID}); err == nil {
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("get membership: %w", err)
		}
		if err := q.CreateSpaceMember(ctx, sqlite.CreateSpaceMemberParams{
			SpaceID: sp.ID, UserID: userID, Role: string(inv.Role), CreatedAt: store.FormatTime(s.now()),
		}); err != nil {
			return fmt.Errorf("create membership: %w", err)
		}
		return nil
	})
	if err != nil {
		return Space{}, err
	}
	return sp, nil
}

// RemoveMember enforces the removal rules: the owner leaves only by
// transferring first; owners remove anyone, admins remove members
// only; anyone may remove themself.
func (s *Service) RemoveMember(ctx context.Context, sp Space, actorID, targetID int64) error {
	actorRole, err := s.Membership(ctx, sp.ID, actorID)
	if err != nil {
		return err
	}
	targetRole, err := s.Membership(ctx, sp.ID, targetID)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			return ErrNotAMember
		}
		return err
	}
	if targetRole == RoleOwner {
		return ErrOwnerImmovable
	}
	allowed := actorID == targetID ||
		actorRole == RoleOwner ||
		(actorRole == RoleAdmin && targetRole == RoleMember)
	if !allowed {
		return ErrForbidden
	}
	n, err := s.store.DeleteSpaceMember(ctx, sqlite.DeleteSpaceMemberParams{SpaceID: sp.ID, UserID: targetID})
	if err != nil {
		return fmt.Errorf("delete member: %w", err)
	}
	if n != 1 {
		return ErrNotAMember
	}
	return nil
}

// ChangeRole promotes or demotes between admin and member (owner only).
func (s *Service) ChangeRole(ctx context.Context, sp Space, actorID, targetID int64, role Role) error {
	if _, err := s.Require(ctx, sp.ID, actorID, RoleOwner); err != nil {
		return err
	}
	if role != RoleAdmin && role != RoleMember {
		return ErrForbidden
	}
	targetRole, err := s.Membership(ctx, sp.ID, targetID)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			return ErrNotAMember
		}
		return err
	}
	if targetRole == RoleOwner {
		return ErrOwnerImmovable
	}
	err = s.store.UpdateMemberRole(ctx, sqlite.UpdateMemberRoleParams{
		SpaceID: sp.ID, UserID: targetID, Role: string(role),
	})
	if err != nil {
		return fmt.Errorf("update role: %w", err)
	}
	return nil
}

// Transfer hands ownership to another member; the previous owner
// becomes an admin.
func (s *Service) Transfer(ctx context.Context, sp Space, actorID, targetID int64) error {
	if _, err := s.Require(ctx, sp.ID, actorID, RoleOwner); err != nil {
		return err
	}
	if targetID == actorID {
		return nil
	}
	if _, err := s.Membership(ctx, sp.ID, targetID); err != nil {
		if errors.Is(err, ErrForbidden) {
			return ErrNotAMember
		}
		return err
	}
	return s.store.Tx(ctx, func(q *sqlite.Queries) error {
		if err := q.UpdateSpaceOwner(ctx, sqlite.UpdateSpaceOwnerParams{ID: sp.ID, OwnerUserID: targetID}); err != nil {
			return fmt.Errorf("update owner: %w", err)
		}
		if err := q.UpdateMemberRole(ctx, sqlite.UpdateMemberRoleParams{
			SpaceID: sp.ID, UserID: targetID, Role: string(RoleOwner),
		}); err != nil {
			return fmt.Errorf("promote new owner: %w", err)
		}
		if err := q.UpdateMemberRole(ctx, sqlite.UpdateMemberRoleParams{
			SpaceID: sp.ID, UserID: actorID, Role: string(RoleAdmin),
		}); err != nil {
			return fmt.Errorf("demote previous owner: %w", err)
		}
		return nil
	})
}

func validTimezone(tz string) bool {
	if tz == "" || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullInt64(n int64) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: n, Valid: true}
}
