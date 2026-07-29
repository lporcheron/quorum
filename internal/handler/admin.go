package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/web/templates"
)

// adminContext authorizes an organizer route and returns the base path
// its forms post to. Two doors: the capability token
// (/polls/{id}/admin/{token}) or, for claimed polls, the signed-in
// owner (/polls/{id}/manage).
func (h *Handler) adminContext(w http.ResponseWriter, r *http.Request) (poll.Poll, string, bool) {
	pollID := r.PathValue("pollID")
	if token := r.PathValue("adminToken"); token != "" {
		p, err := h.polls.Admin(r.Context(), pollID, token)
		if err != nil {
			h.domainError(w, r, err)
			return poll.Poll{}, "", false
		}
		return p, "/polls/" + pollID + "/admin/" + token, true
	}

	base := "/polls/" + pollID + "/manage"
	user := h.currentUser(r)
	if user == nil {
		redirect(w, r, "/login?next="+base)
		return poll.Poll{}, "", false
	}
	p, err := h.polls.ByPublicID(r.Context(), pollID)
	if err != nil {
		h.domainError(w, r, err)
		return poll.Poll{}, "", false
	}
	if p.CreatedByUserID != user.ID {
		h.renderError(w, r, http.StatusForbidden, "error.forbidden")
		return poll.Poll{}, "", false
	}
	return p, base, true
}

// ShowPollAdmin renders the organizer's control panel.
func (h *Handler) ShowPollAdmin(w http.ResponseWriter, r *http.Request) {
	p, base, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	grid, err := h.pollProps(r, p, nil, "")
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	grid.IsAdmin = true
	adminURL := ""
	if strings.Contains(base, "/admin/") {
		adminURL = h.baseURL + base
	}
	h.render(w, r, http.StatusOK, templates.AdminPage(templates.AdminProps{
		Loc:       grid.Loc,
		User:      h.currentUser(r),
		Poll:      p,
		View:      grid.View,
		BasePath:  base,
		AdminURL:  adminURL,
		PublicURL: h.baseURL + "/polls/" + p.PublicID,
		New:       r.URL.Query().Get("new") == "1" && adminURL != "",
		Saved:     r.URL.Query().Get("saved") == "1",
	}, grid))
}

// UpdatePoll saves the details form.
func (h *Handler) UpdatePoll(w http.ResponseWriter, r *http.Request) {
	p, base, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	err := h.polls.UpdateDetails(r.Context(), p, poll.Details{
		Title:             r.PostForm.Get("title"),
		Description:       r.PostForm.Get("description"),
		Location:          r.PostForm.Get("location"),
		VideoURL:          r.PostForm.Get("video_url"),
		HideParticipants:  r.PostForm.Get("hide_participants") == "1",
		RequireVoterEmail: r.PostForm.Get("require_voter_email") == "1",
		AllowComments:     r.PostForm.Get("allow_comments") == "1",
	})
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, base+"?saved=1")
}

// AddOptions appends options from the admin mini-form.
func (h *Handler) AddOptions(w http.ResponseWriter, r *http.Request) {
	p, base, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	slots, dates, err := parseOptionRows(r.PostForm["option_date"], r.PostForm["option_start"], r.PostForm["option_duration"], p.Kind)
	if err == nil {
		err = h.polls.AddOptions(r.Context(), p, slots, dates)
	}
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, base+"?saved=1")
}

// pathID parses a numeric path segment.
func (h *Handler) pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "error.bad_request")
		return 0, false
	}
	return id, true
}

// DeleteOption removes one option and its votes.
func (h *Handler) DeleteOption(w http.ResponseWriter, r *http.Request) {
	p, base, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "optionID")
	if !ok {
		return
	}
	if err := h.polls.RemoveOption(r.Context(), p, id); err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, base)
}

// SetPollStatus pauses or resumes voting.
func (h *Handler) SetPollStatus(w http.ResponseWriter, r *http.Request) {
	p, base, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	if err := h.polls.SetPaused(r.Context(), p, r.PostForm.Get("action") == "pause"); err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, base)
}

// AdminDeleteParticipant removes a voter.
func (h *Handler) AdminDeleteParticipant(w http.ResponseWriter, r *http.Request) {
	p, base, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "participantID")
	if !ok {
		return
	}
	if err := h.polls.RemoveParticipant(r.Context(), p, id); err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, base)
}

// AdminDeleteComment removes any comment.
func (h *Handler) AdminDeleteComment(w http.ResponseWriter, r *http.Request) {
	p, base, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "commentID")
	if !ok {
		return
	}
	if err := h.polls.RemoveComment(r.Context(), p, id); err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, base)
}

// RegenerateAdminToken rotates the admin link and lands on the new URL.
func (h *Handler) RegenerateAdminToken(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	fresh, err := h.polls.RegenerateAdminToken(r.Context(), p)
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, "/polls/"+p.PublicID+"/admin/"+fresh+"?new=1")
}

// DeletePoll removes the poll entirely.
func (h *Handler) DeletePoll(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	if err := h.polls.Delete(r.Context(), p); err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, "/")
}

// ClaimPoll attaches a guest poll to the signed-in account. Reached
// through the capability URL: claiming is exactly the handover from
// link-based to account-based ownership.
func (h *Handler) ClaimPoll(w http.ResponseWriter, r *http.Request) {
	p, base, ok := h.adminContext(w, r)
	if !ok {
		return
	}
	user := h.currentUser(r)
	if user == nil {
		redirect(w, r, "/login?next="+base)
		return
	}
	if err := h.polls.Claim(r.Context(), p, user.ID, user.PersonalSpaceID); err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, base)
}
