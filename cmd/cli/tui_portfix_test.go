package cli

import (
	"strings"
	"testing"
)

func TestParseLastSourceAddress(t *testing.T) {
	out := `Secure Port  MaxSecureAddr  CurrentAddr  SecurityViolation  Security Action
                (Count)        (Count)          (Count)
---------------------------------------------------------------------------
	Fa0/2                5              2              1                 Shutdown
---------------------------------------------------------------------------
Total Addresses in System (excluding one mac per port)     : 2
Max Addresses limit in System (excluding one mac per port) : 8192

Port Security : Enabled
Violation mode : Shutdown
Aging Time : 0 mins
Aging Type : Absolute
SecureStatic address aging : Disabled
Security Violation Count : 1
Last Source Address:Vlan : 0050.7966.6800:1
Last violation Time : 00:01:23`
	if got := parseLastSourceAddress(out); got != "0050.7966.6800" {
		t.Errorf("parseLastSourceAddress = %q, want 0050.7966.6800", got)
	}
}

func TestParseLastSourceAddress_Empty(t *testing.T) {
	if got := parseLastSourceAddress("no data here"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFindPortForMac(t *testing.T) {
	out := `               Secure Mac Address Table
-----------------------------------------------------------------------------
Vlan    Mac Address       Type                          Ports   Remaining Age
                                                                   (mins)
----    -----------       ----                          -----   -------------
   1    0050.7966.6800    SecureSticky                  Fa0/1   -
   2    0011.2233.4455    SecureSticky                  Fa0/5   -
-----------------------------------------------------------------------------
Total Addresses in System (excluding one mac per port)     : 2`
	if got := findPortForMac(out, "0050.7966.6800"); got != "Fa0/1" {
		t.Errorf("findPortForMac = %q, want Fa0/1", got)
	}
	if got := findPortForMac(out, "00aa.bbcc.ddee"); got != "" {
		t.Errorf("findPortForMac unknown = %q, want empty", got)
	}
}

func TestFindPortForMac_CaseInsensitive(t *testing.T) {
	out := `   1    0050.7966.6800    SecureSticky                  Fa0/1   -`
	if got := findPortForMac(out, "0050.7966.6800"); got != "Fa0/1" {
		t.Errorf("case insensitive fail: %q", got)
	}
}

func TestParseErrDisabledPorts(t *testing.T) {
	out := `Port      Name               Status       Vlan   Duplex  Speed Type
Fa0/1     pc-1               connected    1      a-full  a-100  10/100BaseTX
Fa0/2     pc-2               err-disabled 1      auto    auto   10/100BaseTX
Fa0/3                      err-disabled 1      auto    auto   10/100BaseTX
Gi0/1                      connected    1      a-full  a-1000 10/100/1000BaseTX`
	ports := parseErrDisabledPorts(out)
	if len(ports) != 2 {
		t.Fatalf("expected 2 err-disabled ports, got %v", ports)
	}
	if ports[0] != "Fa0/2" || ports[1] != "Fa0/3" {
		t.Errorf("wrong ports: %v", ports)
	}
}

func TestFindStickyPort_Parsers(t *testing.T) {
	// integration of both parsers: MAC from err port -> port in table
	ifaceOut := "Last Source Address:Vlan : 0050.7966.6800:1"
	addrOut := strings.Join([]string{
		"Vlan    Mac Address       Type                          Ports",
		"   1    0050.7966.6800    SecureSticky                  Fa0/1",
		"   2    0011.2233.4455    SecureSticky                  Fa0/5",
	}, "\n")
	mac := parseLastSourceAddress(ifaceOut)
	if mac == "" {
		t.Fatal("no MAC parsed")
	}
	if got := findPortForMac(addrOut, mac); got != "Fa0/1" {
		t.Errorf("expected Fa0/1, got %q", got)
	}
}

// TestRealWorldOutput - raw output as bidar receives it (prompts,
// backspaces, CRLF) must still parse.
func TestRealWorldOutput(t *testing.T) {
	raw := "\r\nNamvaran North Building\r\nCenter-A-FL0-E>enable\r\nPassword: \r\n" +
		"Center-A-FL0-E#terminal length 0\r\n" +
		"Center-A-FL0-E#show port-security interface Fa0/2\r\n" +
		"Secure Port  MaxSecureAddr  CurrentAddr  SecurityViolation  Security Action\r\n" +
		"                (Count)        (Count)          (Count)\r\n" +
		"---------------------------------------------------------------------------\r\n" +
		"	Fa0/2                5              2              1                 Shutdown\r\n" +
		"---------------------------------------------------------------------------\r\n" +
		"Port Security : Enabled\r\n" +
		"Last Source Address:Vlan : 0050.7966.6800:1\r\n" +
		"Security Violation Count : 1\r\n" +
		"Center-A-FL0-E#exit\r\n"
	if got := parseLastSourceAddress(raw); got != "0050.7966.6800" {
		t.Errorf("real-world parse = %q, want 0050.7966.6800", got)
	}
}
