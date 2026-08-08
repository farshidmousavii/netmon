// Package testdb is TEST-ONLY support for integration tests that need a
// real Postgres. It is never imported by production code.
//
// Rationale: `go test ./...` runs package test binaries in parallel, and
// several packages (cmd/cli, internal/providers/ad, internal/providers/arp,
// ...) exercise the same schema. If they all pointed at one database they
// would wipe each other's tables mid-test. ScratchURL gives every package
// (and every test) its own throwaway database, created next to the one in
// BIDAR_TEST_DATABASE_URL and dropped on cleanup (needs CREATEDB).
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshidmousavii/bidar/internal/db"
	"github.com/farshidmousavii/bidar/internal/envconfig"
)

// BaseURL returns BIDAR_TEST_DATABASE_URL or skips the test.
func BaseURL(t *testing.T) string {
	t.Helper()
	base := os.Getenv(envconfig.TestDatabaseURL)
	if base == "" {
		t.Skip(envconfig.TestDatabaseURL + " not set; skipping integration test")
	}
	return base
}

// ScratchURL creates a fresh database next to baseURL (same server, random
// name), returns its URL, and registers cleanup to drop it. The connecting
// role needs CREATEDB.
func ScratchURL(t *testing.T, baseURL string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := fmt.Sprintf("bidar_itest_%d", time.Now().UnixNano())
	quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`

	pool, err := db.Open(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect for scratch db: %v", err)
	}
	if _, err := pool.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		pool.Close()
		t.Fatalf("create scratch database %s: %v (the %s role needs CREATEDB)", name, err, envconfig.TestDatabaseURL)
	}
	pool.Close()

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		pool, err := db.Open(cleanupCtx, baseURL)
		if err != nil {
			t.Errorf("reconnect for scratch db cleanup: %v", err)
			return
		}
		defer pool.Close()
		if _, err := pool.Exec(cleanupCtx, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Errorf("drop scratch database %s: %v", name, err)
		}
	})

	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse %s: %v", envconfig.TestDatabaseURL, err)
	}
	u.Path = "/" + name
	return u.String()
}

// Open applies migrations (idempotent) and opens a pool to url; the pool
// is closed on cleanup. The pool is capped at 2 connections: test
// binaries run in parallel and several pools are alive at once against
// one Postgres server, whose default max_connections (100) would
// otherwise be exhausted under full-suite load.
func Open(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := db.Migrate(ctx, url); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	pool, err := db.Open(ctx, url+"?pool_max_conns=2")
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
