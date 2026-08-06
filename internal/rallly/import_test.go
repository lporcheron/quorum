package rallly

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lporcheron/quorum/internal/ids"
	"github.com/lporcheron/quorum/internal/store/sqlite"
	"github.com/lporcheron/quorum/internal/store/storetest"
)

// fixtureDump builds a synthetic Rallly pg_dump exercising every
// mapping branch: registered vs anonymous users, shared space with a
// capitalized ADMIN role, date and time kinds, a scheduled poll
// matched to its option, a guest poll, deleted rows, ifNeedBe votes
// and COPY text-format escaping.
func fixtureDump() string {
	var b strings.Builder
	block := func(table, cols string, rows ...[]string) {
		fmt.Fprintf(&b, "COPY public.%s (%s) FROM stdin;\n", table, cols)
		for _, r := range rows {
			b.WriteString(strings.Join(r, "\t"))
			b.WriteByte('\n')
		}
		b.WriteString("\\.\n\n")
	}

	block("users", "id, name, email, anonymous, locale, time_zone, created_at",
		[]string{"usr_alice", "Alice", "Alice@Example.com", "f", "fr", "Europe/Paris", "2024-01-02 10:00:00.5"},
		[]string{"usr_ghost", "Guest", `temp-abc@rallly.co`, "t", `\N`, `\N`, "2024-01-03 10:00:00"},
	)
	block("spaces", "id, name, owner_id, created_at",
		[]string{"spc_team", "Team", "usr_alice", "2024-01-02 10:05:00"},
	)
	block("space_members", "id, space_id, user_id, role, created_at",
		[]string{"sm1", "spc_team", "usr_alice", "ADMIN", "2024-01-02 10:05:00"},
		[]string{"sm2", "spc_team", "usr_ghost", "MEMBER", "2024-01-03 10:05:00"},
	)
	block("scheduled_events", "id, start",
		[]string{"evt_1", "2024-03-05 09:00:00"},
	)
	block("polls", "id, title, description, location, user_id, space_id, kind, time_zone, status, deleted, hide_participants, disable_comments, require_participant_email, muted, scheduled_event_id, created_at, updated_at",
		// Timed, scheduled: finalizes onto the option matching evt_1.
		[]string{"pollTimed001", "Standup", `line1\nline2`, "Room 1", "usr_alice", "spc_team", "time", "Europe/Paris", "scheduled", "f", "f", "f", "t", "t", "evt_1", "2024-02-01 08:00:00", "2024-03-01 08:00:00"},
		// Date kind in the owner's personal space, still open.
		[]string{"pollDate0001", "Picnic", "", "", "usr_alice", `\N`, "date", `\N`, "open", "f", "t", "t", "f", "f", `\N`, "2024-02-02 08:00:00", "2024-02-02 08:00:00"},
		// Anonymous creator: becomes a claimable guest poll.
		[]string{"pollGuest001", "Drinks", "", "", "usr_ghost", `\N`, "date", `\N`, "open", "f", "f", "f", "f", "f", `\N`, "2024-02-03 08:00:00", "2024-02-03 08:00:00"},
		// Deleted in Rallly: skipped.
		[]string{"pollGone0001", "Old", "", "", "usr_alice", `\N`, "date", `\N`, "open", "t", "f", "f", "f", "f", `\N`, "2024-02-04 08:00:00", "2024-02-04 08:00:00"},
	)
	block("options", "id, poll_id, start_time, duration_minutes",
		[]string{"opt_t2", "pollTimed001", "2024-03-06 09:00:00", "0"}, // 0 → default 60
		[]string{"opt_t1", "pollTimed001", "2024-03-05 09:00:00", "30"},
		[]string{"opt_d1", "pollDate0001", "2024-04-01 00:00:00", "0"},
		[]string{"opt_d2", "pollDate0001", "2024-04-02 00:00:00", "0"},
		[]string{"opt_g1", "pollGuest001", "2024-05-01 00:00:00", "0"},
	)
	block("participants", "id, name, email, user_id, poll_id, deleted, created_at, updated_at",
		[]string{"par_bob", "Bob", "Bob@example.com", `\N`, "pollTimed001", "f", "2024-02-10 08:00:00", "2024-02-10 08:00:00"},
		[]string{"par_ali", "Alice", `\N`, "usr_alice", "pollTimed001", "f", "2024-02-11 08:00:00", "2024-02-11 08:00:00"},
		[]string{"par_del", "Gone", `\N`, `\N`, "pollTimed001", "t", "2024-02-12 08:00:00", "2024-02-12 08:00:00"},
	)
	block("votes", "id, participant_id, option_id, type, updated_at",
		[]string{"v1", "par_bob", "opt_t1", "yes", "2024-02-10 08:00:00"},
		[]string{"v2", "par_bob", "opt_t2", "ifNeedBe", "2024-02-10 08:00:00"},
		[]string{"v3", "par_ali", "opt_t1", "no", "2024-02-11 08:00:00"},
		[]string{"v4", "par_del", "opt_t1", "yes", "2024-02-12 08:00:00"}, // deleted participant → dropped
	)
	block("comments", "id, poll_id, author_name, content, created_at",
		[]string{"c1", "pollTimed001", "Bob", "Works for me", "2024-02-10 09:00:00"},
	)
	return b.String()
}

func TestImport(t *testing.T) {
	_, st := storetest.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	sum, err := Import(ctx, st, strings.NewReader(fixtureDump()), Options{Now: now})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if sum.Users != 1 || sum.Spaces != 1 || sum.Polls != 3 || sum.Options != 5 ||
		sum.Participants != 2 || sum.Votes != 3 || sum.Comments != 1 || sum.Finalized != 1 {
		t.Fatalf("summary = %+v", sum)
	}

	// Registered user: lowercased email, locale and timezone kept.
	u, err := st.GetUserByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("imported user: %v", err)
	}
	if u.Locale != "fr" || u.Timezone != "Europe/Paris" {
		t.Errorf("user locale/tz = %s/%s", u.Locale, u.Timezone)
	}
	if !u.PersonalSpaceID.Valid {
		t.Error("user has no personal space")
	}

	// Timed poll: finalized on the option matching the scheduled event.
	timed, err := st.GetPollByPublicID(ctx, "pollTimed001")
	if err != nil {
		t.Fatalf("timed poll: %v", err)
	}
	if timed.Kind != "timed" || timed.Timezone.String != "Europe/Paris" {
		t.Errorf("timed poll kind/tz = %s/%v", timed.Kind, timed.Timezone)
	}
	if timed.Status != "finalized" || !timed.FinalizedOptionID.Valid {
		t.Fatalf("timed poll status = %s finalized=%v", timed.Status, timed.FinalizedOptionID)
	}
	if timed.Description != "line1\nline2" {
		t.Errorf("COPY unescaping: description = %q", timed.Description)
	}
	if timed.RequireVoterEmail != 1 || timed.NotifyOrganizer != 0 || timed.AllowComments != 1 {
		t.Errorf("timed poll flags = %+v", timed)
	}
	opts, err := st.ListPollOptions(ctx, timed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 2 || opts[0].StartsAt.String != "2024-03-05T09:00:00Z" {
		t.Fatalf("timed options = %+v", opts)
	}
	if opts[0].DurationMinutes.Int64 != 30 || opts[1].DurationMinutes.Int64 != 60 {
		t.Errorf("durations = %d/%d", opts[0].DurationMinutes.Int64, opts[1].DurationMinutes.Int64)
	}
	if timed.FinalizedOptionID.Int64 != opts[0].ID {
		t.Errorf("finalized onto option %d, want %d (2024-03-05)", timed.FinalizedOptionID.Int64, opts[0].ID)
	}

	// Participants: deleted one dropped, user link mapped, email lowercased.
	parts, err := st.ListPollParticipants(ctx, timed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("participants = %+v", parts)
	}
	byName := map[string]sqlite.Participant{}
	for _, p := range parts {
		byName[p.Name] = p
	}
	if byName["Bob"].Email.String != "bob@example.com" {
		t.Errorf("participant email = %q", byName["Bob"].Email.String)
	}
	if byName["Alice"].UserID.Int64 != u.ID {
		t.Errorf("participant user link = %+v", byName["Alice"].UserID)
	}

	// Votes: deleted participant's vote dropped, ifNeedBe remapped.
	votes, err := st.ListPollVotes(ctx, timed.ID)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]int{}
	for _, v := range votes {
		values[v.Value]++
	}
	if len(votes) != 3 || values["ifneedbe"] != 1 || values["yes"] != 1 || values["no"] != 1 {
		t.Fatalf("votes = %+v", votes)
	}

	comments, err := st.ListPollComments(ctx, timed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].Body != "Works for me" {
		t.Fatalf("comments = %+v", comments)
	}

	// Date poll: all-day options, no timezone.
	date, err := st.GetPollByPublicID(ctx, "pollDate0001")
	if err != nil {
		t.Fatal(err)
	}
	if date.Kind != "allday" || date.Timezone.Valid || date.Status != "live" {
		t.Errorf("date poll = kind %s tz %v status %s", date.Kind, date.Timezone, date.Status)
	}
	if date.HideParticipants != 1 || date.AllowComments != 0 {
		t.Errorf("date poll flags = %+v", date)
	}
	dopts, err := st.ListPollOptions(ctx, date.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dopts) != 2 || dopts[0].AllDayDate.String != "2024-04-01" || dopts[0].StartsAt.Valid {
		t.Fatalf("date options = %+v", dopts)
	}

	// Guest poll: no creator, fresh admin link handed back once.
	guest, err := st.GetPollByPublicID(ctx, "pollGuest001")
	if err != nil {
		t.Fatal(err)
	}
	if guest.CreatedByUserID.Valid || guest.SpaceID.Valid {
		t.Errorf("guest poll ownership = %+v", guest)
	}
	token, ok := sum.GuestAdminLinks["pollGuest001"]
	if !ok {
		t.Fatalf("guest admin link missing: %+v", sum.GuestAdminLinks)
	}
	if ids.HashToken(token) != guest.AdminTokenHash {
		t.Error("guest admin token does not match the stored hash")
	}
	if len(sum.GuestAdminLinks) != 1 {
		t.Errorf("guest links = %+v", sum.GuestAdminLinks)
	}

	// Deleted poll skipped with a note.
	if _, err := st.GetPollByPublicID(ctx, "pollGone0001"); err == nil {
		t.Error("deleted poll was imported")
	}
	if len(sum.Skipped) == 0 {
		t.Error("expected a skip note for the deleted poll")
	}

	// Second import of the same dump must fail (public_id UNIQUE), not
	// silently duplicate.
	if _, err := Import(ctx, st, strings.NewReader(fixtureDump()), Options{Now: now}); err == nil {
		t.Error("re-import unexpectedly succeeded")
	}
}

// edgeDump exercises what a real dump can throw at the importer that
// the happy path does not: timestamps rendered with an offset, a
// scheduled event whose start is rendered differently from the option
// it points at, a cancelled poll, and rows that would collide with
// Quorum's uniqueness rules.
func edgeDump() string {
	var b strings.Builder
	block := func(table, cols string, rows ...[]string) {
		fmt.Fprintf(&b, "COPY public.%s (%s) FROM stdin;\n", table, cols)
		for _, r := range rows {
			b.WriteString(strings.Join(r, "\t"))
			b.WriteByte('\n')
		}
		b.WriteString("\\.\n\n")
	}

	block("users", "id, name, email, anonymous, locale, time_zone, created_at",
		[]string{"usr_bob", "Bob", "Bob@Example.com", "f", "en", "UTC", "2024-01-02 10:00:00"},
		// Same address in another case: collapses onto the first once
		// lowercased, and must be skipped, not abort the import.
		[]string{"usr_dup", "Bob again", "bob@example.com", "f", "en", "UTC", "2024-01-02 11:00:00"},
	)
	block("scheduled_events", "id, start",
		// Same instant as opt_a, rendered with an explicit zero offset.
		[]string{"evt_a", "2024-03-05 09:00:00+00"},
	)
	block("polls", "id, title, description, location, user_id, space_id, kind, time_zone, status, deleted, hide_participants, disable_comments, require_participant_email, muted, hide_scores, scheduled_event_id, created_at, updated_at",
		[]string{"pollCancel01", "Cancelled", "", "", "usr_bob", `\N`, "time", "UTC", "canceled", "f", "f", "f", "f", "f", "f", "evt_a", "2024-02-01 08:00:00", "2024-03-01 08:00:00"},
		// All-day poll whose midnights carry a +02 offset: the dates must
		// stay put instead of sliding to the previous day.
		[]string{"pollOffset01", "Offset", "", "", "usr_bob", `\N`, "date", `\N`, "open", "f", "f", "f", "f", "f", "t", `\N`, "2024-02-02 08:00:00", "2024-02-02 08:00:00"},
	)
	block("options", "id, poll_id, start_time, duration_minutes",
		[]string{"opt_a", "pollCancel01", "2024-03-05 09:00:00", "60"},
		// Duplicate slot: same poll, same start, same duration.
		[]string{"opt_a2", "pollCancel01", "2024-03-05 09:00:00", "60"},
		[]string{"opt_o1", "pollOffset01", "2024-04-01 00:00:00+02", "0"},
		[]string{"opt_o2", "pollOffset01", "2024-04-02 00:00:00+02", "0"},
	)
	block("participants", "id, name, email, user_id, poll_id, deleted, created_at, updated_at",
		[]string{"par_bob", "Bob", `\N`, "usr_bob", "pollCancel01", "f", "2024-02-10 08:00:00", "2024-02-10 08:00:00"},
	)
	block("votes", "id, participant_id, option_id, type, updated_at",
		[]string{"v1", "par_bob", "opt_a", "yes", "2024-02-10 08:00:00"},
		// Second row for the same slot: upserted, counted once.
		[]string{"v2", "par_bob", "opt_a", "no", "2024-02-10 09:00:00"},
		// Points at the deduplicated option: dropped.
		[]string{"v3", "par_bob", "opt_a2", "yes", "2024-02-10 09:00:00"},
	)
	block("comments", "id, poll_id, author_name, content, created_at")
	return b.String()
}

func TestImportEdgeCases(t *testing.T) {
	_, st := storetest.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	sum, err := Import(ctx, st, strings.NewReader(edgeDump()), Options{Now: now})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if sum.Users != 1 {
		t.Errorf("users = %d, want 1 (the case-duplicate address skipped)", sum.Users)
	}
	if sum.Options != 3 {
		t.Errorf("options = %d, want 3 (the duplicate slot skipped)", sum.Options)
	}
	if sum.Votes != 1 {
		t.Errorf("votes = %d, want 1 (upsert and dropped option not counted twice)", sum.Votes)
	}

	// A cancelled poll keeps the option it had settled on, but reads as
	// cancelled — and does not count as finalized.
	cancelled, err := st.GetPollByPublicID(ctx, "pollCancel01")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" {
		t.Errorf("status = %q, want cancelled", cancelled.Status)
	}
	if !cancelled.FinalizedOptionID.Valid {
		t.Error("cancelled poll lost the option it was scheduled on: event start rendered with an offset did not match")
	}
	if sum.Finalized != 0 {
		t.Errorf("finalized = %d, want 0: a cancelled poll is not a finalized one", sum.Finalized)
	}

	// All-day dates come from the dump text, never through an instant.
	offset, err := st.GetPollByPublicID(ctx, "pollOffset01")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := st.ListPollOptions(ctx, offset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 2 || opts[0].AllDayDate.String != "2024-04-01" || opts[1].AllDayDate.String != "2024-04-02" {
		t.Fatalf("all-day dates shifted by the offset: %+v", opts)
	}

	// Rallly's hidden scores have no equivalent: the operator is told.
	if !hasNote(sum.Skipped, "hid the scores") {
		t.Errorf("no note about hide_scores: %q", sum.Skipped)
	}
}

func TestImportDryRunChangesNothing(t *testing.T) {
	_, st := storetest.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	sum, err := Import(ctx, st, strings.NewReader(fixtureDump()), Options{Now: now, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if sum.Polls != 3 || sum.GuestPolls != 1 {
		t.Errorf("dry run summary = %+v", sum)
	}
	if sum.GuestAdminLinks != nil {
		t.Errorf("dry run handed out admin links that were never stored: %+v", sum.GuestAdminLinks)
	}
	count, err := st.CountPolls(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("dry run left %d polls behind", count)
	}

	// And the real run still works on the same target afterwards.
	if _, err := Import(ctx, st, strings.NewReader(fixtureDump()), Options{Now: now}); err != nil {
		t.Fatalf("import after dry run: %v", err)
	}
}

func TestValidateSchema(t *testing.T) {
	// A dump missing polls.kind would import every poll as timed, and
	// missing users.anonymous would promote every guest to an account.
	// Both must fail loudly, naming what is missing.
	dump := strings.ReplaceAll(fixtureDump(), "id, title, description, location, user_id, space_id, kind, time_zone, status",
		"id, title, description, location, user_id, space_id, time_zone, status")
	_, err := Import(context.Background(), nil, strings.NewReader(dump), Options{Now: time.Now()})
	if err == nil {
		t.Fatal("a dump without polls.kind was accepted")
	}
	if !strings.Contains(err.Error(), "polls.kind") {
		t.Errorf("error does not name the missing column: %v", err)
	}

	missingTable := "COPY public.polls (id) FROM stdin;\n\\.\n"
	_, err = Import(context.Background(), nil, strings.NewReader(missingTable), Options{Now: time.Now()})
	if err == nil || !strings.Contains(err.Error(), `table "options" is missing`) {
		t.Errorf("missing table not reported: %v", err)
	}
}

func TestParseDumpAcceptsLongLines(t *testing.T) {
	// A description longer than any fixed scan buffer must not fail the
	// import: bufio.Scanner's limit was the bug this guards.
	long := strings.Repeat("x", 2<<20)
	dump := "COPY public.polls (id, title) FROM stdin;\n" + "p1\t" + long + "\n\\.\n"
	tables, err := parseDump(strings.NewReader(dump))
	if err != nil {
		t.Fatalf("parseDump: %v", err)
	}
	got, _ := tables["polls"].get(tables["polls"].rows[0], "title")
	if len(got) != len(long) {
		t.Errorf("title truncated to %d bytes, want %d", len(got), len(long))
	}
}

func hasNote(notes []string, substr string) bool {
	for _, n := range notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

func TestDecodeCopyValue(t *testing.T) {
	cases := map[string]string{
		`plain`:       "plain",
		`a\tb`:        "a\tb",
		`a\nb`:        "a\nb",
		`a\\nb`:       `a\nb`,
		`\x41\102`:    "AB",
		`caf\303\251`: "café",
	}
	for in, want := range cases {
		got, err := decodeCopyValue(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
	if v, err := decodeCopyValue(`\N`); err != nil || v != null {
		t.Errorf(`\N = %q, %v`, v, err)
	}
	if _, err := decodeCopyValue(`bad\`); err == nil {
		t.Error("dangling backslash accepted")
	}
}
