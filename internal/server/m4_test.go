package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lporcheron/quorum/internal/mail"
)

func TestFinalizeSendsInvitations(t *testing.T) {
	ts, mailer := newTestServer(t)

	// The organizer has an account so notifications have a recipient.
	organizer := jarClient(t)
	signInByEmail(t, ts, mailer, organizer, "organizer@example.com")
	resp, _ := cPost(t, organizer, ts.URL+"/polls", url.Values{
		"title":           {"Team dinner"},
		"kind":            {"timed"},
		"timezone":        {"Europe/Paris"},
		"option_date":     {"2026-09-12"},
		"option_start":    {"19:00"},
		"option_duration": {"120"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	managePath := resp.Request.URL.Path
	public := strings.TrimSuffix(managePath, "/manage")

	// Two guests vote, one with an email.
	_, pageBody := cGet(t, jarClient(t), ts.URL+public)
	ids := optionIDs(t, pageBody)
	anon := jarClient(t)
	cPost(t, anon, ts.URL+public+"/participants", url.Values{
		"name": {"Bob"}, "email": {"bob@example.com"}, "vote_" + ids[0]: {"yes"},
	})
	cPost(t, anon, ts.URL+public+"/participants", url.Values{
		"name": {"NoMail"}, "vote_" + ids[0]: {"ifneedbe"},
	})

	// The organizer got a "new vote" notification for each.
	mailer.waitFor(t, func(msgs []mail.Message) bool {
		n := 0
		for _, m := range msgs {
			if m.To == "organizer@example.com" && strings.Contains(m.Subject, "Team dinner") {
				n++
			}
		}
		return n >= 2
	})

	// Finalize on the first option.
	var body string
	resp, body = cPost(t, organizer, ts.URL+managePath+"/finalize", url.Values{"option_id": {ids[0]}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("finalize: %d", resp.StatusCode)
	}
	if !strings.Contains(body, "19:00") {
		t.Errorf("manage page missing the finalized banner")
	}

	// Everyone with an email gets the invitation with the .ics attached.
	msgs := mailer.waitFor(t, func(msgs []mail.Message) bool {
		got := map[string]bool{}
		for _, m := range msgs {
			if len(m.Attachments) == 1 && m.Attachments[0].Filename == "invite.ics" {
				got[m.To] = true
			}
		}
		return got["bob@example.com"] && got["organizer@example.com"]
	})
	var invite mail.Message
	for _, m := range msgs {
		if m.To == "bob@example.com" && len(m.Attachments) == 1 {
			invite = m
		}
	}
	icsBody := string(invite.Attachments[0].Content)
	for _, want := range []string{"METHOD:REQUEST", "DTSTART:20260912T170000Z", "SUMMARY:Team dinner", "mailto:bob@example.com"} {
		if !strings.Contains(icsBody, want) {
			t.Errorf("invite.ics missing %q:\n%s", want, icsBody)
		}
	}
	if !strings.Contains(invite.Attachments[0].ContentType, "method=REQUEST") {
		t.Errorf("attachment content type = %q", invite.Attachments[0].ContentType)
	}

	// Voting is closed; the public page shows the decision and the .ics
	// link.
	resp, _ = cPost(t, jarClient(t), ts.URL+public+"/participants", url.Values{"name": {"Late"}})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("vote after finalize: %d, want 409", resp.StatusCode)
	}
	_, body = cGet(t, jarClient(t), ts.URL+public)
	if !strings.Contains(body, "calendar.ics") {
		t.Errorf("public page missing the calendar link")
	}

	// The feed serves the confirmed event only.
	resp, feed := cGet(t, jarClient(t), ts.URL+public+"/calendar.ics")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/calendar; charset=utf-8" {
		t.Fatalf("feed: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if strings.Count(feed, "BEGIN:VEVENT") != 1 || !strings.Contains(feed, "STATUS:CONFIRMED") {
		t.Errorf("feed content wrong:\n%s", feed)
	}

	// Cancel: everyone gets the CANCEL object.
	resp, _ = cPost(t, organizer, ts.URL+managePath+"/cancel", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %d", resp.StatusCode)
	}
	mailer.waitFor(t, func(msgs []mail.Message) bool {
		for _, m := range msgs {
			if m.To == "bob@example.com" && len(m.Attachments) == 1 && m.Attachments[0].Filename == "cancel.ics" {
				return strings.Contains(string(m.Attachments[0].Content), "METHOD:CANCEL") &&
					strings.Contains(string(m.Attachments[0].Content), "SEQUENCE:1")
			}
		}
		return false
	})
	_, feed = cGet(t, jarClient(t), ts.URL+public+"/calendar.ics")
	if !strings.Contains(feed, "STATUS:CANCELLED") {
		t.Errorf("feed after cancel:\n%s", feed)
	}
	_, body = cGet(t, jarClient(t), ts.URL+public)
	if !strings.Contains(body, "annul") && !strings.Contains(body, "cancel") {
		t.Errorf("public page missing the cancelled notice")
	}
}

func TestCommentNotifiesOrganizer(t *testing.T) {
	ts, mailer := newTestServer(t)
	organizer := jarClient(t)
	signInByEmail(t, ts, mailer, organizer, "org2@example.com")
	resp, _ := cPost(t, organizer, ts.URL+"/polls", url.Values{
		"title": {"Picnic"}, "kind": {"allday"}, "option_date": {"2026-08-15"}, "allow_comments": {"1"},
	})
	public := strings.TrimSuffix(resp.Request.URL.Path, "/manage")

	cPost(t, jarClient(t), ts.URL+public+"/comments", url.Values{
		"author_name": {"Carol"}, "body": {"Bring games!"},
	})
	mailer.waitFor(t, func(msgs []mail.Message) bool {
		for _, m := range msgs {
			if m.To == "org2@example.com" && strings.Contains(m.Subject, "Picnic") && strings.Contains(m.Text, "Carol") {
				return true
			}
		}
		return false
	})
}

func TestGuestPollSendsNoNotifications(t *testing.T) {
	ts, mailer := newTestServer(t)
	adminPath := createPoll(t, ts, nil) // anonymous creator
	public := pollPath(adminPath)

	_, body := get(t, ts, public, nil)
	ids := optionIDs(t, body)
	cPost(t, jarClient(t), ts.URL+public+"/participants", url.Values{
		"name": {"Silent"}, "vote_" + ids[0]: {"yes"},
	})
	// Nothing should ever arrive: without an organizer account the
	// notification is skipped at enqueue time, synchronously.
	mailer.mu.Lock()
	n := len(mailer.msgs)
	mailer.mu.Unlock()
	if n != 0 {
		t.Errorf("guest poll produced %d notifications", n)
	}
}

func TestCSVExport(t *testing.T) {
	ts, _ := newTestServer(t)
	adminPath := createPoll(t, ts, nil)
	public := pollPath(adminPath)
	_, body := get(t, ts, public, nil)
	ids := optionIDs(t, body)
	cPost(t, jarClient(t), ts.URL+public+"/participants", url.Values{
		"name": {"Ada"}, "email": {"ada@example.com"},
		"vote_" + ids[0]: {"yes"}, "vote_" + ids[1]: {"no"},
	})

	resp, csvBody := get(t, ts, adminPath+"/export.csv", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("export: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{"Ada", "ada@example.com", "yes", "no", "19:00"} {
		if !strings.Contains(csvBody, want) {
			t.Errorf("csv missing %q:\n%s", want, csvBody)
		}
	}
	// The export is organizer-only: an anonymous /manage request is
	// sent to the login page instead of the file.
	resp, _ = get(t, ts, public+"/manage/export.csv", nil)
	if resp.Request.URL.Path != "/login" || strings.Contains(resp.Header.Get("Content-Type"), "text/csv") {
		t.Errorf("csv served without authorization (landed on %s)", resp.Request.URL.Path)
	}
}
