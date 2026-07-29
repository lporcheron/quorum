package templates

import (
	"fmt"
	"time"

	"github.com/lporcheron/quorum/internal/poll"
)

// Localized date parts. Hardcoding two languages is deliberate for M1;
// when a third language lands (M5), move these into the catalogs.
var monthsShort = map[string][12]string{
	"en": {"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
	"fr": {"janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."},
}

var weekdaysShort = map[string][7]string{ // indexed by time.Weekday (Sunday = 0)
	"en": {"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"},
	"fr": {"dim.", "lun.", "mar.", "mer.", "jeu.", "ven.", "sam."},
}

func monthShort(lang string, m time.Month) string {
	ms, ok := monthsShort[lang]
	if !ok {
		ms = monthsShort["en"]
	}
	return ms[m-1]
}

func weekdayShort(lang string, w time.Weekday) string {
	ws, ok := weekdaysShort[lang]
	if !ok {
		ws = weekdaysShort["en"]
	}
	return ws[w]
}

// optionParts resolves an option to civil parts in the viewer location.
func optionParts(o poll.Option, loc *time.Location) (wd time.Weekday, day int, month time.Month, year int) {
	if o.AllDay() {
		return o.Date.Weekday(), o.Date.Day, o.Date.Month, o.Date.Year
	}
	t := o.StartsAt.In(loc)
	return t.Weekday(), t.Day(), t.Month(), t.Year()
}

// OptionWeekday returns e.g. "Sat" / "sam.".
func OptionWeekday(lang string, o poll.Option, loc *time.Location) string {
	wd, _, _, _ := optionParts(o, loc)
	return weekdayShort(lang, wd)
}

// OptionDay returns the day of month, e.g. "12".
func OptionDay(o poll.Option, loc *time.Location) string {
	_, day, _, _ := optionParts(o, loc)
	return fmt.Sprintf("%d", day)
}

// OptionMonthYear returns e.g. "Sep 2026" / "sept. 2026".
func OptionMonthYear(lang string, o poll.Option, loc *time.Location) string {
	_, _, month, year := optionParts(o, loc)
	return fmt.Sprintf("%s %d", monthShort(lang, month), year)
}

// OptionTimeRange returns "19:00 – 21:00" for timed options, "" for
// all-day ones. Always 24-hour: the counts column must align.
func OptionTimeRange(o poll.Option, loc *time.Location) string {
	if o.AllDay() {
		return ""
	}
	start := o.StartsAt.In(loc)
	end := o.EndsAt().In(loc)
	return start.Format("15:04") + " – " + end.Format("15:04")
}

// OptionLabel is the one-line form, e.g. "sam. 12 sept. 2026, 19:00 – 21:00".
func OptionLabel(lang string, o poll.Option, loc *time.Location) string {
	wd, day, month, year := optionParts(o, loc)
	label := fmt.Sprintf("%s %d %s %d", weekdayShort(lang, wd), day, monthShort(lang, month), year)
	if tr := OptionTimeRange(o, loc); tr != "" {
		label += ", " + tr
	}
	return label
}

// durationLabel formats minutes tersely and language-neutrally:
// "45 min", "1 h", "1 h 30".
func durationLabel(minutes int) string {
	h, m := minutes/60, minutes%60
	switch {
	case h == 0:
		return fmt.Sprintf("%d min", m)
	case m == 0:
		return fmt.Sprintf("%d h", h)
	default:
		return fmt.Sprintf("%d h %02d", h, m)
	}
}

// Stamp formats a timestamp for comment bylines, in the viewer's zone.
func Stamp(lang string, t time.Time, loc *time.Location) string {
	lt := t.In(loc)
	return fmt.Sprintf("%d %s %d, %s", lt.Day(), monthShort(lang, lt.Month()), lt.Year(), lt.Format("15:04"))
}
