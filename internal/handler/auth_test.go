package handler

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestNextRejectsOffsiteDestinations pins the sanitizing down at the
// handler, not only in auth.SanitizeRedirect: what made "/\evil.com"
// reachable was not a wrong rule but a second copy of it here.
func TestNextRejectsOffsiteDestinations(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"/dashboard", "/dashboard"},
		{"/polls/abc?new=1", "/polls/abc?new=1"},
		{"", ""},
		{"//evil.com", ""},
		{`/\evil.com`, ""},       // browsers read the backslash as "/"
		{`/\/evil.com`, ""},      // and again, one level in
		{"https://evil.com", ""}, // absolute
		{"evil.com", ""},         // relative to the current directory
		{"/ok\r\nX-Evil: 1", ""}, // header splitting
	} {
		r := httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{"next": {tc.in}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if got := next(r); got != tc.want {
			t.Errorf("next(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
