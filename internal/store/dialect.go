package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"

	"github.com/lporcheron/quorum/internal/store/sqlite"
)

// Dialect identifies the SQL engine behind the single query set.
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

// OpenPostgres connects to PostgreSQL through the pgx database/sql
// driver. The schema and queries are the same single source as SQLite;
// only placeholders and three column types differ, both handled at
// this layer.
func OpenPostgres(ctx context.Context, url string) (*sql.DB, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(10)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}

// rebindDBTX adapts the sqlc-generated SQLite queries (numbered ?N
// placeholders) to PostgreSQL ($N). The query text itself is written
// in the common subset of both dialects — enforced by running the full
// test suite against both engines.
type rebindDBTX struct {
	inner sqlite.DBTX
}

// wrapDBTX returns the engine-appropriate DBTX.
func wrapDBTX(inner sqlite.DBTX, dialect Dialect) sqlite.DBTX {
	if dialect == DialectPostgres {
		return rebindDBTX{inner: inner}
	}
	return inner
}

// rebind rewrites ?N placeholders to $N, skipping quoted literals.
func rebind(query string) string {
	var b strings.Builder
	b.Grow(len(query))
	inStr := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'':
			inStr = !inStr // a doubled '' toggles twice: net unchanged
			b.WriteByte(c)
		case c == '?' && !inStr && i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9':
			b.WriteByte('$')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func (r rebindDBTX) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return r.inner.ExecContext(ctx, rebind(query), args...)
}

func (r rebindDBTX) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return r.inner.PrepareContext(ctx, rebind(query))
}

func (r rebindDBTX) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return r.inner.QueryContext(ctx, rebind(query), args...)
}

func (r rebindDBTX) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return r.inner.QueryRowContext(ctx, rebind(query), args...)
}
