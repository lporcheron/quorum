package handler

import (
	"net/http"
	"strconv"

	"github.com/lporcheron/quorum/internal/poll"
)

// CreateComment posts a comment, attributed to the participant when a
// ptoken field is present, otherwise to the free-form name.
func (h *Handler) CreateComment(w http.ResponseWriter, r *http.Request) {
	p, err := h.polls.ByPublicID(r.Context(), r.PathValue("pollID"))
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	back := "/polls/" + p.PublicID
	var participant *poll.Participant
	if token := r.PostForm.Get("ptoken"); token != "" {
		pa, err := h.polls.ParticipantByToken(r.Context(), p, token)
		if err != nil {
			h.domainError(w, r, err)
			return
		}
		participant = &pa
		back = "/polls/" + p.PublicID + "/p/" + token
	}
	if _, err := h.polls.AddComment(r.Context(), p, participant, r.PostForm.Get("author_name"), r.PostForm.Get("body")); err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, back)
}

// DeleteOwnComment lets a participant remove their own comment.
func (h *Handler) DeleteOwnComment(w http.ResponseWriter, r *http.Request) {
	p, pa, token, ok := h.editContext(w, r)
	if !ok {
		return
	}
	commentID, err := strconv.ParseInt(r.PathValue("commentID"), 10, 64)
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "error.bad_request")
		return
	}
	if err := h.polls.RemoveOwnComment(r.Context(), p, pa, commentID); err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, "/polls/"+p.PublicID+"/p/"+token)
}
