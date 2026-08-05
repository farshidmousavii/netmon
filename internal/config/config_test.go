package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	orig := `backup:
  directory: backups
  archive_path: ""
credentials:
  core_router:
    username: admin
    password: secret
devices:
- credential: core_router
  ip: 172.16.16.16
  name: ISR4331
  port: "22"
  vendor: cisco
snmp:
  community: NCESNMP
  timeout: 10
ssh:
  retry:
    initial_delay: 1
    max_attempts: 3
    max_delay: 5
    multiplier: 2.0
  timeout: 5
version: 1
`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Devices) != 1 || cfg.Devices[0].Name != "ISR4331" {
		t.Fatalf("load wrong: %+v", cfg.Devices)
	}

	// add a device and save
	cfg.Devices = append(cfg.Devices, DeviceConfig{Name: "Test-SW", IP: "10.0.0.1", Vendor: "cisco", Credential: "core_router", Port: "22"})
	if err := cfg.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	// reload and verify
	cfg2, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(cfg2.Devices) != 2 {
		t.Fatalf("want 2 devices, got %d", len(cfg2.Devices))
	}
	if cfg2.Devices[1].Name != "Test-SW" || cfg2.Devices[1].IP != "10.0.0.1" {
		t.Fatalf("new device wrong: %+v", cfg2.Devices[1])
	}
	// version preserved
	if cfg2.Version != 1 {
		t.Fatalf("version = %d, want 1", cfg2.Version)
	}
	// credentials/ssh/snmp preserved
	if cfg2.Credentials["core_router"].Password != "secret" {
		t.Fatalf("credential lost: %+v", cfg2.Credentials)
	}
	if cfg2.SNMP == nil || cfg2.SNMP.Community != "NCESNMP" {
		t.Fatalf("snmp lost: %+v", cfg2.SNMP)
	}
	if cfg2.SSH == nil || cfg2.SSH.Timeout != 5 {
		t.Fatalf("ssh lost: %+v", cfg2.SSH)
	}

	// .bak created
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("no .bak backup: %v", err)
	}
}
