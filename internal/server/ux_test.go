package server

import (
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestRefusedVoteKeepsTheBallot: a ballot refused for a bad email comes
// back on the poll page with the message in place and every choice
// still ticked — not a bare error page that throws the input away.
func TestRefusedVoteKeepsTheBallot(t *testing.T) {
	ts, _ := newTestServer(t)
	public := pollPath(createPoll(t, ts, nil))
	_, page := cGet(t, jarClient(t), ts.URL+public)
	ids := optionIDs(t, page)

	resp, body := cPost(t, jarClient(t), ts.URL+public+"/participants", url.Values{
		"name": {"Dana"}, "email": {"not-an-email"},
		"vote_" + ids[0]: {"yes"}, "vote_" + ids[1]: {"ifneedbe"},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", resp.StatusCode)
	}
	body = html.UnescapeString(body)
	for _, want := range []string{
		"This email address is not valid",
		`value="Dana"`, `value="not-an-email"`,
		`name="vote_` + ids[0] + `" value="yes" checked`,
		`name="vote_` + ids[1] + `" value="ifneedbe" checked`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("re-rendered page lacks %q", want)
		}
	}
}

// TestRefusedCreationKeepsTheForm: a refused creation form keeps its
// kind, its options and its settings, not only the text fields.
func TestRefusedCreationKeepsTheForm(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, body := cPost(t, jarClient(t), ts.URL+"/polls", url.Values{
		"title": {"Picnic"}, "kind": {"allday"}, "video_url": {"javascript:alert(1)"},
		"option_date": {"2026-09-12", "2026-09-19"}, "hide_participants": {"1"},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", resp.StatusCode)
	}
	body = html.UnescapeString(body)
	for _, want := range []string{
		"The video link must be a full address",
		`value="Picnic"`,
		`value="allday" checked`,
		`name="hide_participants" value="1" checked`,
		`"d":"2026-09-12"`, `"d":"2026-09-19"`, // seeds the calendar
		`name="option_date" value="2026-09-19"`, // and the no-JS rows
	} {
		if !strings.Contains(body, want) {
			t.Errorf("re-rendered form lacks %q", want)
		}
	}
}

// TestErrorPageLeadsBackToThePoll: a failed action on a poll offers a
// way back to it, not only to the home page.
func TestErrorPageLeadsBackToThePoll(t *testing.T) {
	ts, _ := newTestServer(t)
	adminPath := createPoll(t, ts, nil)

	req, err := http.NewRequest("POST", ts.URL+adminPath, strings.NewReader(url.Values{"title": {"  "}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", ts.URL+adminPath)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `href="`+adminPath+`"`) || !strings.Contains(string(raw), "Back to the poll") {
		t.Errorf("error page has no way back to the admin page it came from")
	}

	// A missing poll offers no dead link back.
	_, body := cGet(t, jarClient(t), ts.URL+"/polls/doesnotexist")
	if strings.Contains(body, "Back to the poll") {
		t.Errorf("404 offers a way back to a poll that does not exist")
	}
}
