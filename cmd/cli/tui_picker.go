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
//       / filter, ←/→ page, g/G top/bottom, ? help, enter confirm, esc/q back.
type DevicePicker struct {
	Devices  []config.DeviceConfig
	Selected map[string]bool
	Cursor   int
	Offset   int
	PageSize int
	Query    string
	Title    string
	Confirm  string // footer confirm label (e.g. "run", "fix")
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
		p.Query = ""
		return true, false, false
	case "enter":
		return true, false, true
	case "esc", "b", "q":
		return true, true, false
	default:
		// typing filter chars
		if len(msg.String()) == 1 {
			p.Query += msg.String()
			if p.Cursor >= len(p.Filtered()) {
				p.Cursor = len(p.Filtered()) - 1
			}
			if p.Cursor < 0 {
				p.Cursor = 0
			}
		}
	}
	return true, false, false
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
