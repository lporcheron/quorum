// Package space implements organizations: membership, roles,
// invitations, settings. Membership is the only authorization
// primitive for space-scoped data — every access check goes through
// Service.Membership or Service.Require, never ad-hoc SQL.
package space

import (
	"errors"
	"time"

	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/internal/store/sqlite"
)

// Role orders as member < admin < owner. There is exactly one owner.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

func (r Role) rank() int {
	switch r {
	case RoleOwner:
		return 3
	case RoleAdmin:
		return 2
	case RoleMember:
		return 1
	}
	return 0
}

// AtLeast reports whether r grants what min requires.
func (r Role) AtLeast(min Role) bool { return r.rank() >= min.rank() }

var (
	ErrNotFound          = errors.New("not found")
	ErrForbidden         = errors.New("forbidden")
	ErrNameRequired      = errors.New("space name required")
	ErrBadTimezone       = errors.New("invalid timezone")
	ErrBadRetention      = errors.New("invalid retention")
	ErrBadEmail          = errors.New("invalid email")
	ErrAlreadyMember     = errors.New("already a member")
	ErrInvalidInvitation = errors.New("invalid or expired invitation")
	ErrOwnerImmovable    = errors.New("the owner cannot be removed or demoted; transfer ownership first")
	ErrNotAMember        = errors.New("target is not a member of this space")
)

// Space is the domain view of a space row.
type Space struct {
	ID              int64
	PublicID        string
	Slug            string
	Name            string
	OwnerUserID     int64
	DefaultTimezone string // empty = no preference
	RetentionDays   int    // 0 = instance default
	CreatedAt       time.Time
}

// Membership pairs a space with the user's role in it.
type Membership struct {
	Space Space
	Role  Role
}

// Member is one user of a space.
type Member struct {
	UserID int64
	Name   string
	Email  string
	Role   Role
	Since  time.Time
}

// Invitation is a pending, emailed offer to join.
type Invitation struct {
	ID        int64
	SpaceID   int64
	Email     string
	Role      Role
	ExpiresAt time.Time
	CreatedAt time.Time
}

func (i Invitation) Expired(now time.Time) bool { return now.After(i.ExpiresAt) }

func spaceFromRow(r sqlite.Space) (Space, error) {
	createdAt, err := store.ParseTime(r.CreatedAt)
	if err != nil {
		return Space{}, err
	}
	return Space{
		ID:              r.ID,
		PublicID:        r.PublicID,
		Slug:            r.Slug,
		Name:            r.Name,
		OwnerUserID:     r.OwnerUserID,
		DefaultTimezone: r.DefaultTimezone.String,
		RetentionDays:   int(r.RetentionDays.Int64),
		CreatedAt:       createdAt,
	}, nil
}

func invitationFromRow(r sqlite.SpaceInvitation) (Invitation, error) {
	expires, err := store.ParseTime(r.ExpiresAt)
	if err != nil {
		return Invitation{}, err
	}
	created, err := store.ParseTime(r.CreatedAt)
	if err != nil {
		return Invitation{}, err
	}
	return Invitation{
		ID:        r.ID,
		SpaceID:   r.SpaceID,
		Email:     r.Email,
		Role:      Role(r.Role),
		ExpiresAt: expires,
		CreatedAt: created,
	}, nil
}
