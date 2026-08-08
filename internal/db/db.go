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
	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"time"

	// Registers the "pgx/v5" database/sql driver used by the migration
	// runner below.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/farshidmousavii/bidar/internal/envconfig"
	"github.com/farshidmousavii/bidar/migrations"
)

// DatabaseURLEnv is an alias for envconfig.DatabaseURL kept so this
// package's tests reference the same name as before. New code should use
// envconfig.DatabaseURL directly — internal/envconfig is the single
// source of truth for BIDAR_* names.
const DatabaseURLEnv = envconfig.DatabaseURL

// pingTimeout bounds the fail-fast connectivity check in Open.
const pingTimeout = 10 * time.Second

// DatabaseURLFromEnv returns the BIDAR_DATABASE_URL connection string.
// It errors loudly rather than defaulting to anything -- a silently guessed
// database would be worse than no database.
func DatabaseURLFromEnv() (string, error) {
	url := os.Getenv(envconfig.DatabaseURL)
	if url == "" {
		return "", fmt.Errorf("%s is not set: set it to a Postgres connection string (e.g. postgres://user:pass@host:5432/bidar)", envconfig.DatabaseURL)
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

// ErrSchemaNotMigrated is returned by SchemaVersion when the database has
// no schema_migrations table (golang-migrate has never run against it).
var ErrSchemaNotMigrated = errors.New("database schema not initialized (run bidar migrate)")

// SchemaVersion reports the applied migration version and dirty flag from
// the database's schema_migrations bookkeeping. It returns
// ErrSchemaNotMigrated (wrapped) when the table does not exist.
func SchemaVersion(ctx context.Context, pool *pgxpool.Pool) (version uint, dirty bool, err error) {
	err = pool.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty)
	// A missing table surfaces as pgx.ErrNoRows on some paths and as
	// SQLSTATE 42P01 (undefined table) on others — both mean "golang-
	// migrate has never run here".
	if errors.Is(err, pgx.ErrNoRows) || isUndefinedTable(err) {
		return 0, false, fmt.Errorf("%w", ErrSchemaNotMigrated)
	}
	if err != nil {
		return 0, false, fmt.Errorf("read schema version: %w", err)
	}
	return version, dirty, nil
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

// latestSchemaVersion is the highest migration version embedded in the
// binary — the version a migrated database is expected to be at.
func latestSchemaVersion() (uint, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return 0, fmt.Errorf("load embedded migrations: %w", err)
	}
	defer src.Close()

	version, err := src.First()
	if err != nil {
		return 0, fmt.Errorf("no embedded migrations found: %w", err)
	}
	for {
		next, err := src.Next(version)
		if errors.Is(err, os.ErrNotExist) {
			return version, nil
		}
		if err != nil {
			return 0, fmt.Errorf("walk embedded migrations: %w", err)
		}
		version = next
	}
}

// EnsureSchemaUpToDate verifies the database schema is at the version of
// the newest embedded migration. It never applies migrations — that is
// `bidar migrate`'s job — so a daemon started against an un-migrated,
// dirty, or stale schema fails loudly instead of silently running
// migrations as a side effect. On success it returns the current version.
func EnsureSchemaUpToDate(ctx context.Context, pool *pgxpool.Pool) (uint, error) {
	want, err := latestSchemaVersion()
	if err != nil {
		return 0, err
	}

	got, dirty, err := SchemaVersion(ctx, pool)
	if errors.Is(err, ErrSchemaNotMigrated) {
		return 0, fmt.Errorf("%w (expected version %d)", err, want)
	}
	if err != nil {
		return 0, err
	}
	if dirty {
		return 0, fmt.Errorf("database schema is dirty at version %d: run bidar migrate (fix the failed migration first)", got)
	}
	if got != want {
		return 0, fmt.Errorf("database schema at version %d, expected %d: run bidar migrate", got, want)
	}
	return got, nil
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
