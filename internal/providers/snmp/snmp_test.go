package snmp

// Tests for the Phase 2 SNMP provider: a fake snmpClient feeds recorded
// varbind fixtures; the store side runs against real Postgres via the
// testdb harness. Gated on BIDAR_TEST_DATABASE_URL.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gosnmp/gosnmp"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/snmp"
	"github.com/farshidmousavii/bidar/internal/store"
	"github.com/farshidmousavii/bidar/internal/testdb"
)

func octet(v string) any       { return []byte(v) }
func macBytes(s string) []byte { m, _ := net.ParseMAC(s); return []byte(m) }

// fakeClient serves canned responses; per-target behavior is chosen by
// the harness's dial function.
type fakeClient struct {
	get       map[string]snmp.Varbind
	columns   []snmp.TableRow
	static    []snmp.Varbind
	staticErr error
	getErr    error
	fdb       map[string][]snmp.Varbind // generic walk responses (MAC tables)
	fdbErr    error                     // fails every generic walk
	lldpRows  []snmp.TableRow
	lldpErr   error
	cdpRows   []snmp.TableRow
	cdpErr    error
	closed    bool
}

func (f *fakeClient) Get(_ context.Context, oids ...string) ([]snmp.Varbind, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := make([]snmp.Varbind, len(oids))
	for i, o := range oids {
		out[i] = f.get[o] // zero Varbind (nil value) for unserved OIDs
	}
	return out, nil
}

func (f *fakeClient) WalkTable(_ context.Context, baseOID string) ([]snmp.Varbind, error) {
	if baseOID == oidVlanStaticName {
		return f.static, f.staticErr
	}
	if f.fdbErr != nil {
		return nil, f.fdbErr
	}
	if rows, ok := f.fdb[baseOID]; ok {
		return rows, nil
	}
	return nil, nil
}

func (f *fakeClient) WalkTableColumns(_ context.Context, cols ...string) ([]snmp.TableRow, error) {
	switch cols[0] {
	case lldpCols[0]:
		return f.lldpRows, f.lldpErr
	case cdpCols[0]:
		return f.cdpRows, f.cdpErr
	default:
		return f.columns, nil // interface table
	}
}

func (f *fakeClient) Close() error { f.closed = true; return nil }

type targetError string

func (e targetError) Error() string { return string(e) }

type harness struct {
	pool *pgxpool.Pool
	st   *store.Store
	p    *Provider
	now  time.Time
	seq  int
	// deviceID is the last seeded device's row id (PollDeviceByID tests).
	deviceID int64
	// defaultClient answers any dial without an explicit override;
	// dialErr routes specific targets to failure instead.
	defaultClient snmpClient
	profileID     int64
	dialErr       map[string]error
	// dialForCommunity routes community@vlan contexts (BRIDGE-MIB walks)
	// to their own canned clients.
	dialForCommunity map[string]snmpClient
}

// defaultClient is the interface type on purpose: a bare nil literal
// must stay an untyped-nil interface, not a typed-nil *fakeClient.
func newHarness(t *testing.T, defaultClient snmpClient) *harness {
	t.Helper()
	pool := testdb.Open(t, testdb.ScratchURL(t, testdb.BaseURL(t)))
	st := store.New(pool)

	key := bytes.Repeat([]byte{0x42}, 32)
	enc, err := crypto.New(key)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}

	h := &harness{
		pool:             pool,
		st:               st,
		now:              time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC),
		defaultClient:    defaultClient,
		dialErr:          map[string]error{},
		dialForCommunity: map[string]snmpClient{},
	}

	var pid int64
	community, err := enc.Encrypt([]byte("public"))
	if err != nil {
		t.Fatalf("encrypt community: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO snmp_profiles (name, version, community_encrypted, timeout_ms, retries)
		VALUES ('fixture', 'v2c', $1, 2000, 2) RETURNING id`, community).Scan(&pid); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	h.profileID = pid

	p, err := newWithDial(st, enc, slog.Default(),
		func() time.Time { return h.now },
		func(_ context.Context, cfg snmp.Config) (snmpClient, error) {
			if c, ok := h.dialForCommunity[cfg.Community]; ok {
				return c, nil
			}
			if e, ok := h.dialErr[cfg.Target]; ok {
				return nil, e
			}
			if h.defaultClient != nil {
				return h.defaultClient, nil
			}
			return nil, targetError("unexpected dial target " + cfg.Target)
		})
	if err != nil {
		t.Fatalf("newWithDial: %v", err)
	}
	h.p = p

	// Primary fixture device.
	h.addDevice(t, "fixture-sw")
	return h
}

// addDevice seeds one enabled cisco_snmp device and returns its mgmt_ip
// (the dial target). The row id lands on h.deviceID for PollDeviceByID
// tests.
func (h *harness) addDevice(t *testing.T, name string) string {
	t.Helper()
	h.seq++
	ip := fmt.Sprintf("192.0.2.%d", 10+h.seq)
	var id int64
	if err := h.pool.QueryRow(context.Background(), `
		INSERT INTO network_devices (name, protocol_family, role, mgmt_ip, enabled, snmp_profile_id)
		VALUES ($1, 'cisco_snmp', 'unassigned', $2::inet, true, $3) RETURNING id`,
		name, ip, h.profileID).Scan(&id); err != nil {
		t.Fatalf("seed device %s: %v", name, err)
	}
	h.deviceID = id
	return ip
}

// failDial makes every connection to target fail.
func (h *harness) failDial(target, why string) {
	h.dialErr[target] = targetError(why)
}

// addVlanClient registers a canned client for one community@vlan context.
func (h *harness) addVlanClient(community string, c *fakeClient) {
	h.dialForCommunity[community] = c
}

// macSuffix converts a MAC string into its 6 OID index components.
func macSuffix(s string) []int {
	m, err := net.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	out := make([]int, len(m))
	for i, b := range m {
		out[i] = int(b)
	}
	return out
}

// fixtureClient builds the standard happy-path client: system info,
// three interfaces (one sparse), two static VLAN names.
func fixtureClient() *fakeClient {
	upTime := uint32(12345600) // hundredths of a second since boot
	lastChange := uint32(1000) // 10s after boot
	return &fakeClient{
		get: map[string]snmp.Varbind{
			oidSysDescr:  {OID: oidSysDescr, Type: gosnmp.OctetString, Value: []byte("Cisco IOS Software, C2960 Software (C2960-LANBASEK9-M), Version 12.2(55)SE11, RELEASE SOFTWARE (fc3)")},
			oidSysUpTime: {OID: oidSysUpTime, Type: gosnmp.TimeTicks, Value: upTime},
			oidSysName:   {OID: oidSysName, Type: gosnmp.OctetString, Value: []byte("fixture-sw-01")},
		},
		columns: []snmp.TableRow{
			{Index: []int{1}, Values: []any{
				octet("Gi1/0/1"), octet("GigabitEthernet1/0/1"), macBytes("00:11:22:33:44:01"),
				1, 1, lastChange, 10,
			}},
			{Index: []int{2}, Values: []any{
				octet("Gi1/0/2"), octet("GigabitEthernet1/0/2"), nil,
				1, 2, uint32(500), 99, // PVID-only VLAN: not in the static name table
			}},
			{Index: []int{101}, Values: []any{
				octet("Vlan1"), octet("Vlan1"), nil,
				1, 1, nil, nil, // SVI: no PVID, never changed
			}},
		},
		static: []snmp.Varbind{
			{OID: oidVlanStaticName + ".10", Suffix: []int{10}, Value: octet("users")},
			{OID: oidVlanStaticName + ".20", Suffix: []int{20}, Value: octet("voice")},
		},
	}
}

func TestRunPollsSystemInfoInterfacesVLANs(t *testing.T) {
	h := newHarness(t, fixtureClient())

	res, err := h.p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 3 interfaces + 3 VLANs (static 10+20 plus PVID-derived 99).
	if res.ItemsFound != 6 {
		t.Errorf("ItemsFound = %d, want 6", res.ItemsFound)
	}
	if !h.p.Health().Healthy {
		t.Errorf("health = %+v", h.p.Health())
	}

	ctx := context.Background()
	var fw *string
	if err := h.pool.QueryRow(ctx,
		`SELECT firmware_version FROM network_devices WHERE name = 'fixture-sw'`).Scan(&fw); err != nil {
		t.Fatal(err)
	}
	if fw == nil || *fw != "12.2(55)SE11" {
		t.Errorf("firmware_version = %v, want 12.2(55)SE11", fw)
	}

	var ifaces, vlans int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM device_interfaces`).Scan(&ifaces); err != nil {
		t.Fatal(err)
	}
	if ifaces != 3 {
		t.Errorf("interfaces = %d, want 3", ifaces)
	}
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM device_vlans`).Scan(&vlans); err != nil {
		t.Fatal(err)
	}
	if vlans != 3 {
		t.Errorf("vlans = %d, want 3", vlans)
	}

	// Spot-check one interface row end to end.
	var oper string
	var pvid *int32
	var lastChange *time.Time
	if err := h.pool.QueryRow(ctx,
		`SELECT oper_status, pvid, last_change_at FROM device_interfaces WHERE if_index = 1`).
		Scan(&oper, &pvid, &lastChange); err != nil {
		t.Fatal(err)
	}
	if oper != "up" || pvid == nil || *pvid != 10 {
		t.Errorf("iface 1 oper/pvid = %q/%v, want up/10", oper, pvid)
	}
	wantLC := h.now.Add(-time.Duration(12345600-1000) * 10 * time.Millisecond)
	if lastChange == nil || !lastChange.Equal(wantLC) {
		t.Errorf("last_change_at = %v, want %v", lastChange, wantLC)
	}

	var failures int
	if err := h.pool.QueryRow(ctx,
		`SELECT consecutive_failures FROM network_devices WHERE name = 'fixture-sw'`).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 0 {
		t.Errorf("consecutive_failures = %d, want 0", failures)
	}
}

func TestRunDeviceFailureIsolated(t *testing.T) {
	h := newHarness(t, fixtureClient())

	// A second device whose dial always fails.
	ctx := context.Background()
	broken := h.addDevice(t, "broken-sw")
	h.failDial(broken, "connection refused")

	res, err := h.p.Run(ctx)
	if err != nil {
		t.Fatalf("Run should succeed with one healthy device: %v", err)
	}
	if res.ItemsFound != 6 {
		t.Errorf("ItemsFound = %d, want 6 (healthy device only)", res.ItemsFound)
	}

	var failures int
	var lastErr *string
	if err := h.pool.QueryRow(ctx,
		`SELECT consecutive_failures, last_error FROM network_devices WHERE name = 'broken-sw'`).
		Scan(&failures, &lastErr); err != nil {
		t.Fatal(err)
	}
	if failures != 1 || lastErr == nil {
		t.Errorf("broken-sw failures=%d err=%v, want 1/<set>", failures, lastErr)
	}
}

func TestRunAllDevicesFail(t *testing.T) {
	h := newHarness(t, nil) // no default client: every dial fails

	_, err := h.p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when every device fails")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("all 1 devices failed")) {
		t.Errorf("err = %v, want all-devices-failed message", err)
	}
	if h.p.Health().Healthy {
		t.Error("health should be unhealthy")
	}
}

func TestDeviceWithoutProfileSkipped(t *testing.T) {
	h := newHarness(t, fixtureClient())
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE network_devices SET snmp_profile_id = NULL WHERE name = 'fixture-sw'`); err != nil {
		t.Fatal(err)
	}

	res, err := h.p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsFound != 0 {
		t.Errorf("ItemsFound = %d, want 0 (skipped)", res.ItemsFound)
	}
}

func TestParseFirmwareVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Cisco IOS Software, C2960 Software (C2960-LANBASEK9-M), Version 12.2(55)SE11, RELEASE SOFTWARE (fc3)", "12.2(55)SE11"},
		{"Cisco IOS Software [Everest], Catalyst L3 Switch Software (CAT9K_IOSXE), Version 17.06.005", "17.06.005"},
		{"some unrelated banner", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := parseFirmwareVersion(c.in); got != c.want {
			t.Errorf("parseFirmwareVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildVLANsUnionSorted(t *testing.T) {
	static := []snmp.Varbind{
		{Suffix: []int{20}, Value: octet("voice")},
		{Suffix: []int{10}, Value: octet("users")},
		{Suffix: []int{1}, Value: octet("default")},
	}
	got := buildVLANs(static, map[int32]bool{99: true, 10: true})
	if len(got) != 4 {
		t.Fatalf("got %+v, want 4 vlans", got)
	}
	want := []int32{1, 10, 20, 99}
	for i := range want {
		if got[i].VlanNumber != want[i] {
			t.Fatalf("order = %+v, want %v", got, want)
		}
	}
	if got[0].Name == nil || *got[0].Name != "default" {
		t.Errorf("vlan 1 name = %v", got[0].Name)
	}
	if got[3].Name != nil {
		t.Errorf("PVID-only vlan 99 should have no name, got %v", got[3].Name)
	}
}

func TestRunBridgeMIBMACsPerVlan(t *testing.T) {
	h := newHarness(t, fixtureClient())

	// VLAN 10: one MAC learned on bridge port 1 (ifIndex 1).
	h.addVlanClient("public@10", &fakeClient{fdb: map[string][]snmp.Varbind{
		oidDot1dTpFdbPort:     {{OID: oidDot1dTpFdbPort + ".0.17.34.51.68.85", Suffix: macSuffix("00:11:22:33:44:01"), Value: 1}},
		oidDot1dBasePortIfIdx: {{Suffix: []int{1}, Value: 1}},
	}})
	// VLAN 20: one MAC on bridge port 2 (ifIndex 2, the SVI).
	h.addVlanClient("public@20", &fakeClient{fdb: map[string][]snmp.Varbind{
		oidDot1dTpFdbPort:     {{Suffix: macSuffix("00:11:22:33:44:02"), Value: 2}},
		oidDot1dBasePortIfIdx: {{Suffix: []int{2}, Value: 2}},
	}})
	// VLAN 99 (PVID-derived): no client registered -> default client
	// answers with an empty table; a legitimately empty VLAN.

	res, err := h.p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 3 interfaces + 3 VLANs + 2 MAC entries.
	if res.ItemsFound != 8 {
		t.Errorf("ItemsFound = %d, want 8", res.ItemsFound)
	}

	ctx := context.Background()
	var macs int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM mac_table_current`).Scan(&macs); err != nil {
		t.Fatal(err)
	}
	if macs != 2 {
		t.Fatalf("mac rows = %d, want 2", macs)
	}
	rows := map[int]int32{} // if_index -> vlan
	q, err := h.pool.Query(ctx, `
		SELECT i.if_index, m.vlan_number
		FROM mac_table_current m JOIN device_interfaces i ON i.id = m.interface_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	for q.Next() {
		var idx int
		var vlan int32
		if err := q.Scan(&idx, &vlan); err != nil {
			t.Fatal(err)
		}
		rows[idx] = vlan
	}
	if rows[1] != 10 || rows[2] != 20 {
		t.Errorf("mac placement = %v, want if_index 1->vlan 10 and 2->vlan 20", rows)
	}
}

func TestRunQBridgeFallback(t *testing.T) {
	c := fixtureClient()
	c.fdb = map[string][]snmp.Varbind{
		// One Q-BRIDGE entry: vlan 10, MAC ...:03, bridge port 1.
		oidDot1qTpFdbPort:     {{Suffix: append([]int{10}, macSuffix("00:11:22:33:44:03")...), Value: 1}},
		oidDot1dBasePortIfIdx: {{Suffix: []int{1}, Value: 1}},
	}
	h := newHarness(t, c)

	res, err := h.p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 3 interfaces + 3 VLANs + 1 MAC via the fallback path.
	if res.ItemsFound != 7 {
		t.Errorf("ItemsFound = %d, want 7", res.ItemsFound)
	}
	var vlan int32
	var mac string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT vlan_number, mac_address::text FROM mac_table_current`).
		Scan(&vlan, &mac); err != nil {
		t.Fatal(err)
	}
	if vlan != 10 || mac != "00:11:22:33:44:03" {
		t.Errorf("mac row = %d/%s, want 10/00:11:22:33:44:03", vlan, mac)
	}
}

func TestRunAllMACSourcesFail(t *testing.T) {
	c := fixtureClient()
	c.fdbErr = targetError("walk timeout") // fails BRIDGE-MIB and Q-BRIDGE walks
	h := newHarness(t, c)

	_, err := h.p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when every MAC source fails")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("all 1 devices failed")) {
		t.Errorf("err = %v, want all-devices-failed message", err)
	}
	var failures int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT consecutive_failures FROM network_devices WHERE name = 'fixture-sw'`).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 1 {
		t.Errorf("consecutive_failures = %d, want 1", failures)
	}
}

func TestRunNeighborsLLDPAndCDP(t *testing.T) {
	c := fixtureClient()
	// LLDP neighbor on local port 2 (ifIndex 2): chassis ID carries the
	// management address as a networkAddress (subtype 5, family 1 = IPv4).
	c.lldpRows = []snmp.TableRow{{
		Index: append([]int{0, 2}, macSuffix("aa:bb:cc:dd:ee:01")...),
		Values: []any{
			5,                       // chassisIdSubtype networkAddress
			[]byte{1, 192, 0, 2, 9}, // afi IPv4 + 192.0.2.9
			octet("Gi0/1"),          // remote port id
			octet("core-sw-01"),     // remote system name
		},
	}}
	// CDP neighbor on ifIndex 1.
	c.cdpRows = []snmp.TableRow{{
		Index: []int{1, 1},
		Values: []any{
			octet("uplink-core"),               // cdpCacheDeviceId
			octet("Te1/1/1"),                   // cdpCacheDevicePort
			[]byte{0xCC, 1, 0, 4, 10, 0, 0, 1}, // NABBPE: IPv4 10.0.0.1
		},
	}}
	h := newHarness(t, c)

	res, err := h.p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 3 interfaces + 3 VLANs + 0 MACs + 2 neighbors.
	if res.ItemsFound != 8 {
		t.Errorf("ItemsFound = %d, want 8", res.ItemsFound)
	}

	ctx := context.Background()
	type nbr struct {
		protocol, sysName, portID, mgmtIP string
		localIf                           int
	}
	got := map[string]nbr{}
	rows, err := h.pool.Query(ctx, `
		SELECT n.protocol, i.if_index,
		       coalesce(n.remote_system_name, ''), coalesce(n.remote_port_id, ''),
		       coalesce(n.remote_mgmt_ip::text, '')
		FROM neighbors_current n
		LEFT JOIN device_interfaces i ON i.id = n.local_interface_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n nbr
		if err := rows.Scan(&n.protocol, &n.localIf, &n.sysName, &n.portID, &n.mgmtIP); err != nil {
			t.Fatal(err)
		}
		got[n.protocol] = n
	}
	if len(got) != 2 {
		t.Fatalf("neighbors = %v, want lldp+cdp", got)
	}
	l := got["lldp"]
	if l.localIf != 2 || l.sysName != "core-sw-01" || l.portID != "Gi0/1" || l.mgmtIP != "192.0.2.9/32" {
		t.Errorf("lldp neighbor = %+v", l)
	}
	d := got["cdp"]
	if d.localIf != 1 || d.sysName != "uplink-core" || d.portID != "Te1/1/1" || d.mgmtIP != "10.0.0.1/32" {
		t.Errorf("cdp neighbor = %+v", d)
	}
}

func TestRunOneNeighborSourceFailsKeepsOther(t *testing.T) {
	c := fixtureClient()
	c.lldpErr = targetError("no such object")
	c.cdpRows = []snmp.TableRow{{
		Index:  []int{1, 1},
		Values: []any{octet("uplink-core"), octet("Te1/1/1"), []byte{0xCC, 1, 0, 4, 10, 0, 0, 1}},
	}}
	h := newHarness(t, c)

	if _, err := h.p.Run(context.Background()); err != nil {
		t.Fatalf("Run should succeed with one working source: %v", err)
	}
	var count int
	var proto string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*), min(protocol) FROM neighbors_current`).Scan(&count, &proto); err != nil {
		t.Fatal(err)
	}
	if count != 1 || proto != "cdp" {
		t.Errorf("neighbors = %d/%s, want 1/cdp (lldp failed, cdp kept)", count, proto)
	}
}

func TestRunBothNeighborWalksFail(t *testing.T) {
	c := fixtureClient()
	c.lldpErr = targetError("no such object")
	c.cdpErr = targetError("walk timeout")
	h := newHarness(t, c)

	_, err := h.p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when both neighbor walks fail")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("all 1 devices failed")) {
		t.Errorf("err = %v, want all-devices-failed message", err)
	}
	var failures int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT consecutive_failures FROM network_devices WHERE name = 'fixture-sw'`).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 1 {
		t.Errorf("consecutive_failures = %d, want 1", failures)
	}
}

func TestParseNeighborAddresses(t *testing.T) {
	// CDP NABBPE: non-IP protocol prefix is skipped evidence, not an error.
	if got := parseCDPAddress([]byte{0x81, 1, 0, 4, 1, 2, 3, 4}); got != nil {
		t.Errorf("non-IP cdp address parsed as %v", got)
	}
	if got := parseCDPAddress([]byte{0xCC, 1, 0}); got != nil {
		t.Errorf("truncated cdp address parsed as %v", got)
	}
	ip := parseCDPAddress([]byte{0xCC, 1, 0, 4, 10, 1, 2, 3})
	if ip == nil || ip.String() != "10.1.2.3" {
		t.Errorf("cdp address = %v", ip)
	}
	// LLDP IANA form: family 2 = IPv6.
	ip6 := parseIANAAddress(append([]byte{2}, bytes.Repeat([]byte{0x20}, 16)...))
	if ip6 == nil || ip6.String() != "2020:2020:2020:2020:2020:2020:2020:2020" {
		t.Errorf("iana ipv6 = %v", ip6)
	}
	if parseIANAAddress([]byte{9, 1, 2, 3, 4}) != nil {
		t.Error("unknown address family should yield nil")
	}
}

func TestPollDeviceByID(t *testing.T) {
	h := newHarness(t, fixtureClient())

	if err := h.p.PollDeviceByID(context.Background(), h.deviceID); err != nil {
		t.Fatalf("PollDeviceByID: %v", err)
	}
	var ifaces int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM device_interfaces WHERE device_id = $1`, h.deviceID).Scan(&ifaces); err != nil {
		t.Fatal(err)
	}
	if ifaces != 3 {
		t.Errorf("interfaces = %d, want 3", ifaces)
	}

	// Unknown and disabled ids are clear errors, not silent no-ops.
	if err := h.p.PollDeviceByID(context.Background(), 999999); err == nil {
		t.Error("unknown id: expected error")
	}
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE network_devices SET enabled = false WHERE id = $1`, h.deviceID); err != nil {
		t.Fatal(err)
	}
	if err := h.p.PollDeviceByID(context.Background(), h.deviceID); err == nil {
		t.Error("disabled device: expected error")
	}
}

func TestOctetStringPtrSanitizesAgentBytes(t *testing.T) {
	// Real CDP cache data carries raw bytes: invalid UTF-8 sequences and
	// trailing NULs are common. They must be sanitized, not rejected by
	// Postgres mid-poll.
	got := octetStringPtr([]byte{'S', 'W', '-', 0xAC, 0xB0, 0x00, 'C'})
	if got == nil {
		t.Fatal("expected a sanitized string, got nil")
	}
	if !utf8.ValidString(*got) {
		t.Errorf("sanitized string still invalid UTF-8: %q", *got)
	}
	if strings.ContainsRune(*got, '\x00') {
		t.Errorf("embedded NUL not stripped: %q", *got)
	}
	if !strings.HasSuffix(*got, "C") {
		t.Errorf("bytes after an embedded NUL lost: %q", *got)
	}
	if octetStringPtr([]byte{0x00, 0x00}) != nil {
		t.Error("all-NUL octets should yield nil")
	}
}
