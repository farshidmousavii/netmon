package cli

import (
	"testing"

	"github.com/farshidmousavii/bidar/internal/config"
)

func testNav() *NavModel {
	m := newMenuModel(&config.Config{})
	m.selected = -1
	m.focus = 0
	return m
}

// navUpdate - run Update and return updated *NavModel (Update uses value receiver)
func navUpdate(m *NavModel, keyStr string) *NavModel {
	mm, _ := m.Update(key(keyStr))
	mm2 := mm.(NavModel)
	return &mm2
}

func TestNavDomainSelectShowsItems(t *testing.T) {
	m := testNav()
	// right → items of hovered domain (Dashboard)
	m = navUpdate(m, "right")
	if m.selected != 0 || m.focus != 1 {
		t.Fatalf("after right: selected=%d focus=%d, want 0/1", m.selected, m.focus)
	}
	if m.iCursor != 0 {
		t.Fatalf("iCursor=%d, want 0", m.iCursor)
	}
}

func TestNavDomainNavigation(t *testing.T) {
	m := testNav()
	// down 2 → Operations (index 3)
	for i := 0; i < 3; i++ {
		m = navUpdate(m, "down")
	}
	if m.dCursor != 3 {
		t.Fatalf("dCursor=%d, want 3", m.dCursor)
	}
	// right → show Operations items (3: Quick Exec, Port Fix, Backup)
	m = navUpdate(m, "right")
	if m.selected != 3 || m.focus != 1 {
		t.Fatalf("selected=%d focus=%d, want 3/1", m.selected, m.focus)
	}
	if len(m.currentDomain().items) != 3 {
		t.Fatalf("Operations items=%d, want 3", len(m.currentDomain().items))
	}
	// down → Port Fix
	m = navUpdate(m, "down")
	if m.iCursor != 1 || m.currentDomain().items[m.iCursor].label != "Port Fix" {
		t.Fatalf("iCursor=%d label=%s, want 1/Port Fix", m.iCursor, m.currentDomain().items[m.iCursor].label)
	}
	// enter → opens screen (switchMsg)
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter should return a cmd (switchTo)")
	}
}

func TestNavTabAndEsc(t *testing.T) {
	m := testNav()
	// right → focus right
	m = navUpdate(m, "right")
	if m.focus != 1 {
		t.Fatal("focus should be 1 after right")
	}
	// tab → back to left
	m = navUpdate(m, "tab")
	if m.focus != 0 {
		t.Fatal("focus should be 0 after tab")
	}
	// right again, esc → back to left
	m = navUpdate(m, "right")
	m = navUpdate(m, "esc")
	if m.focus != 0 {
		t.Fatal("focus should be 0 after esc")
	}
	// esc again → deselect (selected=-1)
	m = navUpdate(m, "esc")
	if m.selected != -1 {
		t.Fatalf("selected=%d, want -1 after double esc", m.selected)
	}
}
