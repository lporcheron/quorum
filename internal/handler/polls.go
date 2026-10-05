package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/web"
	"github.com/lporcheron/quorum/web/templates"
)

// Home renders the landing page, with the creation form when the
// visitor is allowed to create a poll.
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, templates.Home(h.homeProps(r)))
}

// homeProps assembles the landing page for the current visitor.
func (h *Handler) homeProps(r *http.Request) templates.HomeProps {
	user := h.currentUser(r)
	props := templates.HomeProps{
		Loc:       h.locale(r),
		User:      user,
		Timezones: poll.CommonTimezones,
		CanCreate: h.canCreatePoll(r, user),
		CanNotify: user != nil && h.mailer.Enabled(),
	}
	if user != nil {
		if sp, _, err := h.currentSpace(r, user); err == nil {
			props.SpaceName = sp.Name
		}
	}
	return props
}

// canCreatePoll answers the instance policy on guest poll creation:
// signed-in users always create, signed-out visitors only while
// guest polls are open.
func (h *Handler) canCreatePoll(r *http.Request, user *auth.User) bool {
	return user != nil || h.settings.GuestPollsOpen(r.Context())
}

// CreatePoll handles the creation form and redirects to the admin URL.
func (h *Handler) CreatePoll(w http.ResponseWriter, r *http.Request) {
	if !h.allow(w, r, h.limitCreate) {
		return
	}
	user := h.currentUser(r)
	if !h.canCreatePoll(r, user) {
		h.renderError(w, r, http.StatusForbidden, "error.guest_polls_closed")
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	kind := poll.KindTimed
	if r.PostForm.Get("kind") == string(poll.KindAllDay) {
		kind = poll.KindAllDay
	}
	slots, dates, err := parseOptionRows(r.PostForm["option_date"], r.PostForm["option_start"], r.PostForm["option_duration"], kind)
	if err != nil {
		h.rerenderCreate(w, r, err)
		return
	}
	in := poll.NewPoll{
		Title:             r.PostForm.Get("title"),
		Description:       r.PostForm.Get("description"),
		Location:          r.PostForm.Get("location"),
		VideoURL:          r.PostForm.Get("video_url"),
		Kind:              kind,
		Timezone:          r.PostForm.Get("timezone"),
		HideParticipants:  r.PostForm.Get("hide_participants") == "1",
		RequireVoterEmail: r.PostForm.Get("require_voter_email") == "1",
		AllowComments:     r.PostForm.Get("allow_comments") == "1",
		NotifyOrganizer:   r.PostForm.Get("notify_organizer") == "1",
		Slots:             slots,
		Dates:             dates,
	}
	// A signed-in creator's poll lands in their current space directly,
	// inheriting the space's retention.
	if user != nil {
		sp, _, err := h.currentSpace(r, user)
		if err != nil {
			h.spaceError(w, r, err)
			return
		}
		in.SpaceID = sp.ID
		in.CreatedByUserID = user.ID
		in.RetentionDays = sp.RetentionDays
	}
	p, adminToken, err := h.polls.Create(r.Context(), in)
	if err != nil {
		h.rerenderCreate(w, r, err)
		return
	}
	// A signed-in creator's poll is already in their dashboard; the
	// capability link (and its save-this warning) is for guests.
	if in.CreatedByUserID != 0 {
		redirect(w, r, "/polls/"+p.PublicID+"/manage")
		return
	}
	redirect(w, r, "/polls/"+p.PublicID+"/admin/"+adminToken+"?new=1")
}

// rerenderCreate shows the creation form again with the error and
// everything that was posted, options included.
func (h *Handler) rerenderCreate(w http.ResponseWriter, r *http.Request, err error) {
	status, msgID := errStatus(err)
	if status >= 500 {
		h.domainError(w, r, err)
		return
	}
	f := r.PostForm
	d := &templates.CreateDraft{
		Title: f.Get("title"), Description: f.Get("description"),
		Location: f.Get("location"), VideoURL: f.Get("video_url"),
		Kind: f.Get("kind"), Timezone: f.Get("timezone"),
		HideParticipants:  f.Get("hide_participants") == "1",
		RequireVoterEmail: f.Get("require_voter_email") == "1",
		AllowComments:     f.Get("allow_comments") == "1",
		NotifyOrganizer:   f.Get("notify_organizer") == "1",
	}
	for i, date := range f["option_date"] {
		if strings.TrimSpace(date) == "" {
			continue
		}
		o := templates.DraftOption{Date: date}
		if d.Kind != string(poll.KindAllDay) {
			if i < len(f["option_start"]) {
				o.Start = f["option_start"][i]
			}
			if i < len(f["option_duration"]) {
				o.Duration = f["option_duration"][i]
			}
		}
		d.Options = append(d.Options, o)
	}
	props := h.homeProps(r)
	props.Error = props.Loc.T(msgID)
	props.Submitted = d
	h.render(w, r, status, templates.Home(props))
}

// parseOptionRows turns the parallel option_* form arrays into domain
// input. Rows with an empty date are ignored; on a timed poll a row
// needs date and start, and the arrays are aligned by construction
// (every row submits every field, possibly empty).
func parseOptionRows(dates, starts, durations []string, kind poll.Kind) ([]poll.TimedSlot, []poll.Date, error) {
	var slots []poll.TimedSlot
	var days []poll.Date
	for i, ds := range dates {
		ds = strings.TrimSpace(ds)
		if ds == "" {
			continue
		}
		d, err := poll.ParseDate(ds)
		if err != nil {
			return nil, nil, poll.ErrNoOptions
		}
		if kind == poll.KindAllDay {
			days = append(days, d)
			continue
		}
		if i >= len(starts) || i >= len(durations) {
			return nil, nil, poll.ErrNoOptions
		}
		hour, minute, ok := parseHM(starts[i])
		if !ok {
			return nil, nil, poll.ErrNoOptions
		}
		mins, err := strconv.Atoi(durations[i])
		if err != nil || mins <= 0 || mins > 24*60 {
			return nil, nil, poll.ErrNoOptions
		}
		slots = append(slots, poll.TimedSlot{Date: d, Hour: hour, Minute: minute, Duration: time.Duration(mins) * time.Minute})
	}
	return slots, days, nil
}

func parseHM(s string) (int, int, bool) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, false
	}
	return t.Hour(), t.Minute(), true
}

// pollProps assembles the shared template props for a poll page view.
func (h *Handler) pollProps(r *http.Request, p poll.Poll, me *poll.Participant, editToken string) (templates.PollPageProps, error) {
	v, err := h.polls.View(r.Context(), p)
	if err != nil {
		return templates.PollPageProps{}, err
	}
	tzName, tz := h.viewerTZ(r, p)
	user := h.currentUser(r)
	// A signed-in visitor without an edit link is recognized by their
	// account: their row is shown as theirs, ready to edit.
	if me == nil && user != nil {
		if pa, err := h.polls.ParticipantForUser(r.Context(), p, user.ID); err == nil {
			me = &pa
		} else if !errors.Is(err, poll.ErrNotFound) {
			return templates.PollPageProps{}, err
		}
	}
	props := templates.PollPageProps{
		Loc:       h.locale(r),
		User:      user,
		Poll:      p,
		View:      v,
		TZ:        tz,
		TZName:    tzName,
		Timezones: poll.CommonTimezones,
		Me:        me,
		EditToken: editToken,
		OGImage:   h.baseURL + web.AssetURL("og.png"),

		MailEnabled: h.mailer.Enabled(),
	}
	if p.FinalizedOptionID != 0 {
		props.FinalizedLabel = h.finalizedOptionLabel(r, p, props.Loc.Lang, tz)
	}
	return props, nil
}

// ShowPoll renders the public poll page.
func (h *Handler) ShowPoll(w http.ResponseWriter, r *http.Request) {
	p, err := h.polls.ByPublicID(r.Context(), r.PathValue("pollID"))
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	props, err := h.pollProps(r, p, nil, "")
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	props.Updated = r.URL.Query().Get("updated") == "1"
	h.render(w, r, http.StatusOK, templates.PollPage(props))
}

// PollGrid re-renders the grid partial (HTMX timezone switch). An
// optional p= carries the participant edit token so the vote form
// keeps its edit state.
func (h *Handler) PollGrid(w http.ResponseWriter, r *http.Request) {
	p, err := h.polls.ByPublicID(r.Context(), r.PathValue("pollID"))
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	var me *poll.Participant
	editToken := r.URL.Query().Get("p")
	if editToken != "" {
		pa, err := h.polls.ParticipantByToken(r.Context(), p, editToken)
		if err != nil {
			editToken = ""
		} else {
			me = &pa
		}
	}
	if tz := r.URL.Query().Get("tz"); poll.ValidTimezone(tz) {
		rememberTZ(w, tz)
	}
	props, err := h.pollProps(r, p, me, editToken)
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, templates.Grid(props))
}
