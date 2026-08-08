// Package db owns the Postgres connection and migration concerns for the
// bidar daemon and its CLI tooling (serve / migrate / import-devices).
//
// It is deliberately split into independently usable pieces:
//   - DatabaseURLFromEnv: reads BIDAR_DATABASE_URL, fails fast if unset.
//   - Open: a pgx connection pool (what `bidar serve` holds for its lifetime).
//   - Migrate: runs golang-migrate up against the database (what `bidar
//     migrate` calls) -- it opens its own short-lived connection so a long-
//     running pool is not required to run migrations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	// Registers the "pgx/v5" database/sql driver used by the migration
	// runner below.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/farshidmousavii/bidar/migrations"
)

// DatabaseURLEnv is the environment variable holding the Postgres
// connection string. Pinned in docs/roadmap.md Phase 0.
const DatabaseURLEnv = "BIDAR_DATABASE_URL"

// pingTimeout bounds the fail-fast connectivity check in Open.
const pingTimeout = 10 * time.Second

// DatabaseURLFromEnv returns the BIDAR_DATABASE_URL connection string.
// It errors loudly rather than defaulting to anything -- a silently guessed
// database would be worse than no database.
func DatabaseURLFromEnv() (string, error) {
	url := os.Getenv(DatabaseURLEnv)
	if url == "" {
		return "", fmt.Errorf("%s is not set: set it to a Postgres connection string (e.g. postgres://user:pass@host:5432/bidar)", DatabaseURLEnv)
	}
	return url, nil
}

// Open creates a pgx connection pool for databaseURL and verifies the
// database is reachable (fail fast: a daemon that cannot reach its database
// should not start). The caller owns the pool and must Close it.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is empty")
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}

// Migrate applies all pending migrations (golang-migrate, embedded in the
// binary via migrations.FS). It opens its own database/sql connection so it
// works independently of any pool, and it is safe to call repeatedly:
// applying already-applied migrations returns no error.
//
// ctx is honoured at entry (fail fast on cancellation); the underlying
// golang-migrate Up() run itself is not context-cancellable.
func Migrate(ctx context.Context, databaseURL string) error {
	if databaseURL == "" {
		return fmt.Errorf("database URL is empty")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("migrate cancelled: %w", err)
	}

	sqlDB, err := sql.Open("pgx/v5", databaseURL)
	if err != nil {
		return fmt.Errorf("open database for migration: %w", err)
	}
	defer sqlDB.Close()

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}

	driver, err := pgxmigrate.WithInstance(sqlDB, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("init migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}

	return nil
}
