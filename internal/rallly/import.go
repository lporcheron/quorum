package rallly

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lporcheron/quorum/internal/ids"
	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/internal/store/sqlite"
)

// Summary reports what an import did.
type Summary struct {
	Users        int
	Spaces       int
	Polls        int
	Options      int
	Participants int
	Votes        int
	Comments     int
	Finalized    int
	GuestPolls   int      // polls with no account behind them
	Skipped      []string // human-readable notes about dropped rows
	// GuestAdminLinks maps guest-poll public ids to their fresh admin
	// tokens: the only chance to hand the links back to organizers.
	// Empty after a dry run — nothing was persisted to hand out.
	GuestAdminLinks map[string]string
}

// Options configures an import.
type Options struct {
	// Now is the reference instant for retention horizons and for rows
	// whose own timestamps are missing or unparsable.
	Now time.Time
	// DryRun performs every insert, then rolls the transaction back.
	// Constraint violations and skip notes surface exactly as they would
	// on a real run, and the target database is left untouched.
	DryRun bool
}

// errDryRun aborts the transaction once all the work is done. Rolling
// back is how a dry run stays a dry run: every insert still runs, so
// every constraint is still checked.
var errDryRun = errors.New("dry run")

// Import reads a plain-format Rallly pg_dump and inserts everything
// into the store, in one transaction. Anonymous Rallly guests
// (temp-*@rallly.co accounts) are not imported as users; their polls
// become claimable guest polls. Registered users are recreated by
// email — Rallly has no passwords either, so they simply sign in on
// Quorum and the verified-email merge reunites them with their data.
func Import(ctx context.Context, st *store.Store, dump io.Reader, opts Options) (*Summary, error) {
	tables, err := parseDump(dump)
	if err != nil {
		return nil, err
	}
	if err := validateSchema(tables); err != nil {
		return nil, err
	}

	now := opts.Now
	s := &Summary{GuestAdminLinks: make(map[string]string)}
	deletesAt := store.FormatTime(now.Add(poll.DefaultRetentionDays * 24 * time.Hour))

	err = st.Tx(ctx, func(q *sqlite.Queries) error {
		// --- users (registered only) + their personal spaces ---
		userID := make(map[string]int64)        // rallly user id → quorum id
		personalSpace := make(map[string]int64) // rallly user id → quorum personal space
		// seenEmail guards users.email UNIQUE: Rallly's addresses are
		// unique as stored, but two of them may differ only in case and
		// collapse into one once lowercased. A failed INSERT would abort
		// the whole transaction, so duplicates are skipped up front.
		seenEmail := make(map[string]bool)
		if t := tables["users"]; t != nil {
			for _, row := range t.rows {
				if v, _ := t.get(row, "anonymous"); v == "t" {
					continue
				}
				rid, _ := t.get(row, "id")
				email, ok := t.get(row, "email")
				if !ok || email == "" {
					s.skip(&s.Skipped, "user %s without email", rid)
					continue
				}
				if seenEmail[strings.ToLower(email)] {
					s.skip(&s.Skipped, "user %s: %s already imported (addresses differing only in case)", rid, email)
					continue
				}
				seenEmail[strings.ToLower(email)] = true
				name, _ := t.get(row, "name")
				if name == "" {
					name, _, _ = strings.Cut(email, "@")
				}
				locale, _ := t.get(row, "locale")
				if locale != "fr" {
					locale = "en"
				}
				tz, _ := t.get(row, "time_zone")
				if tz == "" {
					tz = "UTC"
				}
				created := pgTimeOr(t, row, "created_at", now)
				u, err := q.CreateUser(ctx, sqlite.CreateUserParams{
					PublicID:  ids.PublicID(),
					Email:     strings.ToLower(email),
					Name:      name,
					Locale:    locale,
					Timezone:  tz,
					CreatedAt: store.FormatTime(created),
				})
				if err != nil {
					return fmt.Errorf("import user %s: %w", email, err)
				}
				sp, err := q.CreateSpace(ctx, sqlite.CreateSpaceParams{
					PublicID:    ids.PublicID(),
					Slug:        strings.ToLower(ids.PublicID()),
					Name:        name,
					OwnerUserID: u.ID,
					CreatedAt:   store.FormatTime(created),
				})
				if err != nil {
					return fmt.Errorf("personal space for %s: %w", email, err)
				}
				if err := q.CreateSpaceMember(ctx, sqlite.CreateSpaceMemberParams{
					SpaceID: sp.ID, UserID: u.ID, Role: "owner", CreatedAt: store.FormatTime(created),
				}); err != nil {
					return err
				}
				if err := q.SetUserPersonalSpace(ctx, sqlite.SetUserPersonalSpaceParams{
					ID: u.ID, PersonalSpaceID: sql.NullInt64{Int64: sp.ID, Valid: true},
				}); err != nil {
					return err
				}
				userID[rid] = u.ID
				personalSpace[rid] = sp.ID
				s.Users++
			}
		}

		// --- shared spaces + memberships ---
		// members tracks inserted (space, user) pairs: a failed INSERT
		// would abort the whole transaction on PostgreSQL, so
		// duplicates must be skipped up front, not tolerated after.
		spaceID := make(map[string]int64) // rallly space id → quorum id
		members := make(map[[2]int64]bool)
		if t := tables["spaces"]; t != nil {
			for _, row := range t.rows {
				rid, _ := t.get(row, "id")
				ownerRID, _ := t.get(row, "owner_id")
				owner, ok := userID[ownerRID]
				if !ok {
					s.skip(&s.Skipped, "space %s: owner not imported", rid)
					continue
				}
				name, _ := t.get(row, "name")
				if name == "" {
					name = "Imported space"
				}
				created := pgTimeOr(t, row, "created_at", now)
				sp, err := q.CreateSpace(ctx, sqlite.CreateSpaceParams{
					PublicID:    ids.PublicID(),
					Slug:        strings.ToLower(ids.PublicID()),
					Name:        name,
					OwnerUserID: owner,
					CreatedAt:   store.FormatTime(created),
				})
				if err != nil {
					return fmt.Errorf("import space %s: %w", name, err)
				}
				if err := q.CreateSpaceMember(ctx, sqlite.CreateSpaceMemberParams{
					SpaceID: sp.ID, UserID: owner, Role: "owner", CreatedAt: store.FormatTime(created),
				}); err != nil {
					return err
				}
				members[[2]int64{sp.ID, owner}] = true
				spaceID[rid] = sp.ID
				s.Spaces++
			}
		}
		if t := tables["space_members"]; t != nil {
			for _, row := range t.rows {
				srid, _ := t.get(row, "space_id")
				urid, _ := t.get(row, "user_id")
				sp, spOK := spaceID[srid]
				u, uOK := userID[urid]
				if !spOK || !uOK || members[[2]int64{sp, u}] {
					continue // space or user not imported, or already a member (owner)
				}
				role := "member"
				if r, _ := t.get(row, "role"); strings.EqualFold(r, "admin") {
					role = "admin"
				}
				if err := q.CreateSpaceMember(ctx, sqlite.CreateSpaceMemberParams{
					SpaceID: sp, UserID: u, Role: role,
					CreatedAt: store.FormatTime(pgTimeOr(t, row, "created_at", now)),
				}); err != nil {
					return fmt.Errorf("import membership: %w", err)
				}
				members[[2]int64{sp, u}] = true
			}
		}

		// --- scheduled events (for finalization matching) ---
		events := make(map[string]string) // rallly event id → raw start
		if t := tables["scheduled_events"]; t != nil {
			for _, row := range t.rows {
				rid, _ := t.get(row, "id")
				start, _ := t.get(row, "start")
				events[rid] = start
			}
		}

		// --- polls, options ---
		pt := tables["polls"]
		pollID := make(map[string]int64)      // rallly poll id → quorum id
		optionID := make(map[string]int64)    // rallly option id → quorum id
		optionByKey := make(map[string]int64) // optionKey(...) → quorum option id
		seenOption := make(map[string]bool)   // optionKey + duration, for dedup
		ot := tables["options"]
		optionsByPoll := make(map[string][][]string)
		for _, row := range ot.rows {
			prid, _ := ot.get(row, "poll_id")
			optionsByPoll[prid] = append(optionsByPoll[prid], row)
		}

		for _, row := range pt.rows {
			if v, _ := pt.get(row, "deleted"); v == "t" {
				s.skip(&s.Skipped, "poll %q: deleted in Rallly", firstOf(pt, row, "title", "id"))
				continue
			}
			rid, _ := pt.get(row, "id")
			kind := "timed"
			if v, _ := pt.get(row, "kind"); v == "date" {
				kind = "allday"
			}
			tz := sql.NullString{}
			if kind == "timed" {
				z, _ := pt.get(row, "time_zone")
				if z == "" || !validTZ(z) {
					z = "UTC"
				}
				tz = sql.NullString{String: z, Valid: true}
			}
			creator, spaceRef := int64(0), int64(0)
			if urid, ok := pt.get(row, "user_id"); ok {
				if u, ok := userID[urid]; ok {
					creator = u
					spaceRef = personalSpace[urid]
				}
			}
			if srid, ok := pt.get(row, "space_id"); ok {
				if sp, ok := spaceID[srid]; ok && creator != 0 {
					spaceRef = sp
				}
			}
			adminToken := ids.Token()
			title, _ := pt.get(row, "title")
			desc, _ := pt.get(row, "description")
			location, _ := pt.get(row, "location")
			hide, _ := pt.get(row, "hide_participants")
			noComments, _ := pt.get(row, "disable_comments")
			reqEmail, _ := pt.get(row, "require_participant_email")
			muted, _ := pt.get(row, "muted")
			created := pgTimeOr(pt, row, "created_at", now)
			updated := pgTimeOr(pt, row, "updated_at", created)

			p, err := q.CreatePoll(ctx, sqlite.CreatePollParams{
				PublicID:          rid, // keep the Rallly id: recognizable, unique
				SpaceID:           nullInt64(spaceRef),
				CreatedByUserID:   nullInt64(creator),
				AdminTokenHash:    ids.HashToken(adminToken),
				Title:             title,
				Description:       desc,
				Location:          location,
				Kind:              kind,
				Timezone:          tz,
				HideParticipants:  boolInt(hide == "t"),
				RequireVoterEmail: boolInt(reqEmail == "t"),
				AllowComments:     boolInt(noComments != "t"),
				NotifyOrganizer:   boolInt(muted != "t"),
				DeletesAt:         sql.NullString{String: deletesAt, Valid: true},
				CreatedAt:         store.FormatTime(created),
				UpdatedAt:         store.FormatTime(updated),
			})
			if err != nil {
				return fmt.Errorf("import poll %q: %w", title, err)
			}
			pollID[rid] = p.ID
			if creator == 0 {
				s.GuestAdminLinks[rid] = adminToken
				s.GuestPolls++
			}
			s.Polls++
			if v, _ := pt.get(row, "hide_scores"); v == "t" {
				s.skip(&s.Skipped, "poll %q: Rallly hid the scores until voting; Quorum has no equivalent and shows them", title)
			}

			// Options, sorted chronologically like Quorum expects.
			rows := optionsByPoll[rid]
			sort.Slice(rows, func(i, j int) bool {
				a, _ := ot.get(rows[i], "start_time")
				b, _ := ot.get(rows[j], "start_time")
				return a < b
			})
			// Quorum forbids two options on the same slot (uq_poll_options_*).
			// Skip repeats rather than let one abort the transaction.
			pos := 0
			for _, orow := range rows {
				orid, _ := ot.get(orow, "id")
				start, _ := ot.get(orow, "start_time")
				params := sqlite.CreatePollOptionParams{PollID: p.ID, Position: int64(pos)}
				minutes := 0
				if kind == "allday" {
					date, err := dateOnly(start)
					if err != nil {
						return fmt.Errorf("option of %q: %w", title, err)
					}
					params.AllDayDate = sql.NullString{String: date, Valid: true}
				} else {
					startT, err := pgTime(start)
					if err != nil {
						return fmt.Errorf("option of %q: %w", title, err)
					}
					dur, _ := ot.get(orow, "duration_minutes")
					minutes, _ = strconv.Atoi(dur)
					if minutes <= 0 {
						minutes = 60
					}
					params.StartsAt = sql.NullString{String: store.FormatTime(startT), Valid: true}
					params.DurationMinutes = sql.NullInt64{Int64: int64(minutes), Valid: true}
				}
				slot := optionKey(rid, kind, start)
				if seenOption[fmt.Sprintf("%s|%d", slot, minutes)] {
					s.skip(&s.Skipped, "poll %q: duplicate option at %s", title, start)
					continue
				}
				seenOption[fmt.Sprintf("%s|%d", slot, minutes)] = true
				o, err := q.CreatePollOption(ctx, params)
				if err != nil {
					return fmt.Errorf("import option of %q: %w", title, err)
				}
				optionID[orid] = o.ID
				// Finalization matches on the slot alone (a scheduled event
				// carries no duration); the earliest option at a slot wins.
				if _, ok := optionByKey[slot]; !ok {
					optionByKey[slot] = o.ID
				}
				s.Options++
				pos++
			}

			// Status: Rallly "scheduled" (and "finalized") map onto our
			// finalized state when the chosen option is identifiable.
			status, _ := pt.get(row, "status")
			switch status {
			case "open", "live", "":
				// stays live
			case "paused":
				if err := q.UpdatePollStatus(ctx, sqlite.UpdatePollStatusParams{
					ID: p.ID, Status: "paused", UpdatedAt: store.FormatTime(updated),
				}); err != nil {
					return err
				}
			case "scheduled", "finalized", "canceled", "cancelled":
				cancelled := status == "canceled" || status == "cancelled"
				chosen := int64(0)
				if erid, ok := pt.get(row, "scheduled_event_id"); ok {
					if start, ok := events[erid]; ok {
						chosen = optionByKey[optionKey(rid, kind, start)]
					}
				}
				// FinalizePoll also sets the status; a cancelled poll then
				// keeps its chosen option but reads as cancelled.
				if chosen != 0 {
					if err := q.FinalizePoll(ctx, sqlite.FinalizePollParams{
						ID: p.ID, FinalizedOptionID: nullInt64(chosen), UpdatedAt: store.FormatTime(updated),
					}); err != nil {
						return err
					}
					if !cancelled {
						s.Finalized++
					}
				} else {
					s.skip(&s.Skipped, "poll %q: decided in Rallly but no option matches the scheduled date; imported as %s",
						title, map[bool]string{true: "cancelled", false: "paused"}[cancelled])
				}
				switch {
				case cancelled:
					if err := q.UpdatePollStatus(ctx, sqlite.UpdatePollStatusParams{
						ID: p.ID, Status: "cancelled", UpdatedAt: store.FormatTime(updated),
					}); err != nil {
						return err
					}
				case chosen == 0:
					if err := q.UpdatePollStatus(ctx, sqlite.UpdatePollStatusParams{
						ID: p.ID, Status: "paused", UpdatedAt: store.FormatTime(updated),
					}); err != nil {
						return err
					}
				}
			default:
				s.skip(&s.Skipped, "poll %q: unknown status %q, imported live", title, status)
			}
		}

		// --- participants ---
		participantsT := tables["participants"]
		participantID := make(map[string]int64)
		for _, row := range participantsT.rows {
			if v, _ := participantsT.get(row, "deleted"); v == "t" {
				continue
			}
			prid, _ := participantsT.get(row, "poll_id")
			p, ok := pollID[prid]
			if !ok {
				continue
			}
			rid, _ := participantsT.get(row, "id")
			name, _ := participantsT.get(row, "name")
			email, _ := participantsT.get(row, "email")
			var uid int64
			if urid, ok := participantsT.get(row, "user_id"); ok {
				uid = userID[urid]
			}
			created := pgTimeOr(participantsT, row, "created_at", now)
			pa, err := q.CreateParticipant(ctx, sqlite.CreateParticipantParams{
				PublicID:      ids.PublicID(),
				PollID:        p,
				Name:          name,
				Email:         nullString(strings.ToLower(email)),
				UserID:        nullInt64(uid),
				EditTokenHash: ids.HashToken(ids.Token()), // fresh, undisclosed
				CreatedAt:     store.FormatTime(created),
				UpdatedAt:     store.FormatTime(pgTimeOr(participantsT, row, "updated_at", created)),
			})
			if err != nil {
				return fmt.Errorf("import participant %q: %w", name, err)
			}
			participantID[rid] = pa.ID
			s.Participants++
		}

		// --- votes ---
		vt := tables["votes"]
		castVote := make(map[[2]int64]bool) // (participant, option), so the count stays honest
		for _, row := range vt.rows {
			pa, paOK := participantID[index(vt, row, "participant_id")]
			o, oOK := optionID[index(vt, row, "option_id")]
			if !paOK || !oOK {
				continue // deleted participant, deduplicated or vanished option
			}
			value := "no"
			switch v, _ := vt.get(row, "type"); v {
			case "yes":
				value = "yes"
			case "ifNeedBe":
				value = "ifneedbe"
			}
			if err := q.UpsertVote(ctx, sqlite.UpsertVoteParams{
				ParticipantID: pa, OptionID: o, Value: value,
				UpdatedAt: store.FormatTime(pgTimeOr(vt, row, "updated_at", now)),
			}); err != nil {
				return fmt.Errorf("import vote: %w", err)
			}
			if !castVote[[2]int64{pa, o}] {
				castVote[[2]int64{pa, o}] = true
				s.Votes++
			}
		}

		// --- comments ---
		if ct := tables["comments"]; ct != nil {
			for _, row := range ct.rows {
				p, ok := pollID[index(ct, row, "poll_id")]
				if !ok {
					continue
				}
				author, _ := ct.get(row, "author_name")
				body, _ := ct.get(row, "content")
				if _, err := q.CreateComment(ctx, sqlite.CreateCommentParams{
					PublicID:   ids.PublicID(),
					PollID:     p,
					AuthorName: author,
					Body:       body,
					CreatedAt:  store.FormatTime(pgTimeOr(ct, row, "created_at", now)),
				}); err != nil {
					return fmt.Errorf("import comment: %w", err)
				}
				s.Comments++
			}
		}
		if opts.DryRun {
			return errDryRun
		}
		return nil
	})
	switch {
	case errors.Is(err, errDryRun):
		// Nothing was persisted, so there are no links to hand out; the
		// counts and the notes above are the whole point of a dry run.
		s.GuestAdminLinks = nil
	case err != nil:
		return nil, err
	}
	return s, nil
}

func (s *Summary) skip(dst *[]string, format string, args ...any) {
	*dst = append(*dst, fmt.Sprintf(format, args...))
}

// pgTime parses pg_dump COPY timestamps; naive values are UTC.
func pgTime(s string) (time.Time, error) {
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999-07",
		"2006-01-02 15:04:05.999999+00",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", s)
}

// dateOnly takes the calendar date straight from the dump text. All-day
// options are timezone-less by definition, so the date must never go
// through an instant: converting "2024-04-01 00:00:00+02" to UTC would
// move the option to March 31st.
func dateOnly(raw string) (string, error) {
	date := raw
	if i := strings.IndexAny(date, " T"); i >= 0 {
		date = date[:i]
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", fmt.Errorf("unrecognized date %q", raw)
	}
	return date, nil
}

// optionKey identifies the slot an option occupies, canonically: the
// raw dump text cannot be compared directly, because an option's
// start_time and a scheduled event's start come from different columns
// and pg_dump may render them with different precision or offsets.
func optionKey(pollRID, kind, rawStart string) string {
	if kind == "allday" {
		date, err := dateOnly(rawStart)
		if err != nil {
			return ""
		}
		return pollRID + "|" + date
	}
	t, err := pgTime(rawStart)
	if err != nil {
		return ""
	}
	return pollRID + "|" + store.FormatTime(t)
}

func pgTimeOr(t *table, row []string, col string, fallback time.Time) time.Time {
	if v, ok := t.get(row, col); ok {
		if parsed, err := pgTime(v); err == nil {
			return parsed
		}
	}
	return fallback
}

func index(t *table, row []string, col string) string {
	v, _ := t.get(row, col)
	return v
}

func firstOf(t *table, row []string, cols ...string) string {
	for _, c := range cols {
		if v, ok := t.get(row, c); ok && v != "" {
			return v
		}
	}
	return "?"
}

func validTZ(tz string) bool {
	_, err := time.LoadLocation(tz)
	return err == nil && tz != "Local"
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func nullInt64(n int64) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: n, Valid: true}
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
