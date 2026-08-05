package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// PortPicker - multi-select port list (err-disabled ports of one switch).
// Keys: ↑/↓ nav, space toggle, a all, n none, g/G top/bottom,
//       ? help, enter confirm, esc back.
type PortPicker struct {
	Ports    []string
	Selected map[string]bool
	Cursor   int
	Offset   int
	PageSize int
	Title    string
}

func NewPortPicker(ports []string, title string) *PortPicker {
	return &PortPicker{
		Ports:    ports,
		Selected: map[string]bool{},
		PageSize: 15,
		Title:    title,
	}
}

// SelectedPorts - selected ports in order
func (p *PortPicker) SelectedPorts() []string {
	var out []string
	for _, port := range p.Ports {
		if p.Selected[port] {
			out = append(out, port)
		}
	}
	return out
}

func (p *PortPicker) CountSelected() int {
	return len(p.SelectedPorts())
}

// HandleKey - returns (handled, back, confirm)
func (p *PortPicker) HandleKey(msg tea.KeyMsg) (bool, bool, bool) {
	window := func() (int, int) {
		if len(p.Ports) == 0 {
			return 0, 0
		}
		end := p.Offset + p.PageSize
		if end > len(p.Ports) {
			end = len(p.Ports)
		}
		return p.Offset, end
	}
	switch msg.String() {
	case "up", "k":
		if p.Cursor > 0 {
			p.Cursor--
			if p.Cursor < p.Offset {
				p.Offset = p.Cursor
			}
		}
	case "down", "j":
		if p.Cursor < len(p.Ports)-1 {
			p.Cursor++
			_, end := window()
			if p.Cursor >= end {
				p.Offset++
			}
		}
	case "g", "home":
		p.Cursor = 0
		p.Offset = 0
	case "G", "end":
		if len(p.Ports) > 0 {
			p.Cursor = len(p.Ports) - 1
			p.Offset = max(0, len(p.Ports)-p.PageSize)
		}
	case " ":
		if len(p.Ports) > 0 && p.Cursor < len(p.Ports) {
			port := p.Ports[p.Cursor]
			p.Selected[port] = !p.Selected[port]
		}
	case "a":
		for _, port := range p.Ports {
			p.Selected[port] = true
		}
	case "n":
		for _, port := range p.Ports {
			delete(p.Selected, port)
		}
	case "pgup", "left":
		p.Offset -= p.PageSize
		if p.Offset < 0 {
			p.Offset = 0
		}
		p.Cursor = p.Offset
	case "pgdown", "right":
		if len(p.Ports) > 0 && p.Offset+p.PageSize < len(p.Ports) {
			p.Offset += p.PageSize
			p.Cursor = p.Offset
		}
	case "enter":
		return true, false, true
	case "esc", "b", "q":
		return true, true, false
	}
	return true, false, false
}

var portPickerHelpKeys = [][2]string{
	{"↑/k ↓/j", "navigate"},
	{"space", "toggle select"},
	{"a / n", "select all / none"},
	{"←/→", "page"},
	{"g / G", "top / bottom"},
	{"enter", "fix selected"},
	{"esc", "back"},
}

func (p *PortPicker) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" "+p.Title+" ") + "\n\n")

	if len(p.Ports) == 0 {
		b.WriteString(dimStyle.Render("No err-disabled ports found on this switch.\n\n"))
		b.WriteString(renderFooter("esc", "back"))
		return b.String()
	}

	start := p.Offset
	end := start + p.PageSize
	if end > len(p.Ports) {
		end = len(p.Ports)
	}
	if start >= len(p.Ports) {
		start = 0
	}

	for i := start; i < end; i++ {
		port := p.Ports[i]
		check := " "
		if p.Selected[port] {
			check = "●"
		}
		cursor := "  "
		style := dimStyle
		if i == p.Cursor {
			cursor = "▸ "
			style = accStyle
		}
		b.WriteString(fmt.Sprintf("%s %s %s\n", cursor, check, style.Render(port)))
	}

	total := len(p.Ports)
	b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("%d/%d ports · %d selected",
		len(p.SelectedPorts()), total, p.CountSelected())))

	b.WriteString("\n" + renderFooter(
		"space", "select", "a/n", "all/none", "enter", "fix",
		"?", "help", "esc", "back"))
	return b.String()
}
