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

	"github.com/lporcheron/quorum/internal/config"
	"github.com/lporcheron/quorum/internal/handler"
	"github.com/lporcheron/quorum/web"
)

// Server owns the http.Server lifecycle.
type Server struct {
	cfg config.Config
	log *slog.Logger
	h   *handler.Handler
}

// New wires a Server; all dependencies are explicit.
func New(cfg config.Config, log *slog.Logger, h *handler.Handler) *Server {
	return &Server{cfg: cfg, log: log, h: h}
}

// Handler builds the full routing table with the middleware chain.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.h.Home)
	mux.HandleFunc("GET /healthz", s.h.Healthz)
	mux.Handle("GET /static/", cacheStatic(http.FileServerFS(web.StaticFS)))

	return chain(mux,
		recoverPanic(s.log),
		requestID,
		accessLog(s.log),
		securityHeaders,
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
