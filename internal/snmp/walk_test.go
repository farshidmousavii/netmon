package snmp

// Tests for the additive WalkTable / SNMPv3 / context API (walk.go),
// against the same fake SNMPv2c agent as the legacy SnmpWalk tests.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

// ipNetToMediaIfIndex is the OID of the IP-MIB ipNetToMediaIfIndex column
// (1.3.6.1.2.1.4.22.1.2), indexed by IP address — the Phase 1 ARP shape.
const ipNetToMediaIfIndex = "1.3.6.1.2.1.4.22.1.2"

// TestWalkTableSuffixesAndTypedValues walks an ipNetToMedia-shaped table:
// every row must come back with its IP-address suffix and a typed int.
func TestWalkTableSuffixesAndTypedValues(t *testing.T) {
	a := newFakeAgent(t, []fixtureEntry{
		{oid: ipNetToMediaIfIndex + ".10.0.0.1", typ: gosnmp.Integer, value: 2},
		{oid: ipNetToMediaIfIndex + ".10.0.0.2", typ: gosnmp.Integer, value: 2},
		{oid: ipNetToMediaIfIndex + ".10.0.0.5", typ: gosnmp.Integer, value: 4},
		{oid: ipNetToMediaIfIndex + ".10.0.0.6", typ: gosnmp.Integer, value: 4},
	})

	rows, err := WalkTable(context.Background(), Config{
		Target: "127.0.0.1", Port: uint16(a.port()), Community: "public",
	}, ipNetToMediaIfIndex)
	if err != nil {
		t.Fatalf("WalkTable: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4: %+v", len(rows), rows)
	}

	want := []struct {
		oid    string
		suffix []int
		value  int
	}{
		{ipNetToMediaIfIndex + ".10.0.0.1", []int{10, 0, 0, 1}, 2},
		{ipNetToMediaIfIndex + ".10.0.0.2", []int{10, 0, 0, 2}, 2},
		{ipNetToMediaIfIndex + ".10.0.0.5", []int{10, 0, 0, 5}, 4},
		{ipNetToMediaIfIndex + ".10.0.0.6", []int{10, 0, 0, 6}, 4},
	}
	for i, w := range want {
		got := rows[i]
		if got.OID != w.oid {
			t.Errorf("row %d: OID = %q, want %q", i, got.OID, w.oid)
		}
		if !slices.Equal(got.Suffix, w.suffix) {
			t.Errorf("row %d: suffix = %v, want %v", i, got.Suffix, w.suffix)
		}
		if got.Type != gosnmp.Integer {
			t.Errorf("row %d: type = %v, want Integer", i, got.Type)
		}
		v, ok := got.Value.(int)
		if !ok {
			t.Fatalf("row %d: value is %T, want int", i, got.Value)
		}
		if v != w.value {
			t.Errorf("row %d: value = %d, want %d", i, v, w.value)
		}
	}
}

// TestWalkTableOctetStringValues walks an ifDescr-shaped table: values
// keep gosnmp's []byte typing, no stringification.
func TestWalkTableOctetStringValues(t *testing.T) {
	const ifDescr = "1.3.6.1.2.1.2.2.1.2"
	a := newFakeAgent(t, []fixtureEntry{
		{oid: ifDescr + ".1", typ: gosnmp.OctetString, value: "Gi0/1"},
		{oid: ifDescr + ".2", typ: gosnmp.OctetString, value: "Gi0/2"},
	})

	rows, err := WalkTable(context.Background(), Config{
		Target: "127.0.0.1", Port: uint16(a.port()), Community: "public",
	}, ifDescr)
	if err != nil {
		t.Fatalf("WalkTable: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if !slices.Equal(rows[0].Suffix, []int{1}) || string(rows[0].Value.([]byte)) != "Gi0/1" {
		t.Errorf("row 0 wrong: suffix %v value %v", rows[0].Suffix, rows[0].Value)
	}
	if !slices.Equal(rows[1].Suffix, []int{2}) || string(rows[1].Value.([]byte)) != "Gi0/2" {
		t.Errorf("row 1 wrong: suffix %v value %v", rows[1].Suffix, rows[1].Value)
	}
}

// TestWalkTableEmptyTable: a walk over a table with no rows returns an
// empty slice, not an error.
func TestWalkTableEmptyTable(t *testing.T) {
	a := newFakeAgent(t, nil)

	rows, err := WalkTable(context.Background(), Config{
		Target: "127.0.0.1", Port: uint16(a.port()), Community: "public",
	}, ipNetToMediaIfIndex)
	if err != nil {
		t.Fatalf("WalkTable: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

// TestWalkTablePreCancelledContext: a cancelled context fails fast with no
// network traffic.
func TestWalkTablePreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := WalkTable(ctx, Config{Target: "127.0.0.1"}, ipNetToMediaIfIndex)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestWalkTableMidWalkCancellation: cancellation while the walk is between
// bulk round-trips aborts it. Deterministic by construction: the agent
// takes 400ms per request, MaxOids forces one varbind per round trip (so
// three entries need three requests), and the context dies at 1s — during
// the third request, whose response cannot arrive before 1.2s.
func TestWalkTableMidWalkCancellation(t *testing.T) {
	a := newFakeAgent(t, []fixtureEntry{
		{oid: ipNetToMediaIfIndex + ".10.0.0.1", typ: gosnmp.Integer, value: 2},
		{oid: ipNetToMediaIfIndex + ".10.0.0.2", typ: gosnmp.Integer, value: 2},
		{oid: ipNetToMediaIfIndex + ".10.0.0.5", typ: gosnmp.Integer, value: 4},
	})
	a.delay = 400 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel()

	_, err := WalkTable(ctx, Config{
		Target:    "127.0.0.1",
		Port:      uint16(a.port()),
		Community: "public",
		MaxOids:   1, // one varbind per round trip -> 3 round trips
		Timeout:   5 * time.Second,
	}, ipNetToMediaIfIndex)
	if err == nil {
		t.Fatal("expected error for mid-walk cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

// TestWalkTableUnreachable: a walk against a closed port returns an error.
func TestWalkTableUnreachable(t *testing.T) {
	_, err := WalkTable(context.Background(), Config{
		Target: "127.0.0.1", Port: 1, Community: "public", Timeout: time.Second,
	}, ipNetToMediaIfIndex)
	if err == nil {
		t.Fatal("expected error for unreachable agent")
	}
}

// TestNewClientV3Wiring: building a v3 client applies the USM parameters
// exactly (no live v3 agent needed — the wire protocol itself is
// gosnmp's responsibility; we own the mapping).
func TestNewClientV3Wiring(t *testing.T) {
	t.Run("auth+priv", func(t *testing.T) {
		c, err := NewClient(context.Background(), Config{
			Target:  "127.0.0.1",
			Version: gosnmp.Version3,
			Security: &V3Config{
				Username:     "monitor",
				AuthProtocol: gosnmp.SHA256,
				AuthKey:      "auth-passphrase",
				PrivProtocol: gosnmp.AES256,
				PrivKey:      "priv-passphrase",
			},
		})
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		defer c.Close()

		if c.g.SecurityModel != gosnmp.UserSecurityModel {
			t.Errorf("SecurityModel = %v, want UserSecurityModel", c.g.SecurityModel)
		}
		// gosnmp ORs in its own Reportable flag (0x4) on top of the
		// caller's flags, so compare the Auth/Priv bits with a mask.
		if c.g.MsgFlags&gosnmp.AuthPriv != gosnmp.AuthPriv {
			t.Errorf("MsgFlags = %v, want AuthPriv bits set", c.g.MsgFlags)
		}
		sp, ok := c.g.SecurityParameters.(*gosnmp.UsmSecurityParameters)
		if !ok {
			t.Fatalf("SecurityParameters is %T, want *UsmSecurityParameters", c.g.SecurityParameters)
		}
		if sp.UserName != "monitor" || sp.AuthenticationProtocol != gosnmp.SHA256 ||
			sp.AuthenticationPassphrase != "auth-passphrase" ||
			sp.PrivacyProtocol != gosnmp.AES256 || sp.PrivacyPassphrase != "priv-passphrase" {
			t.Errorf("USM parameters wrong: %+v", sp)
		}
	})

	t.Run("noauth-nopriv", func(t *testing.T) {
		c, err := NewClient(context.Background(), Config{
			Target:   "127.0.0.1",
			Version:  gosnmp.Version3,
			Security: &V3Config{Username: "anon"},
		})
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		defer c.Close()
		if c.g.MsgFlags&gosnmp.NoAuthNoPriv != gosnmp.NoAuthNoPriv {
			t.Errorf("MsgFlags = %v, want NoAuthNoPriv bits set", c.g.MsgFlags)
		}
	})

	t.Run("auth-only", func(t *testing.T) {
		c, err := NewClient(context.Background(), Config{
			Target:  "127.0.0.1",
			Version: gosnmp.Version3,
			Security: &V3Config{
				Username: "monitor", AuthProtocol: gosnmp.SHA, AuthKey: "k",
			},
		})
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		defer c.Close()
		if c.g.MsgFlags&gosnmp.AuthNoPriv != gosnmp.AuthNoPriv {
			t.Errorf("MsgFlags = %v, want AuthNoPriv bits set", c.g.MsgFlags)
		}
	})

	t.Run("v3 without security rejected", func(t *testing.T) {
		if _, err := NewClient(context.Background(), Config{Target: "127.0.0.1", Version: gosnmp.Version3}); err == nil {
			t.Fatal("expected error for v3 without Security config")
		}
	})

	t.Run("privacy without auth rejected", func(t *testing.T) {
		_, err := NewClient(context.Background(), Config{
			Target: "127.0.0.1", Version: gosnmp.Version3,
			Security: &V3Config{Username: "u", PrivProtocol: gosnmp.AES, PrivKey: "k"},
		})
		if err == nil {
			t.Fatal("expected error for privacy without auth")
		}
	})

	t.Run("security for v2c rejected", func(t *testing.T) {
		_, err := NewClient(context.Background(), Config{
			Target: "127.0.0.1", Version: gosnmp.Version2c,
			Security: &V3Config{Username: "u"},
		})
		if err == nil {
			t.Fatal("expected error for Security config on v2c")
		}
	})

	t.Run("empty target rejected", func(t *testing.T) {
		if _, err := NewClient(context.Background(), Config{}); err == nil {
			t.Fatal("expected error for empty target")
		}
	})
}

func TestParseAuthProtocol(t *testing.T) {
	valid := map[string]gosnmp.SnmpV3AuthProtocol{
		"":       gosnmp.NoAuth,
		"NOAUTH": gosnmp.NoAuth,
		"none":   gosnmp.NoAuth,
		"MD5":    gosnmp.MD5,
		"sha":    gosnmp.SHA,
		"SHA224": gosnmp.SHA224,
		"SHA256": gosnmp.SHA256,
		"SHA384": gosnmp.SHA384,
		"SHA512": gosnmp.SHA512,
	}
	for in, want := range valid {
		got, err := ParseAuthProtocol(in)
		if err != nil {
			t.Errorf("ParseAuthProtocol(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseAuthProtocol(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParseAuthProtocol("BOGUS"); err == nil {
		t.Error("expected error for unknown auth protocol")
	}
}

func TestParsePrivProtocol(t *testing.T) {
	valid := map[string]gosnmp.SnmpV3PrivProtocol{
		"":        gosnmp.NoPriv,
		"NOPRIV":  gosnmp.NoPriv,
		"none":    gosnmp.NoPriv,
		"DES":     gosnmp.DES,
		"aes":     gosnmp.AES,
		"AES192":  gosnmp.AES192,
		"AES192C": gosnmp.AES192C,
		"AES256":  gosnmp.AES256,
		"AES256C": gosnmp.AES256C,
	}
	for in, want := range valid {
		got, err := ParsePrivProtocol(in)
		if err != nil {
			t.Errorf("ParsePrivProtocol(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParsePrivProtocol(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParsePrivProtocol("BOGUS"); err == nil {
		t.Error("expected error for unknown priv protocol")
	}
}
