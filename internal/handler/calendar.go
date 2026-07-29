package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"time"

	"github.com/lporcheron/quorum/internal/ics"
	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/web/templates"
)

// FinalizePoll closes the poll on the chosen option and notifies
// everyone who left an email.
func (h *Handler) FinalizePoll(w http.ResponseWriter, r *http.Request) {
	p, base, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	optionID, err := atoiInRange(r.PostForm.Get("option_id"), 1, 1<<62)
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "error.bad_request")
		return
	}
	if _, err := h.polls.Finalize(r.Context(), p, int64(optionID)); err != nil {
		h.domainError(w, r, err)
		return
	}
	fresh, err := h.polls.ByPublicID(r.Context(), p.PublicID)
	if err == nil {
		h.notify.EventDecision(r.Context(), fresh, false)
	}
	redirect(w, r, base)
}

// CancelEvent cancels a finalized event and sends the CANCEL objects.
func (h *Handler) CancelEvent(w http.ResponseWriter, r *http.Request) {
	p, base, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	if err := h.polls.CancelEvent(r.Context(), p); err != nil {
		h.domainError(w, r, err)
		return
	}
	fresh, err := h.polls.ByPublicID(r.Context(), p.PublicID)
	if err == nil {
		h.notify.EventDecision(r.Context(), fresh, true)
	}
	redirect(w, r, base)
}

// CalendarFeed serves the per-poll .ics subscription: tentative
// options while voting, the decided event afterwards.
func (h *Handler) CalendarFeed(w http.ResponseWriter, r *http.Request) {
	p, err := h.polls.ByPublicID(r.Context(), r.PathValue("pollID"))
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	v, err := h.polls.View(r.Context(), p)
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	body := ics.Feed(p, v.Options, p.FinalizedOptionID, h.baseURL, time.Now())
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+p.PublicID+`.ics"`)
	w.Write(body)
}

// ExportCSV streams the results table: one row per participant, one
// column per option. Organizer-only — it includes emails.
func (h *Handler) ExportCSV(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	v, err := h.polls.View(r.Context(), p)
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	loc := h.locale(r)

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+p.PublicID+`.csv"`)
	cw := csv.NewWriter(w)

	header := []string{loc.T("vote.name_label"), loc.T("vote.email_label")}
	for _, o := range v.Options {
		header = append(header, templates.OptionLabel(loc.Lang, o, p.TZ()))
	}
	if err := cw.Write(header); err != nil {
		return
	}
	for _, pa := range v.Participants {
		row := []string{pa.Name, pa.Email}
		for _, o := range v.Options {
			row = append(row, string(v.Votes[pa.ID][o.ID]))
		}
		if err := cw.Write(row); err != nil {
			return
		}
	}
	tally := []string{loc.T("poll.winner"), ""}
	for i := range v.Options {
		t := v.Tallies[i]
		tally = append(tally, fmt.Sprintf("%d/%d/%d", t.Yes, t.IfNeedBe, t.No))
	}
	cw.Write(tally) //nolint:errcheck // best-effort trailer
	cw.Flush()
}

// finalizedOptionLabel resolves the chosen option's label for banners.
func (h *Handler) finalizedOptionLabel(r *http.Request, p poll.Poll, lang string, tz *time.Location) string {
	if p.FinalizedOptionID == 0 {
		return ""
	}
	o, err := h.polls.FinalizedOption(r.Context(), p)
	if err != nil {
		return ""
	}
	return templates.OptionLabel(lang, o, tz)
}
