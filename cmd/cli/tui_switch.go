package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/farshidmousavii/bidar/internal/config"
	"github.com/farshidmousavii/bidar/internal/device"
)

// switchDetailsModel - interface table of one switch (live via SSH).
type switchDetailsModel struct {
	cfg     *config.Config
	dev     config.DeviceConfig
	loading bool
	rows    []ifRow
	err     error
}

// ifRow - one interface row from `show interface status`
type ifRow struct {
	port   string
	name   string
	status string
	vlan   string
	duplex string
	speed  string
	kind   string
}

func newSwitchDetailsModel(cfg *config.Config, dev config.DeviceConfig) *switchDetailsModel {
	return &switchDetailsModel{cfg: cfg, dev: dev, loading: true}
}

func (m *switchDetailsModel) Init() tea.Cmd { return m.load() }

func (m *switchDetailsModel) load() tea.Cmd {
	return func() tea.Msg {
		cred, err := m.cfg.GetCredential(m.dev.Credential)
		if err != nil {
			return switchIfMsg{err: err}
		}
		dev, err := device.NewDevice(m.dev, cred, m.cfg)
		if err != nil {
			return switchIfMsg{err: err}
		}
		out, err := dev.RunCommand("show interface status")
		if err != nil {
			return switchIfMsg{err: err}
		}
		return switchIfMsg{rows: parseIfStatus(out)}
	}
}

type switchIfMsg struct {
	rows []ifRow
	err  error
}

// parseIfStatus - parse `show interface status` table.
// Header: Port Name Status Vlan Duplex Speed Type
func parseIfStatus(out string) []ifRow {
	var rows []ifRow
	lines := strings.Split(out, "\n")
	started := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Port") {
			started = true
			continue
		}
		if strings.HasPrefix(line, "---") {
			continue
		}
		if !started {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// Name column may be empty (whitespace) → fields shift left.
		// Status is always the 3rd token; detect by known status words.
		name := ""
		statusIdx := 2
		if isIfStatus(fields[1]) {
			// name empty → status moved to index 1
			statusIdx = 1
		} else if isIfStatus(fields[2]) {
			name = fields[1]
		} else {
			continue // not a data row
		}
		rest := fields[statusIdx:]
		if len(rest) < 4 {
			continue
		}
		rows = append(rows, ifRow{
			port:   fields[0],
			name:   name,
			status: rest[0],
			vlan:   rest[1],
			duplex: rest[2],
			speed:  rest[3],
			kind:   strings.Join(rest[4:], " "),
		})
	}
	return rows
}

var ifStatusWords = []string{"connected", "notconnect", "disabled", "err-disabled", "down"}

// isIfStatus - true if s is a known interface status word
func isIfStatus(s string) bool {
	for _, w := range ifStatusWords {
		if s == w {
			return true
		}
	}
	return false
}

func (m *switchDetailsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case switchIfMsg:
		m.loading = false
		m.rows = v.rows
		m.err = v.err
		return m, nil
	case tea.KeyMsg:
		switch v.String() {
		case "esc", "q":
			return m, backToMenu()
		case "r":
			m.loading = true
			return m, m.load()
		}
	}
	return m, nil
}

func (m *switchDetailsModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" "+m.dev.Name+" — Interfaces ") + "\n\n")

	if m.loading {
		b.WriteString(dimStyle.Render("Loading interfaces...") + "\n" + spinner() + "\n")
		return b.String()
	}
	if m.err != nil {
		b.WriteString(errStyle.Render("✗ "+m.err.Error()) + "\n\n")
		return b.String()
	}
	if len(m.rows) == 0 {
		b.WriteString(dimStyle.Render("No interfaces found.\n\n"))
		return b.String()
	}

	// header
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %-8s %-14s %-12s %-5s %-8s %-8s %s",
		"PORT", "NAME", "STATUS", "VLAN", "DUPLEX", "SPEED", "TYPE")) + "\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", 70)) + "\n")

	portCol := lipgloss.NewStyle().Width(8)
	nameCol := lipgloss.NewStyle().Width(14)
	vlanCol := lipgloss.NewStyle().Width(5)
	duplexCol := lipgloss.NewStyle().Width(8)
	speedCol := lipgloss.NewStyle().Width(8)

	for _, r := range m.rows {
		statusStyle := dimStyle
		switch {
		case strings.Contains(r.status, "connected"):
			statusStyle = okStyle
		case strings.Contains(r.status, "err-disabled"):
			statusStyle = errStyle
		case strings.Contains(r.status, "disabled"):
			statusStyle = warnStyle
		}
		b.WriteString(fmt.Sprintf("  %s%s%s%s%s%s%s\n",
			portCol.Render(textStyle.Render(r.port)),
			nameCol.Render(textStyle.Render(r.name)),
			statusStyle.Render(fmt.Sprintf("%-12s", r.status)),
			vlanCol.Render(textStyle.Render(r.vlan)),
			duplexCol.Render(textStyle.Render(r.duplex)),
			speedCol.Render(textStyle.Render(r.speed)),
			textStyle.Render(r.kind)))
	}

	return b.String()
}
