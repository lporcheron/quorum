package ratelimit

import (
	"testing"
	"time"
)

func TestWindowLimit(t *testing.T) {
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	l := New(3, time.Hour, func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if !l.Allow("ip1") {
			t.Fatalf("request %d refused inside the budget", i+1)
		}
	}
	if l.Allow("ip1") {
		t.Error("fourth request allowed")
	}
	// Another key has its own budget.
	if !l.Allow("ip2") {
		t.Error("other key throttled")
	}
	// The window rolls over.
	now = now.Add(time.Hour)
	if !l.Allow("ip1") {
		t.Error("request refused after window rollover")
	}
}
