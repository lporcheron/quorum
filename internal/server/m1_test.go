package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// postForm posts values and follows redirects; returns the final
// response and its body.
func postForm(t *testing.T, ts *httptest.Server, path string, values url.Values) (*http.Response, string) {
	t.Helper()
	resp, err := ts.Client().PostForm(ts.URL+path, values)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

// createPoll drives the real creation form and returns the admin path.
func createPoll(t *testing.T, ts *httptest.Server, extra url.Values) string {
	t.Helper()
	form := url.Values{
		"title":           {"Team dinner"},
		"kind":            {"timed"},
		"timezone":        {"Europe/Paris"},
		"allow_comments":  {"1"},
		"option_date":     {"2026-09-12", "2026-09-13"},
		"option_start":    {"19:00", "19:00"},
		"option_duration": {"120", "120"},
	}
	for k, vs := range extra {
		form[k] = vs
	}
	resp, body := postForm(t, ts, "/polls", form)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create poll: final status %d\n%s", resp.StatusCode, body)
	}
	p := resp.Request.URL.Path
	if !regexp.MustCompile(`^/polls/[1-9A-HJ-NP-Za-km-z]{12}/admin/[1-9A-HJ-NP-Za-km-z]{26}$`).MatchString(p) {
		t.Fatalf("unexpected admin path %q", p)
	}
	if !strings.Contains(body, "admin-url") {
		t.Errorf("admin page missing the save-this-link banner")
	}
	return p
}

func pollPath(adminPath string) string {
	return adminPath[:strings.Index(adminPath, "/admin/")]
}

var voteFieldRe = regexp.MustCompile(`name="vote_(\d+)"`)

func optionIDs(t *testing.T, body string) []string {
	t.Helper()
	seen := map[string]bool{}
	var ids []string
	for _, m := range voteFieldRe.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	if len(ids) == 0 {
		t.Fatalf("no vote fields found in page")
	}
	return ids
}

func TestCreateVoteAndTallyFlow(t *testing.T) {
	ts := newTestServer(t)
	adminPath := createPoll(t, ts, nil)
	public := pollPath(adminPath)

	// Public page shows the options in the poll's zone (19:00 CEST).
	resp, body := get(t, ts, public, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("public page: %d", resp.StatusCode)
	}
	if !strings.Contains(body, "19:00 – 21:00") {
		t.Errorf("public page missing slot time range")
	}
	ids := optionIDs(t, body)
	if len(ids) != 2 {
		t.Fatalf("want 2 options, got %d", len(ids))
	}

	// Alice votes yes/no; she lands on her personal page.
	resp, body = postForm(t, ts, public+"/participants", url.Values{
		"name":           {"Alice"},
		"vote_" + ids[0]: {"yes"},
		"vote_" + ids[1]: {"no"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("vote: final status %d", resp.StatusCode)
	}
	editPath := resp.Request.URL.Path
	if !strings.Contains(editPath, "/p/") {
		t.Fatalf("expected personal edit URL, got %q", editPath)
	}
	if !strings.Contains(body, "Alice") || !strings.Contains(body, "edit-url") {
		t.Errorf("edit page missing name or personal-link banner")
	}
	// The first option is now the winner.
	if !strings.Contains(body, "qwin") {
		t.Errorf("no winner styling after a yes vote")
	}

	// Alice flips her vote through her personal link.
	resp, body = postForm(t, ts, editPath+"/votes", url.Values{
		"name":           {"Alice"},
		"vote_" + ids[0]: {"no"},
		"vote_" + ids[1]: {"ifneedbe"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update vote: %d", resp.StatusCode)
	}
	if !strings.Contains(body, `value="ifneedbe" checked`) {
		t.Errorf("updated vote not prefilled on edit page")
	}

	// A wrong edit token is a 404.
	resp, _ = get(t, ts, public+"/p/26charsbogus26charsbogus26", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("bogus edit token: %d, want 404", resp.StatusCode)
	}
}

func TestTimezoneSwitch(t *testing.T) {
	ts := newTestServer(t)
	adminPath := createPoll(t, ts, nil)
	public := pollPath(adminPath)

	// 19:00 Europe/Paris (CEST) is 02:00 next day in Tokyo.
	resp, body := get(t, ts, public+"/grid?tz=Asia/Tokyo", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("grid: %d", resp.StatusCode)
	}
	if !strings.Contains(body, "02:00 – 04:00") {
		t.Errorf("Tokyo conversion missing (want 02:00 – 04:00):\n%s", body)
	}
	var cookie string
	for _, c := range resp.Header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "quorum_tz=") {
			cookie = c
		}
	}
	if !strings.Contains(cookie, "quorum_tz=Asia%2FTokyo") && !strings.Contains(cookie, "quorum_tz=Asia/Tokyo") {
		t.Errorf("tz cookie not set: %q", cookie)
	}

	// Invalid tz falls back to the poll's own zone.
	_, body = get(t, ts, public+"/grid?tz=Mars/Olympus", nil)
	if !strings.Contains(body, "19:00 – 21:00") {
		t.Errorf("invalid tz did not fall back to poll zone")
	}
}

func TestAdminManagement(t *testing.T) {
	ts := newTestServer(t)
	adminPath := createPoll(t, ts, nil)
	public := pollPath(adminPath)

	// Wrong token → 403.
	resp, _ := get(t, ts, public+"/admin/26charsbogus26charsbogus26", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("bogus admin token: %d, want 403", resp.StatusCode)
	}

	// Pause voting; the public form must reject votes.
	resp, body := postForm(t, ts, adminPath+"/status", url.Values{"action": {"pause"}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "resume") && !strings.Contains(body, "Rouvrir") {
		t.Fatalf("pause: status %d", resp.StatusCode)
	}
	_, body = get(t, ts, public, nil)
	ids := []string{}
	if m := voteFieldRe.FindStringSubmatch(body); m != nil {
		ids = append(ids, m[1])
	}
	if len(ids) != 0 {
		t.Errorf("vote form still rendered while paused")
	}
	resp, _ = postForm(t, ts, public+"/participants", url.Values{"name": {"Mallory"}})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("vote while paused: %d, want 409", resp.StatusCode)
	}
	if _, err := ts.Client().PostForm(ts.URL+adminPath+"/status", url.Values{"action": {"resume"}}); err != nil {
		t.Fatal(err)
	}

	// Add a third option from the admin form.
	resp, _ = postForm(t, ts, adminPath+"/options", url.Values{
		"option_date":     {"2026-09-14"},
		"option_start":    {"20:00"},
		"option_duration": {"60"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add option: %d", resp.StatusCode)
	}
	_, body = get(t, ts, public, nil)
	if got := len(optionIDs(t, body)); got != 3 {
		t.Errorf("options after add = %d, want 3", got)
	}

	// Regenerate invalidates the old link.
	resp, _ = postForm(t, ts, adminPath+"/regenerate", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("regenerate: %d", resp.StatusCode)
	}
	freshAdmin := resp.Request.URL.Path
	if freshAdmin == adminPath {
		t.Fatalf("admin path unchanged after regenerate")
	}
	resp, _ = get(t, ts, adminPath, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("old admin link still valid: %d", resp.StatusCode)
	}

	// Delete the poll.
	resp, _ = postForm(t, ts, freshAdmin+"/delete", nil)
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/" {
		t.Fatalf("delete: %d at %s", resp.StatusCode, resp.Request.URL.Path)
	}
	resp, _ = get(t, ts, public, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("deleted poll still served: %d", resp.StatusCode)
	}
}

func TestCommentsFlow(t *testing.T) {
	ts := newTestServer(t)
	adminPath := createPoll(t, ts, nil)
	public := pollPath(adminPath)

	resp, body := postForm(t, ts, public+"/comments", url.Values{
		"author_name": {"Carol"},
		"body":        {"Saturday works best for me"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("comment: %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Carol") || !strings.Contains(body, "Saturday works best") {
		t.Errorf("comment not on page")
	}

	// Comments disabled → hidden and rejected.
	adminPath2 := createPoll(t, ts, url.Values{"allow_comments": {""}})
	public2 := pollPath(adminPath2)
	_, body = get(t, ts, public2, nil)
	if strings.Contains(body, "author_name") {
		t.Errorf("comment form rendered on a no-comments poll")
	}
	resp, _ = postForm(t, ts, public2+"/comments", url.Values{"author_name": {"X"}, "body": {"hi"}})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("comment on disabled poll: %d, want 409", resp.StatusCode)
	}
}

func TestHiddenParticipants(t *testing.T) {
	ts := newTestServer(t)
	adminPath := createPoll(t, ts, url.Values{"hide_participants": {"1"}})
	public := pollPath(adminPath)

	_, body := get(t, ts, public, nil)
	ids := optionIDs(t, body)
	if _, err := ts.Client().PostForm(ts.URL+public+"/participants", url.Values{
		"name": {"SecretGuest"}, "vote_" + ids[0]: {"yes"},
	}); err != nil {
		t.Fatal(err)
	}

	// Public page: totals yes, names no.
	_, body = get(t, ts, public, nil)
	if strings.Contains(body, "SecretGuest") {
		t.Errorf("participant name leaked on a hidden-participants poll")
	}
	// Admin page still sees everyone.
	_, body = get(t, ts, adminPath, nil)
	if !strings.Contains(body, "SecretGuest") {
		t.Errorf("admin page does not list the participant")
	}
}

func TestAllDayPoll(t *testing.T) {
	ts := newTestServer(t)
	resp, body := postForm(t, ts, "/polls", url.Values{
		"title":       {"Offsite"},
		"kind":        {"allday"},
		"option_date": {"2026-10-01", "2026-10-02"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create all-day poll: %d\n%s", resp.StatusCode, body)
	}
	public := pollPath(resp.Request.URL.Path)
	_, body = get(t, ts, public, nil)
	if strings.Contains(body, "tz-select") {
		t.Errorf("timezone selector rendered on an all-day poll")
	}
	if strings.Contains(body, " – ") {
		t.Errorf("time range rendered on an all-day poll")
	}
}

func TestCreateValidationRerendersForm(t *testing.T) {
	ts := newTestServer(t)
	resp, body := postForm(t, ts, "/polls", url.Values{
		"title": {"No dates"},
		"kind":  {"timed"},
		// no options at all
		"timezone": {"Europe/Paris"},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if !strings.Contains(body, `value="No dates"`) {
		t.Errorf("title not preserved on re-render")
	}
	if !strings.Contains(body, "role=\"alert\"") {
		t.Errorf("error message missing")
	}
}
