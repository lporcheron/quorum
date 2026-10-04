package poll

import (
	"net/mail"
	"net/url"
	"strings"
)

// maxVideoURL matches the form's maxlength.
const maxVideoURL = 500

// validEmail accepts a bare address only: net/mail also parses
// `Name <addr>` forms, and anything beyond the address itself (a
// display name, CR/LF) has no business in a participant record that
// ends up in calendar invitations and mail headers.
func validEmail(email string) bool {
	if len(email) > 254 || hasControl(email) {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}

// validVideoURL accepts an absolute http(s) URL, or nothing. The link
// is rendered as an href and written into calendar objects.
func validVideoURL(raw string) bool {
	if raw == "" {
		return true
	}
	if len(raw) > maxVideoURL || hasControl(raw) || strings.ContainsAny(raw, " \t") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}
