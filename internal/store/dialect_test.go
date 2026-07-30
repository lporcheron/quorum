package store

import (
	"io/fs"
	"strings"
	"testing"
)

func TestRebind(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT * FROM polls WHERE id = ?1", "SELECT * FROM polls WHERE id = $1"},
		{"WHERE run_at <= ?1 AND attempts < ?2 LIMIT ?3", "WHERE run_at <= $1 AND attempts < $2 LIMIT $3"},
		{"UPDATE x SET a = ?10, b = ?2", "UPDATE x SET a = $10, b = $2"},
		// Question marks inside string literals stay untouched.
		{"SELECT '?1 stays', col FROM t WHERE id = ?1", "SELECT '?1 stays', col FROM t WHERE id = $1"},
		{"SELECT 'it''s ?1 quoted' WHERE id = ?1", "SELECT 'it''s ?1 quoted' WHERE id = $1"},
		// A bare ? (no digit) is not a sqlc placeholder; leave it alone.
		{"SELECT 'a' WHERE x = '?'", "SELECT 'a' WHERE x = '?'"},
	}
	for _, tc := range cases {
		if got := rebind(tc.in); got != tc.want {
			t.Errorf("rebind(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderMigrationsForPostgres(t *testing.T) {
	fsys, err := renderMigrations(DialectPostgres)
	if err != nil {
		t.Fatalf("renderMigrations: %v", err)
	}

	core, err := fs.ReadFile(fsys, "00001_core.sql")
	if err != nil {
		t.Fatalf("read rendered core: %v", err)
	}
	if !strings.Contains(string(core), "id         INTEGER GENERATED ALWAYS AS IDENTITY PRIMARY KEY") {
		t.Errorf("identity substitution missing:\n%s", core)
	}
	if strings.Contains(string(core), "INTEGER PRIMARY KEY,") {
		t.Errorf("sqlite auto-increment form survived rendering")
	}

	authSQL, err := fs.ReadFile(fsys, "00002_auth.sql")
	if err != nil {
		t.Fatalf("read rendered auth: %v", err)
	}
	for _, want := range []string{" BYTEA ", " TIMESTAMPTZ "} {
		if !strings.Contains(string(authSQL), want) {
			t.Errorf("sessions substitution %q missing:\n%s", want, authSQL)
		}
	}
	for _, gone := range []string{" BLOB ", " REAL "} {
		if strings.Contains(string(authSQL), gone) {
			t.Errorf("sqlite type %q survived rendering", gone)
		}
	}

	// SQLite rendering is the untouched source.
	sqliteFS, err := renderMigrations(DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	orig, _ := fs.ReadFile(sqliteFS, "00001_core.sql")
	if strings.Contains(string(orig), "GENERATED ALWAYS AS IDENTITY") {
		t.Errorf("sqlite rendering was modified")
	}
}
