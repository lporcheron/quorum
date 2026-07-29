// Package poll implements the core domain: polls, options, guest
// participation, votes, tallies and comments. No HTTP in here; handlers
// stay thin and this package stays testable on an in-memory database.
package poll

import (
	"errors"
	"time"

	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/internal/store/sqlite"
)

type Kind string

const (
	KindTimed  Kind = "timed"
	KindAllDay Kind = "allday"
)

type Status string

const (
	StatusLive      Status = "live"
	StatusPaused    Status = "paused"
	StatusFinalized Status = "finalized"
	StatusCancelled Status = "cancelled"
)

type VoteValue string

const (
	VoteYes      VoteValue = "yes"
	VoteIfNeedBe VoteValue = "ifneedbe"
	VoteNo       VoteValue = "no"
)

// ParseVoteValue validates form input.
func ParseVoteValue(s string) (VoteValue, bool) {
	switch VoteValue(s) {
	case VoteYes, VoteIfNeedBe, VoteNo:
		return VoteValue(s), true
	}
	return "", false
}

// Domain errors. Handlers map these to HTTP statuses and localized
// messages; the service never formats user-facing text.
var (
	ErrNotFound         = errors.New("not found")
	ErrForbidden        = errors.New("forbidden")
	ErrTitleRequired    = errors.New("title required")
	ErrNoOptions        = errors.New("at least one option required")
	ErrBadTimezone      = errors.New("invalid timezone")
	ErrDuplicateOption  = errors.New("duplicate option")
	ErrNameRequired     = errors.New("participant name required")
	ErrEmailRequired    = errors.New("participant email required")
	ErrPollClosed       = errors.New("poll is not open for votes")
	ErrCommentsDisabled = errors.New("comments are disabled")
	ErrBodyRequired     = errors.New("comment body required")
	ErrNotFinalizable   = errors.New("poll cannot be finalized in its current state")
	ErrNotFinalized     = errors.New("poll is not finalized")
)

// Poll is the domain view of a poll row.
type Poll struct {
	ID                int64
	PublicID          string
	Title             string
	Description       string
	Location          string
	VideoURL          string
	Kind              Kind
	Timezone          string // IANA name; empty iff Kind == KindAllDay
	Status            Status
	HideParticipants  bool
	RequireVoterEmail bool
	AllowComments     bool
	CreatedAt         time.Time
	// CreatedByUserID is 0 while the poll is unclaimed (guest-created).
	CreatedByUserID int64
	// SpaceID is 0 while the poll is unclaimed.
	SpaceID int64
	// RetentionDays is the effective inactivity horizon (never 0).
	RetentionDays int
	// FinalizedOptionID is 0 until the organizer picks the date.
	FinalizedOptionID int64
	// DeletesAt is when the purge may collect the poll.
	DeletesAt time.Time

	adminTokenHash string
}

// Claimed reports whether an account owns this poll.
func (p Poll) Claimed() bool { return p.CreatedByUserID != 0 }

// Open reports whether the poll currently accepts votes.
func (p Poll) Open() bool { return p.Status == StatusLive }

// TZ resolves the poll timezone (UTC for all-day polls, which never
// use it for display).
func (p Poll) TZ() *time.Location {
	if p.Timezone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Option is one proposed slot (timed) or day (all-day).
type Option struct {
	ID       int64
	StartsAt time.Time     // timed only, UTC
	Duration time.Duration // timed only, > 0
	Date     Date          // all-day only
	Position int
}

func (o Option) AllDay() bool { return !o.Date.IsZero() }

func (o Option) EndsAt() time.Time { return o.StartsAt.Add(o.Duration) }

// Participant is a voter, with or without an account.
type Participant struct {
	ID        int64
	PublicID  string
	Name      string
	Email     string
	CreatedAt time.Time
}

// Comment is a message on the poll page.
type Comment struct {
	ID            int64
	PublicID      string
	ParticipantID int64 // 0 when the author was not a participant
	AuthorName    string
	Body          string
	CreatedAt     time.Time
}

// View is everything the poll page needs, computed in one place.
type View struct {
	Options      []Option
	Participants []Participant
	// Votes maps participant ID → option ID → value. A missing entry is
	// "no answer": the option was added after this participant voted.
	Votes    map[int64]map[int64]VoteValue
	Tallies  []Tally // aligned with Options
	Comments []Comment
}

func pollFromRow(r sqlite.Poll) (Poll, error) {
	createdAt, err := store.ParseTime(r.CreatedAt)
	if err != nil {
		return Poll{}, err
	}
	retentionDays := int(r.RetentionDays.Int64)
	if retentionDays <= 0 {
		retentionDays = DefaultRetentionDays
	}
	var deletesAt time.Time
	if r.DeletesAt.Valid {
		if t, err := store.ParseTime(r.DeletesAt.String); err == nil {
			deletesAt = t
		}
	}
	return Poll{
		ID:                r.ID,
		PublicID:          r.PublicID,
		Title:             r.Title,
		Description:       r.Description,
		Location:          r.Location,
		VideoURL:          r.VideoUrl,
		Kind:              Kind(r.Kind),
		Timezone:          r.Timezone.String,
		Status:            Status(r.Status),
		HideParticipants:  r.HideParticipants != 0,
		RequireVoterEmail: r.RequireVoterEmail != 0,
		AllowComments:     r.AllowComments != 0,
		CreatedAt:         createdAt,
		CreatedByUserID:   r.CreatedByUserID.Int64,
		SpaceID:           r.SpaceID.Int64,
		RetentionDays:     retentionDays,
		FinalizedOptionID: r.FinalizedOptionID.Int64,
		DeletesAt:         deletesAt,
		adminTokenHash:    r.AdminTokenHash,
	}, nil
}

func optionFromRow(r sqlite.PollOption) (Option, error) {
	o := Option{ID: r.ID, Position: int(r.Position)}
	if r.AllDayDate.Valid {
		d, err := ParseDate(r.AllDayDate.String)
		if err != nil {
			return Option{}, err
		}
		o.Date = d
		return o, nil
	}
	startsAt, err := store.ParseTime(r.StartsAt.String)
	if err != nil {
		return Option{}, err
	}
	o.StartsAt = startsAt
	o.Duration = time.Duration(r.DurationMinutes.Int64) * time.Minute
	return o, nil
}

func participantFromRow(r sqlite.Participant) (Participant, error) {
	createdAt, err := store.ParseTime(r.CreatedAt)
	if err != nil {
		return Participant{}, err
	}
	return Participant{
		ID:        r.ID,
		PublicID:  r.PublicID,
		Name:      r.Name,
		Email:     r.Email.String,
		CreatedAt: createdAt,
	}, nil
}

func commentFromRow(r sqlite.Comment) (Comment, error) {
	createdAt, err := store.ParseTime(r.CreatedAt)
	if err != nil {
		return Comment{}, err
	}
	return Comment{
		ID:            r.ID,
		PublicID:      r.PublicID,
		ParticipantID: r.ParticipantID.Int64,
		AuthorName:    r.AuthorName,
		Body:          r.Body,
		CreatedAt:     createdAt,
	}, nil
}
