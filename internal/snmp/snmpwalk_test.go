package snmp

// Fixture-based tests for the existing SnmpWalk / SNMPInfo surface, served
// by the fake agent in fixture_agent_test.go. No live network access:
// everything runs over localhost UDP with recorded-style response fixtures.
//
// The SNMPInfo interface itself is only a declaration in this package — its
// implementations live in internal/device (out of scope for daemon-side
// code). These tests pin the behavior internal/device's monitor path
// actually relies on: SnmpWalk's per-type stringification.

import (
	"strings"
	"testing"

	"github.com/gosnmp/gosnmp"
)

// The three OIDs internal/device queries today:
//
//	1.3.6.1.2.1.1.2.0  sysObjectID
//	1.3.6.1.2.1.1.3.0  sysUpTime
//	1.3.6.1.2.1.1.5.0  sysName
const (
	oidSysObjectID = "1.3.6.1.2.1.1.2.0"
	oidSysUpTime   = "1.3.6.1.2.1.1.3.0"
	oidSysName     = "1.3.6.1.2.1.1.5.0"
)

// TestSnmpWalkSysName: OctetString value stringifies to the raw bytes.
func TestSnmpWalkSysName(t *testing.T) {
	a := newFakeAgent(t, []fixtureEntry{
		{oid: oidSysName, typ: gosnmp.OctetString, value: "core-sw1"},
	})

	got, err := snmpWalk("127.0.0.1", uint16(a.port()), []string{oidSysName}, "public", 2)
	if err != nil {
		t.Fatalf("SnmpWalk: %v", err)
	}
	if got != "core-sw1" {
		t.Errorf("SnmpWalk sysName = %q, want %q", got, "core-sw1")
	}
}

// TestSnmpWalkSysObjectID: ObjectIdentifier values come back as gosnmp
// decodes them — with a leading dot — and feed ParseVendorSNMP end to end,
// which is exactly the monitor path (device.GetVendorSNMP -> SnmpWalk ->
// ParseVendorSNMP).
func TestSnmpWalkSysObjectID(t *testing.T) {
	// Real recorded value: Cisco 3725 (CISCO-PRODUCTS-MIB, ciscoProducts 414).
	a := newFakeAgent(t, []fixtureEntry{
		{oid: oidSysObjectID, typ: gosnmp.ObjectIdentifier, value: "1.3.6.1.4.1.9.1.414"},
	})

	got, err := snmpWalk("127.0.0.1", uint16(a.port()), []string{oidSysObjectID}, "public", 2)
	if err != nil {
		t.Fatalf("SnmpWalk: %v", err)
	}
	// Pin the leading-dot form: gosnmp v1.43.2 parseObjectIdentifier
	// prepends '.', and ParseVendorSNMP must be robust to it.
	if want := ".1.3.6.1.4.1.9.1.414"; got != want {
		t.Errorf("SnmpWalk sysObjectID = %q, want %q", got, want)
	}
	if vendor := ParseVendorSNMP(got); vendor != "cisco" {
		t.Errorf("ParseVendorSNMP(%q) = %q, want %q", got, vendor, "cisco")
	}
}

// TestSnmpWalkSysUptime: TimeTicks (hundredths of a second) stringify to
// "N days, HH:MM:SS". 1,234,567 ticks = 12,345 seconds = 3h 25m 45s.
func TestSnmpWalkSysUptime(t *testing.T) {
	a := newFakeAgent(t, []fixtureEntry{
		{oid: oidSysUpTime, typ: gosnmp.TimeTicks, value: uint32(1234567)},
	})

	got, err := snmpWalk("127.0.0.1", uint16(a.port()), []string{oidSysUpTime}, "public", 2)
	if err != nil {
		t.Fatalf("SnmpWalk: %v", err)
	}
	if want := "0 days, 03:25:45"; got != want {
		t.Errorf("SnmpWalk sysUpTime = %q, want %q", got, want)
	}
}

// TestSnmpWalkInteger: bare Integer values stringify via ToBigInt.
func TestSnmpWalkInteger(t *testing.T) {
	a := newFakeAgent(t, []fixtureEntry{
		{oid: "1.3.6.1.4.1.9.9.23.1.2.1.1.1", typ: gosnmp.Integer, value: 42},
	})

	got, err := snmpWalk("127.0.0.1", uint16(a.port()), []string{"1.3.6.1.4.1.9.9.23.1.2.1.1.1"}, "public", 2)
	if err != nil {
		t.Fatalf("SnmpWalk: %v", err)
	}
	if got != "42" {
		t.Errorf("SnmpWalk integer = %q, want %q", got, "42")
	}
}

// TestSnmpWalkLastVarbindWins documents the existing multi-OID behavior:
// the loop overwrites output per variable, so only the last varbind
// survives. This is a known limitation of the legacy function (the new
// WalkTable API does not have it) — pinned here so a change to it is a
// deliberate decision, not an accident.
func TestSnmpWalkLastVarbindWins(t *testing.T) {
	a := newFakeAgent(t, []fixtureEntry{
		{oid: oidSysName, typ: gosnmp.OctetString, value: "first"},
		{oid: oidSysUpTime, typ: gosnmp.TimeTicks, value: uint32(100)},
	})

	got, err := snmpWalk("127.0.0.1", uint16(a.port()), []string{oidSysName, oidSysUpTime}, "public", 2)
	if err != nil {
		t.Fatalf("SnmpWalk: %v", err)
	}
	// 100 ticks = 1 second -> "0 days, 00:00:01"
	if want := "0 days, 00:00:01"; got != want {
		t.Errorf("SnmpWalk returned %q, want %q (last varbind wins)", got, want)
	}
}

// TestSnmpWalkConnectionError: an unreachable agent must return a wrapped
// error, not hang or panic.
func TestSnmpWalkConnectionError(t *testing.T) {
	// Port 1 is closed on virtually every host: dial fails immediately.
	_, err := snmpWalk("127.0.0.1", 1, []string{oidSysName}, "public", 2)
	if err == nil {
		t.Fatal("expected error for unreachable agent")
	}
	if !strings.Contains(err.Error(), "Can not get OID") {
		t.Errorf("error should come from SnmpWalk's Get wrapping, got: %v", err)
	}
}
