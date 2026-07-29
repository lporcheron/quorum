package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lporcheron/quorum/internal/store/sqlite"
)

// Store combines the sqlc-generated queries with transaction support.
type Store struct {
	db *sql.DB
	*sqlite.Queries
}

// New wraps an opened database.
func New(db *sql.DB) *Store {
	return &Store{db: db, Queries: sqlite.New(db)}
}

// Tx runs fn inside a transaction, committing if it returns nil.
func (s *Store) Tx(ctx context.Context, fn func(q *sqlite.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	if err := fn(s.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
