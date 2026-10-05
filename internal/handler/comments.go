package handler

import (
	"net/http"
	"strconv"

	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/web/templates"
)

// CreateComment posts a comment, attributed to the participant when a
// ptoken field is present, otherwise to the free-form name.
func (h *Handler) CreateComment(w http.ResponseWriter, r *http.Request) {
	// Anonymous, and each comment can email the organizer: same budget
	// as votes.
	if !h.allow(w, r, h.limitVote) {
		return
	}
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
	token := r.PostForm.Get("ptoken")
	if token != "" {
		pa, err := h.polls.ParticipantByToken(r.Context(), p, token)
		if err != nil {
			h.domainError(w, r, err)
			return
		}
		participant = &pa
		back = "/polls/" + p.PublicID + "/p/" + token
	}
	var userID int64
	if u := h.currentUser(r); u != nil {
		userID = u.ID
	}
	c, err := h.polls.AddComment(r.Context(), p, participant, userID, r.PostForm.Get("author_name"), r.PostForm.Get("body"))
	if err != nil {
		h.rerenderPoll(w, r, p, participant, token, err, func(props *templates.PollPageProps, msg string) {
			props.CommentError = msg
			props.CommentName = r.PostForm.Get("author_name")
			props.CommentBody = r.PostForm.Get("body")
		})
		return
	}
	h.notify.CommentPosted(r.Context(), p, c.AuthorName)
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
