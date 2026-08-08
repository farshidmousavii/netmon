package arp

// Integration tests for the ARP collector: a fake walker serves recorded
// SNMP varbind fixtures (synthetic TEST-NET-1 addresses and locally
// administered MACs only — never real infrastructure data) while the
// provider writes to a real Postgres, gated on BIDAR_TEST_DATABASE_URL.

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/snmp"
	"github.com/farshidmousavii/bidar/internal/store"
	"github.com/farshidmousavii/bidar/internal/testdb"
)

// Fixture addresses: TEST-NET-1 (192.0.2.0/24, RFC 5737) + locally
// administered MACs (02:xx, RFC 7042). Never real data.

func varbind(suffix []int, value any) snmp.Varbind {
	return snmp.Varbind{Suffix: suffix, Value: value}
}

// fixtureWalker serves canned varbinds per base OID; optional per-OID
// errors.
type fixtureWalker struct {
	tables  map[string][]snmp.Varbind
	errs    map[string]error
	calls   map[string]int
	lastCfg snmp.Config
}

func (f *fixtureWalker) walk(ctx context.Context, cfg snmp.Config, baseOID string) ([]snmp.Varbind, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[baseOID]++
	f.lastCfg = cfg
	if err := f.errs[baseOID]; err != nil {
		return nil, err
	}
	return f.tables[baseOID], nil
}

type arpHarness struct {
	provider *Provider
	walker   *fixtureWalker
	pool     *pgxpool.Pool
	st       *store.Store
	enc      *crypto.Encryptor
}

func newArpHarness(t *testing.T) *arpHarness {
	t.Helper()
	// Each test gets its own scratch database so concurrent package
	// binaries never wipe each other's tables.
	pool := testdb.Open(t, testdb.ScratchURL(t, testdb.BaseURL(t)))

	key := make([]byte, 32)
	for i := range key {
		key[i] = 0xAB
	}
	enc, err := crypto.New(key)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}

	walker := &fixtureWalker{tables: map[string][]snmp.Varbind{}, errs: map[string]error{}}
	st := store.New(pool)
	p, err := newWithWalker(st, enc, slog.Default(), walker.walk)
	if err != nil {
		t.Fatalf("newWithWalker: %v", err)
	}
	return &arpHarness{provider: p, walker: walker, pool: pool, st: st, enc: enc}
}

// seedCoreDevice inserts a core device + v2c profile with an encrypted
// community, returning the device id.
func (h *arpHarness) seedCoreDevice(t *testing.T, name string, ip string, community string) int64 {
	t.Helper()
	ctx := context.Background()
	encCommunity, err := h.enc.Encrypt([]byte(community))
	if err != nil {
		t.Fatalf("encrypt community: %v", err)
	}
	var profileID int64
	if err := h.pool.QueryRow(ctx, `
		INSERT INTO snmp_profiles (name, version, community_encrypted, timeout_ms, retries)
		VALUES ($1, 'v2c', $2, 2000, 2) RETURNING id`, "test-profile-"+name, encCommunity).Scan(&profileID); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	var deviceID int64
	if err := h.pool.QueryRow(ctx, `
		INSERT INTO network_devices (name, protocol_family, role, mgmt_ip, enabled, snmp_profile_id)
		VALUES ($1, 'cisco_snmp', 'core', $2::inet, true, $3) RETURNING id`,
		name, ip, profileID).Scan(&deviceID); err != nil {
		t.Fatalf("insert device: %v", err)
	}
	return deviceID
}

func mustIP(t *testing.T, s string) netip.Addr {
	t.Helper()
	ip, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse ip %s: %v", s, err)
	}
	return ip
}

func TestRunPhysicalTable(t *testing.T) {
	h := newArpHarness(t)
	h.seedCoreDevice(t, "core-a", "192.0.2.1", "testcommunity")

	// SVI names: ifIndex 10 -> Vlan20, ifIndex 20 -> Vlan30.
	h.walker.tables[oidIfName] = []snmp.Varbind{
		varbind([]int{10}, []byte("Vlan20")),
		varbind([]int{20}, []byte("VLAN 30")),
	}
	// ipNetToPhysicalTable: suffix [ifIndex, ip1..ip4], value MAC.
	h.walker.tables[oidIPNetToPhysicalPhysAddress] = []snmp.Varbind{
		varbind([]int{10, 192, 0, 2, 5}, []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x05}),
		varbind([]int{20, 192, 0, 2, 6}, []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x06}),
	}

	res, err := h.provider.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsFound != 2 {
		t.Errorf("ItemsFound = %d, want 2", res.ItemsFound)
	}
	if !h.provider.Health().Healthy {
		t.Errorf("Health = %+v", h.provider.Health())
	}

	// Walked with the decrypted community.
	if h.walker.lastCfg.Community != "testcommunity" {
		t.Errorf("walk community = %q, want decrypted testcommunity", h.walker.lastCfg.Community)
	}

	ctx := context.Background()
	h1, err := h.st.FindHostByIP(ctx, mustIP(t, "192.0.2.5"))
	if err != nil {
		t.Fatalf("find host 192.0.2.5: %v", err)
	}
	if h1.CurrentVLAN == nil || *h1.CurrentVLAN != 20 {
		t.Errorf("host vlan = %v, want 20 (inferred from SVI Vlan20)", h1.CurrentVLAN)
	}
	if h1.VLANSrc == nil || *h1.VLANSrc != "arp_svi" {
		t.Errorf("vlan_source = %v, want arp_svi", h1.VLANSrc)
	}
	if h1.CurrentMAC == nil || h1.CurrentMAC.String() != "02:00:00:00:00:05" {
		t.Errorf("host mac = %v", h1.CurrentMAC)
	}
	if h1.ADStatus != "unknown" {
		t.Errorf("ad_status = %q, want unknown (not in AD)", h1.ADStatus)
	}

	h2, err := h.st.FindHostByIP(ctx, mustIP(t, "192.0.2.6"))
	if err != nil {
		t.Fatalf("find host 192.0.2.6: %v", err)
	}
	if h2.CurrentVLAN == nil || *h2.CurrentVLAN != 30 {
		t.Errorf("host vlan = %v, want 30 (spaced VLAN 30 name)", h2.CurrentVLAN)
	}

	// Observations carry the VLAN + source.
	var vlanCount int
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM host_observations WHERE source='arp' AND vlan_number IS NOT NULL`).Scan(&vlanCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if vlanCount != 2 {
		t.Errorf("observations with vlan = %d, want 2", vlanCount)
	}
}

func TestRunMediaTableFallback(t *testing.T) {
	h := newArpHarness(t)
	h.seedCoreDevice(t, "core-b", "192.0.2.1", "testcommunity")

	// No ipNetToPhysicalTable (empty walk, no error) -> fallback to
	// ipNetToMediaTable.
	h.walker.tables[oidIPNetToPhysicalPhysAddress] = nil
	h.walker.tables[oidIPNetToMediaIfIndex] = []snmp.Varbind{
		varbind([]int{192, 0, 2, 7}, 30),
	}
	h.walker.tables[oidIPNetToMediaPhysAddress] = []snmp.Varbind{
		varbind([]int{192, 0, 2, 7}, []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x07}),
	}
	h.walker.tables[oidIfName] = []snmp.Varbind{
		varbind([]int{30}, []byte("Vlan40")),
	}

	res, err := h.provider.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsFound != 1 {
		t.Errorf("ItemsFound = %d, want 1", res.ItemsFound)
	}

	ctx := context.Background()
	host, err := h.st.FindHostByIP(ctx, mustIP(t, "192.0.2.7"))
	if err != nil {
		t.Fatalf("find host: %v", err)
	}
	if host.CurrentVLAN == nil || *host.CurrentVLAN != 40 {
		t.Errorf("host vlan = %v, want 40", host.CurrentVLAN)
	}
	if host.CurrentMAC == nil || host.CurrentMAC.String() != "02:00:00:00:00:07" {
		t.Errorf("host mac = %v", host.CurrentMAC)
	}
	if h.walker.calls[oidIPNetToMediaIfIndex] != 1 || h.walker.calls[oidIPNetToMediaPhysAddress] != 1 {
		t.Errorf("media walks: %v", h.walker.calls)
	}
}

func TestRunIdempotentHostsStableObservationsAccumulate(t *testing.T) {
	h := newArpHarness(t)
	h.seedCoreDevice(t, "core-c", "192.0.2.1", "testcommunity")
	h.walker.tables[oidIfName] = []snmp.Varbind{varbind([]int{10}, []byte("Vlan20"))}
	h.walker.tables[oidIPNetToPhysicalPhysAddress] = []snmp.Varbind{
		varbind([]int{10, 192, 0, 2, 9}, []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x09}),
	}

	for i := 0; i < 2; i++ {
		if _, err := h.provider.Run(context.Background()); err != nil {
			t.Fatalf("Run %d: %v", i+1, err)
		}
	}

	ctx := context.Background()
	var hosts, obs int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM hosts`).Scan(&hosts); err != nil {
		t.Fatalf("count hosts: %v", err)
	}
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM host_observations WHERE source='arp'`).Scan(&obs); err != nil {
		t.Fatalf("count obs: %v", err)
	}
	// hosts = current state (stable); observations = history (one row per
	// run per entry, by design).
	if hosts != 1 {
		t.Errorf("hosts after 2 runs = %d, want 1", hosts)
	}
	if obs != 2 {
		t.Errorf("observations after 2 runs = %d, want 2 (history log)", obs)
	}
}

func TestRunIPMatchUpdatesExistingHost(t *testing.T) {
	h := newArpHarness(t)
	h.seedCoreDevice(t, "core-d", "192.0.2.1", "testcommunity")
	h.walker.tables[oidIfName] = []snmp.Varbind{varbind([]int{10}, []byte("Vlan20"))}
	h.walker.tables[oidIPNetToPhysicalPhysAddress] = []snmp.Varbind{
		varbind([]int{10, 192, 0, 2, 9}, []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x09}),
	}

	// A host already known to AD (linked by a previous AD sync) with the
	// same IP must be updated, not duplicated.
	hostname := "workstation"
	ctx := context.Background()
	ip := mustIP(t, "192.0.2.9")
	existing := &hostSeed{
		Hostname:    &hostname,
		CurrentIP:   &ip,
		ADStatus:    "known",
		MatchStatus: "matched",
	}
	id, err := h.insertHostDirect(ctx, existing)
	if err != nil {
		t.Fatalf("seed host: %v", err)
	}

	if _, err := h.provider.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var hosts int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM hosts`).Scan(&hosts); err != nil {
		t.Fatalf("count hosts: %v", err)
	}
	if hosts != 1 {
		t.Errorf("hosts = %d, want 1 (IP match must update, not insert)", hosts)
	}

	// The AD-linked host keeps its identity and gains presence + VLAN.
	var adStatus string
	var vlan *int32
	if err := h.pool.QueryRow(ctx,
		`SELECT ad_status, current_vlan FROM hosts WHERE id = $1`, id).Scan(&adStatus, &vlan); err != nil {
		t.Fatalf("read host: %v", err)
	}
	if adStatus != "known" {
		t.Errorf("ad_status = %q, want known (preserved)", adStatus)
	}
	if vlan == nil || *vlan != 20 {
		t.Errorf("current_vlan = %v, want 20", vlan)
	}
}

type hostSeed struct {
	Hostname    *string
	CurrentIP   *netip.Addr
	ADStatus    string
	MatchStatus string
}

func (h *arpHarness) insertHostDirect(ctx context.Context, s *hostSeed) (int64, error) {
	var id int64
	err := h.pool.QueryRow(ctx, `
		INSERT INTO hosts (hostname, current_ip, ad_status, match_status)
		VALUES ($1, $2::inet, $3, $4) RETURNING id`,
		s.Hostname, s.CurrentIP, s.ADStatus, s.MatchStatus).Scan(&id)
	return id, err
}

func TestRunDeviceWithoutProfileSkipped(t *testing.T) {
	h := newArpHarness(t)
	ctx := context.Background()
	var deviceID int64
	if err := h.pool.QueryRow(ctx, `
		INSERT INTO network_devices (name, protocol_family, role, mgmt_ip, enabled)
		VALUES ('core-no-profile', 'cisco_snmp', 'core', '192.0.2.1'::inet, true) RETURNING id`).Scan(&deviceID); err != nil {
		t.Fatalf("insert device: %v", err)
	}

	res, err := h.provider.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsFound != 0 {
		t.Errorf("ItemsFound = %d, want 0", res.ItemsFound)
	}
	if !h.provider.Health().Healthy {
		t.Errorf("Health = %+v, want healthy (device skipped, not fatal)", h.provider.Health())
	}
}

func TestRunAllDevicesFail(t *testing.T) {
	h := newArpHarness(t)
	h.seedCoreDevice(t, "core-e", "192.0.2.1", "testcommunity")
	h.walker.errs[oidIPNetToPhysicalPhysAddress] = errors.New("timeout")
	h.walker.errs[oidIPNetToMediaIfIndex] = errors.New("timeout")
	h.walker.errs[oidIPNetToMediaPhysAddress] = errors.New("timeout")

	_, err := h.provider.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when every device fails")
	}
	if h.provider.Health().Healthy {
		t.Error("Health should be unhealthy")
	}
}

func TestParseVLANFromIfName(t *testing.T) {
	cases := map[string]*int32{
		"Vlan20":   int32p(20),
		"vlan20":   int32p(20),
		"VLAN 30":  int32p(30),
		"Vlan4094": int32p(4094),
		"Gi0/1":    nil,
		"ether1":   nil,
		"Vlan":     nil,
		"Vlan4095": nil, // out of range
	}
	for name, want := range cases {
		got := parseVLANFromIfName(name)
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("parseVLANFromIfName(%q) = %v, want %v", name, got, want)
		}
	}
}

func int32p(v int32) *int32 { return &v }
