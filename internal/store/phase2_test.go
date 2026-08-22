package store

// Integration tests for the Phase 2 store layer: inventory upserts,
// device poll health, and the discovery_jobs queue. Gated on
// BIDAR_TEST_DATABASE_URL.

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farshidmousavii/bidar/internal/domain"
)

func strPtr(s string) *string { return &s }
func int32Ptr(i int32) *int32 { return &i }
func int64Ptr(i int64) *int64 { return &i }
func mustMACAddr(t *testing.T, s string) net.HardwareAddr {
	t.Helper()
	mac, err := net.ParseMAC(s)
	if err != nil {
		t.Fatalf("parse mac %q: %v", s, err)
	}
	return mac
}

// seedDeviceRow inserts a device and returns its id. IPs are unique per
// call (migration 0002's uq_network_devices_mgmt_ip).
var seedDeviceSeq atomic.Int64

func seedDeviceRow(t *testing.T, st *Store, name, family string) int64 {
	t.Helper()
	n := seedDeviceSeq.Add(1)
	var id int64
	if err := st.pool.QueryRow(context.Background(), `
		INSERT INTO network_devices (name, protocol_family, role, mgmt_ip, enabled)
		VALUES ($1, $2, 'unassigned', $3::inet, true)
		RETURNING id`, name, family,
		netip.MustParseAddr("192.0.2."+
			strconv.Itoa(int(n%200)+1)).String()).Scan(&id); err != nil {
		t.Fatalf("seed device %s: %v", name, err)
	}
	return id
}

func TestUpsertDeviceInterfaces(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()
	dev := seedDeviceRow(t, st, "if-sw", "cisco_snmp")
	now1 := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)

	ifaces := []domain.DeviceInterface{
		{IfIndex: 1, IfName: strPtr("Gi1/0/1"), IfDesc: strPtr("ap"), MAC: nil,
			AdminStatus: "up", OperStatus: "up", PVID: int32Ptr(10)},
		{IfIndex: 2, IfName: strPtr("Gi1/0/2"), AdminStatus: "up", OperStatus: "down"},
	}
	ids1, err := st.UpsertDeviceInterfaces(ctx, dev, ifaces, now1)
	if err != nil {
		t.Fatalf("upsert interfaces: %v", err)
	}
	if len(ids1) != 2 || ids1[1] == 0 || ids1[2] == 0 {
		t.Fatalf("ids = %v", ids1)
	}

	// Correlation owns port_role; polling must never clobber it.
	if _, err := st.pool.Exec(ctx,
		`UPDATE device_interfaces SET port_role = 'access' WHERE id = $1`, ids1[1]); err != nil {
		t.Fatal(err)
	}

	now2 := now1.Add(time.Minute)
	ifaces[0].OperStatus = "down" // changed
	ifaces[0].PVID = int32Ptr(20)
	ifaces = append(ifaces, domain.DeviceInterface{IfIndex: 24, IfName: strPtr("Te1/1/4"), AdminStatus: "up", OperStatus: "up"})
	ids2, err := st.UpsertDeviceInterfaces(ctx, dev, ifaces, now2)
	if err != nil {
		t.Fatalf("re-upsert interfaces: %v", err)
	}

	// Existing rows keep their id across syncs (FKs depend on it).
	if ids2[1] != ids1[1] || ids2[2] != ids1[2] {
		t.Errorf("interface ids not stable: %v vs %v", ids1, ids2)
	}

	var oper string
	var role string
	var lastSeen time.Time
	if err := st.pool.QueryRow(ctx,
		`SELECT oper_status, port_role, last_seen_at FROM device_interfaces WHERE id = $1`,
		ids2[1]).Scan(&oper, &role, &lastSeen); err != nil {
		t.Fatal(err)
	}
	if oper != "down" {
		t.Errorf("oper_status = %q, want down", oper)
	}
	if role != "access" {
		t.Errorf("port_role = %q, polling must not touch it", role)
	}
	if !lastSeen.Equal(now2) {
		t.Errorf("last_seen_at = %v, want %v", lastSeen, now2)
	}
}

func TestUpsertDeviceVLANs(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()
	dev := seedDeviceRow(t, st, "vlan-sw", "cisco_snmp")
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)

	vlans := []domain.DeviceVLAN{
		{VlanNumber: 10, Name: strPtr("users")},
		{VlanNumber: 20, Name: strPtr("voice")},
	}
	if err := st.UpsertDeviceVLANs(ctx, dev, vlans, now); err != nil {
		t.Fatalf("upsert vlans: %v", err)
	}
	// Rename one, keep the other.
	if err := st.UpsertDeviceVLANs(ctx, dev,
		[]domain.DeviceVLAN{{VlanNumber: 10, Name: strPtr("users-renamed")}}, now); err != nil {
		t.Fatalf("re-upsert vlans: %v", err)
	}

	var count int
	var name *string
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*) FROM device_vlans WHERE device_id = $1`, dev).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("vlans = %d, want 2 (no duplicates)", count)
	}
	if err := st.pool.QueryRow(ctx,
		`SELECT name FROM device_vlans WHERE device_id = $1 AND vlan_number = 10`, dev).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name == nil || *name != "users-renamed" {
		t.Errorf("vlan 10 name = %v, want users-renamed", name)
	}
}

func TestSyncDeviceMACTable(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()
	dev := seedDeviceRow(t, st, "mac-sw", "cisco_snmp")

	ids, err := st.UpsertDeviceInterfaces(ctx, dev, []domain.DeviceInterface{
		{IfIndex: 1, AdminStatus: "up", OperStatus: "up"},
		{IfIndex: 2, AdminStatus: "up", OperStatus: "up"},
	}, time.Now())
	if err != nil {
		t.Fatalf("seed interfaces: %v", err)
	}

	macA := mustMACAddr(t, "00:11:22:33:44:01")
	macB := mustMACAddr(t, "00:11:22:33:44:02")
	vlan10 := int32(10)
	now1 := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)

	syncA := func(at time.Time, ifaceID *int64) error {
		return st.SyncDeviceMACTable(ctx, dev, []domain.MACTableEntry{
			{InterfaceID: ifaceID, VLANNumber: &vlan10, MAC: macA},
			{InterfaceID: int64Ptr(ids[2]), VLANNumber: &vlan10, MAC: macB},
		}, at)
	}

	// First sync: both MACs are transitions -> history rows.
	if err := syncA(now1, int64Ptr(ids[1])); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	countHistory := func() int {
		t.Helper()
		var n int
		if err := st.pool.QueryRow(ctx,
			`SELECT count(*) FROM mac_table_history WHERE device_id = $1`, dev).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := countHistory(); n != 2 {
		t.Fatalf("history after first sync = %d, want 2", n)
	}

	// Second sync, unchanged: current refreshes, history stays put.
	now2 := now1.Add(5 * time.Minute)
	if err := syncA(now2, int64Ptr(ids[1])); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if n := countHistory(); n != 2 {
		t.Errorf("history after unchanged sync = %d, want 2", n)
	}
	var firstSeen, lastSeen time.Time
	if err := st.pool.QueryRow(ctx,
		`SELECT first_seen_at, last_seen_at FROM mac_table_current
		 WHERE device_id = $1 AND mac_address = $2`, dev, macA).
		Scan(&firstSeen, &lastSeen); err != nil {
		t.Fatal(err)
	}
	if !firstSeen.Equal(now1) || !lastSeen.Equal(now2) {
		t.Errorf("first/last = %v/%v, want %v/%v", firstSeen, lastSeen, now1, now2)
	}

	// Third sync: macA moved ports -> exactly one new history row.
	now3 := now2.Add(5 * time.Minute)
	if err := syncA(now3, int64Ptr(ids[2])); err != nil {
		t.Fatalf("sync 3: %v", err)
	}
	if n := countHistory(); n != 3 {
		t.Errorf("history after move = %d, want 3", n)
	}
	var curIface *int64
	if err := st.pool.QueryRow(ctx,
		`SELECT interface_id FROM mac_table_current
		 WHERE device_id = $1 AND mac_address = $2`, dev, macA).Scan(&curIface); err != nil {
		t.Fatal(err)
	}
	if curIface == nil || *curIface != ids[2] {
		t.Errorf("macA interface = %v, want %d", curIface, ids[2])
	}
}

func TestReplaceDeviceNeighbors(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()
	dev := seedDeviceRow(t, st, "nbr-sw", "cisco_snmp")
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)

	run := func(name string) error {
		return st.ReplaceDeviceNeighbors(ctx, dev, []domain.Neighbor{
			{Protocol: "cdp", RemoteSystemName: strPtr(name), RemotePortID: strPtr("Gi0/1")},
		}, now)
	}
	if err := run("old-core"); err != nil {
		t.Fatalf("replace 1: %v", err)
	}
	if err := run("new-core"); err != nil {
		t.Fatalf("replace 2: %v", err)
	}

	var count int
	var sysName *string
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*), min(remote_system_name) FROM neighbors_current WHERE device_id = $1`, dev).
		Scan(&count, &sysName); err != nil {
		t.Fatal(err)
	}
	if count != 1 || sysName == nil || *sysName != "new-core" {
		t.Errorf("neighbors = %d/%v, want 1/new-core (full replacement)", count, sysName)
	}
}

func TestReplaceMikrotikEvidencePerSource(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()
	dev := seedDeviceRow(t, st, "ros-1", "mikrotik_routeros")
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	mac := mustMACAddr(t, "00:11:22:33:44:03")

	// dhcp_lease snapshot + an arp row.
	if err := st.ReplaceMikrotikEvidence(ctx, dev, "dhcp_lease", []domain.MikrotikEvidence{
		{MAC: mac, IP: ipPtr("192.0.2.50"), Hostname: strPtr("pc-one")},
		{MAC: mustMACAddr(t, "00:11:22:33:44:04"), IP: ipPtr("192.0.2.51")},
	}, now); err != nil {
		t.Fatalf("dhcp sync: %v", err)
	}
	if err := st.ReplaceMikrotikEvidence(ctx, dev, "arp", []domain.MikrotikEvidence{
		{MAC: mac, IP: ipPtr("192.0.2.50")},
	}, now); err != nil {
		t.Fatalf("arp sync: %v", err)
	}

	// New dhcp snapshot replaces only dhcp rows.
	if err := st.ReplaceMikrotikEvidence(ctx, dev, "dhcp_lease", []domain.MikrotikEvidence{
		{MAC: mac, IP: ipPtr("192.0.2.99")},
	}, now.Add(time.Minute)); err != nil {
		t.Fatalf("dhcp re-sync: %v", err)
	}

	var dhcpCount, arpCount int
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE source='dhcp_lease'), count(*) FILTER (WHERE source='arp')
		 FROM mikrotik_leases WHERE device_id = $1`, dev).Scan(&dhcpCount, &arpCount); err != nil {
		t.Fatal(err)
	}
	if dhcpCount != 1 || arpCount != 1 {
		t.Errorf("dhcp/arp counts = %d/%d, want 1/1 (per-source replacement)", dhcpCount, arpCount)
	}
}

func ipPtr(s string) *netip.Addr {
	ip := netip.MustParseAddr(s)
	return &ip
}

func TestUpdateDevicePollHealth(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()
	dev := seedDeviceRow(t, st, "health-sw", "cisco_snmp")
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)

	// Two failures.
	for i := range 2 {
		msg := "timeout"
		if err := st.UpdateDevicePollHealth(ctx, dev, &msg, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("failure %d: %v", i, err)
		}
	}
	var failures int
	var lastErr *string
	var lastSeen *time.Time
	if err := st.pool.QueryRow(ctx,
		`SELECT consecutive_failures, last_error, last_seen_at FROM network_devices WHERE id = $1`, dev).
		Scan(&failures, &lastErr, &lastSeen); err != nil {
		t.Fatal(err)
	}
	if failures != 2 || lastErr == nil || *lastErr != "timeout" || lastSeen != nil {
		t.Errorf("after failures: failures=%d err=%v seen=%v", failures, lastErr, lastSeen)
	}

	// Success resets everything.
	if err := st.UpdateDevicePollHealth(ctx, dev, nil, now.Add(time.Minute)); err != nil {
		t.Fatalf("success: %v", err)
	}
	if err := st.pool.QueryRow(ctx,
		`SELECT consecutive_failures, last_error, last_seen_at FROM network_devices WHERE id = $1`, dev).
		Scan(&failures, &lastErr, &lastSeen); err != nil {
		t.Fatal(err)
	}
	if failures != 0 || lastErr != nil || lastSeen == nil {
		t.Errorf("after success: failures=%d err=%v seen=%v", failures, lastErr, lastSeen)
	}
}

func TestListEnabledDevicesByFamily(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()

	ciscoOn := seedDeviceRow(t, st, "fam-cisco-on", "cisco_snmp")
	seedDeviceRow(t, st, "fam-cisco-off", "cisco_snmp") // disabled below
	seedDeviceRow(t, st, "fam-ros-on", "mikrotik_routeros")
	if _, err := st.pool.Exec(ctx,
		`UPDATE network_devices SET enabled = false WHERE name = 'fam-cisco-off'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(ctx,
		`UPDATE network_devices SET poll_interval_sec = 300 WHERE id = $1`, ciscoOn); err != nil {
		t.Fatal(err)
	}

	devices, err := st.ListEnabledDevicesByFamily(ctx, "cisco_snmp")
	if err != nil {
		t.Fatalf("list by family: %v", err)
	}
	var names []string
	intervalOK := false
	for _, d := range devices {
		names = append(names, d.Name)
		if d.ID == ciscoOn && d.PollIntervalSec == 300 {
			intervalOK = true
		}
	}
	if len(names) != 1 || names[0] != "fam-cisco-on" {
		t.Errorf("devices = %v, want [fam-cisco-on]", names)
	}
	if !intervalOK {
		t.Error("poll_interval_sec not scanned into Device")
	}
}

func TestDiscoveryJobLifecycle(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()
	dev := seedDeviceRow(t, st, "job-dev", "cisco_snmp")
	base := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)

	id, enqueued, err := st.EnqueueJob(ctx, "snmp", "device", &dev, base)
	if err != nil || !enqueued || id == 0 {
		t.Fatalf("enqueue: id=%d enqueued=%v err=%v", id, enqueued, err)
	}

	// Duplicate enqueue while active is a no-op.
	if _, enqueuedAgain, err := st.EnqueueJob(ctx, "snmp", "device", &dev, base); err != nil || enqueuedAgain {
		t.Fatalf("duplicate enqueue: enqueued=%v err=%v", enqueuedAgain, err)
	}

	// Claim leases the job.
	j, err := st.ClaimDueJob(ctx, "worker-1", 5*time.Minute, base)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if j.ID != id || j.Status != "running" || j.Attempt != 1 || j.LeaseOwner == nil || *j.LeaseOwner != "worker-1" {
		t.Fatalf("claimed job = %+v", j)
	}

	// A leased job is not claimable by anyone else.
	if _, err := st.ClaimDueJob(ctx, "worker-2", 5*time.Minute, base.Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second claim err = %v, want ErrNotFound", err)
	}

	// Failure requeues with backoff and keeps the attempt count.
	retryAt := base.Add(2 * time.Minute)
	if err := st.FailJob(ctx, id, "worker-1", "fixture failure", retryAt); err != nil {
		t.Fatalf("fail job: %v", err)
	}
	var status, errMsg string
	var scheduledAt time.Time
	if err := st.pool.QueryRow(ctx,
		`SELECT status, error_message, scheduled_at FROM discovery_jobs WHERE id = $1`, id).
		Scan(&status, &errMsg, &scheduledAt); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || errMsg != "fixture failure" || !scheduledAt.Equal(retryAt) {
		t.Errorf("failed job = %s/%s/%v", status, errMsg, scheduledAt)
	}

	// Not due before retryAt...
	if _, err := st.ClaimDueJob(ctx, "worker-2", 5*time.Minute, retryAt.Add(-time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("early claim err = %v, want ErrNotFound", err)
	}
	// ...due after retryAt, with the incremented attempt.
	j2, err := st.ClaimDueJob(ctx, "worker-2", 5*time.Minute, retryAt)
	if err != nil {
		t.Fatalf("retry claim: %v", err)
	}
	if j2.ID != id || j2.Attempt != 2 {
		t.Errorf("retry claim = %+v, want same id, attempt 2", j2)
	}

	// Success closes it out.
	if err := st.CompleteJob(ctx, id, "worker-2", retryAt.Add(time.Minute)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	var fin string
	if err := st.pool.QueryRow(ctx,
		`SELECT status FROM discovery_jobs WHERE id = $1`, id).Scan(&fin); err != nil {
		t.Fatal(err)
	}
	if fin != "succeeded" {
		t.Errorf("status = %s, want succeeded", fin)
	}

	// An expired lease makes a stuck running job claimable again.
	if _, enqueued, err := st.EnqueueJob(ctx, "snmp", "device", &dev, base); err != nil || !enqueued {
		t.Fatalf("re-enqueue: enqueued=%v err=%v", enqueued, err)
	}
	stuck, err := st.ClaimDueJob(ctx, "worker-x", time.Hour, base)
	if err != nil {
		t.Fatalf("stuck claim: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`UPDATE discovery_jobs SET lease_expires_at = $2 WHERE id = $1`, stuck.ID, base.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := st.ClaimDueJob(ctx, "worker-y", 5*time.Minute, base)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if reclaimed.ID != stuck.ID {
		t.Errorf("reclaimed job %d, want %d", reclaimed.ID, stuck.ID)
	}
}

func TestGetSNMPProfileV3Fields(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()

	var pid int64
	if err := st.pool.QueryRow(ctx, `
		INSERT INTO snmp_profiles (name, version, community_encrypted,
		    v3_username, v3_auth_protocol, v3_auth_key_encrypted,
		    v3_priv_protocol, v3_priv_key_encrypted, timeout_ms, retries)
		VALUES ('v3-fixture', 'v3', NULL,
		    'bidar-ro', 'SHA', '\x010203', 'AES256', '\x040506', 2500, 4)
		RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	p, err := st.GetSNMPProfile(ctx, pid)
	if err != nil {
		t.Fatalf("GetSNMPProfile: %v", err)
	}
	if p.Version != "v3" || p.V3Username != "bidar-ro" ||
		p.V3AuthProtocol != "SHA" || string(p.V3AuthKeyEncrypted) != "\x01\x02\x03" ||
		p.V3PrivProtocol != "AES256" || string(p.V3PrivKeyEncrypted) != "\x04\x05\x06" ||
		p.TimeoutMS != 2500 || p.Retries != 4 {
		t.Errorf("profile = %+v", p)
	}
}
