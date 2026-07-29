package poll

import (
	"fmt"
	"time"
)

// Date is a civil date without timezone: an all-day option is the same
// date for everyone on Earth. Never a midnight timestamp.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// ParseDate parses strict YYYY-MM-DD.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date %q: %w", s, err)
	}
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}, nil
}

func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

func (d Date) IsZero() bool { return d == Date{} }

// Weekday returns the day of week of the civil date.
func (d Date) Weekday() time.Weekday {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC).Weekday()
}

// TimedSlot is a proposed meeting slot expressed in the poll's local
// wall-clock time; conversion to UTC happens exactly once, here, so DST
// is handled in one tested place.
type TimedSlot struct {
	Date     Date
	Hour     int
	Minute   int
	Duration time.Duration
}

// StartUTC resolves the wall-clock slot in loc to an instant. Around
// DST transitions time.Date normalizes: a nonexistent local time (the
// spring-forward gap) resolves to the instant after the jump.
func (s TimedSlot) StartUTC(loc *time.Location) time.Time {
	return time.Date(s.Date.Year, s.Date.Month, s.Date.Day, s.Hour, s.Minute, 0, 0, loc).UTC()
}
