package ad

import (
	"encoding/binary"
	"strconv"
	"testing"
	"time"
)

func TestParseGUID(t *testing.T) {
	// Real AD objectGUID wire format: first three groups little-endian.
	// Target: 7ab2e864-1c5e-4ada-9c2f-8b2f4fe8201f
	raw := []byte{
		0x64, 0xe8, 0xb2, 0x7a, // LE of 0x7ab2e864
		0x5e, 0x1c, // LE of 0x1c5e
		0xda, 0x4a, // LE of 0x4ada
		0x9c, 0x2f, 0x8b, 0x2f, 0x4f, 0xe8, 0x20, 0x1f,
	}
	got, err := parseGUID(raw)
	if err != nil {
		t.Fatalf("parseGUID: %v", err)
	}
	if want := "7ab2e864-1c5e-4ada-9c2f-8b2f4fe8201f"; got != want {
		t.Errorf("parseGUID = %q, want %q", got, want)
	}

	if _, err := parseGUID([]byte{1, 2, 3}); err == nil {
		t.Error("expected error for short objectGUID")
	}
}

func TestParseSID(t *testing.T) {
	t.Run("domain computer SID", func(t *testing.T) {
		// S-1-5-21-397955417-626881126-188441444-512
		subs := []uint32{21, 397955417, 626881126, 188441444, 512}
		raw := make([]byte, 8+len(subs)*4)
		raw[0], raw[1] = 1, byte(len(subs))
		copy(raw[2:8], []byte{0, 0, 0, 0, 0, 5}) // authority 5 (NT)
		for i, s := range subs {
			binary.LittleEndian.PutUint32(raw[8+i*4:], s)
		}
		got, err := parseSID(raw)
		if err != nil {
			t.Fatalf("parseSID: %v", err)
		}
		if want := "S-1-5-21-397955417-626881126-188441444-512"; got != want {
			t.Errorf("parseSID = %q, want %q", got, want)
		}
	})

	t.Run("local system SID", func(t *testing.T) {
		got, err := parseSID([]byte{1, 1, 0, 0, 0, 0, 0, 5, 18, 0, 0, 0})
		if err != nil {
			t.Fatalf("parseSID: %v", err)
		}
		if want := "S-1-5-18"; got != want {
			t.Errorf("parseSID = %q, want %q", got, want)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		if _, err := parseSID([]byte{1, 5, 0, 0, 0, 0, 0, 5}); err == nil {
			t.Error("expected error for length/sub-authority mismatch")
		}
		if _, err := parseSID([]byte{1}); err == nil {
			t.Error("expected error for truncated SID")
		}
	})
}

func TestParseFileTime(t *testing.T) {
	t.Run("reference value", func(t *testing.T) {
		// Windows FILETIME for 2023-01-01T00:00:00Z, computed from the
		// epoch offset independently of the parser.
		want := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
		ticks := (want.Unix() + 11644473600) * 10_000_000
		got, err := parseFileTime(strconv.FormatInt(ticks, 10))
		if err != nil {
			t.Fatalf("parseFileTime: %v", err)
		}
		if got == nil || !got.Equal(want) {
			t.Errorf("parseFileTime = %v, want %v", got, want)
		}
	})

	t.Run("empty means never logged on", func(t *testing.T) {
		got, err := parseFileTime("")
		if err != nil {
			t.Fatalf("parseFileTime: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil for empty value, got %v", got)
		}
	})

	t.Run("garbage rejected", func(t *testing.T) {
		if _, err := parseFileTime("not-a-number"); err == nil {
			t.Error("expected error for garbage FILETIME")
		}
	})
}

func TestSplitDN(t *testing.T) {
	ou, domain := splitDN("CN=PC1,OU=IT,OU=Floor 1,DC=corp,DC=local")
	if ou != "IT/Floor 1" {
		t.Errorf("ou = %q, want %q", ou, "IT/Floor 1")
	}
	if domain != "corp.local" {
		t.Errorf("domain = %q, want %q", domain, "corp.local")
	}

	ou, domain = splitDN("CN=SRV1,DC=corp,DC=local")
	if ou != "" || domain != "corp.local" {
		t.Errorf("no-OU case: ou=%q domain=%q", ou, domain)
	}
}

func TestShortHostname(t *testing.T) {
	if got := shortHostname("pc1.corp.local"); got != "pc1" {
		t.Errorf("shortHostname(FQDN) = %q, want pc1", got)
	}
	if got := shortHostname("pc1"); got != "pc1" {
		t.Errorf("shortHostname(short) = %q, want pc1", got)
	}
}
