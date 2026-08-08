package icmpsweep

// Tests with a fake pinger (no real network) plus one loopback
// integration test with the real ping binary, both gated on
// BIDAR_TEST_DATABASE_URL. Synthetic addresses only (TEST-NET-1).

import (
	"context"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farshidmousavii/bidar/internal/store"
	"github.com/farshidmousavii/bidar/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

type icmpHarness struct {
	provider *Provider
	pool     *pgxpool.Pool
	st       *store.Store
	ping     *fakePinger
}

type fakePinger struct {
	alive     map[netip.Addr]bool
	mu        sync.Mutex
	active    int
	maxActive int
	total     atomic.Int64
}

func (f *fakePinger) ping(ctx context.Context, ip netip.Addr) bool {
	f.mu.Lock()
	f.active++
	f.total.Add(1)
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()

	// Bound each fake ping in time so a cancelled test can't hang.
	select {
	case <-ctx.Done():
		return false
	case <-time.After(10 * time.Millisecond):
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive[ip]
}

func newICMPHarness(t *testing.T, cfg Config, alive map[netip.Addr]bool) *icmpHarness {
	t.Helper()
	pool := testdb.Open(t, testdb.ScratchURL(t, testdb.BaseURL(t)))
	ping := &fakePinger{alive: alive}
	p, err := newWithPinger(cfg, store.New(pool), slog.Default(), ping.ping)
	if err != nil {
		t.Fatalf("newWithPinger: %v", err)
	}
	return &icmpHarness{provider: p, pool: pool, st: store.New(pool), ping: ping}
}

func (h *icmpHarness) seedSubnet(t *testing.T, cidr string, enabled bool) int64 {
	t.Helper()
	var id int64
	if err := h.pool.QueryRow(context.Background(), `
		INSERT INTO subnets (cidr, label, enabled) VALUES ($1::inet, $2, $3) RETURNING id`,
		cidr, "test-"+cidr, enabled).Scan(&id); err != nil {
		t.Fatalf("insert subnet %s: %v", cidr, err)
	}
	return id
}

func TestSweepWritesObservationsWithoutCreatingHosts(t *testing.T) {
	// 192.0.2.0/30: usable .1 and .2 (network .0 and broadcast .3
	// skipped). Both alive — but NO host rows may be created from a bare
	// IP: ICMP is liveness evidence, not identity.
	alive := map[netip.Addr]bool{
		netip.MustParseAddr("192.0.2.1"): true,
		netip.MustParseAddr("192.0.2.2"): true,
	}
	h := newICMPHarness(t, Config{}, alive)
	h.seedSubnet(t, "192.0.2.0/30", true)

	res, err := h.provider.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsFound != 2 {
		t.Errorf("ItemsFound = %d, want 2", res.ItemsFound)
	}

	ctx := context.Background()
	var obs, hosts, linked int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM host_observations WHERE source='icmp'`).Scan(&obs); err != nil {
		t.Fatalf("count obs: %v", err)
	}
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM hosts`).Scan(&hosts); err != nil {
		t.Fatalf("count hosts: %v", err)
	}
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM host_observations WHERE source='icmp' AND host_id IS NOT NULL`).Scan(&linked); err != nil {
		t.Fatalf("count linked: %v", err)
	}
	if obs != 2 {
		t.Errorf("icmp observations = %d, want 2", obs)
	}
	if hosts != 0 {
		t.Errorf("hosts = %d, want 0 (ICMP must not fabricate bare-IP hosts)", hosts)
	}
	if linked != 0 {
		t.Errorf("linked observations = %d, want 0 (no host to link)", linked)
	}
}

func TestSweepUpdatesExistingHostByIP(t *testing.T) {
	alive := map[netip.Addr]bool{netip.MustParseAddr("192.0.2.1"): true}
	h := newICMPHarness(t, Config{}, alive)
	h.seedSubnet(t, "192.0.2.0/30", true)

	// A host that ARP/DHCP already identified exists with this IP: ICMP
	// must update its liveness and link the observation.
	ip := netip.MustParseAddr("192.0.2.1")
	var hostID int64
	if err := h.pool.QueryRow(context.Background(), `
		INSERT INTO hosts (current_ip, ad_status, match_status)
		VALUES ($1::inet, 'unknown', 'matched') RETURNING id`, ip).Scan(&hostID); err != nil {
		t.Fatalf("seed host: %v", err)
	}

	if _, err := h.provider.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	ctx := context.Background()
	var linked, hosts int
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM host_observations WHERE source='icmp' AND host_id = $1`, hostID).Scan(&linked); err != nil {
		t.Fatalf("count linked: %v", err)
	}
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM hosts`).Scan(&hosts); err != nil {
		t.Fatalf("count hosts: %v", err)
	}
	if linked != 1 {
		t.Errorf("linked observations = %d, want 1", linked)
	}
	if hosts != 1 {
		t.Errorf("hosts = %d, want 1", hosts)
	}
}

func TestDisabledSubnetSkipped(t *testing.T) {
	h := newICMPHarness(t, Config{}, map[netip.Addr]bool{netip.MustParseAddr("192.0.2.1"): true})
	h.seedSubnet(t, "192.0.2.0/30", false) // disabled

	res, err := h.provider.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsFound != 0 {
		t.Errorf("ItemsFound = %d, want 0 (disabled subnet)", res.ItemsFound)
	}
}

func TestConcurrencyBounded(t *testing.T) {
	// /28 = 14 usable hosts; concurrency 4; the fake tracks the max
	// concurrent in-flight pings.
	var addrs []netip.Addr
	alive := map[netip.Addr]bool{}
	for i := 1; i <= 14; i++ {
		a := netip.MustParseAddr("192.0.2." + itoa(i))
		addrs = append(addrs, a)
		alive[a] = true
	}
	h := newICMPHarness(t, Config{Concurrency: 4}, alive)
	h.seedSubnet(t, "192.0.2.0/28", true)

	if _, err := h.provider.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	h.ping.mu.Lock()
	got := h.ping.maxActive
	h.ping.mu.Unlock()
	if got > 4 {
		t.Errorf("max concurrent pings = %d, want <= 4", got)
	}
}

func TestSubnetSizeGuard(t *testing.T) {
	h := newICMPHarness(t, Config{MaxSubnetHosts: 8}, map[netip.Addr]bool{})
	// /24 has 254 usable hosts: over the limit, must be skipped without
	// enumerating or pinging anything.
	h.seedSubnet(t, "192.0.2.0/24", true)

	res, err := h.provider.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsFound != 0 {
		t.Errorf("ItemsFound = %d, want 0 (subnet over limit)", res.ItemsFound)
	}
	if h.ping.total.Load() != 0 {
		t.Errorf("pings issued = %d, want 0", h.ping.total.Load())
	}
}

func TestUsableAddresses(t *testing.T) {
	cases := []struct {
		cidr string
		want int
	}{
		{"192.0.2.0/30", 2},
		{"192.0.2.0/29", 6},
		{"192.0.2.0/31", 2}, // RFC 3021: both usable
		{"192.0.2.5/32", 1},
		{"192.0.2.0/28", 14},
	}
	for _, c := range cases {
		got := len(usableAddresses(netip.MustParsePrefix(c.cidr)))
		if got != c.want {
			t.Errorf("usableAddresses(%s) = %d, want %d", c.cidr, got, c.want)
		}
	}
}

func TestRealPingLoopback(t *testing.T) {
	// Integration: real ping binary against loopback. Gated on the DB env
	// like the other integration tests; skipped when ping is missing.
	if os.Getenv("BIDAR_TEST_DATABASE_URL") == "" {
		t.Skip("BIDAR_TEST_DATABASE_URL not set; skipping loopback ping integration test")
	}
	if _, err := exec.LookPath("ping"); err != nil {
		t.Skip("ping binary not available")
	}

	pool := testdb.Open(t, testdb.ScratchURL(t, testdb.BaseURL(t)))
	p, err := New(Config{Timeout: 2 * time.Second}, store.New(pool), slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO subnets (cidr, label, enabled) VALUES ('127.0.0.1/32', 'loopback-test', true)`); err != nil {
		t.Fatalf("seed subnet: %v", err)
	}

	res, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsFound != 1 {
		t.Errorf("ItemsFound = %d, want 1 (loopback answers ping)", res.ItemsFound)
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
