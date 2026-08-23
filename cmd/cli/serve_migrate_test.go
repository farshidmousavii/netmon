package cli

// Tests for bidar serve and bidar migrate. Integration tests are gated on
// BIDAR_TEST_DATABASE_URL, same pattern as the import-devices tests.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/db"
	"github.com/farshidmousavii/bidar/internal/envconfig"
	"github.com/farshidmousavii/bidar/internal/testdb"
)

// serveTestEnv sets the daemon env vars against the shared test database
// (migrated, idempotently) and returns the URL.
func serveTestEnv(t *testing.T) string {
	t.Helper()
	// Own scratch database per test (see testdb package doc).
	scratch := testdb.ScratchURL(t, testdb.BaseURL(t))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := db.Migrate(ctx, scratch); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Setenv(db.DatabaseURLEnv, scratch)
	t.Setenv(crypto.MasterKeyEnv, testMasterKey)
	t.Setenv(daemonLogLevelEnv, "debug")
	return scratch
}

// The daemon log level env var, via the single source of truth. (Renamed
// from the misleading "dlogLevelEnvForTest": this is the real production
// env var name, not test-only.)
const daemonLogLevelEnv = envconfig.LogLevel

// serveScratchDB creates an UN-migrated database for the schema-missing
// test (testdb.ScratchURL does not apply migrations).
func serveScratchDB(t *testing.T, baseURL string) string {
	t.Helper()
	return testdb.ScratchURL(t, baseURL)
}

func TestMigrateSchema(t *testing.T) {
	serveTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	version, err := migrateSchema(ctx)
	if err != nil {
		t.Fatalf("migrateSchema: %v", err)
	}
	// 0001_init + 0002_network_devices_mgmt_ip_unique
	// + 0003_phase2_inventory + 0004_discovery_jobs + 0005_routeros_port.
	if version != 5 {
		t.Errorf("schema version = %d, want 5", version)
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
