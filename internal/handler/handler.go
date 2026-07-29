// Package handler holds the HTTP handlers. Handlers stay thin: parse
// the request, call the domain, render a template.
package handler

import (
	"database/sql"
	"log/slog"

	"github.com/lporcheron/quorum/internal/i18n"
)

// Handler bundles the dependencies shared by all HTTP handlers.
type Handler struct {
	log *slog.Logger
	db  *sql.DB
	tr  *i18n.Translator
}

// New wires a Handler; all dependencies are explicit.
func New(log *slog.Logger, db *sql.DB, tr *i18n.Translator) *Handler {
	return &Handler{log: log, db: db, tr: tr}
}
