package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/farshidmousavii/netmon/internal/config"
)

// deviceListMsg - holds the loaded device list (simulated async load)
type deviceListMsg struct {
	devices []config.DeviceConfig
	err     error
}

type deviceListModel struct {
	cfg      *config.Config
	devices  []config.DeviceConfig
	loading  bool
	err      error
	cursor   int
	offset   int
	pageSize int
	query    string
	filtered []config.DeviceConfig
}

func newDeviceListModel(cfg *config.Config) *deviceListModel {
	m := &deviceListModel{cfg: cfg, pageSize: 15, loading: true}
	m.load()
	return m
}

func (m *deviceListModel) load() {
	// Simulate async load; real config already in memory
	m.devices = m.cfg.Devices
	m.filtered = m.devices
	m.loading = false
}

func (m *deviceListModel) applyFilter() {
	if m.query == "" {
		m.filtered = m.devices
		return
	}
	q := strings.ToLower(m.query)
	var f []config.DeviceConfig
	for _, d := range m.devices {
		if strings.Contains(strings.ToLower(d.Name), q) ||
			strings.Contains(d.IP, q) ||
			strings.Contains(strings.ToLower(d.Vendor), q) {
			f = append(f, d)
		}
	}
	m.filtered = f
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m deviceListModel) Init() tea.Cmd { return nil }

func (m deviceListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "b":
			return m, backToMenu()
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
			}
		case "pgup", "left":
			m.offset -= m.pageSize
			if m.offset < 0 {
				m.offset = 0
			}
		case "pgdown", "right":
			if m.offset+m.pageSize < len(m.filtered) {
				m.offset += m.pageSize
			}
		case "/":
			// enter filter mode: handled by simple prompt via state
			m.query = ""
			return m, tea.Printf("filter (type then enter): ")
		default:
			// typing filter text
			if len(msg.String()) == 1 {
				m.query += msg.String()
				m.applyFilter()
			}
		}
	}
	return m, nil
}

func (m deviceListModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" Device List ") + "\n\n")

	// header
	b.WriteString(dimStyle.Render(fmt.Sprintf("%-2s %-22s %-16s %-10s", "", "NAME", "IP", "VENDOR")) + "\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", 52)) + "\n")

	if m.loading {
		b.WriteString(dimStyle.Render("Loading...\n"))
	} else if m.err != nil {
		b.WriteString(errStyle.Render("Error: "+m.err.Error()) + "\n")
	} else if len(m.filtered) == 0 {
		b.WriteString(dimStyle.Render("No devices match.\n"))
	} else {
		start := m.offset
		end := start + m.pageSize
		if end > len(m.filtered) {
			end = len(m.filtered)
		}
		if start >= len(m.filtered) {
			start = 0
		}

		// fixed-width columns (lipgloss Width handles ANSI)
		nameCol := lipgloss.NewStyle().Width(22)
		ipCol := lipgloss.NewStyle().Width(16)
		vendorCol := lipgloss.NewStyle().Width(10)

		for i := start; i < end; i++ {
			d := m.filtered[i]
			cursor := "  "
			style := dimStyle
			if i == m.cursor {
				cursor = "▸ "
				style = accStyle
			}
			b.WriteString(fmt.Sprintf("%s %s%s%s\n",
				cursor,
				nameCol.Render(style.Render(d.Name)),
				ipCol.Render(style.Render(d.IP)),
				vendorCol.Render(style.Render(d.Vendor))))
		}
	}

	total := len(m.filtered)
	b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("Page %d/%d · %d devices",
		m.offset/m.pageSize+1, max(1, (total+m.pageSize-1)/m.pageSize), total)))
	if m.query != "" {
		b.WriteString(dimStyle.Render(fmt.Sprintf(" · filter: %q", m.query)))
	}
	b.WriteString("\n" + renderFooter("↑/↓", "nav", "←/→", "page", "/", "filter", "esc", "back"))
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
