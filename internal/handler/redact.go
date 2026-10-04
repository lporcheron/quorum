package handler

import "strings"

// redacted replaces a capability token in logged paths.
const redacted = "REDACTED"

// RedactPath masks the capability tokens some routes carry in their
// path — /polls/{id}/admin/{adminToken}, /polls/{id}/p/{editToken},
// /invitations/{token} — so that whoever reads the logs cannot take
// over a poll, a vote or a space seat. Every log line that records a
// request path goes through it.
func RedactPath(path string) string {
	seg := strings.Split(path, "/")
	switch {
	case len(seg) > 4 && seg[0] == "" && seg[1] == "polls" && (seg[3] == "admin" || seg[3] == "p"):
		seg[4] = redacted
	case len(seg) > 2 && seg[0] == "" && seg[1] == "invitations":
		seg[2] = redacted
	default:
		return path
	}
	return strings.Join(seg, "/")
}
