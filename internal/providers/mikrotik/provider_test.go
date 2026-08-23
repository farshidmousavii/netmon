package mikrotik

// Tests for the Phase 2 MikroTik device-polling provider: a fake RouterOS
// client and a fake SNMP stats client feed recorded fixtures; the store
// side runs against real Postgres via the testdb harness. Gated on
// BIDAR_TEST_DATABASE_URL.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshidmousavii/bidar/internal/crypto"
	snmplib "github.com/farshidmousavii/bidar/internal/snmp"
	"github.com/farshidmousavii/bidar/internal/store"
	"github.com/farshidmousavii/bidar/internal/testdb"
)

type fakeROS struct {
	arps   []ARP
	arpErr error
	regs   []WirelessRegistration
	regErr error // legacy wireless table failure -> ROS7 wifi probe
	wifi   []map[string]string
	closed bool
}

func (f *fakeROS) ARPs(context.Context) ([]ARP, error) { return f.arps, f.arpErr }

func (f *fakeROS) WirelessRegistrations(context.Context) ([]WirelessRegistration, error) {
	return f.regs, f.regErr
}

func (f *fakeROS) RunCommand(_ context.Context, cmd string, _ map[string]string) ([]map[string]string, error) {
	if cmd == "/interface/wifi/registration-table/print" {
		return f.wifi, nil
	}
	return nil, fmt.Errorf("unknown command %s", cmd)
}

func (f *fakeROS) Close() error { f.closed = true; return nil }

type fakeStats struct {
	get    map[string]snmplib.Varbind
	ifaces []snmplib.TableRow
	err    error
}

func (f *fakeStats) Get(_ context.Context, oids ...string) ([]snmplib.Varbind, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]snmplib.Varbind, len(oids))
	for i, o := range oids {
		out[i] = f.get[o]
	}
	return out, nil
}

func (f *fakeStats) WalkTableColumns(_ context.Context, cols ...string) ([]snmplib.TableRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ifaces, nil
}

func (f *fakeStats) Close() error { return nil }

type harness struct {
	pool           *pgxpool.Pool
	st             *store.Store
	p              *Provider
	now            time.Time
	seq            int
	profileID      int64
	enc            *crypto.Encryptor
	lastDialedPort int
}

func newHarness(t *testing.T, ros *fakeROS, stats *fakeStats) *harness {
	t.Helper()
	pool := testdb.Open(t, testdb.ScratchURL(t, testdb.BaseURL(t)))
	st := store.New(pool)

	key := bytes.Repeat([]byte{0x24}, 32)
	enc, err := crypto.New(key)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}

	h := &harness{
		pool: pool,
		st:   st,
		now:  time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC),
		enc:  enc,
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
		func(_ context.Context, _ string, port int, _, _ string) (rosClient, error) {
			h.lastDialedPort = port
			return ros, nil
		},
		func(_ context.Context, _ snmplib.Config) (statsClient, error) { return stats, nil },
	)
	if err != nil {
		t.Fatalf("newWithDial: %v", err)
	}
	h.p = p
	return h
}

// addDevice seeds one enabled mikrotik_routeros device. An empty password
// leaves the RouterOS credentials unset.
func (h *harness) addDevice(t *testing.T, name, password string) {
	t.Helper()
	h.seq++
	ip := fmt.Sprintf("192.0.2.%d", 10+h.seq)
	if password != "" {
		pwEnc, err := h.enc.Encrypt([]byte(password))
		if err != nil {
			t.Fatalf("encrypt password: %v", err)
		}
		if _, err := h.pool.Exec(context.Background(), `
			INSERT INTO network_devices
			    (name, protocol_family, role, mgmt_ip, enabled, snmp_profile_id,
			     routeros_username, routeros_password_enc)
			VALUES ($1, 'mikrotik_routeros', 'unassigned', $2::inet, true, $3, 'admin', $4)`,
			name, ip, h.profileID, pwEnc); err != nil {
			t.Fatalf("seed device %s: %v", name, err)
		}
		return
	}
	if _, err := h.pool.Exec(context.Background(), `
		INSERT INTO network_devices
		    (name, protocol_family, role, mgmt_ip, enabled, snmp_profile_id)
		VALUES ($1, 'mikrotik_routeros', 'unassigned', $2::inet, true, $3)`,
		name, ip, h.profileID); err != nil {
		t.Fatalf("seed device %s: %v", name, err)
	}
}

func TestRunPollsEvidenceAndPresence(t *testing.T) {
	ros := &fakeROS{
		arps: []ARP{
			{Address: mustAddr(t, "192.0.2.60"), MAC: mustMAC(t, "02:aa:00:00:00:01"), Interface: "bridge-lan", DHCP: true},
			{Address: mustAddr(t, "192.0.2.61"), MAC: mustMAC(t, "02:aa:00:00:00:02"), Interface: "ether2"},
		},
		regs: []WirelessRegistration{
			{MAC: mustMAC(t, "02:aa:00:00:00:03"), Interface: "wlan1", LastIP: mustAddr(t, "192.0.2.70")},
		},
	}
	stats := &fakeStats{
		get: map[string]snmplib.Varbind{
			oidSysDescr: {OID: oidSysDescr, Value: []byte("RouterOS 7.14.3 (stable), MikroTik fixture")},
		},
		ifaces: []snmplib.TableRow{
			{Index: []int{1}, Values: []any{octetStr("bridge-lan"), octetStr("bridge"), nil, 1, 1, nil, nil}},
			{Index: []int{2}, Values: []any{octetStr("ether2"), octetStr("ether2"), nil, 1, 1, nil, nil}},
		},
	}
	h := newHarness(t, ros, stats)
	h.addDevice(t, "ros-hq", "ros-secret")

	res, err := h.p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 2 ARP + 1 wireless + 1 firmware + 2 interfaces.
	if res.ItemsFound != 6 {
		t.Errorf("ItemsFound = %d, want 6", res.ItemsFound)
	}

	ctx := context.Background()
	var arpRows, wifiRows int
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE source='arp'), count(*) FILTER (WHERE source='wireless_reg')
		 FROM mikrotik_leases`).Scan(&arpRows, &wifiRows); err != nil {
		t.Fatal(err)
	}
	if arpRows != 2 || wifiRows != 1 {
		t.Errorf("evidence rows arp/wifi = %d/%d, want 2/1", arpRows, wifiRows)
	}

	var hosts, obs int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM hosts`).Scan(&hosts); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM host_observations WHERE source='mikrotik'`).Scan(&obs); err != nil {
		t.Fatal(err)
	}
	if hosts != 3 || obs != 3 {
		t.Errorf("hosts/observations = %d/%d, want 3/3 (decision C: presence evidence)", hosts, obs)
	}

	var fw *string
	if err := h.pool.QueryRow(ctx,
		`SELECT firmware_version FROM network_devices WHERE name='ros-hq'`).Scan(&fw); err != nil {
		t.Fatal(err)
	}
	if fw == nil || *fw != "7.14.3" {
		t.Errorf("firmware_version = %v, want 7.14.3", fw)
	}
}

func TestWirelessFallbackToROS7Wifi(t *testing.T) {
	ros := &fakeROS{
		arps:   []ARP{{Address: mustAddr(t, "192.0.2.60"), MAC: mustMAC(t, "02:aa:00:00:00:01"), Interface: "bridge-lan"}},
		regErr: fmt.Errorf("no such command"),
		wifi: []map[string]string{
			{"id": "*W9", "interface": "wifi1", "mac-address": "02:aa:00:00:00:09", "last-ip": "192.0.2.79"},
		},
	}
	h := newHarness(t, ros, &fakeStats{})
	h.addDevice(t, "ros-v7", "secret")

	if _, err := h.p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var wifiMAC string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT mac_address::text FROM mikrotik_leases WHERE source='wireless_reg'`).
		Scan(&wifiMAC); err != nil {
		t.Fatal(err)
	}
	if wifiMAC != "02:aa:00:00:00:09" {
		t.Errorf("wireless fallback mac = %s", wifiMAC)
	}
}

func TestMissingCredentialsFailClearly(t *testing.T) {
	h := newHarness(t, &fakeROS{}, &fakeStats{})
	h.addDevice(t, "ros-nocreds", "")

	_, err := h.p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for device without RouterOS credentials")
	}
	var lastErr *string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT last_error FROM network_devices WHERE name='ros-nocreds'`).Scan(&lastErr); err != nil {
		t.Fatal(err)
	}
	if lastErr == nil || !bytes.Contains([]byte(*lastErr), []byte("no RouterOS API credentials")) {
		t.Errorf("last_error = %v, want credential guidance", lastErr)
	}
}

func TestSNMPFailureDoesNotFailPoll(t *testing.T) {
	ros := &fakeROS{
		arps: []ARP{{Address: mustAddr(t, "192.0.2.60"), MAC: mustMAC(t, "02:aa:00:00:00:01"), Interface: "bridge-lan"}},
	}
	h := newHarness(t, ros, &fakeStats{err: fmt.Errorf("snmp unreachable")})
	h.addDevice(t, "ros-snmpdown", "secret")

	res, err := h.p.Run(context.Background())
	if err != nil {
		t.Fatalf("Run should succeed when only SNMP stats fail: %v", err)
	}
	// 1 ARP + 1 empty wireless replace + 0 stats.
	if res.ItemsFound != 1 {
		t.Errorf("ItemsFound = %d, want 1", res.ItemsFound)
	}
	var failures int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT consecutive_failures FROM network_devices WHERE name='ros-snmpdown'`).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 0 {
		t.Errorf("consecutive_failures = %d, want 0", failures)
	}
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse addr %q: %v", s, err)
	}
	return addr
}

func mustMAC(t *testing.T, s string) net.HardwareAddr {
	t.Helper()
	mac, err := net.ParseMAC(s)
	if err != nil {
		t.Fatalf("parse mac %q: %v", s, err)
	}
	return mac
}

func octetStr(s string) any { return []byte(s) }

func TestDialUsesResolvedRouterOSPort(t *testing.T) {
	ros := &fakeROS{
		arps: []ARP{{Address: mustAddr(t, "192.0.2.60"), MAC: mustMAC(t, "02:aa:00:00:00:01"), Interface: "bridge-lan"}},
	}
	h := newHarness(t, ros, &fakeStats{})
	h.addDevice(t, "ros-port", "secret") // no explicit port -> coalesce default 8728

	if _, err := h.p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.lastDialedPort != 8728 {
		t.Errorf("dialed port = %d, want 8728 (column default)", h.lastDialedPort)
	}
}
