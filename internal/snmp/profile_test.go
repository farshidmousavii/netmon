package snmp

// Tests for the snmp_profiles -> Config mapping (profile.go).

import (
	"strings"
	"testing"

	"github.com/gosnmp/gosnmp"
)

func TestConfigFromProfileV2C(t *testing.T) {
	cfg, err := ConfigFromProfile("10.0.0.1", Profile{
		Version:   "v2c",
		Community: "fixture-ro",
		TimeoutMS: 1500,
		Retries:   3,
	})
	if err != nil {
		t.Fatalf("ConfigFromProfile: %v", err)
	}
	if cfg.Target != "10.0.0.1" {
		t.Errorf("Target = %q", cfg.Target)
	}
	if cfg.Version != gosnmp.Version2c {
		t.Errorf("Version = %v, want v2c", cfg.Version)
	}
	if cfg.Community != "fixture-ro" {
		t.Errorf("Community = %q", cfg.Community)
	}
	if cfg.Timeout != 1500*1e6 { // 1500ms
		t.Errorf("Timeout = %v, want 1.5s", cfg.Timeout)
	}
	if cfg.Retries != 3 {
		t.Errorf("Retries = %d", cfg.Retries)
	}
	if cfg.Security != nil {
		t.Errorf("v2c profile must not carry Security config: %+v", cfg.Security)
	}
}

func TestConfigFromProfileV2CVersionCaseInsensitive(t *testing.T) {
	cfg, err := ConfigFromProfile("10.0.0.1", Profile{Version: " V2C ", Community: "pub"})
	if err != nil {
		t.Fatalf("ConfigFromProfile: %v", err)
	}
	if cfg.Version != gosnmp.Version2c {
		t.Errorf("Version = %v", cfg.Version)
	}
}

func TestConfigFromProfileV2CMissingCommunityErrors(t *testing.T) {
	_, err := ConfigFromProfile("10.0.0.1", Profile{Version: "v2c"})
	if err == nil || !strings.Contains(err.Error(), "community") {
		t.Fatalf("err = %v, want a community error", err)
	}
}

func TestConfigFromProfileV3(t *testing.T) {
	cfg, err := ConfigFromProfile("10.0.0.7", Profile{
		Version:        "v3",
		V3Username:     "bidar-ro",
		V3AuthProtocol: "SHA",
		V3AuthKey:      "authpass",
		V3PrivProtocol: "AES256C",
		V3PrivKey:      "privpass",
	})
	if err != nil {
		t.Fatalf("ConfigFromProfile: %v", err)
	}
	if cfg.Version != gosnmp.Version3 {
		t.Fatalf("Version = %v, want v3", cfg.Version)
	}
	if cfg.Community != "" {
		t.Errorf("v3 profile leaked a community: %q", cfg.Community)
	}
	if cfg.Security == nil {
		t.Fatal("Security config missing")
	}
	if cfg.Security.Username != "bidar-ro" ||
		cfg.Security.AuthProtocol != gosnmp.SHA ||
		cfg.Security.AuthKey != "authpass" ||
		cfg.Security.PrivProtocol != gosnmp.AES256C ||
		cfg.Security.PrivKey != "privpass" {
		t.Errorf("Security = %+v", cfg.Security)
	}
}

func TestConfigFromProfileV3ProtocolNamesCaseInsensitive(t *testing.T) {
	cfg, err := ConfigFromProfile("10.0.0.7", Profile{
		Version:        "V3",
		V3Username:     "u",
		V3AuthProtocol: "sha256",
		V3PrivProtocol: "aes",
	})
	if err != nil {
		t.Fatalf("ConfigFromProfile: %v", err)
	}
	if cfg.Security.AuthProtocol != gosnmp.SHA256 || cfg.Security.PrivProtocol != gosnmp.AES {
		t.Errorf("protocols = %v/%v, want SHA256/AES", cfg.Security.AuthProtocol, cfg.Security.PrivProtocol)
	}
}

func TestConfigFromProfileV3BadAuthProtocolErrors(t *testing.T) {
	_, err := ConfigFromProfile("10.0.0.7", Profile{
		Version:        "v3",
		V3Username:     "u",
		V3AuthProtocol: "ROT13",
	})
	if err == nil || !strings.Contains(err.Error(), "auth protocol") {
		t.Fatalf("err = %v, want an auth protocol error", err)
	}
}

func TestConfigFromProfileUnsupportedVersionErrors(t *testing.T) {
	for _, v := range []string{"", "v1", "snmpv3"} {
		if _, err := ConfigFromProfile("10.0.0.1", Profile{Version: v, Community: "x"}); err == nil {
			t.Errorf("version %q: expected error", v)
		}
	}
}

func TestConfigFromProfileZeroTimeoutRetriesPassThrough(t *testing.T) {
	// Zero values mean "take NewClient's defaults" — the mapping must not
	// invent its own numbers.
	cfg, err := ConfigFromProfile("10.0.0.1", Profile{Version: "v2c", Community: "pub"})
	if err != nil {
		t.Fatalf("ConfigFromProfile: %v", err)
	}
	if cfg.Timeout != 0 || cfg.Retries != 0 {
		t.Errorf("Timeout/Retries = %v/%d, want zero (NewClient defaults apply)",
			cfg.Timeout, cfg.Retries)
	}
}
