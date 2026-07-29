package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var inviteLinkRe = regexp.MustCompile(`/invitations/([1-9A-HJ-NP-Za-km-z]{26})`)

// createSpace makes a space through the dashboard form and returns its
// settings path (found in the redirected dashboard).
func createSpace(t *testing.T, ts *httptest.Server, c *http.Client, name string) string {
	t.Helper()
	resp, body := cPost(t, c, ts.URL+"/spaces", url.Values{"name": {name}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, name) {
		t.Fatalf("create space: %d", resp.StatusCode)
	}
	m := regexp.MustCompile(`/spaces/([a-z0-9]+)/settings`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no settings link on dashboard:\n%s", body)
	}
	return "/spaces/" + m[1]
}

func TestSpaceLifecycle(t *testing.T) {
	ts, mailer := newTestServer(t)

	owner := jarClient(t)
	signInByEmail(t, ts, mailer, owner, "owner@example.com")
	spacePath := createSpace(t, ts, owner, "Bleemeo")

	// Settings: rename, default tz, retention.
	resp, body := cPost(t, owner, ts.URL+spacePath+"/settings", url.Values{
		"name":             {"Bleemeo Team"},
		"default_timezone": {"Europe/Paris"},
		"retention_days":   {"30"},
	})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Bleemeo Team") || !strings.Contains(body, `value="30"`) {
		t.Fatalf("settings update: %d", resp.StatusCode)
	}

	// Invite a member: the test mailer is enabled, so the link travels
	// by email.
	resp, body = cPost(t, owner, ts.URL+spacePath+"/invitations", url.Values{
		"email": {"member@example.com"}, "role": {"member"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invite: %d", resp.StatusCode)
	}
	if !strings.Contains(body, "member@example.com") {
		t.Errorf("pending invitation not listed")
	}
	_, mailBody := mailer.last(t)
	m := inviteLinkRe.FindStringSubmatch(mailBody)
	if m == nil {
		t.Fatalf("no invitation link in mail:\n%s", mailBody)
	}
	invitePath := "/invitations/" + m[1]

	// The invitee signs in and accepts.
	member := jarClient(t)
	signInByEmail(t, ts, mailer, member, "member@example.com")
	resp, body = cGet(t, member, ts.URL+invitePath)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Bleemeo Team") {
		t.Fatalf("invitation page: %d", resp.StatusCode)
	}
	resp, body = cPost(t, member, ts.URL+invitePath, nil)
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/dashboard" {
		t.Fatalf("accept: %d at %s", resp.StatusCode, resp.Request.URL.Path)
	}
	if !strings.Contains(body, "Bleemeo Team") {
		t.Errorf("dashboard not switched to the joined space")
	}
	// A member sees no settings link.
	if strings.Contains(body, spacePath+"/settings") {
		t.Errorf("member sees the settings link")
	}
	resp, _ = cGet(t, member, ts.URL+spacePath+"/settings")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("member opened settings: %d", resp.StatusCode)
	}

	// The invitation is single use.
	resp, _ = cGet(t, member, ts.URL+invitePath)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("consumed invitation still resolves: %d", resp.StatusCode)
	}
}

func TestSpacePollAuthorization(t *testing.T) {
	ts, mailer := newTestServer(t)

	// Owner creates the space; admin and member join it.
	owner := jarClient(t)
	signInByEmail(t, ts, mailer, owner, "owner@example.com")
	spacePath := createSpace(t, ts, owner, "Team")

	join := func(email, role string) *http.Client {
		if _, body := cPost(t, owner, ts.URL+spacePath+"/invitations", url.Values{
			"email": {email}, "role": {role},
		}); body == "" {
			t.Fatal("invite failed")
		}
		_, mailBody := mailer.last(t)
		m := inviteLinkRe.FindStringSubmatch(mailBody)
		if m == nil {
			t.Fatalf("no invite link for %s", email)
		}
		c := jarClient(t)
		signInByEmail(t, ts, mailer, c, email)
		if resp, _ := cPost(t, c, ts.URL+"/invitations/"+m[1], nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("accept for %s: %d", email, resp.StatusCode)
		}
		return c
	}
	admin := join("admin@example.com", "admin")
	member := join("member@example.com", "member")

	// The member creates a poll: it lands in the current space with the
	// space retention, no claim needed.
	resp, _ := cPost(t, member, ts.URL+"/polls", url.Values{
		"title":       {"Member poll"},
		"kind":        {"allday"},
		"option_date": {"2026-10-01"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("member create poll: %d", resp.StatusCode)
	}
	// Signed-in creators land on /manage, no capability token needed.
	if !strings.HasSuffix(resp.Request.URL.Path, "/manage") {
		t.Fatalf("signed-in creation landed on %s, want .../manage", resp.Request.URL.Path)
	}
	pollPublic := strings.TrimSuffix(resp.Request.URL.Path, "/manage")

	// The space admin manages the member's poll through /manage.
	resp, _ = cGet(t, admin, ts.URL+pollPublic+"/manage")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("space admin manage: %d, want 200", resp.StatusCode)
	}
	// The owner too.
	resp, _ = cGet(t, owner, ts.URL+pollPublic+"/manage")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("space owner manage: %d, want 200", resp.StatusCode)
	}

	// Another member manages only their own polls.
	other := join("other@example.com", "member")
	resp, _ = cGet(t, other, ts.URL+pollPublic+"/manage")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("plain member managed someone else's poll: %d, want 403", resp.StatusCode)
	}

	// The member's own dashboard shows the manage link for their poll.
	_, body := cGet(t, member, ts.URL+"/dashboard")
	if !strings.Contains(body, pollPublic+"/manage") {
		t.Errorf("member dashboard missing manage link for own poll")
	}
	// The admin's dashboard shows it too (manageable by role).
	_, body = cGet(t, admin, ts.URL+"/dashboard")
	if !strings.Contains(body, pollPublic+"/manage") || !strings.Contains(body, "Member poll") {
		t.Errorf("admin dashboard missing the member's poll")
	}
}

func TestSpaceRetentionAppliesToPolls(t *testing.T) {
	ts, mailer := newTestServer(t)
	owner := jarClient(t)
	signInByEmail(t, ts, mailer, owner, "owner@example.com")
	spacePath := createSpace(t, ts, owner, "Short")
	if resp, _ := cPost(t, owner, ts.URL+spacePath+"/settings", url.Values{
		"name": {"Short"}, "default_timezone": {""}, "retention_days": {"7"},
	}); resp.StatusCode != http.StatusOK {
		t.Fatal("settings")
	}

	// Poll created in this space inherits the 7-day horizon: vote on it
	// 1 day later and it still expires 7 days after the vote, not 180.
	// (Here we just check creation worked; the horizon math is covered
	// by domain tests.)
	resp, _ := cPost(t, owner, ts.URL+"/polls", url.Values{
		"title": {"Quick"}, "kind": {"allday"}, "option_date": {"2026-10-01"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d", resp.StatusCode)
	}
}

func TestTransferOwnershipFlow(t *testing.T) {
	ts, mailer := newTestServer(t)
	owner := jarClient(t)
	signInByEmail(t, ts, mailer, owner, "owner@example.com")
	spacePath := createSpace(t, ts, owner, "Handover")

	// Bring in an admin.
	cPost(t, owner, ts.URL+spacePath+"/invitations", url.Values{"email": {"heir@example.com"}, "role": {"admin"}})
	_, mailBody := mailer.last(t)
	m := inviteLinkRe.FindStringSubmatch(mailBody)
	heir := jarClient(t)
	signInByEmail(t, ts, mailer, heir, "heir@example.com")
	cPost(t, heir, ts.URL+"/invitations/"+m[1], nil)

	// Find the heir's user id on the settings page (transfer form).
	_, body := cGet(t, owner, ts.URL+spacePath+"/settings")
	tm := regexp.MustCompile(`/members/(\d+)/transfer`).FindStringSubmatch(body)
	if tm == nil {
		t.Fatalf("no transfer form on settings page")
	}
	resp, _ := cPost(t, owner, ts.URL+spacePath+"/members/"+tm[1]+"/transfer", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transfer: %d", resp.StatusCode)
	}
	// The previous owner is now an admin: the page shows a transfer
	// action against them (the heir is owner and sees it).
	_, body = cGet(t, heir, ts.URL+spacePath+"/settings")
	if !strings.Contains(body, "/transfer") {
		t.Errorf("heir does not see owner controls after transfer")
	}
	// The previous owner no longer has owner controls over roles.
	resp, _ = cPost(t, owner, ts.URL+spacePath+"/members/"+tm[1]+"/role", url.Values{"role": {"member"}})
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusConflict {
		t.Errorf("previous owner still changes roles: %d", resp.StatusCode)
	}
}
