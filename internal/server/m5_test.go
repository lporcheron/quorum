package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestManualLanguageSwitch(t *testing.T) {
	ts, _ := newTestServer(t)
	c := jarClient(t)

	// English browser asks for French explicitly.
	resp, body := cPost(t, c, ts.URL+"/lang", url.Values{"lang": {"fr"}, "next": {"/login"}})
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/login" {
		t.Fatalf("switch landed on %s (%d)", resp.Request.URL.Path, resp.StatusCode)
	}
	if !strings.Contains(body, `lang="fr"`) || !strings.Contains(body, "Se connecter") {
		t.Errorf("page not in French after switch")
	}
	// The choice sticks on later navigation and beats Accept-Language.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp2, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp2.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), `lang="fr"`) {
		t.Errorf("cookie did not override Accept-Language")
	}

	// Garbage language is refused.
	resp, _ = cPost(t, c, ts.URL+"/lang", url.Values{"lang": {"xx"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad lang: %d", resp.StatusCode)
	}
}

func TestInstanceAdminPage(t *testing.T) {
	ts, mailer := newTestServer(t)

	// A regular account is refused.
	pleb := jarClient(t)
	signInByEmail(t, ts, mailer, pleb, "pleb@example.com")
	resp, _ := cGet(t, pleb, ts.URL+"/admin")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin got %d, want 403", resp.StatusCode)
	}

	// The configured admin manages settings.
	root := jarClient(t)
	signInByEmail(t, ts, mailer, root, "root@example.com")
	resp, body := cGet(t, root, ts.URL+"/admin")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "pleb@example.com") {
		t.Fatalf("admin page: %d (users missing?)", resp.StatusCode)
	}
	if !strings.Contains(body, "instance_name") {
		t.Errorf("settings form missing")
	}

	// Rename the instance and close registrations, hot.
	resp, body = cPostS(t, ts, root, "/admin/settings", url.Values{
		"instance_name": {"Bleemeo Polls"},
		// registrations_open unchecked
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save settings: %d", resp.StatusCode)
	}
	if !strings.Contains(body, `value="Bleemeo Polls"`) {
		t.Errorf("instance name not saved")
	}
	// The header now carries the instance name for everyone.
	_, home := cGet(t, jarClient(t), ts.URL+"/")
	if !strings.Contains(home, "Bleemeo Polls") {
		t.Errorf("header does not show the instance name")
	}
	// Registrations are closed at runtime: an unknown email gets no
	// magic link (silent no-op, no account enumeration).
	before := mailer.count()
	stranger := jarClient(t)
	cPost(t, stranger, ts.URL+"/auth/email", url.Values{"email": {"stranger@example.com"}})
	if after := mailer.count(); after != before {
		t.Errorf("magic link sent while registrations closed")
	}
	// Existing users still sign in.
	again := jarClient(t)
	signInByEmail(t, ts, mailer, again, "pleb@example.com")
}

func TestMetricsExposition(t *testing.T) {
	ts, _ := newTestServer(t)
	get(t, ts, "/", nil) // generate at least one observation
	resp, body := get(t, ts, "/metrics", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics: %d", resp.StatusCode)
	}
	for _, want := range []string{
		`quorum_http_requests_total{class="2xx"}`,
		"quorum_http_request_seconds_count",
		"quorum_polls 0",
		"quorum_jobs_pending 0",
		"quorum_build_info",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestMagicLinkRateLimit(t *testing.T) {
	ts, _ := newTestServer(t)
	c := jarClient(t)
	// The email budget is 5 per hour per IP.
	for i := 0; i < 5; i++ {
		resp, _ := cPost(t, c, ts.URL+"/auth/email", url.Values{"email": {"burst@example.com"}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: %d", i+1, resp.StatusCode)
		}
	}
	resp, _ := cPost(t, c, ts.URL+"/auth/email", url.Values{"email": {"burst@example.com"}})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("sixth request: %d, want 429", resp.StatusCode)
	}
}
