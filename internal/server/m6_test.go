package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lporcheron/quorum/internal/mail"
)

func TestCSRFRejectsForgedPosts(t *testing.T) {
	ts, mailer := newTestServer(t)
	c := jarClient(t)
	signInByEmail(t, ts, mailer, c, "target@example.com")

	// A session-authenticated mutation without the token is refused...
	resp, _ := cPost(t, c, ts.URL+"/spaces", url.Values{"name": {"Forged"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("missing token: %d, want 403", resp.StatusCode)
	}
	// ...and with a wrong token too.
	resp, _ = cPost(t, c, ts.URL+"/spaces", url.Values{"name": {"Forged"}, "csrf": {"26charsbogus26charsbogus26"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong token: %d, want 403", resp.StatusCode)
	}
	// The forged space never appeared.
	_, body := cGet(t, c, ts.URL+"/dashboard")
	if strings.Contains(body, "Forged") {
		t.Errorf("forged mutation went through")
	}
	// With the real token it works.
	resp, body = cPostS(t, ts, c, "/spaces", url.Values{"name": {"Legit"}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Legit") {
		t.Errorf("legitimate mutation refused: %d", resp.StatusCode)
	}

	// Capability-token routes stay CSRF-exempt: the guest flow works
	// with no session token at all.
	adminPath := createPoll(t, ts, nil)
	if _, body := get(t, ts, pollPath(adminPath), nil); !strings.Contains(body, "vote_") {
		t.Errorf("guest voting broke")
	}
}

func TestAccountDeletionFlow(t *testing.T) {
	ts, mailer := newTestServer(t)
	c := jarClient(t)
	signInByEmail(t, ts, mailer, c, "leaver@example.com")

	// Create a poll so the account has data.
	resp, _ := cPost(t, c, ts.URL+"/polls", url.Values{
		"title": {"Mine"}, "kind": {"allday"}, "option_date": {"2026-11-01"},
	})
	public := strings.TrimSuffix(resp.Request.URL.Path, "/manage")

	// The account page shows the danger zone.
	resp, body := cGet(t, c, ts.URL+"/account")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "/account/delete") {
		t.Fatalf("account page: %d", resp.StatusCode)
	}

	// Owning a shared space blocks deletion.
	spacePath := createSpace(t, ts, c, "Blocking")
	cPostS(t, ts, c, spacePath+"/invitations", url.Values{"email": {"other@example.com"}, "role": {"member"}})
	_, mailBody := mailer.last(t)
	m := inviteLinkRe.FindStringSubmatch(mailBody)
	other := jarClient(t)
	signInByEmail(t, ts, mailer, other, "other@example.com")
	cPostS(t, ts, other, "/invitations/"+m[1], nil)

	resp, _ = cPostS(t, ts, c, "/account/delete", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("deletion while owning a shared space: %d, want 409", resp.StatusCode)
	}

	// Transfer the space, then delete for real.
	_, body = cGet(t, c, ts.URL+spacePath+"/settings")
	tm := transferRe.FindStringSubmatch(body)
	if tm == nil {
		t.Fatal("no transfer form")
	}
	cPostS(t, ts, c, spacePath+"/members/"+tm[1]+"/transfer", nil)
	resp, _ = cPostS(t, ts, c, "/account/delete", nil)
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/" {
		t.Fatalf("delete account: %d at %s", resp.StatusCode, resp.Request.URL.Path)
	}

	// Signed out, account gone, personal poll gone.
	resp, _ = cGet(t, c, ts.URL+"/dashboard")
	if resp.Request.URL.Path != "/login" {
		t.Errorf("still signed in after deletion")
	}
	resp, _ = get(t, ts, public, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("personal poll survived: %d", resp.StatusCode)
	}
}

func TestNotificationMute(t *testing.T) {
	ts, mailer := newTestServer(t)
	c := jarClient(t)
	signInByEmail(t, ts, mailer, c, "quiet@example.com")

	// Created without the notify checkbox: votes stay silent.
	resp, _ := cPost(t, c, ts.URL+"/polls", url.Values{
		"title": {"Quiet"}, "kind": {"allday"}, "option_date": {"2026-11-02"},
	})
	public := strings.TrimSuffix(resp.Request.URL.Path, "/manage")
	_, body := get(t, ts, public, nil)
	ids := optionIDs(t, body)

	before := mailer.count()
	cPost(t, jarClient(t), ts.URL+public+"/participants", url.Values{
		"name": {"Visitor"}, "vote_" + ids[0]: {"yes"},
	})
	if mailer.count() != before {
		t.Errorf("muted poll notified the organizer")
	}

	// Turn notifications on from the details form; the next vote mails.
	cPostS(t, ts, c, public+"/manage", url.Values{
		"title": {"Quiet"}, "notify_organizer": {"1"}, "allow_comments": {"1"},
	})
	cPost(t, jarClient(t), ts.URL+public+"/participants", url.Values{
		"name": {"Second"}, "vote_" + ids[0]: {"yes"},
	})
	mailer.waitFor(t, func(msgs []mail.Message) bool {
		for _, m := range msgs {
			if m.To == "quiet@example.com" && strings.Contains(m.Subject, "Quiet") {
				return true
			}
		}
		return false
	})
}
