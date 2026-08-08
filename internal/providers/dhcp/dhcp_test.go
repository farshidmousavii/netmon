package dhcp

// Integration tests for the DHCP provider: Windows source_type reads
// fixture lease-export files (synthetic addresses only); mikrotik
// source_type talks to the fake RouterOS server from the mikrotik package
// tests (rebuilt here); everything reconciles into a real Postgres via
// testdb. Gated on BIDAR_TEST_DATABASE_URL.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/providers/mikrotik"
	"github.com/farshidmousavii/bidar/internal/store"
	"github.com/farshidmousavii/bidar/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

type dhcpHarness struct {
	provider *Provider
	pool     *pgxpool.Pool
	st       *store.Store
	enc      *crypto.Encryptor
	now      time.Time
}

func newDHCPHarness(t *testing.T, staleness time.Duration, dial routerosDial) *dhcpHarness {
	t.Helper()
	pool := testdb.Open(t, testdb.ScratchURL(t, testdb.BaseURL(t)))

	key := make([]byte, 32)
	for i := range key {
		key[i] = 0xCD
	}
	enc, err := crypto.New(key)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	p, err := newWithDeps(Config{Staleness: staleness}, store.New(pool), enc, slog.Default(),
		func() time.Time { return now }, dial)
	if err != nil {
		t.Fatalf("newWithDeps: %v", err)
	}
	return &dhcpHarness{provider: p, pool: pool, st: store.New(pool), enc: enc, now: now}
}

func (h *dhcpHarness) seedSource(t *testing.T, name, sourceType, connConfig string, password string) int64 {
	t.Helper()
	ctx := context.Background()
	var credEnc []byte
	if password != "" {
		enc, err := h.enc.Encrypt([]byte(password))
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		credEnc = enc
	}
	var id int64
	if err := h.pool.QueryRow(ctx, `
		INSERT INTO dhcp_sources (name, source_type, connection_config, credential_enc)
		VALUES ($1, $2, $3::jsonb, $4) RETURNING id`,
		name, sourceType, connConfig, credEnc).Scan(&id); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	return id
}

// writeWindowsExport writes a fixture lease-export file.
func writeWindowsExport(t *testing.T, dir string, exportedAt time.Time, leases string) string {
	t.Helper()
	content := fmt.Sprintf(`{"exported_at": %q, "server": "fixture", "lease_count": 1, "leases": %s}`,
		exportedAt.Format(time.RFC3339), leases)
	path := filepath.Join(dir, "leases.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

const activeLeases = `[
	{"AddressId": "192.0.2.50", "ClientId": "00-11-22-33-44-55", "HostName": "pc-one", "AddressState": "Active", "LeaseExpiryTime": "2026-08-15T12:00:00Z"},
	{"AddressId": "192.0.2.51", "ClientId": "00-11-22-33-44-66", "HostName": "", "AddressState": "Active", "LeaseExpiryTime": "2026-08-15T12:00:00Z"},
	{"AddressId": "192.0.2.52", "ClientId": "00-11-22-33-44-77", "HostName": "offered-pc", "AddressState": "Offered", "LeaseExpiryTime": "2026-08-08T12:30:00Z"}
]`

func TestWindowsFileExport(t *testing.T) {
	h := newDHCPHarness(t, 24*time.Hour, nil)
	dir := t.TempDir()
	path := writeWindowsExport(t, dir, h.now.Add(-1*time.Hour), activeLeases)

	h.seedSource(t, "win-fixture", "windows", fmt.Sprintf(`{"path": %q}`, path), "")

	res, err := h.provider.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Only the two Active leases count; the Offered one is skipped.
	if res.ItemsFound != 2 {
		t.Errorf("ItemsFound = %d, want 2 (Offered lease filtered)", res.ItemsFound)
	}
	if !h.provider.Health().Healthy {
		t.Errorf("Health = %+v", h.provider.Health())
	}

	ctx := context.Background()
	// Active lease with a hostname: host created with name + IP + MAC.
	h1, err := h.st.FindHostByIP(ctx, netip.MustParseAddr("192.0.2.50"))
	if err != nil {
		t.Fatalf("find host: %v", err)
	}
	if h1.Hostname == nil || *h1.Hostname != "pc-one" {
		t.Errorf("hostname = %v, want pc-one", h1.Hostname)
	}
	if h1.CurrentMAC == nil || h1.CurrentMAC.String() != "00:11:22:33:44:55" {
		t.Errorf("mac = %v", h1.CurrentMAC)
	}
	// No host for the Offered lease.
	if _, err := h.st.FindHostByIP(ctx, netip.MustParseAddr("192.0.2.52")); err == nil {
		t.Error("Offered lease must not create a host")
	}

	// Observations: 2, source=dhcp, linked.
	var obs int
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM host_observations WHERE source='dhcp' AND host_id IS NOT NULL`).Scan(&obs); err != nil {
		t.Fatalf("count obs: %v", err)
	}
	if obs != 2 {
		t.Errorf("dhcp observations = %d, want 2", obs)
	}
}

func TestWindowsStaleFileIsSourceFailure(t *testing.T) {
	h := newDHCPHarness(t, 24*time.Hour, nil)
	dir := t.TempDir()
	// 25h old vs 24h threshold: stale.
	path := writeWindowsExport(t, dir, h.now.Add(-25*time.Hour), activeLeases)
	h.seedSource(t, "win-stale", "windows", fmt.Sprintf(`{"path": %q}`, path), "")

	res, err := h.provider.Run(context.Background())
	if err == nil {
		t.Fatalf("expected error: every source failed (stale file), got %+v", res)
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error should mention staleness, got: %v", err)
	}
	if h.provider.Health().Healthy {
		t.Error("Health should be unhealthy")
	}
}

func TestWindowsMissingFileIsSourceFailure(t *testing.T) {
	h := newDHCPHarness(t, 24*time.Hour, nil)
	h.seedSource(t, "win-missing", "windows", `{"path": "/nonexistent/leases.json"}`, "")

	_, err := h.provider.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for missing lease file")
	}
}

// stubLeaseClient returns canned RouterOS leases.
type stubLeaseClient struct {
	leases []mikrotik.DHCPLease
	err    error
	cfg    mikrotik.Config
}

func (s *stubLeaseClient) DHCPLeases(ctx context.Context) ([]mikrotik.DHCPLease, error) {
	return s.leases, s.err
}

func (s *stubLeaseClient) Close() error { return nil }

func TestWindowsSourceFailsButMikrotikSucceeds(t *testing.T) {
	// One broken Windows source + one healthy MikroTik source: the
	// provider must stay healthy and return the MikroTik leases.
	stub := &stubLeaseClient{leases: []mikrotik.DHCPLease{
		{ID: "*1", Address: netip.MustParseAddr("192.0.2.60"), MAC: mustMAC(t, "02:00:00:00:00:60"), Hostname: "ros-client", Server: "dhcp-ros", Status: "bound"},
	}}
	dial := func(ctx context.Context, cfg mikrotik.Config) (leaseClient, error) {
		stub.cfg = cfg
		return stub, nil
	}

	h := newDHCPHarness(t, 24*time.Hour, dial)
	h.seedSource(t, "win-broken", "windows", `{"path": "/nonexistent/leases.json"}`, "")
	h.seedSource(t, "ros-fixture", "mikrotik", `{"host": "127.0.0.1", "username": "admin"}`, "ros-secret")

	res, err := h.provider.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsFound != 1 {
		t.Errorf("ItemsFound = %d, want 1 (mikrotik lease)", res.ItemsFound)
	}
	if !h.provider.Health().Healthy {
		t.Error("Health should stay healthy when one source works")
	}

	ctx := context.Background()
	h1, err := h.st.FindHostByIP(ctx, netip.MustParseAddr("192.0.2.60"))
	if err != nil {
		t.Fatalf("find ros client: %v", err)
	}
	if h1.Hostname == nil || *h1.Hostname != "ros-client" {
		t.Errorf("hostname = %v, want ros-client", h1.Hostname)
	}
}

func TestISCSourceSkipped(t *testing.T) {
	h := newDHCPHarness(t, 24*time.Hour, nil)
	h.seedSource(t, "isc-fixture", "isc", `{"path": "/etc/dhcp/dhcpd.leases"}`, "")

	res, err := h.provider.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsFound != 0 {
		t.Errorf("ItemsFound = %d, want 0", res.ItemsFound)
	}
	if !h.provider.Health().Healthy {
		t.Error("Health should be healthy for skipped source")
	}
}

func mustMAC(t *testing.T, s string) net.HardwareAddr {
	t.Helper()
	m, err := net.ParseMAC(s)
	if err != nil {
		t.Fatalf("parse mac %s: %v", s, err)
	}
	return m
}

func TestConfigFromEnv(t *testing.T) {
	t.Run("unset defaults to 24h", func(t *testing.T) {
		t.Setenv("BIDAR_DHCP_STALENESS", "")
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatalf("ConfigFromEnv: %v", err)
		}
		if cfg.Staleness != DefaultStaleness {
			t.Errorf("Staleness = %v, want %v", cfg.Staleness, DefaultStaleness)
		}
	})

	t.Run("custom duration", func(t *testing.T) {
		t.Setenv("BIDAR_DHCP_STALENESS", "30m")
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatalf("ConfigFromEnv: %v", err)
		}
		if cfg.Staleness != 30*time.Minute {
			t.Errorf("Staleness = %v, want 30m", cfg.Staleness)
		}
	})

	t.Run("invalid rejected", func(t *testing.T) {
		t.Setenv("BIDAR_DHCP_STALENESS", "soon")
		if _, err := ConfigFromEnv(); err == nil {
			t.Fatal("expected error for invalid duration")
		}
	})
}
