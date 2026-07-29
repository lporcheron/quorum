package handler

import (
	"net/http"

	"github.com/lporcheron/quorum/web/templates"
)

// Home renders the landing page.
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	loc := h.tr.Locale(r.Header.Get("Accept-Language"))
	props := templates.HomeProps{
		Lang:    loc.Lang,
		Title:   loc.T("home.title"),
		Tagline: loc.T("home.tagline"),
		Blurb:   loc.T("home.blurb"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.Home(props).Render(r.Context(), w); err != nil {
		h.log.ErrorContext(r.Context(), "render home", "error", err)
	}
}
