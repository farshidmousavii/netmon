package cli

// Tests for bidar serve and bidar migrate. Integration tests are gated on
// BIDAR_TEST_DATABASE_URL, same pattern as the import-devices tests.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/db"
	"github.com/farshidmousavii/bidar/internal/envconfig"
)

// serveTestEnv sets the daemon env vars against the shared test database
// (migrated, idempotently) and returns the URL.
func serveTestEnv(t *testing.T) string {
	t.Helper()
	base := os.Getenv(envconfig.TestDatabaseURL)
	if base == "" {
		t.Skip(envconfig.TestDatabaseURL + " not set; skipping serve/migrate integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := db.Migrate(ctx, base); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Setenv(db.DatabaseURLEnv, base)
	t.Setenv(crypto.MasterKeyEnv, testMasterKey)
	t.Setenv(daemonLogLevelEnv, "debug")
	return base
}

// The daemon log level env var, via the single source of truth. (Renamed
// from the misleading "dlogLevelEnvForTest": this is the real production
// env var name, not test-only.)
const daemonLogLevelEnv = envconfig.LogLevel

// serveScratchDB creates an UN-migrated database for the schema-missing
// test and registers cleanup.
func serveScratchDB(t *testing.T, baseURL string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := fmt.Sprintf("bidar_serve_test_%d", time.Now().UnixNano())

	pool, err := db.Open(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := pool.Exec(ctx, "CREATE DATABASE \""+name+"\""); err != nil {
		pool.Close()
		t.Fatalf("create scratch db %s: %v", name, err)
	}
	pool.Close()

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		p, err := db.Open(cctx, baseURL)
		if err != nil {
			t.Errorf("reconnect for cleanup: %v", err)
			return
		}
		defer p.Close()
		if _, err := p.Exec(cctx, "DROP DATABASE \""+name+"\" WITH (FORCE)"); err != nil {
			t.Errorf("drop scratch db: %v", err)
		}
	})

	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func TestMigrateSchema(t *testing.T) {
	serveTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	version, err := migrateSchema(ctx)
	if err != nil {
		t.Fatalf("migrateSchema: %v", err)
	}
	// 0001_init + 0002_network_devices_mgmt_ip_unique.
	if version != 2 {
		t.Errorf("schema version = %d, want 2", version)
	}
}

func TestMigrateSchemaEnvUnset(t *testing.T) {
	t.Setenv(db.DatabaseURLEnv, "")
	_, err := migrateSchema(context.Background())
	if err == nil {
		t.Fatal("expected error with BIDAR_DATABASE_URL unset")
	}
	if !strings.Contains(err.Error(), envconfig.DatabaseURL) {
		t.Errorf("error should mention BIDAR_DATABASE_URL, got: %v", err)
	}
}

func TestServeStubStartStop(t *testing.T) {
	serveTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())

	var serveErr error
	done := make(chan struct{})
	go func() {
		serveErr = serve(ctx)
		close(done)
	}()

	// Give the daemon time to pass all startup checks and block.
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit after context cancellation")
	}
	if serveErr != nil {
		t.Fatalf("serve returned error on graceful shutdown: %v", serveErr)
	}
}

func TestServeStubMissingMasterKey(t *testing.T) {
	t.Setenv(db.DatabaseURLEnv, "postgres://x:y@127.0.0.1:1/none")
	t.Setenv(crypto.MasterKeyEnv, "")

	err := serve(context.Background())
	if err == nil {
		t.Fatal("expected error with BIDAR_MASTER_KEY unset")
	}
	if !strings.Contains(err.Error(), envconfig.MasterKey) {
		t.Errorf("error should mention BIDAR_MASTER_KEY, got: %v", err)
	}
}

func TestServeStubSchemaNotMigrated(t *testing.T) {
	base := os.Getenv(envconfig.TestDatabaseURL)
	if base == "" {
		t.Skip(envconfig.TestDatabaseURL + " not set; skipping serve integration test")
	}
	scratch := serveScratchDB(t, base)

	t.Setenv(db.DatabaseURLEnv, scratch)
	t.Setenv(crypto.MasterKeyEnv, testMasterKey)
	t.Setenv(daemonLogLevelEnv, "info")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := serve(ctx)
	if err == nil {
		t.Fatal("expected error for un-migrated schema")
	}
	if !strings.Contains(err.Error(), "bidar migrate") {
		t.Errorf("error should point at bidar migrate, got: %v", err)
	}
}
