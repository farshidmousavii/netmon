package snmp

// Tests for the scalar GET API (get.go), against the same fake SNMPv2c
// agent as the walk tests — it already answers GetRequest PDUs.

import (
	"context"
	"testing"

	"github.com/gosnmp/gosnmp"
)

// sysDescr.0 / sysUpTime.0 / sysName.0 — the classic system-info scalars
// the Phase 2 SNMP provider polls first.
const (
	sysDescrOid  = "1.3.6.1.2.1.1.1.0"
	sysUpTimeOid = "1.3.6.1.2.1.1.3.0"
	sysNameOid   = "1.3.6.1.2.1.1.5.0"
)

func TestGetScalarsInRequestOrder(t *testing.T) {
	a := newFakeAgent(t, []fixtureEntry{
		{oid: sysDescrOid, typ: gosnmp.OctetString, value: "Cisco IOS Software, fixture"},
		{oid: sysUpTimeOid, typ: gosnmp.TimeTicks, value: uint32(123456700)},
		{oid: sysNameOid, typ: gosnmp.OctetString, value: "fixture-sw-01"},
	})

	c, err := NewClient(context.Background(), Config{
		Target: "127.0.0.1", Port: uint16(a.port()), Community: "public",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	got, err := c.Get(context.Background(), sysDescrOid, sysUpTimeOid, sysNameOid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d varbinds, want 3: %+v", len(got), got)
	}

	// gosnmp decodes OctetString as []byte — same convention as WalkTable.
	if got[0].OID != sysDescrOid || string(got[0].Value.([]byte)) != "Cisco IOS Software, fixture" {
		t.Errorf("varbind 0 = %+v, want %s OctetString", got[0], sysDescrOid)
	}
	if got[0].Suffix != nil {
		t.Errorf("scalar Suffix = %v, want nil", got[0].Suffix)
	}
	if got[1].OID != sysUpTimeOid || got[1].Value != uint32(123456700) {
		t.Errorf("varbind 1 = %+v, want %s TimeTicks 123456700", got[1], sysUpTimeOid)
	}
	if got[2].OID != sysNameOid || string(got[2].Value.([]byte)) != "fixture-sw-01" {
		t.Errorf("varbind 2 = %+v, want %s OctetString", got[2], sysNameOid)
	}
}

func TestGetMissingOIDHasNoValue(t *testing.T) {
	a := newFakeAgent(t, []fixtureEntry{
		{oid: sysNameOid, typ: gosnmp.OctetString, value: "fixture-sw-01"},
	})

	c, err := NewClient(context.Background(), Config{
		Target: "127.0.0.1", Port: uint16(a.port()), Community: "public",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	got, err := c.Get(context.Background(), sysNameOid, sysDescrOid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d varbinds, want 2", len(got))
	}
	if got[0].Value == nil {
		t.Errorf("served OID came back without a value: %+v", got[0])
	}
	// The unserved OID must come back as a no-value varbind (Null /
	// EndOfMibView family), not an error — missing evidence, caller decides.
	if got[1].Value != nil {
		t.Errorf("unserved OID returned a value: %+v", got[1])
	}
}

func TestGetNoOidsErrors(t *testing.T) {
	a := newFakeAgent(t, nil)
	c, err := NewClient(context.Background(), Config{
		Target: "127.0.0.1", Port: uint16(a.port()), Community: "public",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	if _, err := c.Get(context.Background()); err == nil {
		t.Fatal("expected error for empty oid list")
	}
}

func TestGetPreCancelledContext(t *testing.T) {
	a := newFakeAgent(t, []fixtureEntry{{oid: sysNameOid, typ: gosnmp.OctetString, value: "x"}})
	c, err := NewClient(context.Background(), Config{
		Target: "127.0.0.1", Port: uint16(a.port()), Community: "public",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Get(ctx, sysNameOid); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
