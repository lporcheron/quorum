// Package store opens the database and keeps the schema current. One
// set of migrations and one sqlc-generated query package serve both
// engines: the SQLite text is the single source, and the three
// SQLite-only idioms it contains are substituted when rendering for
// PostgreSQL (see renderMigrations).
package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"testing/fstest"

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

// pgSubstitutions maps the only three SQLite idioms the migrations may
// use onto their PostgreSQL forms. Anything else in the migration
// files must be common to both engines — the PostgreSQL test run is
// the enforcement.
var pgSubstitutions = [][2]string{
	{"INTEGER PRIMARY KEY", "INTEGER GENERATED ALWAYS AS IDENTITY PRIMARY KEY"},
	{" BLOB ", " BYTEA "},
	{" REAL ", " TIMESTAMPTZ "}, // sessions.expiry, per scs store convention
}

// renderMigrations returns the migration filesystem for a dialect.
func renderMigrations(dialect Dialect) (fs.FS, error) {
	if dialect != DialectPostgres {
		return migrations.FS, nil
	}
	rendered := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		raw, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		text := string(raw)
		for _, sub := range pgSubstitutions {
			text = strings.ReplaceAll(text, sub[0], sub[1])
		}
		rendered[e.Name()] = &fstest.MapFile{Data: []byte(text)}
	}
	return rendered, nil
}

// Migrate applies all pending embedded migrations for the dialect.
func Migrate(ctx context.Context, db *sql.DB, dialect Dialect, log *slog.Logger) error {
	gooseDialect := goose.DialectSQLite3
	if dialect == DialectPostgres {
		gooseDialect = goose.DialectPostgres
	}
	fsys, err := renderMigrations(dialect)
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(gooseDialect, db, fsys)
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
