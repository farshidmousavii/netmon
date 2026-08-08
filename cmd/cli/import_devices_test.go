package cli

// Integration tests for import-devices against a real Postgres, gated on
// BIDAR_TEST_DATABASE_URL (same pattern as internal/db). The migration must
// be applied first (run TestMigrateIntegration in internal/db, or
// `bidar migrate`).
//
// Local run:
//
//	docker run -d --rm --name bidar-migtest \
//	  -e POSTGRES_PASSWORD=bidartest -e POSTGRES_DB=bidar -p 5434:5432 postgres:16-alpine
//	BIDAR_TEST_DATABASE_URL=postgres://postgres:bidartest@localhost:5434/bidar \
//	  go test ./cmd/cli/ -run TestImportDevices -v

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/db"
	"github.com/farshidmousavii/bidar/internal/envconfig"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testMasterKey is a fixed, valid base64-encoded 32-byte key (0xAB x32)
// shared by the import tests and their decrypt assertions.
var testMasterKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xAB}, 32))

func importTestEnv(t *testing.T) (string, string) {
	t.Helper()
	url := os.Getenv(envconfig.TestDatabaseURL)
	if url == "" {
		t.Skip("BIDAR_TEST_DATABASE_URL not set; skipping import-devices integration test")
	}
	t.Setenv(db.DatabaseURLEnv, url)
	t.Setenv(crypto.MasterKeyEnv, testMasterKey)
	return url, testMasterKey
}

// importTestPool opens a pool to the test DB, applies the schema (Migrate
// is idempotent, so this is a no-op when already applied), and wipes the
// three import tables so each test starts clean.
func importTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := os.Getenv(db.DatabaseURLEnv)

	if err := db.Migrate(ctx, url); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(context.Background(), `DELETE FROM network_devices`); err != nil {
		t.Fatalf("wipe network_devices: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM snmp_profiles`); err != nil {
		t.Fatalf("wipe snmp_profiles: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM ssh_credentials`); err != nil {
		t.Fatalf("wipe ssh_credentials: %v", err)
	}
	return pool
}

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}

const fixtureYAML = `version: 1

credentials:
  shared:
    username: admin
    password: sharedpass
  mtk:
    username: admin
    password: mtkpass

devices:
  - name: sw-acc-01
    ip: 192.0.2.10
    port: '22'
    vendor: cisco
    credential: shared
    type: switch
  - name: sw-acc-02
    ip: 192.0.2.11
    port: '22'
    vendor: cisco
    credential: shared
    type: switch
  - name: gw-mtk-01
    ip: 192.0.2.12
    port: '22'
    vendor: mikrotik
    credential: mtk
    type: router

snmp:
  community: NCESNMP
  timeout: 5

backup:
  directory: backups
  archive_path: ""
`

const fixtureYAMLNoSNMP = `version: 1

credentials:
  shared:
    username: admin
    password: sharedpass

devices:
  - name: sw-acc-01
    ip: 192.0.2.10
    port: '22'
    vendor: cisco
    credential: shared
    type: switch

backup:
  directory: backups
  archive_path: ""
`

const fixtureCSV = `#snmp_community=NCESNMP
#snmp_timeout=5
#backup_dir=backups
#backup_archive=""
#ssh_timeout=5
#ssh_retry_attempts=3
#ssh_retry_initial_delay=1
#ssh_retry_max_delay=5
name,ip,port,vendor,username,password
sw-csv-01,192.0.2.20,22,cisco,admin,csvpass1
gw-csv-02,192.0.2.21,22,mikrotik,admin,csvpass2
`

func TestImportDevicesYAML(t *testing.T) {
	importTestEnv(t)
	pool := importTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	path := writeFixture(t, "config.yaml", fixtureYAML)
	summary, err := importDevices(ctx, path)
	if err != nil {
		t.Fatalf("importDevices: %v", err)
	}

	if summary.DevicesImported != 3 {
		t.Errorf("DevicesImported = %d, want 3", summary.DevicesImported)
	}
	if len(summary.Unassigned) != 3 {
		t.Errorf("Unassigned = %v, want all 3 devices", summary.Unassigned)
	}

	// Devices: mapping, function, role, links.
	rows, err := pool.Query(ctx, `
		SELECT name, protocol_family, function, role, enabled,
		       host(mgmt_ip), snmp_profile_id, ssh_credential_id
		FROM network_devices ORDER BY name`)
	if err != nil {
		t.Fatalf("query devices: %v", err)
	}
	defer rows.Close()
	type deviceRow struct {
		name, family, function, role, ip string
		enabled                          bool
		snmpProfileID, sshCredID         *int64
	}
	var devices []deviceRow
	for rows.Next() {
		var d deviceRow
		if err := rows.Scan(&d.name, &d.family, &d.function, &d.role, &d.enabled,
			&d.ip, &d.snmpProfileID, &d.sshCredID); err != nil {
			t.Fatalf("scan device: %v", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate devices: %v", err)
	}
	if len(devices) != 3 {
		t.Fatalf("got %d device rows, want 3", len(devices))
	}
	for _, d := range devices {
		if d.role != "unassigned" || !d.enabled {
			t.Errorf("device %s: role=%q enabled=%v, want unassigned/true", d.name, d.role, d.enabled)
		}
		if d.snmpProfileID == nil {
			t.Errorf("device %s: snmp_profile_id is null, want the imported profile", d.name)
		}
		if d.sshCredID == nil {
			t.Errorf("device %s: ssh_credential_id is null", d.name)
		}
	}
	wantFamily := map[string]string{"sw-acc-01": "cisco_snmp", "sw-acc-02": "cisco_snmp", "gw-mtk-01": "mikrotik_routeros"}
	wantFunction := map[string]string{"sw-acc-01": "switch", "sw-acc-02": "switch", "gw-mtk-01": "router"}
	for _, d := range devices {
		if d.family != wantFamily[d.name] {
			t.Errorf("device %s: protocol_family = %q, want %q", d.name, d.family, wantFamily[d.name])
		}
		if d.function != wantFunction[d.name] {
			t.Errorf("device %s: function = %q, want %q", d.name, d.function, wantFunction[d.name])
		}
	}

	// Exactly one snmp profile, decryptable, linked everywhere.
	var profileCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM snmp_profiles`).Scan(&profileCount); err != nil {
		t.Fatalf("count profiles: %v", err)
	}
	if profileCount != 1 {
		t.Errorf("snmp_profiles count = %d, want 1", profileCount)
	}
	enc, err := crypto.NewFromEnv()
	if err != nil {
		t.Fatalf("crypto.NewFromEnv: %v", err)
	}
	var profileName, version string
	var timeoutMS int
	var communityEnc []byte
	if err := pool.QueryRow(ctx,
		`SELECT name, version, community_encrypted, timeout_ms FROM snmp_profiles`).
		Scan(&profileName, &version, &communityEnc, &timeoutMS); err != nil {
		t.Fatalf("scan profile: %v", err)
	}
	if profileName != "imported-config.yaml" || version != "v2c" || timeoutMS != 5000 {
		t.Errorf("profile = %q/%q/%dms, want imported-config.yaml/v2c/5000", profileName, version, timeoutMS)
	}
	if community, err := enc.Decrypt(communityEnc); err != nil || string(community) != "NCESNMP" {
		t.Errorf("community decrypt: %q err=%v, want NCESNMP", community, err)
	}

	// Exactly two ssh_credentials: the shared credential is deduplicated.
	var credCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ssh_credentials`).Scan(&credCount); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if credCount != 2 {
		t.Errorf("ssh_credentials count = %d, want 2 (shared credential deduplicated)", credCount)
	}
	credIDs := map[string]int64{}
	rows2, err := pool.Query(ctx, `SELECT name, id FROM ssh_credentials`)
	if err != nil {
		t.Fatalf("query credentials: %v", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var name string
		var id int64
		if err := rows2.Scan(&name, &id); err != nil {
			t.Fatalf("scan credential: %v", err)
		}
		credIDs[name] = id
	}
	if err := rows2.Err(); err != nil {
		t.Fatalf("iterate credentials: %v", err)
	}
	// The two cisco switches share one ssh_credentials row.
	sharedID := credIDs["shared"]
	for _, d := range devices {
		if d.name == "sw-acc-01" || d.name == "sw-acc-02" {
			if *d.sshCredID != sharedID {
				t.Errorf("device %s: ssh_credential_id %d, want shared row %d", d.name, *d.sshCredID, sharedID)
			}
		}
	}
}

func TestImportDevicesCSV(t *testing.T) {
	importTestEnv(t)
	pool := importTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	path := writeFixture(t, "devices.csv", fixtureCSV)
	summary, err := importDevices(ctx, path)
	if err != nil {
		t.Fatalf("importDevices: %v", err)
	}
	if summary.DevicesImported != 2 {
		t.Errorf("DevicesImported = %d, want 2", summary.DevicesImported)
	}

	// CSV per-row credentials -> one ssh_credentials row each.
	var credCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ssh_credentials`).Scan(&credCount); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if credCount != 2 {
		t.Errorf("ssh_credentials count = %d, want 2 (csv_<name> rows)", credCount)
	}

	// The #snmp_community=/#snmp_timeout= settings become the profile.
	var profileName string
	var timeoutMS int
	if err := pool.QueryRow(ctx, `SELECT name, timeout_ms FROM snmp_profiles`).
		Scan(&profileName, &timeoutMS); err != nil {
		t.Fatalf("scan profile: %v", err)
	}
	if profileName != "imported-devices.csv" || timeoutMS != 5000 {
		t.Errorf("profile = %q/%dms, want imported-devices.csv/5000", profileName, timeoutMS)
	}

	// Vendor mapping from CSV rows.
	var family string
	if err := pool.QueryRow(ctx,
		`SELECT protocol_family FROM network_devices WHERE name = 'gw-csv-02'`).Scan(&family); err != nil {
		t.Fatalf("query gw-csv-02: %v", err)
	}
	if family != "mikrotik_routeros" {
		t.Errorf("gw-csv-02 protocol_family = %q, want mikrotik_routeros", family)
	}
}

func TestImportDevicesNoSNMPBlock(t *testing.T) {
	importTestEnv(t)
	pool := importTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	path := writeFixture(t, "config-nosnmp.yaml", fixtureYAMLNoSNMP)
	summary, err := importDevices(ctx, path)
	if err != nil {
		t.Fatalf("importDevices: %v", err)
	}
	if summary.DevicesImported != 1 {
		t.Errorf("DevicesImported = %d, want 1", summary.DevicesImported)
	}

	var profileCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM snmp_profiles`).Scan(&profileCount); err != nil {
		t.Fatalf("count profiles: %v", err)
	}
	if profileCount != 0 {
		t.Errorf("snmp_profiles count = %d, want 0 (no SNMP block -> no fabricated profile)", profileCount)
	}
	var snmpProfileID *int64
	if err := pool.QueryRow(ctx,
		`SELECT snmp_profile_id FROM network_devices WHERE name = 'sw-acc-01'`).Scan(&snmpProfileID); err != nil {
		t.Fatalf("query snmp_profile_id: %v", err)
	}
	if snmpProfileID != nil {
		t.Errorf("snmp_profile_id = %d, want NULL", *snmpProfileID)
	}
}

func TestImportDevicesIdempotentAndPreservesRole(t *testing.T) {
	importTestEnv(t)
	pool := importTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	path := writeFixture(t, "config.yaml", fixtureYAML)

	for i := 0; i < 2; i++ {
		if _, err := importDevices(ctx, path); err != nil {
			t.Fatalf("import pass %d: %v", i+1, err)
		}
	}

	// No duplicates after two runs.
	counts := func() (devices, profiles, creds int) {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM network_devices`).Scan(&devices); err != nil {
			t.Fatalf("count devices: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM snmp_profiles`).Scan(&profiles); err != nil {
			t.Fatalf("count profiles: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM ssh_credentials`).Scan(&creds); err != nil {
			t.Fatalf("count credentials: %v", err)
		}
		return
	}
	d1, p1, c1 := counts()
	if d1 != 3 || p1 != 1 || c1 != 2 {
		t.Fatalf("after 2 imports: devices=%d profiles=%d creds=%d, want 3/1/2", d1, p1, c1)
	}

	// An operator assigns role=core; a third import must not reset it.
	if _, err := pool.Exec(ctx,
		`UPDATE network_devices SET role = 'core' WHERE name = 'sw-acc-01'`); err != nil {
		t.Fatalf("set role=core: %v", err)
	}
	summary, err := importDevices(ctx, path)
	if err != nil {
		t.Fatalf("import pass 3: %v", err)
	}
	if len(summary.Unassigned) != 2 {
		t.Errorf("Unassigned = %v, want 2 (sw-acc-01 is now core)", summary.Unassigned)
	}
	var role string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM network_devices WHERE name = 'sw-acc-01'`).Scan(&role); err != nil {
		t.Fatalf("query role: %v", err)
	}
	if role != "core" {
		t.Errorf("sw-acc-01 role = %q after re-import, want core (preserved)", role)
	}
	d2, p2, c2 := counts()
	if d2 != 3 || p2 != 1 || c2 != 2 {
		t.Errorf("after 3rd import: devices=%d profiles=%d creds=%d, want 3/1/2", d2, p2, c2)
	}
}

// TestImportDevicesFailFastOrder: BIDAR_MASTER_KEY is checked before the
// database, so the command fails with a clear message even when the DB
// env var is unset. No DB needed for this test.
func TestImportDevicesFailFastOrder(t *testing.T) {
	t.Setenv(db.DatabaseURLEnv, "") // would fail if reached
	t.Setenv(crypto.MasterKeyEnv, "")

	path := writeFixture(t, "config.yaml", fixtureYAML)
	_, err := importDevices(context.Background(), path)
	if err == nil {
		t.Fatal("expected error with BIDAR_MASTER_KEY unset")
	}
	if !strings.Contains(err.Error(), envconfig.MasterKey) {
		t.Errorf("error should mention BIDAR_MASTER_KEY, got: %v", err)
	}

	// With the key set but the DB URL unset, the error moves to the DB var.
	t.Setenv(crypto.MasterKeyEnv, testMasterKey)
	_, err = importDevices(context.Background(), path)
	if err == nil {
		t.Fatal("expected error with BIDAR_DATABASE_URL unset")
	}
	if !strings.Contains(err.Error(), envconfig.DatabaseURL) {
		t.Errorf("error should mention BIDAR_DATABASE_URL, got: %v", err)
	}
}
