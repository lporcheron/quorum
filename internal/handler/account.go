package handler

import (
	"errors"
	"net/http"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/web/templates"
)

// ShowAccount renders the account page.
func (h *Handler) ShowAccount(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		redirect(w, r, "/login?next=/account")
		return
	}
	h.render(w, r, http.StatusOK, templates.AccountPage(templates.AccountProps{
		Loc:  h.locale(r),
		User: user,
	}))
}

// DeleteAccount erases the account and ends the session.
func (h *Handler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)
	if user == nil {
		redirect(w, r, "/login?next=/account")
		return
	}
	if err := h.auth.DeleteAccount(r.Context(), user.ID); err != nil {
		if errors.Is(err, auth.ErrOwnsSharedSpace) {
			h.renderError(w, r, http.StatusConflict, "error.owns_shared_space")
			return
		}
		h.authError(w, r, err)
		return
	}
	if err := h.sessions.Destroy(r.Context()); err != nil {
		h.log.ErrorContext(r.Context(), "destroy session after account deletion", "error", err)
	}
	redirect(w, r, "/")
}
