// Package storetest opens a migrated database for tests: in-memory
// SQLite by default, PostgreSQL when TEST_DATABASE_URL is set — the
// same suites then prove the single query set on both engines.
package storetest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"

	"github.com/lporcheron/quorum/internal/store"
)

// Dialect reports which engine this test run targets.
func Dialect() store.Dialect {
	if os.Getenv("TEST_DATABASE_URL") != "" {
		return store.DialectPostgres
	}
	return store.DialectSQLite
}

// Open returns a migrated database and its store, cleaned up with the
// test. On PostgreSQL each test lives in its own throwaway schema.
func Open(t *testing.T) (*sql.DB, *store.Store) {
	t.Helper()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	if base := os.Getenv("TEST_DATABASE_URL"); base != "" {
		db := openPostgresSchema(t, ctx, base)
		if err := store.Migrate(ctx, db, store.DialectPostgres, log); err != nil {
			t.Fatalf("migrate (postgres): %v", err)
		}
		return db, store.New(db, store.DialectPostgres)
	}

	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, store.DialectSQLite, log); err != nil {
		t.Fatalf("migrate (sqlite): %v", err)
	}
	return db, store.New(db, store.DialectSQLite)
}

// openPostgresSchema creates a private schema and connects with
// search_path pinned to it, so parallel tests never collide.
func openPostgresSchema(t *testing.T, ctx context.Context, base string) *sql.DB {
	t.Helper()
	buf := make([]byte, 6)
	rand.Read(buf) // never fails per crypto/rand contract
	schema := "t_" + hex.EncodeToString(buf)

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open postgres admin connection: %v", err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	db, err := store.OpenPostgres(ctx, u.String())
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
		admin.Close()
	})
	return db
}
