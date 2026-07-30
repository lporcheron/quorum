// Package server assembles the HTTP server: routes, middleware,
// graceful shutdown.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/lporcheron/quorum/internal/config"
	"github.com/lporcheron/quorum/internal/handler"
	"github.com/lporcheron/quorum/internal/metrics"
	"github.com/lporcheron/quorum/web"
)

// Server owns the http.Server lifecycle.
type Server struct {
	cfg      config.Config
	log      *slog.Logger
	h        *handler.Handler
	sessions *scs.SessionManager
	metrics  *metrics.Metrics
}

// New wires a Server; all dependencies are explicit.
func New(cfg config.Config, log *slog.Logger, h *handler.Handler, sessions *scs.SessionManager, m *metrics.Metrics) *Server {
	return &Server{cfg: cfg, log: log, h: h, sessions: sessions, metrics: m}
}

// Handler builds the full routing table with the middleware chain.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.h.Home)
	mux.HandleFunc("GET /healthz", s.h.Healthz)
	mux.Handle("GET /static/", cacheStatic(http.FileServerFS(web.StaticFS)))
	// Browsers probe this path unprompted; point them at the real file.
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/favicon-32.png", http.StatusMovedPermanently)
	})

	// Poll lifecycle. Mutations are POST only: plain HTML forms are the
	// no-JS fallback for the critical paths.
	mux.HandleFunc("POST /polls", s.h.CreatePoll)
	mux.HandleFunc("GET /polls/{pollID}", s.h.ShowPoll)
	mux.HandleFunc("GET /polls/{pollID}/grid", s.h.PollGrid)
	mux.HandleFunc("GET /polls/{pollID}/calendar.ics", s.h.CalendarFeed)
	mux.HandleFunc("POST /polls/{pollID}/participants", s.h.CreateParticipant)
	mux.HandleFunc("POST /polls/{pollID}/comments", s.h.CreateComment)

	// Participant capability URLs.
	mux.HandleFunc("GET /polls/{pollID}/p/{editToken}", s.h.ShowPollAsParticipant)
	mux.HandleFunc("POST /polls/{pollID}/p/{editToken}/votes", s.h.UpdateVotes)
	mux.HandleFunc("POST /polls/{pollID}/p/{editToken}/delete", s.h.DeleteParticipantSelf)
	mux.HandleFunc("POST /polls/{pollID}/p/{editToken}/comments/{commentID}/delete", s.h.DeleteOwnComment)

	// Organizer capability URLs.
	mux.HandleFunc("GET /polls/{pollID}/admin/{adminToken}", s.h.ShowPollAdmin)
	mux.HandleFunc("POST /polls/{pollID}/admin/{adminToken}", s.h.UpdatePoll)
	mux.HandleFunc("POST /polls/{pollID}/admin/{adminToken}/options", s.h.AddOptions)
	mux.HandleFunc("POST /polls/{pollID}/admin/{adminToken}/options/{optionID}/delete", s.h.DeleteOption)
	mux.HandleFunc("POST /polls/{pollID}/admin/{adminToken}/status", s.h.SetPollStatus)
	mux.HandleFunc("POST /polls/{pollID}/admin/{adminToken}/participants/{participantID}/delete", s.h.AdminDeleteParticipant)
	mux.HandleFunc("POST /polls/{pollID}/admin/{adminToken}/comments/{commentID}/delete", s.h.AdminDeleteComment)
	mux.HandleFunc("POST /polls/{pollID}/admin/{adminToken}/regenerate", s.h.RegenerateAdminToken)
	mux.HandleFunc("POST /polls/{pollID}/admin/{adminToken}/delete", s.h.DeletePoll)
	mux.HandleFunc("POST /polls/{pollID}/admin/{adminToken}/claim", s.h.ClaimPoll)
	mux.HandleFunc("POST /polls/{pollID}/admin/{adminToken}/finalize", s.h.FinalizePoll)
	mux.HandleFunc("POST /polls/{pollID}/admin/{adminToken}/cancel", s.h.CancelEvent)
	mux.HandleFunc("GET /polls/{pollID}/admin/{adminToken}/export.csv", s.h.ExportCSV)

	// Session-authorized mirror of the organizer routes, for claimed
	// polls reached from the dashboard (no token in the URL).
	mux.HandleFunc("GET /polls/{pollID}/manage", s.h.ShowPollAdmin)
	mux.HandleFunc("POST /polls/{pollID}/manage", s.h.UpdatePoll)
	mux.HandleFunc("POST /polls/{pollID}/manage/options", s.h.AddOptions)
	mux.HandleFunc("POST /polls/{pollID}/manage/options/{optionID}/delete", s.h.DeleteOption)
	mux.HandleFunc("POST /polls/{pollID}/manage/status", s.h.SetPollStatus)
	mux.HandleFunc("POST /polls/{pollID}/manage/participants/{participantID}/delete", s.h.AdminDeleteParticipant)
	mux.HandleFunc("POST /polls/{pollID}/manage/comments/{commentID}/delete", s.h.AdminDeleteComment)
	mux.HandleFunc("POST /polls/{pollID}/manage/regenerate", s.h.RegenerateAdminToken)
	mux.HandleFunc("POST /polls/{pollID}/manage/delete", s.h.DeletePoll)
	mux.HandleFunc("POST /polls/{pollID}/manage/finalize", s.h.FinalizePoll)
	mux.HandleFunc("POST /polls/{pollID}/manage/cancel", s.h.CancelEvent)
	mux.HandleFunc("GET /polls/{pollID}/manage/export.csv", s.h.ExportCSV)

	// Spaces.
	mux.HandleFunc("POST /spaces", s.h.CreateSpace)
	mux.HandleFunc("POST /spaces/switch", s.h.SwitchSpace)
	mux.HandleFunc("GET /spaces/{slug}/settings", s.h.ShowSpaceSettings)
	mux.HandleFunc("POST /spaces/{slug}/settings", s.h.UpdateSpaceSettings)
	mux.HandleFunc("POST /spaces/{slug}/invitations", s.h.InviteMember)
	mux.HandleFunc("POST /spaces/{slug}/invitations/{invitationID}/cancel", s.h.CancelInvitation)
	mux.HandleFunc("POST /spaces/{slug}/members/{userID}/remove", s.h.RemoveMember)
	mux.HandleFunc("POST /spaces/{slug}/members/{userID}/role", s.h.ChangeMemberRole)
	mux.HandleFunc("POST /spaces/{slug}/members/{userID}/transfer", s.h.TransferOwnership)
	mux.HandleFunc("POST /spaces/{slug}/leave", s.h.LeaveSpace)
	mux.HandleFunc("GET /invitations/{token}", s.h.ShowInvitation)
	mux.HandleFunc("POST /invitations/{token}", s.h.AcceptInvitation)

	// Sign-in and account.
	mux.HandleFunc("GET /login", s.h.Login)
	mux.HandleFunc("GET /auth/{provider}/start", s.h.OAuthStart)
	mux.HandleFunc("GET /auth/{provider}/callback", s.h.OAuthCallback)
	mux.HandleFunc("POST /auth/email", s.h.RequestMagicLink)
	mux.HandleFunc("GET /auth/email/callback", s.h.MagicLinkCallback)
	mux.HandleFunc("POST /auth/logout", s.h.Logout)
	mux.HandleFunc("GET /dashboard", s.h.Dashboard)
	mux.HandleFunc("POST /lang", s.h.SetLanguage)

	// Instance control (gated on QUORUM_ADMIN_EMAILS).
	mux.HandleFunc("GET /admin", s.h.ShowInstanceAdmin)
	mux.HandleFunc("POST /admin/settings", s.h.UpdateInstanceSettings)
	mux.HandleFunc("POST /admin/jobs/{jobID}/retry", s.h.RetryDeadJob)
	mux.HandleFunc("POST /admin/jobs/{jobID}/delete", s.h.DeleteDeadJob)

	if s.metrics.Enabled() {
		mux.Handle("GET /metrics", s.metrics)
	}

	return chain(mux,
		recoverPanic(s.log),
		requestID,
		accessLog(s.log, s.metrics),
		securityHeaders,
		s.sessions.LoadAndSave,
	)
}

// cacheStatic sets a modest cache lifetime on embedded assets; hashed
// filenames (and immutable caching) come with the real asset pipeline.
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	s.log.Info("listening", "addr", s.cfg.Addr, "base_url", s.cfg.BaseURL)

	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	s.log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
