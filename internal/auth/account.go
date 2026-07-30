package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lporcheron/quorum/internal/store/sqlite"
)

// ErrOwnsSharedSpace blocks deletion while the user still owns a space
// with other members: ownership must be transferred first, mirroring
// the owner-immovable rule of spaces.
var ErrOwnsSharedSpace = errors.New("transfer shared spaces before deleting the account")

// DeleteAccount erases the account and its personal data:
//
//   - participations (and their votes and comments) are deleted;
//   - polls in spaces where the user is alone (the personal space
//     included) are deleted;
//   - polls created in shared spaces stay with the team, detached from
//     the account — space admins keep managing them;
//   - solely-owned spaces, identities, login tokens and the user row go.
//
// Guest polls (claimable capability URLs) are untouched.
func (s *Service) DeleteAccount(ctx context.Context, userID int64) error {
	urow, err := s.store.GetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	shared, err := s.store.ListSharedOwnedSpaces(ctx, userID)
	if err != nil {
		return fmt.Errorf("list shared owned spaces: %w", err)
	}
	if len(shared) > 0 {
		return ErrOwnsSharedSpace
	}

	return s.store.Tx(ctx, func(q *sqlite.Queries) error {
		if err := q.DeleteCommentsByUserParticipants(ctx, nullInt64(userID)); err != nil {
			return fmt.Errorf("delete comments: %w", err)
		}
		if err := q.DeleteParticipantsByUser(ctx, nullInt64(userID)); err != nil {
			return fmt.Errorf("delete participations: %w", err)
		}

		soleSpaces, err := q.ListSolelyOwnedSpaceIDs(ctx, userID)
		if err != nil {
			return fmt.Errorf("list solely owned spaces: %w", err)
		}
		if err := q.SetUserPersonalSpace(ctx, sqlite.SetUserPersonalSpaceParams{ID: userID}); err != nil {
			return fmt.Errorf("clear personal space: %w", err)
		}
		for _, spaceID := range soleSpaces {
			if err := q.DeletePollsBySpace(ctx, nullInt64(spaceID)); err != nil {
				return fmt.Errorf("delete polls of space %d: %w", spaceID, err)
			}
		}
		// Polls created in shared spaces survive, ownerless: the space
		// keeps them and its admins keep managing them.
		if err := q.DetachPollsFromUser(ctx, nullInt64(userID)); err != nil {
			return fmt.Errorf("detach polls: %w", err)
		}
		for _, spaceID := range soleSpaces {
			if err := q.DeleteSpace(ctx, spaceID); err != nil {
				return fmt.Errorf("delete space %d: %w", spaceID, err)
			}
		}
		if err := q.DeleteLoginTokensByEmail(ctx, urow.Email); err != nil {
			return fmt.Errorf("delete login tokens: %w", err)
		}
		// Cascades identities and remaining memberships.
		if err := q.DeleteUser(ctx, userID); err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		return nil
	})
}

func nullInt64(n int64) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: n, Valid: true}
}
