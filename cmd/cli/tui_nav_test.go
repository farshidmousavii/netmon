package cli

import (
	"testing"

	"github.com/farshidmousavii/bidar/internal/config"
)

func testShell() *ShellModel {
	m := newRootModel(&config.Config{})
	m.selected = -1
	m.focus = 0
	return m
}

// shellUpdate - run Update and return updated *ShellModel (Update uses value receiver)
func shellUpdate(m *ShellModel, keyStr string) *ShellModel {
	mm, _ := m.Update(key(keyStr))
	mm2 := mm.(ShellModel)
	return &mm2
}

func TestShellDomainSelectShowsItems(t *testing.T) {
	m := testShell()
	// right → items of hovered domain (Dashboard)
	m = shellUpdate(m, "right")
	if m.selected != 0 || m.focus != 1 {
		t.Fatalf("after right: selected=%d focus=%d, want 0/1", m.selected, m.focus)
	}
	if m.iCursor != 0 {
		t.Fatalf("iCursor=%d, want 0", m.iCursor)
	}
}

func TestShellDomainNavigation(t *testing.T) {
	m := testShell()
	// down to Operations (index 4)
	for i := 0; i < 4; i++ {
		m = shellUpdate(m, "down")
	}
	if m.dCursor != 4 {
		t.Fatalf("dCursor=%d, want 4", m.dCursor)
	}
	// right → show Operations items (3: Quick Exec, Port Fix, Backup)
	m = shellUpdate(m, "right")
	if m.selected != 4 || m.focus != 1 {
		t.Fatalf("selected=%d focus=%d, want 4/1", m.selected, m.focus)
	}
	if len(m.currentDomain().items) != 3 {
		t.Fatalf("Operations items=%d, want 3", len(m.currentDomain().items))
	}
	// down → Port Fix
	m = shellUpdate(m, "down")
	if m.iCursor != 1 || m.currentDomain().items[m.iCursor].label != "Port Fix" {
		t.Fatalf("iCursor=%d label=%s, want 1/Port Fix", m.iCursor, m.currentDomain().items[m.iCursor].label)
	}
	// enter → opens screen (content set)
	m = shellUpdate(m, "enter")
	if m.content == nil {
		t.Fatal("enter should open a content screen")
	}
}

func TestShellTabAndEsc(t *testing.T) {
	m := testShell()
	// right → focus right
	m = shellUpdate(m, "right")
	if m.focus != 1 {
		t.Fatal("focus should be 1 after right")
	}
	// tab → back to left
	m = shellUpdate(m, "tab")
	if m.focus != 0 {
		t.Fatal("focus should be 0 after tab")
	}
	// right again, esc → back to left
	m = shellUpdate(m, "right")
	m = shellUpdate(m, "esc")
	if m.focus != 0 {
		t.Fatal("focus should be 0 after esc")
	}
	// esc again → deselect (selected=-1)
	m = shellUpdate(m, "esc")
	if m.selected != -1 {
		t.Fatalf("selected=%d, want -1 after double esc", m.selected)
	}
}

func TestShellNumberJump(t *testing.T) {
	m := testShell()
	// press 5 → Operations (index 4)
	m = shellUpdate(m, "5")
	if m.dCursor != 4 {
		t.Fatalf("jump 5: dCursor=%d, want 4", m.dCursor)
	}
}

func TestShellAllDomainsPresent(t *testing.T) {
	if len(domains) != 8 {
		t.Fatalf("domains=%d, want 8 (Dashboard/Assets/Network/Discovery/Operations/Monitoring/Reports/Settings)", len(domains))
	}
	// Discovery, Reports, Settings empty (no items yet)
	for _, idx := range []int{3, 6, 7} {
		if len(domains[idx].items) != 0 {
			t.Fatalf("domain %s should have no items yet, got %d", domains[idx].label, len(domains[idx].items))
		}
	}
}
