package poll

import "time"

// ValidTimezone reports whether tz is a loadable IANA zone name.
// "Local" is rejected: a server-relative zone means nothing to voters.
func ValidTimezone(tz string) bool {
	if tz == "" || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// CommonTimezones is the curated list offered by the timezone
// selector. Any valid IANA name is accepted on input (e.g. the one
// detected by the browser); this list only feeds the dropdown.
var CommonTimezones = []string{
	"Pacific/Honolulu",
	"America/Anchorage",
	"America/Los_Angeles",
	"America/Denver",
	"America/Chicago",
	"America/New_York",
	"America/Toronto",
	"America/Halifax",
	"America/Sao_Paulo",
	"America/Argentina/Buenos_Aires",
	"Atlantic/Azores",
	"Europe/London",
	"Europe/Dublin",
	"Europe/Lisbon",
	"Europe/Paris",
	"Europe/Brussels",
	"Europe/Amsterdam",
	"Europe/Berlin",
	"Europe/Madrid",
	"Europe/Rome",
	"Europe/Zurich",
	"Europe/Stockholm",
	"Europe/Warsaw",
	"Europe/Athens",
	"Europe/Helsinki",
	"Europe/Kyiv",
	"Europe/Istanbul",
	"Europe/Moscow",
	"Africa/Casablanca",
	"Africa/Lagos",
	"Africa/Cairo",
	"Africa/Nairobi",
	"Africa/Johannesburg",
	"Asia/Dubai",
	"Asia/Karachi",
	"Asia/Kolkata",
	"Asia/Dhaka",
	"Asia/Bangkok",
	"Asia/Singapore",
	"Asia/Hong_Kong",
	"Asia/Shanghai",
	"Asia/Tokyo",
	"Asia/Seoul",
	"Australia/Perth",
	"Australia/Sydney",
	"Pacific/Auckland",
	"UTC",
}
