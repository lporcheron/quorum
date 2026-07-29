// Package ics renders RFC 5545 calendar objects for polls: the
// METHOD:REQUEST invitation attached to finalization emails, its
// CANCEL counterpart, and the per-poll subscription feed.
package ics

import (
	"fmt"
	"net/url"
	"time"

	ical "github.com/arran4/golang-ical"

	"github.com/lporcheron/quorum/internal/poll"
)

// Organizer identifies the event organizer in outgoing invitations.
// The email is the instance sender (a real mailbox), the name is the
// human organizer.
type Organizer struct {
	Email string
	Name  string
}

// uid returns the stable identifier for a poll's event; REQUEST and
// CANCEL must share it for clients to match them.
func uid(p poll.Poll, o poll.Option, baseURL string) string {
	host := "quorum"
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		host = u.Hostname()
	}
	return fmt.Sprintf("quorum-%s-%d@%s", p.PublicID, o.ID, host)
}

func setEventTimes(ev *ical.VEvent, o poll.Option) {
	if o.AllDay() {
		day := time.Date(o.Date.Year, o.Date.Month, o.Date.Day, 0, 0, 0, 0, time.UTC)
		ev.SetAllDayStartAt(day)
		ev.SetAllDayEndAt(day.AddDate(0, 0, 1)) // DTEND is exclusive
		return
	}
	ev.SetStartAt(o.StartsAt.UTC())
	ev.SetEndAt(o.EndsAt().UTC())
}

func fillEvent(ev *ical.VEvent, p poll.Poll, o poll.Option, baseURL string, now time.Time) {
	ev.SetDtStampTime(now.UTC())
	ev.SetSummary(p.Title)
	if p.Description != "" {
		ev.SetDescription(p.Description)
	}
	if p.Location != "" {
		ev.SetLocation(p.Location)
	}
	if p.VideoURL != "" {
		ev.SetURL(p.VideoURL)
	} else {
		ev.SetURL(baseURL + "/polls/" + p.PublicID)
	}
	setEventTimes(ev, o)
}

// Invite renders the METHOD:REQUEST object sent when a poll is
// finalized.
func Invite(p poll.Poll, o poll.Option, org Organizer, attendees []string, baseURL string, now time.Time) []byte {
	cal := ical.NewCalendar()
	cal.SetProductId("-//Quorum//Quorum//EN")
	cal.SetMethod(ical.MethodRequest)
	ev := cal.AddEvent(uid(p, o, baseURL))
	fillEvent(ev, p, o, baseURL, now)
	ev.SetStatus(ical.ObjectStatusConfirmed)
	ev.SetSequence(0)
	setOrganizer(ev, org)
	for _, a := range attendees {
		ev.AddAttendee(a, ical.WithRSVP(false))
	}
	return []byte(cal.Serialize())
}

// Cancel renders the METHOD:CANCEL counterpart of a previously sent
// invitation. The sequence bump tells clients this supersedes it.
func Cancel(p poll.Poll, o poll.Option, org Organizer, attendees []string, baseURL string, now time.Time) []byte {
	cal := ical.NewCalendar()
	cal.SetProductId("-//Quorum//Quorum//EN")
	cal.SetMethod(ical.MethodCancel)
	ev := cal.AddEvent(uid(p, o, baseURL))
	fillEvent(ev, p, o, baseURL, now)
	ev.SetStatus(ical.ObjectStatusCancelled)
	ev.SetSequence(1)
	setOrganizer(ev, org)
	for _, a := range attendees {
		ev.AddAttendee(a)
	}
	return []byte(cal.Serialize())
}

// Feed renders the per-poll subscription calendar: every option as a
// TENTATIVE event while the poll runs, the chosen option alone
// (CONFIRMED, or CANCELLED after cancellation) once decided.
func Feed(p poll.Poll, options []poll.Option, finalizedOptionID int64, baseURL string, now time.Time) []byte {
	cal := ical.NewCalendar()
	cal.SetProductId("-//Quorum//Quorum//EN")
	cal.SetMethod(ical.MethodPublish)
	cal.SetName(p.Title)

	for _, o := range options {
		switch {
		case finalizedOptionID == 0: // still voting: every option, tentative
			ev := cal.AddEvent(uid(p, o, baseURL))
			fillEvent(ev, p, o, baseURL, now)
			ev.SetStatus(ical.ObjectStatusTentative)
		case o.ID == finalizedOptionID:
			ev := cal.AddEvent(uid(p, o, baseURL))
			fillEvent(ev, p, o, baseURL, now)
			if p.Status == poll.StatusCancelled {
				ev.SetStatus(ical.ObjectStatusCancelled)
				ev.SetSequence(1)
			} else {
				ev.SetStatus(ical.ObjectStatusConfirmed)
			}
		}
	}
	return []byte(cal.Serialize())
}

func setOrganizer(ev *ical.VEvent, org Organizer) {
	if org.Email == "" {
		return
	}
	props := []ical.PropertyParameter{}
	if org.Name != "" {
		props = append(props, ical.WithCN(org.Name))
	}
	ev.SetOrganizer("mailto:"+org.Email, props...)
}
