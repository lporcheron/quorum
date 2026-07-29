package ics

import (
	"strings"
	"testing"
	"time"

	"github.com/lporcheron/quorum/internal/poll"
)

var (
	testNow = time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC)
	base    = "https://polls.example.com"
	org     = Organizer{Email: "quorum@example.com", Name: "Alice"}
)

func timedPoll() (poll.Poll, poll.Option) {
	p := poll.Poll{
		ID: 1, PublicID: "abcdef123456", Title: "Team dinner",
		Location: "Chez Paul", Kind: poll.KindTimed, Timezone: "Europe/Paris",
		Status: poll.StatusFinalized,
	}
	// 19:00–21:00 CEST on 2026-09-12 = 17:00–19:00 UTC.
	o := poll.Option{ID: 42, StartsAt: time.Date(2026, 9, 12, 17, 0, 0, 0, time.UTC), Duration: 2 * time.Hour}
	return p, o
}

func TestInviteRequest(t *testing.T) {
	p, o := timedPoll()
	out := string(Invite(p, o, org, []string{"bob@example.com", "carol@example.com"}, base, testNow))

	for _, want := range []string{
		"METHOD:REQUEST",
		"UID:quorum-abcdef123456-42@polls.example.com",
		"DTSTART:20260912T170000Z",
		"DTEND:20260912T190000Z",
		"SUMMARY:Team dinner",
		"LOCATION:Chez Paul",
		"ORGANIZER;CN=Alice:mailto:quorum@example.com",
		"ATTENDEE;RSVP=false:mailto:bob@example.com",
		"ATTENDEE;RSVP=false:mailto:carol@example.com",
		"STATUS:CONFIRMED",
		"SEQUENCE:0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("invite missing %q:\n%s", want, out)
		}
	}
}

func TestCancelSupersedesInvite(t *testing.T) {
	p, o := timedPoll()
	invite := string(Invite(p, o, org, nil, base, testNow))
	cancel := string(Cancel(p, o, org, []string{"bob@example.com"}, base, testNow))

	// Same UID so clients match the pair; higher sequence; CANCEL.
	uidLine := "UID:quorum-abcdef123456-42@polls.example.com"
	if !strings.Contains(invite, uidLine) || !strings.Contains(cancel, uidLine) {
		t.Errorf("UID mismatch between invite and cancel")
	}
	for _, want := range []string{"METHOD:CANCEL", "STATUS:CANCELLED", "SEQUENCE:1"} {
		if !strings.Contains(cancel, want) {
			t.Errorf("cancel missing %q:\n%s", want, cancel)
		}
	}
}

func TestAllDayUsesDateValues(t *testing.T) {
	p := poll.Poll{ID: 2, PublicID: "allday123456", Title: "Offsite", Kind: poll.KindAllDay, Status: poll.StatusFinalized}
	o := poll.Option{ID: 7, Date: poll.Date{Year: 2026, Month: time.October, Day: 1}}
	out := string(Invite(p, o, org, nil, base, testNow))

	// A whole-day event is the same date everywhere: VALUE=DATE, no
	// midnight timestamp, exclusive DTEND on the next day.
	if !strings.Contains(out, "DTSTART;VALUE=DATE:20261001") {
		t.Errorf("all-day DTSTART wrong:\n%s", out)
	}
	if !strings.Contains(out, "DTEND;VALUE=DATE:20261002") {
		t.Errorf("all-day DTEND wrong:\n%s", out)
	}
	if strings.Contains(out, "20261001T") {
		t.Errorf("all-day event has a time component:\n%s", out)
	}
}

func TestFeedStates(t *testing.T) {
	p, o := timedPoll()
	o2 := poll.Option{ID: 43, StartsAt: o.StartsAt.AddDate(0, 0, 1), Duration: 2 * time.Hour}
	options := []poll.Option{o, o2}

	// Live poll: every option, tentative.
	p.Status = poll.StatusLive
	feed := string(Feed(p, options, 0, base, testNow))
	if strings.Count(feed, "BEGIN:VEVENT") != 2 || strings.Count(feed, "STATUS:TENTATIVE") != 2 {
		t.Errorf("live feed wrong:\n%s", feed)
	}

	// Finalized: only the chosen option, confirmed.
	p.Status = poll.StatusFinalized
	feed = string(Feed(p, options, o.ID, base, testNow))
	if strings.Count(feed, "BEGIN:VEVENT") != 1 || !strings.Contains(feed, "STATUS:CONFIRMED") {
		t.Errorf("finalized feed wrong:\n%s", feed)
	}

	// Cancelled: the event stays, cancelled, sequence bumped.
	p.Status = poll.StatusCancelled
	feed = string(Feed(p, options, o.ID, base, testNow))
	if !strings.Contains(feed, "STATUS:CANCELLED") || !strings.Contains(feed, "SEQUENCE:1") {
		t.Errorf("cancelled feed wrong:\n%s", feed)
	}
}
