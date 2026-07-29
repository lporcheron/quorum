package poll

import (
	"testing"
	"time"
)

func TestParseDate(t *testing.T) {
	d, err := ParseDate("2026-09-12")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	if d.String() != "2026-09-12" || d.Weekday() != time.Saturday {
		t.Errorf("d = %v (%v)", d, d.Weekday())
	}
	for _, bad := range []string{"", "12/09/2026", "2026-13-01", "2026-02-30", "2026-9-1"} {
		if _, err := ParseDate(bad); err == nil {
			t.Errorf("ParseDate(%q) accepted", bad)
		}
	}
}

// The number-one bug source in this kind of tool: the same wall-clock
// time on both sides of a DST transition maps to different UTC offsets.
func TestTimedSlotAcrossDST(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	// Europe/Paris springs forward on 2026-03-29 (02:00 → 03:00).
	before := TimedSlot{Date: Date{2026, time.March, 28}, Hour: 18, Duration: time.Hour}
	after := TimedSlot{Date: Date{2026, time.March, 30}, Hour: 18, Duration: time.Hour}

	if got := before.StartUTC(paris); got.Format(time.RFC3339) != "2026-03-28T17:00:00Z" {
		t.Errorf("18:00 CET = %v UTC, want 17:00Z", got)
	}
	if got := after.StartUTC(paris); got.Format(time.RFC3339) != "2026-03-30T16:00:00Z" {
		t.Errorf("18:00 CEST = %v UTC, want 16:00Z", got)
	}

	// A slot inside the nonexistent 02:00–03:00 gap normalizes forward
	// instead of failing: 02:30 becomes 03:30 CEST.
	gap := TimedSlot{Date: Date{2026, time.March, 29}, Hour: 2, Minute: 30, Duration: time.Hour}
	if got := gap.StartUTC(paris).In(paris).Format("15:04"); got != "03:30" {
		t.Errorf("gap slot resolved to %s local, want 03:30", got)
	}

	// Fall back on 2026-10-25 (03:00 → 02:00): the ambiguous 02:30
	// resolves to a single instant — assert it stays 02:30 local.
	ambiguous := TimedSlot{Date: Date{2026, time.October, 25}, Hour: 2, Minute: 30, Duration: time.Hour}
	if got := ambiguous.StartUTC(paris).In(paris).Format("15:04"); got != "02:30" {
		t.Errorf("ambiguous slot resolved to %s local, want 02:30", got)
	}
}

func TestSameWallClockDifferentZones(t *testing.T) {
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	newYork, _ := time.LoadLocation("America/New_York")
	slot := TimedSlot{Date: Date{2026, time.June, 1}, Hour: 9, Duration: time.Hour}

	diff := slot.StartUTC(newYork).Sub(slot.StartUTC(tokyo))
	if diff != 13*time.Hour {
		t.Errorf("Tokyo→New York 09:00 gap = %v, want 13h", diff)
	}
}
