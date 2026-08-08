package store

// Integration tests for the admin-CLI queries (devices list/set-role,
// dhcp-sources list/set-path). Gated on BIDAR_TEST_DATABASE_URL.

import (
	"context"
	"encoding/json"
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

func TestSetDHCPSourcePath(t *testing.T) {
	st := newAdminStore(t)
	ctx := context.Background()

	seedSource := func(name, sourceType, config string) {
		t.Helper()
		if _, err := st.pool.Exec(ctx, `
			INSERT INTO dhcp_sources (name, source_type, connection_config)
			VALUES ($1, $2, $3::jsonb)`, name, sourceType, config); err != nil {
			t.Fatalf("seed source %s: %v", name, err)
		}
	}
	seedSource("win-1", "windows", `{"host": "192.0.2.11"}`)
	seedSource("ros-1", "mikrotik", `{"host": "192.0.2.12", "username": "admin"}`)

	// Set path on windows: host key must survive (jsonb merge).
	if _, err := st.SetDHCPSourcePath(ctx, "win-1", "/mnt/dhcp/leases.json"); err != nil {
		t.Fatalf("set path: %v", err)
	}
	sources, err := st.ListAllDHCPSources(ctx)
	if err != nil {
		t.Fatalf("ListAllDHCPSources: %v", err)
	}
	var winCfg map[string]any
	for _, src := range sources {
		if src.Name == "win-1" {
			if err := json.Unmarshal(src.ConnectionConfig, &winCfg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			break
		}
	}
	if winCfg == nil {
		t.Fatal("win-1 not found in list")
	}
	if winCfg["path"] != "/mnt/dhcp/leases.json" {
		t.Errorf("path = %v, want /mnt/dhcp/leases.json", winCfg["path"])
	}
	if winCfg["host"] != "192.0.2.11" {
		t.Errorf("host lost in merge: %v", winCfg["host"])
	}

	// Non-windows source: clear error, not a silent no-op.
	if _, err := st.SetDHCPSourcePath(ctx, "ros-1", "/tmp/x.json"); err == nil {
		t.Error("expected error for non-windows source")
	}

	// Unknown source.
	if _, err := st.SetDHCPSourcePath(ctx, "ghost", "/tmp/x.json"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown source: err = %v, want ErrNotFound", err)
	}
}
