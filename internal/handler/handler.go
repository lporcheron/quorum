// Package handler holds the HTTP handlers. Handlers stay thin: parse
// the request, call the domain, render a template.
package handler

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/internal/i18n"
	"github.com/lporcheron/quorum/internal/ids"
	"github.com/lporcheron/quorum/internal/mail"
	"github.com/lporcheron/quorum/internal/notify"
	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/internal/ratelimit"
	"github.com/lporcheron/quorum/internal/setting"
	"github.com/lporcheron/quorum/internal/space"
	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/web/templates"
)

// maxFormBytes bounds every form body; the largest legitimate payload
// is a poll creation with dozens of options, well under this.
const maxFormBytes = 64 << 10

// Deps carries everything the handlers need; New freezes it.
type Deps struct {
	Log         *slog.Logger
	DB          *sql.DB
	Store       *store.Store
	Translator  *i18n.Translator
	Polls       *poll.Service
	Spaces      *space.Service
	Auth        *auth.Service
	Providers   []*auth.Provider
	Sessions    *scs.SessionManager
	Mailer      mail.Mailer
	Notifier    *notify.Notifier
	Settings    *setting.Service
	BaseURL     string
	AdminEmails []string
	TrustProxy  bool
}

// Handler bundles the dependencies shared by all HTTP handlers.
type Handler struct {
	log         *slog.Logger
	db          *sql.DB
	st          *store.Store
	tr          *i18n.Translator
	polls       *poll.Service
	spaces      *space.Service
	auth        *auth.Service
	providers   []*auth.Provider
	sessions    *scs.SessionManager
	mailer      mail.Mailer
	notify      *notify.Notifier
	settings    *setting.Service
	baseURL     string
	adminEmails map[string]bool
	trustProxy  bool

	// Per-IP fixed-window budgets on the abuse-prone endpoints.
	limitCreate *ratelimit.Limiter
	limitVote   *ratelimit.Limiter
	limitEmail  *ratelimit.Limiter
}

// New wires a Handler.
func New(d Deps) *Handler {
	admins := make(map[string]bool, len(d.AdminEmails))
	for _, e := range d.AdminEmails {
		admins[strings.ToLower(e)] = true
	}
	return &Handler{
		log: d.Log, db: d.DB, st: d.Store, tr: d.Translator, polls: d.Polls,
		spaces: d.Spaces, auth: d.Auth, providers: d.Providers,
		sessions: d.Sessions, mailer: d.Mailer, notify: d.Notifier,
		settings: d.Settings, baseURL: strings.TrimSuffix(d.BaseURL, "/"),
		adminEmails: admins, trustProxy: d.TrustProxy,
		limitCreate: ratelimit.New(30, time.Hour, nil),
		limitVote:   ratelimit.New(120, time.Hour, nil),
		limitEmail:  ratelimit.New(5, time.Hour, nil),
	}
}

// clientIP identifies the caller for rate limiting.
func (h *Handler) clientIP(r *http.Request) string {
	if h.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// allow consumes one rate-limit slot or answers 429.
func (h *Handler) allow(w http.ResponseWriter, r *http.Request, l *ratelimit.Limiter) bool {
	if l.Allow(h.clientIP(r)) {
		return true
	}
	h.renderError(w, r, http.StatusTooManyRequests, "error.rate_limited")
	return false
}

// sendMail sends one synchronous email with the shared HTML shell.
// Only capability-carrying mail (magic links, invitations) goes
// through here; notifications ride the job queue.
func (h *Handler) sendMail(ctx context.Context, to, subject, body, ctaLabel, ctaURL string) error {
	html, err := templates.RenderEmail(ctx, subject, body, ctaLabel, ctaURL)
	if err != nil {
		return err
	}
	return h.mailer.Send(ctx, mail.Message{To: to, Subject: subject, Text: body, HTML: html})
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

const langCookie = "quorum_lang"

// locale resolves the UI language: explicit cookie choice first, then
// the Accept-Language header.
func (h *Handler) locale(r *http.Request) *i18n.Locale {
	if c, err := r.Cookie(langCookie); err == nil && c.Value != "" {
		return h.tr.Locale(c.Value)
	}
	return h.tr.Locale(r.Header.Get("Accept-Language"))
}

const sessCSRFKey = "csrf"

// csrfToken returns the session's CSRF token, minted lazily and only
// for signed-in visitors — anonymous page views must not create
// session rows.
func (h *Handler) csrfToken(r *http.Request) string {
	tok := h.sessions.GetString(r.Context(), sessCSRFKey)
	if tok == "" && h.currentUser(r) != nil {
		tok = ids.Token()
		h.sessions.Put(r.Context(), sessCSRFKey, tok)
	}
	return tok
}

// CSRF wraps a session-authenticated mutation with a synchronizer
// token check. Capability-token routes stay unwrapped: their secret
// URL already defeats cross-site forgery.
func (h *Handler) CSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.parseForm(w, r) {
			return
		}
		want := h.sessions.GetString(r.Context(), sessCSRFKey)
		got := r.PostForm.Get("csrf")
		if want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			h.renderError(w, r, http.StatusForbidden, "error.csrf")
			return
		}
		next(w, r)
	}
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	// Chrome (and the lazily-minted CSRF token) must be resolved before
	// WriteHeader: scs commits the session with the response headers.
	user := h.currentUser(r)
	isAdmin := user != nil && h.adminEmails[user.Email]
	ctx := templates.WithChrome(r.Context(), h.settings.InstanceName(r.Context()), r.URL.RequestURI(), h.csrfToken(r), isAdmin)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(ctx, w); err != nil {
		h.log.ErrorContext(r.Context(), "render", "error", err, "path", r.URL.Path)
	}
}

// NotFound is the catch-all for unmatched paths: the localized error
// page instead of Go's plain-text default.
func (h *Handler) NotFound(w http.ResponseWriter, r *http.Request) {
	h.renderError(w, r, http.StatusNotFound, "error.page_not_found")
}

// SetLanguage stores the manual language choice and returns whence the
// visitor came.
func (h *Handler) SetLanguage(w http.ResponseWriter, r *http.Request) {
	if !h.parseForm(w, r) {
		return
	}
	lang := r.PostForm.Get("lang")
	if lang != "en" && lang != "fr" {
		h.renderError(w, r, http.StatusBadRequest, "error.bad_request")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     langCookie,
		Value:    lang,
		Path:     "/",
		MaxAge:   365 * 24 * 3600,
		SameSite: http.SameSiteLaxMode,
	})
	dest := next(r)
	if dest == "" {
		dest = "/"
	}
	redirect(w, r, dest)
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
	case errors.Is(err, poll.ErrNotFinalizable):
		return http.StatusConflict, "error.not_finalizable"
	case errors.Is(err, poll.ErrNotFinalized):
		return http.StatusConflict, "error.not_finalized"
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
