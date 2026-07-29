// Command quorum runs the Quorum server: a self-hostable availability
// poll in a single static binary.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	// The binary must not depend on the system tz database.
	_ "time/tzdata"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/internal/config"
	"github.com/lporcheron/quorum/internal/handler"
	"github.com/lporcheron/quorum/internal/i18n"
	"github.com/lporcheron/quorum/internal/job"
	"github.com/lporcheron/quorum/internal/mail"
	"github.com/lporcheron/quorum/internal/notify"
	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/internal/server"
	"github.com/lporcheron/quorum/internal/space"
	"github.com/lporcheron/quorum/internal/store"
)

// version is stamped by the Makefile via -ldflags.
var version = "dev"

func main() {
	if err := run(context.Background(), os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "quorum:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string, logOut io.Writer) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}

	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	var logHandler slog.Handler = slog.NewJSONHandler(logOut, opts)
	if cfg.LogFormat == "text" {
		logHandler = slog.NewTextHandler(logOut, opts)
	}
	log := slog.New(logHandler)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("starting quorum", "version", version, "db", cfg.DBPath)

	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, log); err != nil {
		return err
	}

	tr, err := i18n.New()
	if err != nil {
		return err
	}

	st := store.New(db)
	polls := poll.NewService(st, nil)
	spaces := space.NewService(st, nil)
	authsvc := auth.NewService(st, nil, cfg.RegistrationsOpen, cfg.EmailAllowedDomains)
	providers := auth.NewProviders(cfg, cfg.BaseURL)
	sessions := auth.NewSessionManager(db, cfg.BaseURL)
	mailer := mail.New(cfg.SMTP)
	if !mailer.Enabled() {
		log.Info("smtp not configured; email features are disabled")
	}

	queue := job.NewQueue(st, nil)
	notifier := notify.New(log, queue, mailer, polls, authsvc, tr, cfg.BaseURL, cfg.SMTP.From, nil)
	worker := job.NewWorker(queue, log, notifier.Handlers())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if err := worker.Run(ctx); err != nil {
			log.Error("job worker stopped", "error", err)
		}
	}()

	h := handler.New(log, db, tr, polls, spaces, authsvc, providers, sessions, mailer, notifier, cfg.BaseURL)
	err = server.New(cfg, log, h, sessions).Run(ctx)
	<-workerDone
	return err
}
