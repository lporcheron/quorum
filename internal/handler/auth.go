package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/internal/mail"
	"github.com/lporcheron/quorum/web/templates"
)

const (
	sessFlowKey = "oauthFlow"
	sessNextKey = "loginNext"
)

// next extracts and sanitizes the post-login destination.
func next(r *http.Request) string {
	p := r.FormValue("next")
	if strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") {
		return p
	}
	return ""
}

func (h *Handler) loginProps(r *http.Request) templates.LoginProps {
	return templates.LoginProps{
		Loc:         h.locale(r),
		User:        h.currentUser(r),
		Providers:   h.providers,
		MailEnabled: h.mailer.Enabled(),
		Next:        next(r),
	}
}

// Login renders the sign-in page.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if h.currentUser(r) != nil {
		redirect(w, r, "/dashboard")
		return
	}
	h.render(w, r, http.StatusOK, templates.LoginPage(h.loginProps(r)))
}

func (h *Handler) provider(key string) *auth.Provider {
	for _, p := range h.providers {
		if p.Key == key {
			return p
		}
	}
	return nil
}

// OAuthStart begins the provider flow: state, nonce and PKCE verifier
// go into the session, the browser goes to the provider.
func (h *Handler) OAuthStart(w http.ResponseWriter, r *http.Request) {
	p := h.provider(r.PathValue("provider"))
	if p == nil {
		h.renderError(w, r, http.StatusNotFound, "error.not_found")
		return
	}
	url, fs, err := p.Begin(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "oauth begin", "provider", p.Key, "error", err)
		h.renderError(w, r, http.StatusBadGateway, "error.oauth_failed")
		return
	}
	h.sessions.Put(r.Context(), sessFlowKey, fs.State+"|"+fs.Nonce+"|"+fs.Verifier)
	h.sessions.Put(r.Context(), sessNextKey, next(r))
	http.Redirect(w, r, url, http.StatusFound)
}

// OAuthCallback finishes the provider flow and signs the user in.
func (h *Handler) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	p := h.provider(r.PathValue("provider"))
	if p == nil {
		h.renderError(w, r, http.StatusNotFound, "error.not_found")
		return
	}
	flow := h.sessions.PopString(r.Context(), sessFlowKey)
	dest := h.sessions.PopString(r.Context(), sessNextKey)
	parts := strings.SplitN(flow, "|", 3)
	if len(parts) != 3 || parts[0] == "" || r.URL.Query().Get("state") != parts[0] {
		h.renderError(w, r, http.StatusBadRequest, "error.oauth_failed")
		return
	}
	fs := auth.FlowState{State: parts[0], Nonce: parts[1], Verifier: parts[2]}
	login, err := p.Finish(r.Context(), r.URL.Query().Get("code"), fs)
	if err != nil {
		h.log.ErrorContext(r.Context(), "oauth finish", "provider", p.Key, "error", err)
		h.renderError(w, r, http.StatusBadGateway, "error.oauth_failed")
		return
	}
	user, err := h.auth.Complete(r.Context(), login, h.defaults(r))
	if err != nil {
		h.authError(w, r, err)
		return
	}
	h.signIn(w, r, user, dest)
}

// signIn stores the user in a renewed session (fixation defense).
func (h *Handler) signIn(w http.ResponseWriter, r *http.Request, user auth.User, dest string) {
	if err := h.sessions.RenewToken(r.Context()); err != nil {
		h.log.ErrorContext(r.Context(), "renew session", "error", err)
		h.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	h.sessions.Put(r.Context(), auth.SessionUserKey, user.ID)
	if dest == "" {
		dest = "/dashboard"
	}
	redirect(w, r, dest)
}

// authError maps auth domain errors to pages.
func (h *Handler) authError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrRegistrationsClosed):
		h.renderError(w, r, http.StatusForbidden, "error.registrations_closed")
	case errors.Is(err, auth.ErrEmailNotAllowed):
		h.renderError(w, r, http.StatusForbidden, "error.email_not_allowed")
	case errors.Is(err, auth.ErrEmailUnverified):
		h.renderError(w, r, http.StatusForbidden, "error.email_unverified")
	case errors.Is(err, auth.ErrInvalidToken):
		h.renderError(w, r, http.StatusForbidden, "error.invalid_login_token")
	case errors.Is(err, mail.ErrDisabled):
		h.renderError(w, r, http.StatusConflict, "error.mail_disabled")
	default:
		h.log.ErrorContext(r.Context(), "auth error", "error", err, "path", r.URL.Path)
		h.renderError(w, r, http.StatusInternalServerError, "error.internal")
	}
}

// RequestMagicLink emails a sign-in link. The response is the same
// whether or not the address has an account.
func (h *Handler) RequestMagicLink(w http.ResponseWriter, r *http.Request) {
	if !h.mailer.Enabled() {
		h.renderError(w, r, http.StatusConflict, "error.mail_disabled")
		return
	}
	if !h.parseForm(w, r) {
		return
	}
	loc := h.locale(r)
	err := h.auth.RequestMagicLink(r.Context(), r.PostForm.Get("email"), next(r), func(email, token string) error {
		link := h.baseURL + "/auth/email/callback?token=" + token
		return h.sendMail(r.Context(), email,
			loc.T("login.magic_subject"),
			loc.TD("login.magic_body", map[string]any{"Link": link}),
			loc.T("nav.login"), link,
		)
	})
	if err != nil && !errors.Is(err, auth.ErrEmailNotAllowed) {
		h.authError(w, r, err)
		return
	}
	props := h.loginProps(r)
	props.Sent = true
	h.render(w, r, http.StatusOK, templates.LoginPage(props))
}

// MagicLinkCallback consumes the emailed token.
func (h *Handler) MagicLinkCallback(w http.ResponseWriter, r *http.Request) {
	user, dest, err := h.auth.ConsumeMagicLink(r.Context(), r.URL.Query().Get("token"), h.defaults(r))
	if err != nil {
		h.authError(w, r, err)
		return
	}
	h.signIn(w, r, user, dest)
}

// Logout destroys the session.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if err := h.sessions.Destroy(r.Context()); err != nil {
		h.log.ErrorContext(r.Context(), "destroy session", "error", err)
	}
	redirect(w, r, "/")
}
