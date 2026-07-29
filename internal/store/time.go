package store

import (
	"fmt"
	"time"
)

// Instants are stored as UTC RFC 3339 TEXT with second precision:
// fixed width, sorts lexicographically in chronological order, maps to
// timestamptz when the PostgreSQL store lands. These two functions are
// the only place the encoding lives.

const timeLayout = "2006-01-02T15:04:05Z"

// FormatTime encodes an instant for storage.
func FormatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

// ParseTime decodes a stored instant.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored time %q: %w", s, err)
	}
	return t, nil
}
