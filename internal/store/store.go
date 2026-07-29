// Package store opens the database and keeps the schema current. The
// sqlc-generated query code will live in subpackages (one per engine).
package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/lporcheron/quorum/migrations"
)

// Open opens (creating it if needed) the SQLite database at path with
// the pragmas the application relies on. Use ":memory:" for tests.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %q: %w", path, err)
	}
	// SQLite allows a single writer; one connection sidesteps
	// SQLITE_BUSY entirely. Revisit with a dedicated read pool if
	// contention ever shows up in practice.
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite database %q: %w", path, err)
	}
	return db, nil
}

// Migrate applies all pending embedded migrations.
func Migrate(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, r := range results {
		log.LogAttrs(ctx, slog.LevelInfo, "migration applied",
			slog.String("source", r.Source.Path),
			slog.Duration("duration", r.Duration),
		)
	}
	return nil
}
