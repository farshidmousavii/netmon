package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// PortPicker - multi-select port list (err-disabled ports of one switch).
// Keys: ↑/↓ nav, space toggle, a all, n none, enter confirm, esc back.
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
	switch msg.String() {
	case "up", "k":
		if p.Cursor > 0 {
			p.Cursor--
		}
	case "down", "j":
		if p.Cursor < len(p.Ports)-1 {
			p.Cursor++
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
	case "pgdown", "right":
		if p.Offset+p.PageSize < len(p.Ports) {
			p.Offset += p.PageSize
		}
	case "enter":
		return true, false, true
	case "esc", "b", "q":
		return true, true, false
	}
	return true, false, false
}

func (p *PortPicker) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" "+p.Title+" ") + "\n\n")

	start := p.Offset
	end := start + p.PageSize
	if end > len(p.Ports) {
		end = len(p.Ports)
	}
	if start >= len(p.Ports) && len(p.Ports) > 0 {
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
			style = titleStyle
		}
		b.WriteString(fmt.Sprintf("%s[%s] %s\n", cursor, check, style.Render(port)))
	}

	total := len(p.Ports)
	b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("Page %d/%d · %d ports · %d selected",
		p.Offset/p.PageSize+1, max(1, (total+p.PageSize-1)/p.PageSize), total, p.CountSelected())))
	b.WriteString("\n" + dimStyle.Render("space select · a all · n none · enter fix · esc back"))
	return b.String()
}
