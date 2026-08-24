package store

// Integration tests for the admin-CLI queries (devices list/set-role,
// dhcp-sources list/add). Gated on BIDAR_TEST_DATABASE_URL.

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/farshidmousavii/bidar/internal/testdb"
)

func newAdminStore(t *testing.T) *Store {
	t.Helper()
	pool := testdb.Open(t, testdb.ScratchURL(t, testdb.BaseURL(t)))
	return New(pool)
}

func seedDevice(t *testing.T, st *Store, name, ip, family, role string, enabled bool) {
	t.Helper()
	_, err := st.pool.Exec(context.Background(), `
		INSERT INTO network_devices (name, protocol_family, role, mgmt_ip, enabled)
		VALUES ($1, $2, $3, $4::inet, $5)`,
		name, family, role, ip, enabled)
	if err != nil {
		t.Fatalf("seed device %s: %v", name, err)
	}
}

func TestListDevicesRoleFilter(t *testing.T) {
	st := newAdminStore(t)
	seedDevice(t, st, "sw-a", "192.0.2.1", "cisco_snmp", "core", true)
	seedDevice(t, st, "sw-b", "192.0.2.2", "cisco_snmp", "access", true)
	seedDevice(t, st, "sw-c", "192.0.2.3", "mikrotik_routeros", "unassigned", false)

	all, err := st.ListDevices(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("all devices = %d, want 3", len(all))
	}
	if all[2].Enabled {
		t.Errorf("disabled device should be listed with Enabled=false")
	}

	role := "core"
	cores, err := st.ListDevices(context.Background(), &role)
	if err != nil {
		t.Fatalf("ListDevices(core): %v", err)
	}
	if len(cores) != 1 || cores[0].Name != "sw-a" {
		t.Errorf("cores = %+v, want just sw-a", cores)
	}
}

func TestSetDeviceRole(t *testing.T) {
	st := newAdminStore(t)
	seedDevice(t, st, "sw-a", "192.0.2.1", "cisco_snmp", "unassigned", true)

	ctx := context.Background()

	// By name.
	if _, err := st.SetDeviceRole(ctx, "sw-a", "core"); err != nil {
		t.Fatalf("set by name: %v", err)
	}
	// By mgmt_ip.
	if _, err := st.SetDeviceRole(ctx, "192.0.2.1", "access"); err != nil {
		t.Fatalf("set by ip: %v", err)
	}
	// Case-insensitive name.
	if _, err := st.SetDeviceRole(ctx, "SW-A", "core"); err != nil {
		t.Fatalf("set by uppercase name: %v", err)
	}

	dev, err := st.ListDevices(ctx, nil)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(dev) != 1 || dev[0].Role != "core" {
		t.Errorf("final role = %+v, want core", dev)
	}

	// Unknown device.
	if _, err := st.SetDeviceRole(ctx, "ghost", "core"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown device: err = %v, want ErrNotFound", err)
	}
	// Invalid role.
	if _, err := st.SetDeviceRole(ctx, "sw-a", "root"); err == nil {
		t.Error("expected error for invalid role")
	}
	// Ambiguous name.
	seedDevice(t, st, "dup", "192.0.2.9", "cisco_snmp", "unassigned", true)
	if _, err := st.SetDeviceRole(ctx, "dup", "core"); err != nil {
		t.Fatalf("set unique name: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO network_devices (name, protocol_family, role, mgmt_ip, enabled)
		 VALUES ('dup', 'cisco_snmp', 'unassigned', '192.0.2.10'::inet, true)`); err != nil {
		t.Fatalf("seed duplicate name: %v", err)
	}
	if _, err := st.SetDeviceRole(ctx, "dup", "core"); err == nil {
		t.Error("expected error for ambiguous name")
	}
}

func TestAddDHCPSource(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()

	id, err := st.AddDHCPSource(ctx, "src-1", "isc", []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("add isc source: %v", err)
	}
	if id == 0 {
		t.Error("expected a real id")
	}

	// Duplicate name: clear error.
	if _, err := st.AddDHCPSource(ctx, "src-1", "isc", []byte(`{}`), nil); err == nil {
		t.Error("expected error for duplicate name")
	}

	// Mikrotik with encrypted credential bytes.
	cred := []byte{0x01, 0x02, 0x03}
	if _, err := st.AddDHCPSource(ctx, "ros-2", "mikrotik", []byte(`{"host": "192.0.2.13", "username": "admin"}`), cred); err != nil {
		t.Fatalf("add mikrotik source: %v", err)
	}

	// Invalid type rejected.
	if _, err := st.AddDHCPSource(ctx, "bad", "sql", []byte(`{}`), nil); err == nil {
		t.Error("expected error for invalid source type")
	}
	// Empty name rejected.
	if _, err := st.AddDHCPSource(ctx, "  ", "isc", []byte(`{}`), nil); err == nil {
		t.Error("expected error for empty name")
	}

	sources, err := st.ListAllDHCPSources(ctx)
	if err != nil {
		t.Fatalf("ListAllDHCPSources: %v", err)
	}
	if len(sources) != 2 {
		t.Errorf("sources = %d, want 2", len(sources))
	}
}

func TestSetDeviceRouterOSAuth(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()

	seedDevice(t, st, "ros-auth-me", "192.0.2.60", "mikrotik_routeros", "unassigned", true)
	seedDevice(t, st, "cisco-nope", "192.0.2.61", "cisco_snmp", "core", true)

	// Happy path: credentials stored (caller encrypts), port untouched.
	if _, err := st.SetDeviceRouterOSAuth(ctx, "ros-auth-me", "admin", []byte{0x01}, nil); err != nil {
		t.Fatalf("set auth: %v", err)
	}
	var user string
	var pwEnc []byte
	var port *int32
	if err := st.pool.QueryRow(ctx,
		`SELECT routeros_username, routeros_password_enc, routeros_port
		 FROM network_devices WHERE name = 'ros-auth-me'`).Scan(&user, &pwEnc, &port); err != nil {
		t.Fatal(err)
	}
	if user != "admin" || len(pwEnc) == 0 {
		t.Errorf("credentials = %q/%v", user, pwEnc)
	}
	if port == nil || *port != 8728 {
		t.Errorf("port = %v, want column default 8728", port)
	}

	// Optional port update.
	p := int32(8729)
	if _, err := st.SetDeviceRouterOSAuth(ctx, "ros-auth-me", "admin2", []byte{0x02}, &p); err != nil {
		t.Fatalf("set auth with port: %v", err)
	}
	if err := st.pool.QueryRow(ctx,
		`SELECT routeros_username, routeros_port FROM network_devices WHERE name = 'ros-auth-me'`).
		Scan(&user, &port); err != nil {
		t.Fatal(err)
	}
	if user != "admin2" || port == nil || *port != 8729 {
		t.Errorf("after port update: user=%q port=%v", user, port)
	}

	// Wrong family rejected.
	if _, err := st.SetDeviceRouterOSAuth(ctx, "cisco-nope", "admin", []byte{0x01}, nil); err == nil ||
		!bytes.Contains([]byte(err.Error()), []byte("not mikrotik_routeros")) {
		t.Errorf("cisco device: err = %v, want family rejection", err)
	}

	// Ambiguous / unknown targets behave like set-role.
	seedDevice(t, st, "dup", "192.0.2.62", "mikrotik_routeros", "unassigned", true)
	st.pool.Exec(ctx, `INSERT INTO network_devices (name, protocol_family, role, mgmt_ip, enabled)
		VALUES ('dup', 'mikrotik_routeros', 'unassigned', '192.0.2.63'::inet, true)`)
	if _, err := st.SetDeviceRouterOSAuth(ctx, "dup", "a", []byte{1}, nil); err == nil {
		t.Error("expected error for ambiguous name")
	}
	if _, err := st.SetDeviceRouterOSAuth(ctx, "ghost", "a", []byte{1}, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown device: err = %v, want ErrNotFound", err)
	}

	// Empty username rejected.
	if _, err := st.SetDeviceRouterOSAuth(ctx, "ros-auth-me", "  ", []byte{1}, nil); err == nil {
		t.Error("expected error for empty username")
	}
}

func TestSetDeviceEnabled(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()

	seedDevice(t, st, "en-sw", "192.0.2.70", "cisco_snmp", "unassigned", true)

	// Disable by name.
	if _, err := st.SetDeviceEnabled(ctx, "en-sw", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	var enabled bool
	if err := st.pool.QueryRow(ctx,
		`SELECT enabled FROM network_devices WHERE name = 'en-sw'`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Error("device still enabled after disable")
	}

	// Re-enable.
	if _, err := st.SetDeviceEnabled(ctx, "en-sw", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := st.pool.QueryRow(ctx,
		`SELECT enabled FROM network_devices WHERE name = 'en-sw'`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Error("device still disabled after enable")
	}

	// Unknown device: ErrNotFound, same contract as set-role.
	if _, err := st.SetDeviceEnabled(ctx, "ghost", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown device: err = %v, want ErrNotFound", err)
	}
}
