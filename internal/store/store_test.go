package store

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
)

func openMigrated(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(ctx, db, DialectSQLite, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return ctx, db
}

func TestMigrateCreatesSchema(t *testing.T) {
	ctx, db := openMigrated(t)

	for _, table := range []string{
		"users", "identities", "spaces", "space_members",
		"polls", "poll_options", "participants", "votes", "comments",
	} {
		var n int
		err := db.QueryRowContext(ctx,
			"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table,
		).Scan(&n)
		if err != nil {
			t.Fatalf("query sqlite_master: %v", err)
		}
		if n != 1 {
			t.Errorf("table %s missing", table)
		}
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	ctx, db := openMigrated(t)

	var on int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&on); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if on != 1 {
		t.Fatal("foreign_keys pragma is off")
	}

	_, err := db.ExecContext(ctx,
		`INSERT INTO identities (user_id, provider, subject, created_at)
		 VALUES (999, 'google', 'sub', '2026-07-29T00:00:00Z')`)
	if err == nil {
		t.Fatal("insert with dangling user_id succeeded, want FK violation")
	}
}

func TestPollOptionShapeChecks(t *testing.T) {
	ctx, db := openMigrated(t)

	_, err := db.ExecContext(ctx,
		`INSERT INTO polls (public_id, admin_token_hash, title, kind, timezone, created_at, updated_at)
		 VALUES ('p1', 'h1', 'Team dinner', 'timed', 'Europe/Paris',
		         '2026-07-29T00:00:00Z', '2026-07-29T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert poll: %v", err)
	}

	// A timed option must not carry an all-day date, and vice versa.
	_, err = db.ExecContext(ctx,
		`INSERT INTO poll_options (poll_id, starts_at, duration_minutes, all_day_date, position)
		 VALUES (1, '2026-08-01T18:00:00Z', 60, '2026-08-01', 0)`)
	if err == nil {
		t.Error("mixed timed+allday option accepted, want CHECK violation")
	}

	// An all-day poll must have no timezone.
	_, err = db.ExecContext(ctx,
		`INSERT INTO polls (public_id, admin_token_hash, title, kind, timezone, created_at, updated_at)
		 VALUES ('p2', 'h2', 'Offsite', 'allday', 'Europe/Paris',
		         '2026-07-29T00:00:00Z', '2026-07-29T00:00:00Z')`)
	if err == nil {
		t.Error("allday poll with a timezone accepted, want CHECK violation")
	}
}
