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
	"testing"
	"time"

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
	return nil, nil
}

func (f *fakeClient) WalkTableColumns(_ context.Context, cols ...string) ([]snmp.TableRow, error) {
	return f.columns, nil
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
	// defaultClient answers any dial without an explicit override;
	// dialErr routes specific targets to failure instead.
	defaultClient snmpClient
	profileID     int64
	dialErr       map[string]error
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
		pool:          pool,
		st:            st,
		now:           time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC),
		defaultClient: defaultClient,
		dialErr:       map[string]error{},
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
// (the dial target).
func (h *harness) addDevice(t *testing.T, name string) string {
	t.Helper()
	h.seq++
	ip := fmt.Sprintf("192.0.2.%d", 10+h.seq)
	if _, err := h.pool.Exec(context.Background(), `
		INSERT INTO network_devices (name, protocol_family, role, mgmt_ip, enabled, snmp_profile_id)
		VALUES ($1, 'cisco_snmp', 'unassigned', $2::inet, true, $3)`,
		name, ip, h.profileID); err != nil {
		t.Fatalf("seed device %s: %v", name, err)
	}
	return ip
}

// failDial makes every connection to target fail.
func (h *harness) failDial(target, why string) {
	h.dialErr[target] = targetError(why)
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
