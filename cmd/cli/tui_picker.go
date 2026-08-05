package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/farshidmousavii/bidar/internal/config"
)

// DevicePicker - shared multi-select device list.
// Keys: ↑/↓ nav, space toggle, a select all, n select none,
//
//	/ filter, ←/→ page, g/G top/bottom, ? help, enter confirm, esc/q back.
type DevicePicker struct {
	Devices   []config.DeviceConfig
	Selected  map[string]bool
	Cursor    int
	Offset    int
	PageSize  int
	Query     string
	Title     string
	Confirm   string // footer confirm label (e.g. "run", "fix")
	Filtering bool   // in filter input mode (/ pressed)
}

func NewDevicePicker(devices []config.DeviceConfig, title string) *DevicePicker {
	return &DevicePicker{
		Devices:  devices,
		Selected: map[string]bool{},
		PageSize: 12,
		Title:    title,
		Confirm:  "run",
	}
}

// Filtered - devices matching current query
func (p *DevicePicker) Filtered() []config.DeviceConfig {
	if p.Query == "" {
		return p.Devices
	}
	q := strings.ToLower(p.Query)
	var f []config.DeviceConfig
	for _, d := range p.Devices {
		if strings.Contains(strings.ToLower(d.Name), q) ||
			strings.Contains(d.IP, q) ||
			strings.Contains(strings.ToLower(d.Vendor), q) {
			f = append(f, d)
		}
	}
	return f
}

// SelectedDevices - returns selected configs (in original order)
func (p *DevicePicker) SelectedDevices() []config.DeviceConfig {
	var out []config.DeviceConfig
	for _, d := range p.Devices {
		if p.Selected[d.Name] {
			out = append(out, d)
		}
	}
	return out
}

func (p *DevicePicker) CountSelected() int {
	return len(p.SelectedDevices())
}

// HandleKey - returns (handled, back, confirm)
func (p *DevicePicker) HandleKey(msg tea.KeyMsg) (bool, bool, bool) {
	// filter input mode: keystrokes go to query
	if p.Filtering {
		switch msg.String() {
		case "enter", "esc", "/":
			p.Filtering = false
		case "backspace":
			if len(p.Query) > 0 {
				p.Query = p.Query[:len(p.Query)-1]
				p.resetViewport()
			}
		case "ctrl+c":
			return true, false, false
		default:
			if len(msg.String()) == 1 {
				p.Query += msg.String()
				p.resetViewport()
			}
		}
		return true, false, false
	}
	// viewport window for cursor clamping
	window := func() (int, int) {
		f := p.Filtered()
		if len(f) == 0 {
			return 0, 0
		}
		end := p.Offset + p.PageSize
		if end > len(f) {
			end = len(f)
		}
		return p.Offset, end
	}
	switch msg.String() {
	case "up", "k":
		if p.Cursor > 0 {
			p.Cursor--
			// scroll up if cursor left the window
			if p.Cursor < p.Offset {
				p.Offset = p.Cursor
			}
		}
	case "down", "j":
		f := p.Filtered()
		if p.Cursor < len(f)-1 {
			p.Cursor++
			// scroll down if cursor passed window end
			_, end := window()
			if p.Cursor >= end {
				p.Offset++
			}
		}
	case "g", "home":
		p.Cursor = 0
		p.Offset = 0
	case "G", "end":
		f := p.Filtered()
		if len(f) > 0 {
			p.Cursor = len(f) - 1
			p.Offset = max(0, len(f)-p.PageSize)
		}
	case " ":
		f := p.Filtered()
		if len(f) > 0 && p.Cursor < len(f) {
			name := f[p.Cursor].Name
			p.Selected[name] = !p.Selected[name]
		}
	case "a":
		for _, d := range p.Filtered() {
			p.Selected[d.Name] = true
		}
	case "n":
		for _, d := range p.Filtered() {
			delete(p.Selected, d.Name)
		}
	case "pgup", "left":
		p.Offset -= p.PageSize
		if p.Offset < 0 {
			p.Offset = 0
		}
		p.Cursor = p.Offset
	case "pgdown", "right":
		f := p.Filtered()
		if len(f) > 0 && p.Offset+p.PageSize < len(f) {
			p.Offset += p.PageSize
			p.Cursor = p.Offset
		}
	case "/":
		p.Filtering = true
		return true, false, false
	case "enter":
		return true, false, true
	case "esc", "b", "q":
		return true, true, false
	}
	return true, false, false
}

// keyMsg - build a KeyMsg from a single rune (testing + mouse wheel reuse)
func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// HandleMouse - click row / wheel scroll for pickers.
// rowTop = Y of first list row (after title/header), pageSize rows visible.
// Returns true if the event was consumed.
func (p *DevicePicker) HandleMouse(msg tea.MouseMsg) bool {
	e := tea.MouseEvent(msg)
	f := p.Filtered()
	if len(f) == 0 {
		return false
	}
	switch e.Button {
	case tea.MouseButtonLeft:
		if e.Action == tea.MouseActionPress {
			idx := e.Y - 2 // rows start after title + blank line
			if idx >= 0 && idx < p.PageSize && p.Offset+idx < len(f) {
				p.Cursor = p.Offset + idx
				return true
			}
		}
	case tea.MouseButtonWheelUp:
		if e.Action == tea.MouseActionPress {
			p.HandleKey(key("up"))
			return true
		}
	case tea.MouseButtonWheelDown:
		if e.Action == tea.MouseActionPress {
			p.HandleKey(key("down"))
			return true
		}
	}
	return false
}

// handlePickerMouse - shared mouse handling for pickers
func handlePickerMouse(p *DevicePicker, msg tea.Msg) bool {
	if mm, ok := msg.(tea.MouseMsg); ok {
		return p.HandleMouse(mm)
	}
	return false
}

// resetViewport - jump cursor to top after filter changes
func (p *DevicePicker) resetViewport() {
	p.Cursor = 0
	p.Offset = 0
}

var pickerHelpKeys = [][2]string{
	{"↑/k ↓/j", "navigate"},
	{"space", "toggle select"},
	{"a / n", "select all / none"},
	{"/", "filter"},
	{"←/→", "page"},
	{"g / G", "top / bottom"},
	{"enter", "confirm"},
	{"esc", "back"},
}

// View - renders the picker
func (p *DevicePicker) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" "+p.Title+" ") + "\n\n")

	// filter input bar
	if p.Filtering {
		b.WriteString(accStyle.Render("/ "+p.Query+"▌") + "  " +
			dimStyle.Render("filter (enter done · esc cancel)") + "\n\n")
	}

	filtered := p.Filtered()
	if len(filtered) == 0 {
		b.WriteString(dimStyle.Render("No devices match. Clear filter with / + backspace.\n\n"))
		b.WriteString(renderFooter("esc", "back"))
		return b.String()
	}

	start := p.Offset
	end := start + p.PageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	if start >= len(filtered) {
		start = 0
	}

	// header row
	b.WriteString(dimStyle.Render(fmt.Sprintf("%-2s %-3s %-22s %-16s %-10s",
		"", "sel", "NAME", "IP", "VENDOR")) + "\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", 56)) + "\n")

	// fixed-width column styles (lipgloss Width handles ANSI correctly)
	nameCol := lipgloss.NewStyle().Width(22)
	ipCol := lipgloss.NewStyle().Width(16)
	vendorCol := lipgloss.NewStyle().Width(10)

	for i := start; i < end; i++ {
		d := filtered[i]
		check := " "
		if p.Selected[d.Name] {
			check = "●"
		}
		cursor := "  "
		style := dimStyle
		if i == p.Cursor {
			cursor = "▸ "
			style = accStyle
		}
		b.WriteString(fmt.Sprintf("%s %s %s%s%s\n",
			cursor, check,
			nameCol.Render(style.Render(d.Name)),
			ipCol.Render(style.Render(d.IP)),
			vendorCol.Render(style.Render(d.Vendor))))
	}

	total := len(filtered)
	b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("%d/%d devices · %d selected",
		len(p.SelectedDevices()), total, p.CountSelected())))
	if p.Query != "" {
		b.WriteString(dimStyle.Render(fmt.Sprintf(" · filter: %q", p.Query)))
	}

	b.WriteString("\n" + renderFooter(
		"space", "select", "a/n", "all/none", "/", "filter",
		"enter", p.Confirm, "?", "help", "esc", "back"))
	return b.String()
}
