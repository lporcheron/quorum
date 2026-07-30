package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lporcheron/quorum/internal/store/sqlite"
)

// Store combines the sqlc-generated queries with transaction support,
// adapted to the engine behind db.
type Store struct {
	db      *sql.DB
	dialect Dialect
	*sqlite.Queries
}

// New wraps an opened database.
func New(db *sql.DB, dialect Dialect) *Store {
	return &Store{
		db:      db,
		dialect: dialect,
		Queries: sqlite.New(wrapDBTX(db, dialect)),
	}
}

// Dialect reports the engine this store talks to.
func (s *Store) Dialect() Dialect { return s.dialect }

// Tx runs fn inside a transaction, committing if it returns nil.
func (s *Store) Tx(ctx context.Context, fn func(q *sqlite.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	if err := fn(sqlite.New(wrapDBTX(tx, s.dialect))); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
