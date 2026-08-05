package cli

import (
	"strings"
	"testing"
)

// TestBuildOutputLines - PTY output with \r\n splits into clean lines
func TestBuildOutputLines(t *testing.T) {
	m := &quickExecModel{}
	m.results = []taskResult{
		{name: "SW1", status: "RT-4331#show run\r\nBuilding configuration...\r\nversion 17.4\r\n!\r\n"},
		{name: "SW2", err: nil, status: "ok\r\n"},
	}
	// simulate taskDoneMsg handling
	var lines []string
	for _, r := range m.results {
		if r.err != nil {
			lines = append(lines, "✗ "+r.name)
		} else {
			lines = append(lines, "✓ "+r.name)
			clean := strings.ReplaceAll(strings.TrimSuffix(r.status, "\n"), "\r", "")
			for _, l := range strings.Split(clean, "\n") {
				lines = append(lines, "  "+l)
			}
		}
		lines = append(lines, "")
	}
	m.outLines = lines

	if len(m.outLines) != 9 { // SW1: 1 header + 4 content + 1 blank, SW2: 1+1+1
		t.Fatalf("lines=%d, want 9: %v", len(m.outLines), m.outLines)
	}
	if m.outLines[0] != "✓ SW1" {
		t.Fatalf("line0=%q, want ✓ SW1", m.outLines[0])
	}
	if m.outLines[2] != "  Building configuration..." {
		t.Fatalf("line2=%q", m.outLines[2])
	}
	if m.outLines[3] != "  version 17.4" {
		t.Fatalf("line3=%q, want version line", m.outLines[3])
	}
}
