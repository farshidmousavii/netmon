package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/farshidmousavii/bidar/migrations"
)

// Integration tests against a real Postgres. Skipped unless
// BIDAR_TEST_DATABASE_URL points at a scratch database, per
// docs/coding-standards.md ("store layer: integration tests against a real
// Postgres"). The connecting role needs CREATEDB: this test runs up AND
// down in a throwaway database it creates and drops, so it never disturbs
// databases other integration tests (e.g. cmd/cli's import-devices tests)
// may be using concurrently — `go test ./...` runs package binaries in
// parallel. Local run:
//
//	docker run -d --rm --name bidar-migtest \
//	  -e POSTGRES_PASSWORD=bidartest -e POSTGRES_DB=bidar -p 5434:5432 postgres:16-alpine
//	BIDAR_TEST_DATABASE_URL=postgres://postgres:bidartest@localhost:5434/bidar \
//	  go test ./internal/db/ -run TestMigrateIntegration -v
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("BIDAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("BIDAR_TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	return url
}

// scratchDB creates a fresh database next to the one in baseURL (same
// server, random name), returns its URL, and registers cleanup to drop it.
func scratchDB(t *testing.T, baseURL string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := fmt.Sprintf("bidar_migrate_test_%d", time.Now().UnixNano())
	quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`

	pool, err := Open(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect for scratch db: %v", err)
	}
	if _, err := pool.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		pool.Close()
		t.Fatalf("create scratch database %s: %v (the BIDAR_TEST_DATABASE_URL role needs CREATEDB)", name, err)
	}
	pool.Close()

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		pool, err := Open(cleanupCtx, baseURL)
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
		t.Fatalf("parse BIDAR_TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func TestMigrateIntegration(t *testing.T) {
	scratch := scratchDB(t, testDatabaseURL(t))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	url := scratch

	// Sanity: the pool constructor works against a live database too.
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	// Up.
	if err := Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate up: %v", err)
	}

	// Idempotency: a second up is a no-op, not an error.
	if err := Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate up (second run) should be a no-op: %v", err)
	}

	expected := map[string]bool{
		"buildings":         false,
		"snmp_profiles":     false,
		"ssh_credentials":   false,
		"subnets":           false,
		"network_devices":   false,
		"dhcp_sources":      false,
		"hosts":             false,
		"host_observations": false,
		"provider_runs":     false,
	}
	// schema_migrations is created by golang-migrate itself.
	expected["schema_migrations"] = false

	rows, err := pool.Query(ctx,
		"SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'")
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		expected[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	for name, present := range expected {
		if !present {
			t.Errorf("table %q missing after 0001_init up", name)
		}
	}

	// Every index specified in docs/database-schema.md must exist.
	// pg_indexes also lists auto-created PRIMARY KEY indexes (one per
	// table), so count only the non-pkey indexes we declared.
	indexCounts := map[string]int{
		// 0002's uq_network_devices_mgmt_ip constraint index counts too.
		"network_devices":   2, // (role) WHERE enabled + UNIQUE (mgmt_ip)
		"hosts":             4, // lower(hostname), current_ip, current_mac, (match_status) WHERE needs_review
		"host_observations": 2, // host_id, (source, observed_at)
		"provider_runs":     1, // (provider, started_at)
	}
	for table, want := range indexCounts {
		var got int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM pg_indexes WHERE schemaname = 'public' AND tablename = $1 AND indexname NOT LIKE '%_pkey'", table).Scan(&got); err != nil {
			t.Fatalf("count indexes on %s: %v", table, err)
		}
		if got != want {
			t.Errorf("table %s: expected %d declared indexes, got %d", table, want, got)
		}
	}

	// Down: everything 0001_init created must be dropped cleanly.
	downDB, err := sql.Open("pgx/v5", url)
	if err != nil {
		t.Fatalf("open down-connection: %v", err)
	}
	defer downDB.Close()

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("iofs.New: %v", err)
	}
	driver, err := pgxmigrate.WithInstance(downDB, &pgxmigrate.Config{})
	if err != nil {
		t.Fatalf("pgx driver: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		t.Fatalf("migrate instance: %v", err)
	}
	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate down: %v", err)
	}

	var remaining []string
	rows, err = pool.Query(ctx,
		"SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'")
	if err != nil {
		t.Fatalf("query remaining tables: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan remaining table: %v", err)
		}
		remaining = append(remaining, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate remaining tables: %v", err)
	}
	// golang-migrate leaves its own schema_migrations bookkeeping table
	// behind at version -1; every bidar table must be gone.
	if len(remaining) != 1 || remaining[0] != "schema_migrations" {
		t.Errorf("after down, expected only schema_migrations, got %v", remaining)
	}
}

// TestEnsureSchemaUpToDate: the daemon's startup check — fails clearly on
// an un-migrated schema, passes with the current version, and never
// applies migrations itself.
func TestEnsureSchemaUpToDate(t *testing.T) {
	scratch := scratchDB(t, testDatabaseURL(t))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := Open(ctx, scratch)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	// Un-migrated schema: must fail with guidance, and must NOT have
	// applied anything (SchemaVersion still reports not migrated).
	if _, err := EnsureSchemaUpToDate(ctx, pool); err == nil {
		t.Fatal("expected error for un-migrated schema")
	} else if !strings.Contains(err.Error(), "bidar migrate") {
		t.Errorf("error should point at bidar migrate, got: %v", err)
	}

	// After a real migrate, the check passes and reports the version.
	if err := Migrate(ctx, scratch); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	version, err := EnsureSchemaUpToDate(ctx, pool)
	if err != nil {
		t.Fatalf("EnsureSchemaUpToDate after migrate: %v", err)
	}
	want, err := latestSchemaVersion()
	if err != nil {
		t.Fatalf("latestSchemaVersion: %v", err)
	}
	if version != want {
		t.Errorf("version = %d, want %d (latest embedded migration)", version, want)
	}
}
