// Package handler holds the HTTP handlers. Handlers stay thin: parse
// the request, call the domain, render a template.
package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/internal/i18n"
	"github.com/lporcheron/quorum/internal/mail"
	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/internal/space"
	"github.com/lporcheron/quorum/web/templates"
)

// maxFormBytes bounds every form body; the largest legitimate payload
// is a poll creation with dozens of options, well under this.
const maxFormBytes = 64 << 10

// Handler bundles the dependencies shared by all HTTP handlers.
type Handler struct {
	log       *slog.Logger
	db        *sql.DB
	tr        *i18n.Translator
	polls     *poll.Service
	spaces    *space.Service
	auth      *auth.Service
	providers []*auth.Provider
	sessions  *scs.SessionManager
	mailer    mail.Mailer
	baseURL   string
}

// New wires a Handler; all dependencies are explicit.
func New(log *slog.Logger, db *sql.DB, tr *i18n.Translator, polls *poll.Service,
	spaces *space.Service, authsvc *auth.Service, providers []*auth.Provider,
	sessions *scs.SessionManager, mailer mail.Mailer, baseURL string,
) *Handler {
	return &Handler{
		log: log, db: db, tr: tr, polls: polls, spaces: spaces,
		auth: authsvc, providers: providers, sessions: sessions, mailer: mailer,
		baseURL: strings.TrimSuffix(baseURL, "/"),
	}
}

// atoiInRange parses a bounded integer form field.
func atoiInRange(s string, min, max int) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("value %q out of range [%d, %d]", s, min, max)
	}
	return n, nil
}

// currentUser returns the signed-in user, or nil.
func (h *Handler) currentUser(r *http.Request) *auth.User {
	id := h.sessions.GetInt64(r.Context(), auth.SessionUserKey)
	if id == 0 {
		return nil
	}
	u, err := h.auth.UserByID(r.Context(), id)
	if err != nil {
		return nil
	}
	return &u
}

func (h *Handler) locale(r *http.Request) *i18n.Locale {
	return h.tr.Locale(r.Header.Get("Accept-Language"))
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		h.log.ErrorContext(r.Context(), "render", "error", err, "path", r.URL.Path)
	}
}

// parseForm reads the body with a hard size cap.
func (h *Handler) parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "error.bad_request")
		return false
	}
	return true
}

func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, msgID string) {
	loc := h.locale(r)
	h.render(w, r, status, templates.ErrorPage(templates.ErrorProps{
		Loc: loc, User: h.currentUser(r), Message: loc.T(msgID),
	}))
}

// defaults gathers the profile hints for a first sign-in.
func (h *Handler) defaults(r *http.Request) auth.Defaults {
	d := auth.Defaults{Locale: h.locale(r).Lang}
	if c, err := r.Cookie(tzCookie); err == nil && poll.ValidTimezone(c.Value) {
		d.Timezone = c.Value
	}
	return d
}

// errStatus maps a domain error to an HTTP status and a message id.
func errStatus(err error) (int, string) {
	switch {
	case errors.Is(err, poll.ErrNotFound):
		return http.StatusNotFound, "error.not_found"
	case errors.Is(err, poll.ErrForbidden):
		return http.StatusForbidden, "error.forbidden"
	case errors.Is(err, poll.ErrTitleRequired):
		return http.StatusUnprocessableEntity, "error.title_required"
	case errors.Is(err, poll.ErrNoOptions):
		return http.StatusUnprocessableEntity, "error.no_options"
	case errors.Is(err, poll.ErrBadTimezone):
		return http.StatusUnprocessableEntity, "error.bad_timezone"
	case errors.Is(err, poll.ErrDuplicateOption):
		return http.StatusUnprocessableEntity, "error.duplicate_option"
	case errors.Is(err, poll.ErrNameRequired):
		return http.StatusUnprocessableEntity, "error.name_required"
	case errors.Is(err, poll.ErrEmailRequired):
		return http.StatusUnprocessableEntity, "error.email_required"
	case errors.Is(err, poll.ErrPollClosed):
		return http.StatusConflict, "error.poll_closed"
	case errors.Is(err, poll.ErrCommentsDisabled):
		return http.StatusConflict, "error.comments_disabled"
	case errors.Is(err, poll.ErrBodyRequired):
		return http.StatusUnprocessableEntity, "error.body_required"
	}
	return http.StatusInternalServerError, "error.internal"
}

// domainError renders the mapped error page, logging server faults.
func (h *Handler) domainError(w http.ResponseWriter, r *http.Request, err error) {
	status, msgID := errStatus(err)
	if status >= 500 {
		h.log.ErrorContext(r.Context(), "handler error", "error", err, "path", r.URL.Path)
	}
	h.renderError(w, r, status, msgID)
}

const tzCookie = "quorum_tz"

// viewerTZ resolves the timezone used for display: explicit ?tz=, then
// the tz cookie (set by quorum.js from the browser), then the poll's
// own zone. All-day polls have no timezone at all.
func (h *Handler) viewerTZ(r *http.Request, p poll.Poll) (string, *time.Location) {
	if p.Kind == poll.KindAllDay {
		return "", time.UTC
	}
	candidates := []string{r.URL.Query().Get("tz")}
	if c, err := r.Cookie(tzCookie); err == nil {
		candidates = append(candidates, c.Value)
	}
	candidates = append(candidates, p.Timezone)
	for _, tz := range candidates {
		if poll.ValidTimezone(tz) {
			loc, err := time.LoadLocation(tz)
			if err == nil {
				return tz, loc
			}
		}
	}
	return "UTC", time.UTC
}

// rememberTZ persists an explicit timezone choice.
func rememberTZ(w http.ResponseWriter, tz string) {
	http.SetCookie(w, &http.Cookie{
		Name:     tzCookie,
		Value:    tz,
		Path:     "/",
		MaxAge:   365 * 24 * 3600,
		SameSite: http.SameSiteLaxMode,
	})
}

func redirect(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, path, http.StatusSeeOther)
}
