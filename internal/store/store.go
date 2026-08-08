// Package store owns every SQL statement in the daemon (docs/coding-
// standards.md: providers and api call store.*, never raw SQL). One file
// per table/aggregate. Constructed with the pool — no globals.
package store

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps the pgx pool and exposes the daemon's queries.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store bound to pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ErrNotFound is returned by single-row finders when no row matches
// (callers use errors.Is; pgx.ErrNoRows stays internal).
var ErrNotFound = errors.New("not found")

func isNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// notFound wraps a lookup error as ErrNotFound when the row is absent.
func notFound(err error) error {
	if isNotFound(err) {
		return fmt.Errorf("%w", ErrNotFound)
	}
	return err
}
