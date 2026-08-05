package cli

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/farshidmousavii/bidar/internal/config"
)

func mkDevices(n int) []config.DeviceConfig {
	out := make([]config.DeviceConfig, n)
	for i := range out {
		out[i] = config.DeviceConfig{Name: string(rune('a'+i%26)) + string(rune('0'+i/26)), IP: "10.0.0.1"}
	}
	return out
}

// scrollDown - press down n times
func scrollDown(p *DevicePicker, n int) {
	for i := 0; i < n; i++ {
		p.HandleKey(key("down"))
	}
}

func TestPickerScrollFollowsCursor(t *testing.T) {
	p := NewDevicePicker(mkDevices(30), "t")
	p.PageSize = 12

	// move to bottom of first page (index 11)
	scrollDown(p, 11)
	if p.Cursor != 11 || p.Offset != 0 {
		t.Fatalf("first page: cursor=%d offset=%d, want 11/0", p.Cursor, p.Offset)
	}
	// one more down → page flips, cursor at top of new page
	p.HandleKey(key("down"))
	if p.Cursor != 12 || p.Offset != 1 {
		t.Fatalf("page flip: cursor=%d offset=%d, want 12/1", p.Cursor, p.Offset)
	}
	// scroll to bottom of list
	scrollDown(p, 30)
	if p.Cursor != 29 || p.Offset != 18 {
		t.Fatalf("bottom: cursor=%d offset=%d, want 29/18", p.Cursor, p.Offset)
	}
	// up from bottom → scrolls back
	p.HandleKey(key("up"))
	if p.Cursor != 28 || p.Offset != 18 {
		t.Fatalf("up: cursor=%d offset=%d, want 28/18 (offset stays; cursor still in window)", p.Cursor, p.Offset)
	}
}

func TestPickerUpScrollsPastPageBoundary(t *testing.T) {
	p := NewDevicePicker(mkDevices(30), "t")
	p.PageSize = 12
	// jump to bottom, then scroll up through the page boundary (12→11)
	scrollDown(p, 29)
	p.HandleKey(key("up")) // 28
	p.HandleKey(key("up")) // 27
	// boundary: 16 times up lands on 11 → window follows (offset tracks cursor
	// up when cursor leaves the window; cursor stays visible)
	for i := 0; i < 16; i++ {
		p.HandleKey(key("up"))
	}
	if p.Cursor != 11 || p.Offset != 11 {
		t.Fatalf("boundary up: cursor=%d offset=%d, want 11/11", p.Cursor, p.Offset)
	}
	// cursor always inside window after any amount of scrolling
	start, end := p.Offset, p.Offset+p.PageSize
	if end > 30 {
		end = 30
	}
	if p.Cursor < start || p.Cursor >= end {
		t.Fatalf("cursor=%d outside window [%d,%d)", p.Cursor, start, end)
	}
}

func TestPickerPageKeysResetCursor(t *testing.T) {
	p := NewDevicePicker(mkDevices(40), "t")
	p.PageSize = 12
	scrollDown(p, 39) // bottom
	if p.Cursor != 39 || p.Offset != 28 {
		t.Fatalf("bottom: cursor=%d offset=%d, want 39/28", p.Cursor, p.Offset)
	}
	// pgup → cursor lands on new window top
	p.HandleKey(key("pgup"))
	if p.Cursor != 16 || p.Offset != 16 {
		t.Fatalf("pgup: cursor=%d offset=%d, want 16/16", p.Cursor, p.Offset)
	}
	// pgdown → next window
	p.HandleKey(key("pgdown"))
	if p.Cursor != 28 || p.Offset != 28 {
		t.Fatalf("pgdown: cursor=%d offset=%d, want 28/28", p.Cursor, p.Offset)
	}
}

func TestPickerCursorInView(t *testing.T) {
	p := NewDevicePicker(mkDevices(30), "t")
	p.PageSize = 12
	for i := 0; i < 100; i++ {
		p.HandleKey(key("down"))
	}
	// cursor always inside window, never past end
	start, end := p.Offset, p.Offset+p.PageSize
	if end > 30 {
		end = 30
	}
	if p.Cursor < start || p.Cursor >= end {
		t.Fatalf("cursor=%d outside window [%d,%d)", p.Cursor, start, end)
	}
	if p.Offset != 18 {
		t.Fatalf("offset=%d, want 18", p.Offset)
	}
}

func TestPortPickerScrollFollowsCursor(t *testing.T) {
	ports := make([]string, 25)
	for i := range ports {
		ports[i] = "Fa0/" + string(rune('0'+i%10)) + string(rune('0'+i/10))
	}
	p := NewPortPicker(ports, "t")
	p.PageSize = 12
	for i := 0; i < 12; i++ {
		p.HandleKey(key("down"))
	}
	if p.Cursor != 12 || p.Offset != 1 {
		t.Fatalf("page flip: cursor=%d offset=%d, want 12/1", p.Cursor, p.Offset)
	}
	p.HandleKey(key("pgdown"))
	if p.Cursor != 13 || p.Offset != 13 {
		t.Fatalf("pgdown: cursor=%d offset=%d, want 13/13", p.Cursor, p.Offset)
	}
}

func TestPickerFilterMode(t *testing.T) {
	p := NewDevicePicker(mkDevices(30), "t")
	p.PageSize = 12
	// navigate somewhere, then filter
	scrollDown(p, 20)
	// press / → filtering mode
	p.HandleKey(key("/"))
	if !p.Filtering {
		t.Fatal("expected filtering mode after /")
	}
	// type filter
	p.HandleKey(key("a"))
	p.HandleKey(key("b"))
	if p.Query != "ab" {
		t.Fatalf("query = %q, want ab", p.Query)
	}
	// backspace removes char
	p.HandleKey(key("backspace"))
	if p.Query != "a" {
		t.Fatalf("query after backspace = %q, want a", p.Query)
	}
	// typing resets viewport (cursor to top)
	if p.Cursor != 0 || p.Offset != 0 {
		t.Fatalf("viewport after filter: cursor=%d offset=%d, want 0/0", p.Cursor, p.Offset)
	}
	// enter exits filtering
	p.HandleKey(key("enter"))
	if p.Filtering {
		t.Fatal("expected filtering off after enter")
	}
}

func TestPickerFilterEmptyRestoresAll(t *testing.T) {
	p := NewDevicePicker(mkDevices(30), "t")
	p.HandleKey(key("/"))
	// type query that matches nothing, then backspace it all
	p.HandleKey(key("z"))
	p.HandleKey(key("z"))
	if len(p.Filtered()) != 0 {
		t.Fatalf("filter zz should match 0, got %d", len(p.Filtered()))
	}
	p.HandleKey(key("backspace"))
	p.HandleKey(key("backspace"))
	if len(p.Filtered()) != 30 {
		t.Fatalf("empty query should restore all, got %d", len(p.Filtered()))
	}
}

func TestPickerMouseClick(t *testing.T) {
	p := NewDevicePicker(mkDevices(30), "t")
	p.PageSize = 12
	// click row 5 (Y=2+5=7) → cursor at index 5
	p.HandleMouse(tea.MouseMsg{Type: tea.MouseLeft, X: 5, Y: 7, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if p.Cursor != 5 {
		t.Fatalf("click row 5: cursor=%d, want 5", p.Cursor)
	}
	// click past page end (row 20) → ignored
	p.HandleMouse(tea.MouseMsg{Type: tea.MouseLeft, X: 5, Y: 25, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if p.Cursor != 5 {
		t.Fatalf("click out of range should be ignored, cursor=%d", p.Cursor)
	}
	// wheel down → cursor+1
	p.HandleMouse(tea.MouseMsg{Type: tea.MouseWheelDown, X: 5, Y: 7, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if p.Cursor != 6 {
		t.Fatalf("wheel down: cursor=%d, want 6", p.Cursor)
	}
}

func TestDeviceListScrollFollowsCursor(t *testing.T) {
	m := &deviceListModel{devices: mkDevices(30), filtered: mkDevices(30), pageSize: 15, loading: false}
	// to bottom of first page (index 14)
	for i := 0; i < 14; i++ {
		mm, _ := m.Update(key("down"))
		mm2 := mm.(deviceListModel)
		m = &mm2
	}
	if m.cursor != 14 || m.offset != 0 {
		t.Fatalf("first page: cursor=%d offset=%d, want 14/0", m.cursor, m.offset)
	}
	// one more down → page flips, cursor on first of new page
	mm, _ := m.Update(key("down"))
	mm2 := mm.(deviceListModel)
	m = &mm2
	if m.cursor != 15 || m.offset != 1 {
		t.Fatalf("page flip: cursor=%d offset=%d, want 15/1", m.cursor, m.offset)
	}
	// down to bottom of list
	for i := 0; i < 20; i++ {
		mm, _ := m.Update(key("down"))
		mm2 := mm.(deviceListModel)
		m = &mm2
	}
	if m.cursor != 29 || m.offset != 15 {
		t.Fatalf("bottom: cursor=%d offset=%d, want 29/15", m.cursor, m.offset)
	}
	// up from bottom → scrolls back
	mm, _ = m.Update(key("up"))
	mm2 = mm.(deviceListModel)
	m = &mm2
	if m.cursor != 28 || m.offset != 15 {
		t.Fatalf("up: cursor=%d offset=%d, want 28/15", m.cursor, m.offset)
	}
	// page up → cursor lands on window top
	mm, _ = m.Update(key("pgup"))
	mm2 = mm.(deviceListModel)
	m = &mm2
	if m.cursor != 0 || m.offset != 0 {
		t.Fatalf("pgup: cursor=%d offset=%d, want 0/0", m.cursor, m.offset)
	}
}
