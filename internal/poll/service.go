package poll

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lporcheron/quorum/internal/ids"
	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/internal/store/sqlite"
)

// DefaultRetentionDays is how long a poll lives without activity
// before the purge (M5) may collect it; every vote or comment pushes
// the horizon back. Spaces can override it per poll at creation time.
const DefaultRetentionDays = 180

// Service owns all poll operations.
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

// NewPoll is the creation input, options expressed in the poll's local
// wall-clock time (timed) or as civil dates (all-day).
type NewPoll struct {
	Title             string
	Description       string
	Location          string
	VideoURL          string
	Kind              Kind
	Timezone          string // required iff Kind == KindTimed
	HideParticipants  bool
	RequireVoterEmail bool
	AllowComments     bool
	NotifyOrganizer   bool
	Slots             []TimedSlot
	Dates             []Date
	// SpaceID/CreatedByUserID attach the poll to an account's space at
	// creation (0 for guest polls, claimable later).
	SpaceID         int64
	CreatedByUserID int64
	// RetentionDays overrides DefaultRetentionDays (0 = default),
	// typically from the space settings.
	RetentionDays int
}

// Details is the editable subset of a poll.
type Details struct {
	Title             string
	Description       string
	Location          string
	VideoURL          string
	HideParticipants  bool
	RequireVoterEmail bool
	AllowComments     bool
	NotifyOrganizer   bool
}

// Create validates and persists a poll with its options. It returns
// the poll and the admin token — the only time the token exists in
// clear on the server side.
func (s *Service) Create(ctx context.Context, in NewPoll) (Poll, string, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return Poll{}, "", ErrTitleRequired
	}
	var loc *time.Location
	switch in.Kind {
	case KindTimed:
		if !ValidTimezone(in.Timezone) {
			return Poll{}, "", ErrBadTimezone
		}
		loc, _ = time.LoadLocation(in.Timezone)
		if len(in.Slots) == 0 {
			return Poll{}, "", ErrNoOptions
		}
	case KindAllDay:
		if len(in.Dates) == 0 {
			return Poll{}, "", ErrNoOptions
		}
	default:
		return Poll{}, "", fmt.Errorf("unknown poll kind %q", in.Kind)
	}

	now := s.now()
	adminToken := ids.Token()
	timezone := sql.NullString{}
	if in.Kind == KindTimed {
		timezone = sql.NullString{String: in.Timezone, Valid: true}
	}

	retentionDays := in.RetentionDays
	if retentionDays <= 0 {
		retentionDays = DefaultRetentionDays
	}
	var row sqlite.Poll
	err := s.store.Tx(ctx, func(q *sqlite.Queries) error {
		var err error
		row, err = q.CreatePoll(ctx, sqlite.CreatePollParams{
			PublicID:          ids.PublicID(),
			SpaceID:           nullInt64(in.SpaceID),
			CreatedByUserID:   nullInt64(in.CreatedByUserID),
			AdminTokenHash:    ids.HashToken(adminToken),
			Title:             in.Title,
			Description:       strings.TrimSpace(in.Description),
			Location:          strings.TrimSpace(in.Location),
			VideoUrl:          strings.TrimSpace(in.VideoURL),
			Kind:              string(in.Kind),
			Timezone:          timezone,
			HideParticipants:  boolInt(in.HideParticipants),
			RequireVoterEmail: boolInt(in.RequireVoterEmail),
			AllowComments:     boolInt(in.AllowComments),
			NotifyOrganizer:   boolInt(in.NotifyOrganizer),
			RetentionDays:     nullInt64(int64(in.RetentionDays)),
			DeletesAt:         sql.NullString{String: store.FormatTime(now.Add(time.Duration(retentionDays) * 24 * time.Hour)), Valid: true},
			CreatedAt:         store.FormatTime(now),
			UpdatedAt:         store.FormatTime(now),
		})
		if err != nil {
			return fmt.Errorf("insert poll: %w", err)
		}
		return insertOptions(ctx, q, row.ID, 0, in.Slots, in.Dates, loc)
	})
	if err != nil {
		return Poll{}, "", err
	}
	p, err := pollFromRow(row)
	return p, adminToken, err
}

// insertOptions appends slots/dates as options starting at position
// next, deduplicating and ordering chronologically first.
func insertOptions(ctx context.Context, q *sqlite.Queries, pollID int64, next int, slots []TimedSlot, dates []Date, loc *time.Location) error {
	type opt struct {
		startsAt sql.NullString
		duration sql.NullInt64
		date     sql.NullString
		sortKey  string
	}
	var opts []opt
	seen := make(map[string]bool)
	for _, slot := range slots {
		if slot.Duration <= 0 {
			return fmt.Errorf("%w: slot duration must be positive", ErrNoOptions)
		}
		start := store.FormatTime(slot.StartUTC(loc))
		key := fmt.Sprintf("%s/%d", start, slot.Duration)
		if seen[key] {
			continue
		}
		seen[key] = true
		opts = append(opts, opt{
			startsAt: sql.NullString{String: start, Valid: true},
			duration: sql.NullInt64{Int64: int64(slot.Duration / time.Minute), Valid: true},
			sortKey:  start,
		})
	}
	for _, d := range dates {
		key := d.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		opts = append(opts, opt{
			date:    sql.NullString{String: d.String(), Valid: true},
			sortKey: d.String(),
		})
	}
	sort.Slice(opts, func(i, j int) bool { return opts[i].sortKey < opts[j].sortKey })

	for i, o := range opts {
		_, err := q.CreatePollOption(ctx, sqlite.CreatePollOptionParams{
			PollID:          pollID,
			StartsAt:        o.startsAt,
			DurationMinutes: o.duration,
			AllDayDate:      o.date,
			Position:        int64(next + i),
		})
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return ErrDuplicateOption
			}
			return fmt.Errorf("insert option: %w", err)
		}
	}
	return nil
}

// ByPublicID loads a poll for its public page.
func (s *Service) ByPublicID(ctx context.Context, publicID string) (Poll, error) {
	row, err := s.store.GetPollByPublicID(ctx, publicID)
	if errors.Is(err, sql.ErrNoRows) {
		return Poll{}, ErrNotFound
	}
	if err != nil {
		return Poll{}, fmt.Errorf("get poll: %w", err)
	}
	return pollFromRow(row)
}

// Admin loads a poll iff the capability token matches; possession of
// the admin URL is the only authorization in M1.
func (s *Service) Admin(ctx context.Context, publicID, token string) (Poll, error) {
	p, err := s.ByPublicID(ctx, publicID)
	if err != nil {
		return Poll{}, err
	}
	if !ids.MatchesHash(token, p.adminTokenHash) {
		return Poll{}, ErrForbidden
	}
	return p, nil
}

// View assembles everything the poll page shows.
func (s *Service) View(ctx context.Context, p Poll) (View, error) {
	rows, err := s.store.ListPollOptions(ctx, p.ID)
	if err != nil {
		return View{}, fmt.Errorf("list options: %w", err)
	}
	v := View{Votes: make(map[int64]map[int64]VoteValue)}
	for _, r := range rows {
		o, err := optionFromRow(r)
		if err != nil {
			return View{}, err
		}
		v.Options = append(v.Options, o)
	}

	prows, err := s.store.ListPollParticipants(ctx, p.ID)
	if err != nil {
		return View{}, fmt.Errorf("list participants: %w", err)
	}
	for _, r := range prows {
		pa, err := participantFromRow(r)
		if err != nil {
			return View{}, err
		}
		v.Participants = append(v.Participants, pa)
	}

	vrows, err := s.store.ListPollVotes(ctx, p.ID)
	if err != nil {
		return View{}, fmt.Errorf("list votes: %w", err)
	}
	records := make([]VoteRecord, 0, len(vrows))
	for _, r := range vrows {
		val, ok := ParseVoteValue(r.Value)
		if !ok {
			continue
		}
		records = append(records, VoteRecord{ParticipantID: r.ParticipantID, OptionID: r.OptionID, Value: val})
		m := v.Votes[r.ParticipantID]
		if m == nil {
			m = make(map[int64]VoteValue)
			v.Votes[r.ParticipantID] = m
		}
		m[r.OptionID] = val
	}
	v.Tallies = ComputeTallies(v.Options, len(v.Participants), records)

	crows, err := s.store.ListPollComments(ctx, p.ID)
	if err != nil {
		return View{}, fmt.Errorf("list comments: %w", err)
	}
	for _, r := range crows {
		c, err := commentFromRow(r)
		if err != nil {
			return View{}, err
		}
		v.Comments = append(v.Comments, c)
	}
	return v, nil
}

// ParticipantByToken resolves a personal edit link, scoped to p.
func (s *Service) ParticipantByToken(ctx context.Context, p Poll, token string) (Participant, error) {
	row, err := s.store.GetParticipantByEditTokenHash(ctx, ids.HashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return Participant{}, ErrNotFound
	}
	if err != nil {
		return Participant{}, fmt.Errorf("get participant: %w", err)
	}
	if row.PollID != p.ID {
		return Participant{}, ErrNotFound
	}
	return participantFromRow(row)
}

// validVotes keeps only known options with well-formed values.
func (s *Service) validVotes(ctx context.Context, p Poll, votes map[int64]VoteValue) (map[int64]VoteValue, error) {
	rows, err := s.store.ListPollOptions(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("list options: %w", err)
	}
	valid := make(map[int64]VoteValue, len(votes))
	for _, r := range rows {
		if val, ok := votes[r.ID]; ok {
			valid[r.ID] = val
		}
	}
	return valid, nil
}

func checkVoter(p Poll, name, email string) (string, string, error) {
	name = strings.TrimSpace(name)
	email = strings.ToLower(strings.TrimSpace(email))
	if !p.Open() {
		return "", "", ErrPollClosed
	}
	if name == "" {
		return "", "", ErrNameRequired
	}
	if p.RequireVoterEmail && (email == "" || !strings.Contains(email, "@")) {
		return "", "", ErrEmailRequired
	}
	return name, email, nil
}

// Join records a first-time voter and returns their personal edit
// token — shown once, stored hashed. userID links the participant to a
// signed-in account (0 for guests).
func (s *Service) Join(ctx context.Context, p Poll, name, email string, userID int64, votes map[int64]VoteValue) (Participant, string, error) {
	name, email, err := checkVoter(p, name, email)
	if err != nil {
		return Participant{}, "", err
	}
	votes, err = s.validVotes(ctx, p, votes)
	if err != nil {
		return Participant{}, "", err
	}

	now := s.now()
	editToken := ids.Token()
	var row sqlite.Participant
	err = s.store.Tx(ctx, func(q *sqlite.Queries) error {
		var err error
		row, err = q.CreateParticipant(ctx, sqlite.CreateParticipantParams{
			PublicID:      ids.PublicID(),
			PollID:        p.ID,
			Name:          name,
			Email:         nullString(email),
			UserID:        nullInt64(userID),
			EditTokenHash: ids.HashToken(editToken),
			CreatedAt:     store.FormatTime(now),
			UpdatedAt:     store.FormatTime(now),
		})
		if err != nil {
			return fmt.Errorf("insert participant: %w", err)
		}
		if err := insertVotes(ctx, q, row.ID, votes, now); err != nil {
			return err
		}
		return extendRetention(ctx, q, p, now)
	})
	if err != nil {
		return Participant{}, "", err
	}
	pa, err := participantFromRow(row)
	return pa, editToken, err
}

// UpdateVotes replaces a participant's votes (and name/email) — the
// personal edit link flow. Options absent from votes become "no answer".
func (s *Service) UpdateVotes(ctx context.Context, p Poll, participant Participant, name, email string, votes map[int64]VoteValue) error {
	name, email, err := checkVoter(p, name, email)
	if err != nil {
		return err
	}
	votes, err = s.validVotes(ctx, p, votes)
	if err != nil {
		return err
	}

	now := s.now()
	return s.store.Tx(ctx, func(q *sqlite.Queries) error {
		if err := q.UpdateParticipant(ctx, sqlite.UpdateParticipantParams{
			ID:        participant.ID,
			Name:      name,
			Email:     nullString(email),
			UpdatedAt: store.FormatTime(now),
		}); err != nil {
			return fmt.Errorf("update participant: %w", err)
		}
		if err := q.DeleteParticipantVotes(ctx, participant.ID); err != nil {
			return fmt.Errorf("clear votes: %w", err)
		}
		if err := insertVotes(ctx, q, participant.ID, votes, now); err != nil {
			return err
		}
		return extendRetention(ctx, q, p, now)
	})
}

func insertVotes(ctx context.Context, q *sqlite.Queries, participantID int64, votes map[int64]VoteValue, now time.Time) error {
	for optionID, val := range votes {
		if err := q.UpsertVote(ctx, sqlite.UpsertVoteParams{
			ParticipantID: participantID,
			OptionID:      optionID,
			Value:         string(val),
			UpdatedAt:     store.FormatTime(now),
		}); err != nil {
			return fmt.Errorf("insert vote: %w", err)
		}
	}
	return nil
}

func extendRetention(ctx context.Context, q *sqlite.Queries, p Poll, now time.Time) error {
	if err := q.ExtendPollRetention(ctx, sqlite.ExtendPollRetentionParams{
		ID:        p.ID,
		DeletesAt: sql.NullString{String: store.FormatTime(now.Add(time.Duration(p.RetentionDays) * 24 * time.Hour)), Valid: true},
	}); err != nil {
		return fmt.Errorf("extend retention: %w", err)
	}
	return nil
}

// RemoveParticipant deletes a voter and their votes (admin action, or
// the participant leaving via their edit link).
func (s *Service) RemoveParticipant(ctx context.Context, p Poll, participantID int64) error {
	if err := s.store.DeleteParticipant(ctx, sqlite.DeleteParticipantParams{ID: participantID, PollID: p.ID}); err != nil {
		return fmt.Errorf("delete participant: %w", err)
	}
	return nil
}

// UpdateDetails edits the poll header fields.
func (s *Service) UpdateDetails(ctx context.Context, p Poll, in Details) error {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return ErrTitleRequired
	}
	err := s.store.UpdatePollDetails(ctx, sqlite.UpdatePollDetailsParams{
		ID:                p.ID,
		Title:             in.Title,
		Description:       strings.TrimSpace(in.Description),
		Location:          strings.TrimSpace(in.Location),
		VideoUrl:          strings.TrimSpace(in.VideoURL),
		HideParticipants:  boolInt(in.HideParticipants),
		RequireVoterEmail: boolInt(in.RequireVoterEmail),
		AllowComments:     boolInt(in.AllowComments),
		NotifyOrganizer:   boolInt(in.NotifyOrganizer),
		UpdatedAt:         store.FormatTime(s.now()),
	})
	if err != nil {
		return fmt.Errorf("update poll: %w", err)
	}
	return nil
}

// AddOptions appends options to an existing poll (admin action).
func (s *Service) AddOptions(ctx context.Context, p Poll, slots []TimedSlot, dates []Date) error {
	if p.Kind == KindTimed && len(dates) > 0 || p.Kind == KindAllDay && len(slots) > 0 {
		return fmt.Errorf("%w: option kind does not match poll kind", ErrDuplicateOption)
	}
	if len(slots)+len(dates) == 0 {
		return ErrNoOptions
	}
	return s.store.Tx(ctx, func(q *sqlite.Queries) error {
		maxPos, err := q.MaxPollOptionPosition(ctx, p.ID)
		if err != nil {
			return fmt.Errorf("max position: %w", err)
		}
		return insertOptions(ctx, q, p.ID, int(maxPos)+1, slots, dates, p.TZ())
	})
}

// RemoveOption deletes an option; votes cascade away with it.
func (s *Service) RemoveOption(ctx context.Context, p Poll, optionID int64) error {
	if err := s.store.DeletePollOption(ctx, sqlite.DeletePollOptionParams{ID: optionID, PollID: p.ID}); err != nil {
		return fmt.Errorf("delete option: %w", err)
	}
	return nil
}

// SetPaused toggles voting between live and paused.
func (s *Service) SetPaused(ctx context.Context, p Poll, paused bool) error {
	status := StatusLive
	if paused {
		status = StatusPaused
	}
	err := s.store.UpdatePollStatus(ctx, sqlite.UpdatePollStatusParams{
		ID:        p.ID,
		Status:    string(status),
		UpdatedAt: store.FormatTime(s.now()),
	})
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}

// Finalize closes the poll on the chosen option.
func (s *Service) Finalize(ctx context.Context, p Poll, optionID int64) (Option, error) {
	if p.Status != StatusLive && p.Status != StatusPaused {
		return Option{}, ErrNotFinalizable
	}
	row, err := s.store.GetPollOption(ctx, sqlite.GetPollOptionParams{ID: optionID, PollID: p.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return Option{}, ErrNotFound
	}
	if err != nil {
		return Option{}, fmt.Errorf("get option: %w", err)
	}
	if err := s.store.FinalizePoll(ctx, sqlite.FinalizePollParams{
		ID:                p.ID,
		FinalizedOptionID: nullInt64(optionID),
		UpdatedAt:         store.FormatTime(s.now()),
	}); err != nil {
		return Option{}, fmt.Errorf("finalize poll: %w", err)
	}
	return optionFromRow(row)
}

// CancelEvent cancels a finalized event; the chosen option is kept so
// the CANCEL calendar object can reference it.
func (s *Service) CancelEvent(ctx context.Context, p Poll) error {
	if p.Status != StatusFinalized {
		return ErrNotFinalized
	}
	err := s.store.UpdatePollStatus(ctx, sqlite.UpdatePollStatusParams{
		ID:        p.ID,
		Status:    string(StatusCancelled),
		UpdatedAt: store.FormatTime(s.now()),
	})
	if err != nil {
		return fmt.Errorf("cancel event: %w", err)
	}
	return nil
}

// FinalizedOption loads the chosen option of a finalized poll.
func (s *Service) FinalizedOption(ctx context.Context, p Poll) (Option, error) {
	if p.FinalizedOptionID == 0 {
		return Option{}, ErrNotFinalized
	}
	row, err := s.store.GetPollOption(ctx, sqlite.GetPollOptionParams{ID: p.FinalizedOptionID, PollID: p.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return Option{}, ErrNotFound
	}
	if err != nil {
		return Option{}, fmt.Errorf("get finalized option: %w", err)
	}
	return optionFromRow(row)
}

// Expired lists polls past their retention horizon, oldest first.
func (s *Service) Expired(ctx context.Context, limit int) ([]Poll, error) {
	rows, err := s.store.ListExpiredPolls(ctx, sqlite.ListExpiredPollsParams{
		Now:     sql.NullString{String: store.FormatTime(s.now()), Valid: true},
		MaxRows: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list expired polls: %w", err)
	}
	return pollsFromRows(rows)
}

// NeedingReminder lists claimed polls expiring within the window whose
// organizer has not been warned yet.
func (s *Service) NeedingReminder(ctx context.Context, within time.Duration, limit int) ([]Poll, error) {
	rows, err := s.store.ListPollsNeedingReminder(ctx, sqlite.ListPollsNeedingReminderParams{
		Soon:    sql.NullString{String: store.FormatTime(s.now().Add(within)), Valid: true},
		MaxRows: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list polls needing reminder: %w", err)
	}
	return pollsFromRows(rows)
}

// MarkReminded records that the expiry warning went out.
func (s *Service) MarkReminded(ctx context.Context, p Poll) error {
	err := s.store.MarkPollReminded(ctx, sqlite.MarkPollRemindedParams{
		ID:         p.ID,
		RemindedAt: sql.NullString{String: store.FormatTime(s.now()), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("mark poll reminded: %w", err)
	}
	return nil
}

// Delete removes the poll and everything under it.
func (s *Service) Delete(ctx context.Context, p Poll) error {
	if err := s.store.DeletePoll(ctx, p.ID); err != nil {
		return fmt.Errorf("delete poll: %w", err)
	}
	return nil
}

// RegenerateAdminToken invalidates the current admin link and returns
// a fresh token.
func (s *Service) RegenerateAdminToken(ctx context.Context, p Poll) (string, error) {
	token := ids.Token()
	err := s.store.UpdatePollAdminTokenHash(ctx, sqlite.UpdatePollAdminTokenHashParams{
		ID:             p.ID,
		AdminTokenHash: ids.HashToken(token),
		UpdatedAt:      store.FormatTime(s.now()),
	})
	if err != nil {
		return "", fmt.Errorf("rotate admin token: %w", err)
	}
	return token, nil
}

// AddComment posts a comment; participant is nil for a drive-by
// commenter who only gives a name.
func (s *Service) AddComment(ctx context.Context, p Poll, participant *Participant, authorName, body string) (Comment, error) {
	if !p.AllowComments {
		return Comment{}, ErrCommentsDisabled
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return Comment{}, ErrBodyRequired
	}
	authorName = strings.TrimSpace(authorName)
	participantID := sql.NullInt64{}
	if participant != nil {
		authorName = participant.Name
		participantID = sql.NullInt64{Int64: participant.ID, Valid: true}
	}
	if authorName == "" {
		return Comment{}, ErrNameRequired
	}

	now := s.now()
	var row sqlite.Comment
	err := s.store.Tx(ctx, func(q *sqlite.Queries) error {
		var err error
		row, err = q.CreateComment(ctx, sqlite.CreateCommentParams{
			PublicID:      ids.PublicID(),
			PollID:        p.ID,
			ParticipantID: participantID,
			AuthorName:    authorName,
			Body:          body,
			CreatedAt:     store.FormatTime(now),
		})
		if err != nil {
			return fmt.Errorf("insert comment: %w", err)
		}
		return extendRetention(ctx, q, p, now)
	})
	if err != nil {
		return Comment{}, err
	}
	return commentFromRow(row)
}

// RemoveComment deletes any comment (admin action).
func (s *Service) RemoveComment(ctx context.Context, p Poll, commentID int64) error {
	if err := s.store.DeleteComment(ctx, sqlite.DeleteCommentParams{ID: commentID, PollID: p.ID}); err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	return nil
}

// RemoveOwnComment deletes a comment iff it belongs to the participant.
func (s *Service) RemoveOwnComment(ctx context.Context, p Poll, participant Participant, commentID int64) error {
	row, err := s.store.GetComment(ctx, sqlite.GetCommentParams{ID: commentID, PollID: p.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get comment: %w", err)
	}
	if !row.ParticipantID.Valid || row.ParticipantID.Int64 != participant.ID {
		return ErrForbidden
	}
	return s.RemoveComment(ctx, p, commentID)
}

// Claim attaches a guest poll to an account and its personal space.
// Fails once claimed: ownership never silently moves.
func (s *Service) Claim(ctx context.Context, p Poll, userID, spaceID int64) error {
	n, err := s.store.ClaimPoll(ctx, sqlite.ClaimPollParams{
		ID:        p.ID,
		UserID:    nullInt64(userID),
		SpaceID:   nullInt64(spaceID),
		UpdatedAt: store.FormatTime(s.now()),
	})
	if err != nil {
		return fmt.Errorf("claim poll: %w", err)
	}
	if n != 1 {
		return ErrForbidden
	}
	return nil
}

// ListByCreator returns the polls owned by a user, newest first.
func (s *Service) ListByCreator(ctx context.Context, userID int64) ([]Poll, error) {
	rows, err := s.store.ListPollsByCreator(ctx, nullInt64(userID))
	if err != nil {
		return nil, fmt.Errorf("list polls by creator: %w", err)
	}
	return pollsFromRows(rows)
}

// SpacePoll is a poll listed in a space, with its creator's name.
type SpacePoll struct {
	Poll
	OwnerName string
}

// ListBySpace returns a space's polls, newest first. The caller is
// responsible for the membership check (internal/space owns it).
func (s *Service) ListBySpace(ctx context.Context, spaceID int64) ([]SpacePoll, error) {
	rows, err := s.store.ListPollsBySpace(ctx, nullInt64(spaceID))
	if err != nil {
		return nil, fmt.Errorf("list polls by space: %w", err)
	}
	out := make([]SpacePoll, 0, len(rows))
	for _, r := range rows {
		p, err := pollFromRow(r.Poll)
		if err != nil {
			return nil, err
		}
		out = append(out, SpacePoll{Poll: p, OwnerName: r.OwnerName.String})
	}
	return out, nil
}

// ListVotedBy returns the polls where the user voted, newest first.
func (s *Service) ListVotedBy(ctx context.Context, userID int64) ([]Poll, error) {
	rows, err := s.store.ListPollsVotedByUser(ctx, nullInt64(userID))
	if err != nil {
		return nil, fmt.Errorf("list voted polls: %w", err)
	}
	return pollsFromRows(rows)
}

func pollsFromRows(rows []sqlite.Poll) ([]Poll, error) {
	out := make([]Poll, 0, len(rows))
	for _, r := range rows {
		p, err := pollFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func nullInt64(n int64) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: n, Valid: true}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
