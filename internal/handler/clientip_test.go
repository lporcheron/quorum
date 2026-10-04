package handler

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	for _, tc := range []struct {
		name   string
		trust  bool
		remote string
		xff    []string
		want   string
	}{
		{"direct", false, "203.0.113.9:4242", nil, "203.0.113.9"},
		{"untrusted header ignored", false, "203.0.113.9:4242", []string{"1.2.3.4"}, "203.0.113.9"},
		// The client-chosen prefix must not count: the proxy's own entry
		// is the last one.
		{"spoofed prefix", true, "10.0.0.2:80", []string{"6.6.6.6, 198.51.100.7"}, "198.51.100.7"},
		{"split headers", true, "10.0.0.2:80", []string{"6.6.6.6", "198.51.100.7"}, "198.51.100.7"},
		{"no header behind proxy", true, "10.0.0.2:80", nil, "10.0.0.2"},
		{"ipv6 keyed by /64", true, "10.0.0.2:80", []string{"2001:db8:1:2:aaaa::1"}, "2001:db8:1:2::/64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{trustProxy: tc.trust}
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := h.clientIP(r); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
