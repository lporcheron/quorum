// Package notify composes and dispatches notification email through
// the job queue: new vote, new comment, finalized event (with its
// calendar invitation), cancellation, and the expiry reminder.
//
// Capability-carrying email (magic links, invitations) is NOT sent
// from here: their tokens are only ever hashed at rest, so they go out
// synchronously from the handlers instead of sitting in a job payload.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/internal/i18n"
	"github.com/lporcheron/quorum/internal/ics"
	"github.com/lporcheron/quorum/internal/job"
	"github.com/lporcheron/quorum/internal/mail"
	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/web/templates"
)

const (
	typeNotify   = "email.notify"
	typeEvent    = "email.event"
	typeReminder = "email.reminder"
)

// Notifier enqueues notification jobs and executes them.
type Notifier struct {
	log     *slog.Logger
	queue   *job.Queue
	mailer  mail.Mailer
	polls   *poll.Service
	users   *auth.Service
	tr      *i18n.Translator
	baseURL string
	from    string
	now     func() time.Time
}

// New wires a Notifier; now is injectable for tests.
func New(log *slog.Logger, queue *job.Queue, mailer mail.Mailer, polls *poll.Service,
	users *auth.Service, tr *i18n.Translator, baseURL, from string, now func() time.Time,
) *Notifier {
	if now == nil {
		now = time.Now
	}
	return &Notifier{
		log: log, queue: queue, mailer: mailer, polls: polls, users: users,
		tr: tr, baseURL: strings.TrimSuffix(baseURL, "/"), from: from, now: now,
	}
}

// Handlers registers the job types this package executes.
func (n *Notifier) Handlers() map[string]job.Handler {
	return map[string]job.Handler{
		typeNotify:   n.handleNotify,
		typeEvent:    n.handleEvent,
		typeReminder: n.handleReminder,
	}
}

type notifyPayload struct {
	PollID string `json:"poll_id"` // public id
	Kind   string `json:"kind"`    // "vote" | "comment"
	Actor  string `json:"actor"`
}

type eventPayload struct {
	PollID    string `json:"poll_id"`
	To        string `json:"to"`
	Cancelled bool   `json:"cancelled"`
	Locale    string `json:"locale"`
}

// skippable reports whether notifications for this poll can go out at
// all: no mailer or no accountable organizer means nothing to send.
func (n *Notifier) skippable(p poll.Poll) bool {
	return !n.mailer.Enabled() || p.CreatedByUserID == 0
}

// VoteCast tells the organizer somebody voted.
func (n *Notifier) VoteCast(ctx context.Context, p poll.Poll, actor string) {
	if n.skippable(p) {
		return
	}
	n.enqueue(ctx, typeNotify, notifyPayload{PollID: p.PublicID, Kind: "vote", Actor: actor})
}

// CommentPosted tells the organizer somebody commented.
func (n *Notifier) CommentPosted(ctx context.Context, p poll.Poll, actor string) {
	if n.skippable(p) {
		return
	}
	n.enqueue(ctx, typeNotify, notifyPayload{PollID: p.PublicID, Kind: "comment", Actor: actor})
}

// EventDecision fans out the finalized (or cancelled) event to every
// participant who left an email, plus the organizer — one job per
// recipient so a bouncing address only retries its own mail.
func (n *Notifier) EventDecision(ctx context.Context, p poll.Poll, cancelled bool) {
	if !n.mailer.Enabled() {
		return
	}
	locale := "en"
	if organizer, err := n.organizer(ctx, p); err == nil {
		locale = organizer.Locale
		n.enqueue(ctx, typeEvent, eventPayload{PollID: p.PublicID, To: organizer.Email, Cancelled: cancelled, Locale: locale})
	}
	v, err := n.polls.View(ctx, p)
	if err != nil {
		n.log.ErrorContext(ctx, "event fan-out", "error", err)
		return
	}
	for _, pa := range v.Participants {
		if pa.Email != "" {
			n.enqueue(ctx, typeEvent, eventPayload{PollID: p.PublicID, To: pa.Email, Cancelled: cancelled, Locale: locale})
		}
	}
}

// Remind nudges the organizer before the poll expires (scheduled by
// the M5 purge).
func (n *Notifier) Remind(ctx context.Context, p poll.Poll) {
	if n.skippable(p) {
		return
	}
	n.enqueue(ctx, typeNotify, notifyPayload{PollID: p.PublicID, Kind: "reminder"})
}

func (n *Notifier) enqueue(ctx context.Context, typ string, payload any) {
	if err := n.queue.Enqueue(ctx, typ, payload); err != nil {
		// Notification loss is acceptable; losing the user's action is not.
		n.log.ErrorContext(ctx, "enqueue notification", "type", typ, "error", err)
	}
}

// organizer resolves the account behind a claimed poll.
func (n *Notifier) organizer(ctx context.Context, p poll.Poll) (auth.User, error) {
	if p.CreatedByUserID == 0 {
		return auth.User{}, errors.New("poll has no organizer account")
	}
	return n.users.UserByID(ctx, p.CreatedByUserID)
}

// pollFor resolves a job's poll; a deleted poll retires the job.
func (n *Notifier) pollFor(ctx context.Context, publicID string) (poll.Poll, bool, error) {
	p, err := n.polls.ByPublicID(ctx, publicID)
	if errors.Is(err, poll.ErrNotFound) {
		return poll.Poll{}, false, nil
	}
	if err != nil {
		return poll.Poll{}, false, err
	}
	return p, true, nil
}

func (n *Notifier) send(ctx context.Context, to, subject, body, ctaLabel, ctaURL string) error {
	html, err := templates.RenderEmail(ctx, subject, body, ctaLabel, ctaURL)
	if err != nil {
		return fmt.Errorf("render email: %w", err)
	}
	text := body
	if ctaURL != "" {
		text += "\n\n" + ctaURL
	}
	return n.mailer.Send(ctx, mail.Message{To: to, Subject: subject, Text: text, HTML: html})
}

// handleNotify sends organizer notifications (vote, comment, reminder).
func (n *Notifier) handleNotify(ctx context.Context, raw []byte) error {
	var payload notifyPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode notify payload: %w", err)
	}
	p, ok, err := n.pollFor(ctx, payload.PollID)
	if err != nil || !ok {
		return err
	}
	organizer, err := n.organizer(ctx, p)
	if err != nil {
		return nil // poll was unclaimed since; nothing to send
	}
	loc := n.tr.Locale(organizer.Locale)
	data := map[string]any{"Actor": payload.Actor, "Title": p.Title}
	var subject, body string
	switch payload.Kind {
	case "vote":
		subject = loc.TD("email.vote.subject", data)
		body = loc.TD("email.vote.body", data)
	case "comment":
		subject = loc.TD("email.comment.subject", data)
		body = loc.TD("email.comment.body", data)
	case "reminder":
		data["Date"] = templates.Stamp(loc.Lang, p.DeletesAt, p.TZ())
		subject = loc.TD("email.reminder.subject", data)
		body = loc.TD("email.reminder.body", data)
	default:
		return fmt.Errorf("unknown notify kind %q", payload.Kind)
	}
	return n.send(ctx, organizer.Email, subject, body,
		loc.T("email.cta_manage"), n.baseURL+"/polls/"+p.PublicID+"/manage")
}

// handleEvent sends the finalized/cancelled event with its calendar
// object attached.
func (n *Notifier) handleEvent(ctx context.Context, raw []byte) error {
	var payload eventPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode event payload: %w", err)
	}
	p, ok, err := n.pollFor(ctx, payload.PollID)
	if err != nil || !ok {
		return err
	}
	option, err := n.polls.FinalizedOption(ctx, p)
	if errors.Is(err, poll.ErrNotFinalized) || errors.Is(err, poll.ErrNotFound) {
		return nil // decision was undone or the option vanished
	}
	if err != nil {
		return err
	}

	organizerName := ""
	if organizer, oerr := n.organizer(ctx, p); oerr == nil {
		organizerName = organizer.Name
	}
	v, err := n.polls.View(ctx, p)
	if err != nil {
		return err
	}
	var attendees []string
	for _, pa := range v.Participants {
		if pa.Email != "" {
			attendees = append(attendees, pa.Email)
		}
	}

	loc := n.tr.Locale(payload.Locale)
	org := ics.Organizer{Email: n.from, Name: organizerName}
	data := map[string]any{
		"Title": p.Title,
		"Date":  templates.OptionLabel(loc.Lang, option, p.TZ()),
	}
	var subject, body string
	attachment := mail.Attachment{Filename: "invite.ics", ContentType: "text/calendar; charset=utf-8; method=REQUEST"}
	if payload.Cancelled {
		subject = loc.TD("email.cancelled.subject", data)
		body = loc.TD("email.cancelled.body", data)
		attachment.Filename = "cancel.ics"
		attachment.ContentType = "text/calendar; charset=utf-8; method=CANCEL"
		attachment.Content = ics.Cancel(p, option, org, attendees, n.baseURL, n.now())
	} else {
		subject = loc.TD("email.finalized.subject", data)
		body = loc.TD("email.finalized.body", data)
		attachment.Content = ics.Invite(p, option, org, attendees, n.baseURL, n.now())
	}

	html, err := templates.RenderEmail(ctx, subject, body, loc.T("email.cta_open"), n.baseURL+"/polls/"+p.PublicID)
	if err != nil {
		return fmt.Errorf("render email: %w", err)
	}
	return n.mailer.Send(ctx, mail.Message{
		To:          payload.To,
		Subject:     subject,
		Text:        body + "\n\n" + n.baseURL + "/polls/" + p.PublicID,
		HTML:        html,
		Attachments: []mail.Attachment{attachment},
	})
}

// handleReminder is kept as its own type so the M5 purge can schedule
// reminders directly with a poll id payload.
func (n *Notifier) handleReminder(ctx context.Context, raw []byte) error {
	var payload notifyPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode reminder payload: %w", err)
	}
	payload.Kind = "reminder"
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return n.handleNotify(ctx, body)
}
