package cli

import "testing"

func TestParseIfStatus(t *testing.T) {
	out := `Port      Name               Status       Vlan    Duplex  Speed Type
Fa0/1     pc-1               connected     1      a-full  a-100  10/100BaseTX
Fa0/2     pc-2               err-disabled  1      auto    auto   10/100BaseTX
Fa0/3                      notconnect     1      auto    auto   10/100BaseTX
Gi0/1                      connected      1      a-full  a-1000 10/100/1000BaseTX`
	rows := parseIfStatus(out)
	if len(rows) != 4 {
		t.Fatalf("rows=%d, want 4", len(rows))
	}
	if rows[0].port != "Fa0/1" || rows[0].status != "connected" || rows[0].vlan != "1" {
		t.Fatalf("row0 wrong: %+v", rows[0])
	}
	if rows[1].status != "err-disabled" {
		t.Fatalf("row1 status=%s, want err-disabled", rows[1].status)
	}
	if rows[2].name != "" {
		t.Fatalf("row2 name should be empty, got %q", rows[2].name)
	}
	if rows[3].port != "Gi0/1" || rows[3].speed != "a-1000" {
		t.Fatalf("row3 wrong: %+v", rows[3])
	}
}

func TestParseIfStatusEmpty(t *testing.T) {
	if rows := parseIfStatus(""); len(rows) != 0 {
		t.Fatalf("empty should give 0 rows, got %d", len(rows))
	}
}
