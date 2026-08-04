package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/farshidmousavii/netmon/internal/config"
)

// DevicePicker - shared multi-select device list.
// Keys: ↑/↓ nav, space toggle, a select all, n select none,
//       / filter, ←/→ page, enter confirm, esc/q back.
type DevicePicker struct {
	Devices  []config.DeviceConfig // full list (filtered applied to display)
	Selected map[string]bool       // device name -> selected
	Cursor   int
	Offset   int
	PageSize int
	Query    string
	Title    string
	Help     string
}

func NewDevicePicker(devices []config.DeviceConfig, title string) *DevicePicker {
	p := &DevicePicker{
		Devices:  devices,
		Selected: map[string]bool{},
		PageSize: 12,
		Title:    title,
		Help:     "space select · a all · n none · / filter · ←/→ page · enter run · esc back",
	}
	return p
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

// HandleKey - returns (handled, quitBack, confirm)
func (p *DevicePicker) HandleKey(msg tea.KeyMsg) (bool, bool, bool) {
	switch msg.String() {
	case "up", "k":
		if p.Cursor > 0 {
			p.Cursor--
		}
	case "down", "j":
		if p.Cursor < len(p.Devices)-1 {
			p.Cursor++
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
	case "pgdown", "right":
		if p.Offset+p.PageSize < len(p.Filtered()) {
			p.Offset += p.PageSize
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

// View - renders the picker
func (p *DevicePicker) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" "+p.Title+" ") + "\n\n")

	filtered := p.Filtered()
	start := p.Offset
	end := start + p.PageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	if start >= len(filtered) && len(filtered) > 0 {
		start = 0
	}

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
			style = titleStyle
		}
		b.WriteString(fmt.Sprintf("%s[%s] %s %s\n",
			cursor, check, style.Render(fmt.Sprintf("%-22s %-16s %-10s", d.Name, d.IP, d.Vendor)), ""))
	}

	total := len(filtered)
	b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("Page %d/%d · %d devices · %d selected",
		p.Offset/p.PageSize+1, max(1, (total+p.PageSize-1)/p.PageSize), total, p.CountSelected())))
	if p.Query != "" {
		b.WriteString(dimStyle.Render(fmt.Sprintf(" · filter: %q", p.Query)))
	}
	b.WriteString("\n" + dimStyle.Render(p.Help))
	return b.String()
}
