package handler

import "testing"

func TestRedactPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/polls/abc/admin/SECRET", "/polls/abc/admin/REDACTED"},
		{"/polls/abc/admin/SECRET/options/3/delete", "/polls/abc/admin/REDACTED/options/3/delete"},
		{"/polls/abc/p/SECRET/votes", "/polls/abc/p/REDACTED/votes"},
		{"/invitations/SECRET", "/invitations/REDACTED"},
		{"/polls/abc", "/polls/abc"},
		{"/polls/abc/manage", "/polls/abc/manage"},
		{"/dashboard", "/dashboard"},
		{"/", "/"},
	} {
		if got := RedactPath(tc.in); got != tc.want {
			t.Errorf("RedactPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
