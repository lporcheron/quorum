package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/web/templates"
)

// parseVotes reads vote_<optionID>=yes|ifneedbe|no fields.
func parseVotes(form map[string][]string) map[int64]poll.VoteValue {
	votes := make(map[int64]poll.VoteValue)
	for key, vals := range form {
		idStr, ok := strings.CutPrefix(key, "vote_")
		if !ok || len(vals) == 0 {
			continue
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		if v, ok := poll.ParseVoteValue(vals[0]); ok {
			votes[id] = v
		}
	}
	return votes
}

// CreateParticipant records a first-time guest vote and redirects to
// the personal edit page, where the edit link is shown once.
func (h *Handler) CreateParticipant(w http.ResponseWriter, r *http.Request) {
	p, err := h.polls.ByPublicID(r.Context(), r.PathValue("pollID"))
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	var userID int64
	if u := h.currentUser(r); u != nil {
		userID = u.ID
	}
	_, editToken, err := h.polls.Join(r.Context(), p, r.PostForm.Get("name"), r.PostForm.Get("email"), userID, parseVotes(r.PostForm))
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, "/polls/"+p.PublicID+"/p/"+editToken+"?joined=1")
}

// editContext resolves the poll and participant behind a personal link.
func (h *Handler) editContext(w http.ResponseWriter, r *http.Request) (poll.Poll, poll.Participant, string, bool) {
	p, err := h.polls.ByPublicID(r.Context(), r.PathValue("pollID"))
	if err != nil {
		h.domainError(w, r, err)
		return poll.Poll{}, poll.Participant{}, "", false
	}
	token := r.PathValue("editToken")
	pa, err := h.polls.ParticipantByToken(r.Context(), p, token)
	if err != nil {
		h.domainError(w, r, err)
		return poll.Poll{}, poll.Participant{}, "", false
	}
	return p, pa, token, true
}

// ShowPollAsParticipant is the poll page in edit mode.
func (h *Handler) ShowPollAsParticipant(w http.ResponseWriter, r *http.Request) {
	p, pa, token, ok := h.editContext(w, r)
	if !ok {
		return
	}
	props, err := h.pollProps(r, p, &pa, token)
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	props.JustJoined = r.URL.Query().Get("joined") == "1"
	props.EditURL = h.baseURL + "/polls/" + p.PublicID + "/p/" + token
	h.render(w, r, http.StatusOK, templates.PollPage(props))
}

// UpdateVotes replaces the participant's votes.
func (h *Handler) UpdateVotes(w http.ResponseWriter, r *http.Request) {
	p, pa, token, ok := h.editContext(w, r)
	if !ok {
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	err := h.polls.UpdateVotes(r.Context(), p, pa, r.PostForm.Get("name"), r.PostForm.Get("email"), parseVotes(r.PostForm))
	if err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, "/polls/"+p.PublicID+"/p/"+token)
}

// DeleteParticipantSelf removes the participant through their own link.
func (h *Handler) DeleteParticipantSelf(w http.ResponseWriter, r *http.Request) {
	p, pa, _, ok := h.editContext(w, r)
	if !ok {
		return
	}
	if err := h.polls.RemoveParticipant(r.Context(), p, pa.ID); err != nil {
		h.domainError(w, r, err)
		return
	}
	redirect(w, r, "/polls/"+p.PublicID)
}
