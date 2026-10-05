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

// voter reads the ballot's identity fields.
func (h *Handler) voter(r *http.Request, userID int64) poll.Voter {
	return poll.Voter{
		Name: r.PostForm.Get("name"), Email: r.PostForm.Get("email"),
		Locale: h.locale(r).Lang, UserID: userID,
	}
}

// rerenderPoll shows the poll page again after a refused submission:
// the message in place and set() echoing what the visitor typed, so
// nothing has to be entered twice. Server faults keep the error page.
func (h *Handler) rerenderPoll(w http.ResponseWriter, r *http.Request, p poll.Poll, me *poll.Participant, token string, err error, set func(*templates.PollPageProps, string)) {
	status, msgID := errStatus(err)
	if status >= 500 || status == http.StatusNotFound {
		h.domainError(w, r, err)
		return
	}
	props, perr := h.pollProps(r, p, me, token)
	if perr != nil {
		h.domainError(w, r, perr)
		return
	}
	set(&props, props.Loc.T(msgID))
	h.render(w, r, status, templates.PollPage(props))
}

// rerenderVote echoes a refused ballot.
func (h *Handler) rerenderVote(w http.ResponseWriter, r *http.Request, p poll.Poll, me *poll.Participant, token string, err error) {
	h.rerenderPoll(w, r, p, me, token, err, func(props *templates.PollPageProps, msg string) {
		props.VoteError = msg
		props.Draft = &templates.VoteDraft{
			Name: r.PostForm.Get("name"), Email: r.PostForm.Get("email"), Votes: parseVotes(r.PostForm),
		}
	})
}

// CreateParticipant records a first-time guest vote and redirects to
// the personal edit page, where the edit link is shown once. A
// signed-in voter who already has a row gets it updated instead, and
// lands back on the poll.
func (h *Handler) CreateParticipant(w http.ResponseWriter, r *http.Request) {
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
	var userID int64
	if u := h.currentUser(r); u != nil {
		// The vote now acts on the account's row: a forged form must
		// not be able to change it.
		if !h.csrfOK(r) {
			h.renderError(w, r, http.StatusForbidden, "error.csrf")
			return
		}
		userID = u.ID
	}
	pa, editToken, err := h.polls.Join(r.Context(), p, h.voter(r, userID), parseVotes(r.PostForm))
	if err != nil {
		h.rerenderVote(w, r, p, nil, "", err)
		return
	}
	if editToken == "" {
		redirect(w, r, "/polls/"+p.PublicID+"?updated=1")
		return
	}
	h.notify.VoteCast(r.Context(), p, pa.Name)
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
	props.Updated = r.URL.Query().Get("updated") == "1"
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
	err := h.polls.UpdateVotes(r.Context(), p, pa, h.voter(r, 0), parseVotes(r.PostForm))
	if err != nil {
		h.rerenderVote(w, r, p, &pa, token, err)
		return
	}
	redirect(w, r, "/polls/"+p.PublicID+"/p/"+token+"?updated=1")
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
