package dhcp

// Integration tests for the DHCP provider: mikrotik
// source_type talks to the fake RouterOS server from the mikrotik package
// tests (rebuilt here); everything reconciles into a real Postgres via
// testdb. Gated on BIDAR_TEST_DATABASE_URL.

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
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

func newDHCPHarness(t *testing.T, dial routerosDial) *dhcpHarness {
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
	p, err := newWithDeps(Config{}, store.New(pool), enc, slog.Default(),
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

func TestWindowsUnimplemented(t *testing.T) {
	h := newDHCPHarness(t, nil)
	h.seedSource(t, "win-fixture", "windows", `{"path": "/tmp/leases.json"}`, "")
	_, err := h.provider.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for unimplemented windows source")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("error should mention not implemented, got: %v", err)
	}
}

func TestCiscoUnimplemented(t *testing.T) {
	h := newDHCPHarness(t, nil)
	h.seedSource(t, "cisco-fixture", "cisco", `{"host": "192.0.2.1"}`, "")
	_, err := h.provider.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for unimplemented cisco source")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("error should mention not implemented, got: %v", err)
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

	h := newDHCPHarness(t, dial)
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
	h := newDHCPHarness(t, nil)
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
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	_ = cfg
}
