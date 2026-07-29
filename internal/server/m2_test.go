package server

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// jarClient keeps cookies across requests: a signed-in browser.
func jarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}

func cGet(t *testing.T, c *http.Client, url string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func cPost(t *testing.T, c *http.Client, url string, values url.Values) (*http.Response, string) {
	t.Helper()
	resp, err := c.PostForm(url, values)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

var magicTokenRe = regexp.MustCompile(`token=([1-9A-HJ-NP-Za-km-z]{26})`)

// signInByEmail drives the full magic-link flow for a fresh browser.
func signInByEmail(t *testing.T, ts *httptest.Server, mailer *testMailer, c *http.Client, email string) {
	t.Helper()
	resp, body := cPost(t, c, ts.URL+"/auth/email", url.Values{"email": {email}, "next": {"/dashboard"}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "inbox") && !strings.Contains(body, "boîte") {
		t.Fatalf("magic link request: %d", resp.StatusCode)
	}
	to, mailBody := mailer.last(t)
	if to != strings.ToLower(email) {
		t.Fatalf("mail sent to %q", to)
	}
	m := magicTokenRe.FindStringSubmatch(mailBody)
	if m == nil {
		t.Fatalf("no token in mail body:\n%s", mailBody)
	}
	resp, body = cGet(t, c, ts.URL+"/auth/email/callback?token="+m[1])
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/dashboard" {
		t.Fatalf("callback landed on %s (%d)", resp.Request.URL.Path, resp.StatusCode)
	}
	if !strings.Contains(body, "/auth/logout") {
		t.Fatalf("dashboard has no logout; not signed in?\n%s", body)
	}
}

func TestMagicLinkSignInAndLogout(t *testing.T) {
	ts, mailer := newTestServer(t)
	c := jarClient(t)
	signInByEmail(t, ts, mailer, c, "Carol@Example.com")

	// The account exists with the email localpart as name.
	_, body := cGet(t, c, ts.URL+"/dashboard")
	if !strings.Contains(body, "carol") {
		t.Errorf("dashboard missing user name")
	}

	// Reusing the same link fails (single use) — covered at the domain
	// level; here check logout kills the session.
	resp, _ := cPost(t, c, ts.URL+"/auth/logout", nil)
	if resp.Request.URL.Path != "/" {
		t.Errorf("logout landed on %s", resp.Request.URL.Path)
	}
	resp, _ = cGet(t, c, ts.URL+"/dashboard")
	if resp.Request.URL.Path != "/login" {
		t.Errorf("dashboard after logout landed on %s, want /login", resp.Request.URL.Path)
	}
}

func TestDashboardRequiresLogin(t *testing.T) {
	ts, _ := newTestServer(t)
	c := jarClient(t)
	resp, body := cGet(t, c, ts.URL+"/dashboard")
	if resp.Request.URL.Path != "/login" {
		t.Fatalf("landed on %s, want /login", resp.Request.URL.Path)
	}
	if !strings.Contains(body, "/auth/email") {
		t.Errorf("login page missing email form")
	}
}

func TestClaimPollAndManage(t *testing.T) {
	ts, mailer := newTestServer(t)

	// A guest creates a poll (no session).
	adminPath := createPoll(t, ts, nil)
	public := pollPath(adminPath)

	// The same person signs in and claims it via the admin link.
	c := jarClient(t)
	signInByEmail(t, ts, mailer, c, "owner@example.com")

	_, body := cGet(t, c, ts.URL+adminPath)
	if !strings.Contains(body, "/claim") {
		t.Fatalf("admin page missing claim form for a signed-in user")
	}
	resp, body := cPost(t, c, ts.URL+adminPath+"/claim", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim: %d", resp.StatusCode)
	}
	if strings.Contains(body, "/claim\"") {
		t.Errorf("claim form still shown after claiming")
	}

	// The poll is now in the dashboard and manageable without a token.
	_, body = cGet(t, c, ts.URL+"/dashboard")
	if !strings.Contains(body, public+"/manage") || !strings.Contains(body, "Team dinner") {
		t.Errorf("dashboard missing the claimed poll:\n%s", body)
	}
	resp, body = cGet(t, c, ts.URL+public+"/manage")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Team dinner") {
		t.Fatalf("manage page: %d", resp.StatusCode)
	}
	// Managing works through the session path: pause the poll.
	resp, _ = cPost(t, c, ts.URL+public+"/manage/status", url.Values{"action": {"pause"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pause via manage: %d", resp.StatusCode)
	}

	// Claiming twice fails.
	resp, _ = cPost(t, c, ts.URL+adminPath+"/claim", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("second claim: %d, want 403", resp.StatusCode)
	}

	// Another account cannot manage it.
	other := jarClient(t)
	signInByEmail(t, ts, mailer, other, "intruder@example.com")
	resp, _ = cGet(t, other, ts.URL+public+"/manage")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("intruder manage: %d, want 403", resp.StatusCode)
	}
	// And an anonymous visitor is sent to the login page.
	anon := jarClient(t)
	resp, _ = cGet(t, anon, ts.URL+public+"/manage")
	if resp.Request.URL.Path != "/login" {
		t.Errorf("anonymous manage landed on %s, want /login", resp.Request.URL.Path)
	}
}

func TestSignedInVoteLinksAccount(t *testing.T) {
	ts, mailer := newTestServer(t)
	adminPath := createPoll(t, ts, nil)
	public := pollPath(adminPath)

	c := jarClient(t)
	signInByEmail(t, ts, mailer, c, "voter@example.com")

	// The vote form is prefilled with the account identity.
	_, body := cGet(t, c, ts.URL+public)
	if !strings.Contains(body, `value="voter"`) || !strings.Contains(body, `value="voter@example.com"`) {
		t.Errorf("vote form not prefilled from the account")
	}
	ids := optionIDs(t, body)
	resp, _ := cPost(t, c, ts.URL+public+"/participants", url.Values{
		"name": {"Voter V."}, "vote_" + ids[0]: {"yes"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("vote: %d", resp.StatusCode)
	}

	// The poll shows up under "polls I voted in".
	_, body = cGet(t, c, ts.URL+"/dashboard")
	if !strings.Contains(body, "Team dinner") {
		t.Errorf("voted poll missing from dashboard:\n%s", body)
	}
}
