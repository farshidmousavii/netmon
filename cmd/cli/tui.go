package cli

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/farshidmousavii/netmon/internal/config"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39")) // blue
	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("45"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // red
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))  // green
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // orange
	portStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("227"))
	deviceStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("51"))
)

// runTUIEngine - main entry for TUI mode
func runTUIEngine(ctx context.Context, cfg *config.Config) error {
	p := tea.NewProgram(newRootModel(cfg), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

// ─── Root model ───
// Owns the current screen; handles global keys + screen switches.

type RootModel struct {
	cfg    *config.Config
	mode   tea.Model
	width  int
	height int
}

func newRootModel(cfg *config.Config) *RootModel {
	return &RootModel{cfg: cfg, mode: newMenuModel(cfg)}
}

func (m RootModel) Init() tea.Cmd { return nil }

func (m RootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case switchMsg:
		if msg.model == nil {
			// back to menu, reuse root cfg
			m.mode = newMenuModel(m.cfg)
			return m, m.mode.Init()
		}
		m.mode = msg.model
		return m, m.mode.Init()
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	updated, cmd := m.mode.Update(msg)
	m.mode = updated
	return m, cmd
}

func (m RootModel) View() string {
	if m.mode == nil {
		return ""
	}
	return m.mode.View()
}

// ─── Shared messages ───

type switchMsg struct{ model tea.Model }

func switchTo(model tea.Model) tea.Cmd {
	return func() tea.Msg { return switchMsg{model: model} }
}

func backToMenu() tea.Cmd {
	return func() tea.Msg {
		// menu rebuilds from stored cfg (see MenuModel.Update)
		return switchMsg{model: nil}
	}
}

// ─── Menu model ───

type MenuModel struct {
	cfg     *config.Config
	cursor  int
	options []struct {
		label string
		make  func() tea.Model
	}
}

func newMenuModel(cfg *config.Config) *MenuModel {
	m := &MenuModel{cfg: cfg}
	m.options = []struct {
		label string
		make  func() tea.Model
	}{
		{"📊 Overview", func() tea.Model { return newOverviewModel(cfg) }},
		{"🖧 Device list", func() tea.Model { return newDeviceListModel(cfg) }},
		{"🔧 Port fix", func() tea.Model { return newPortFixModel(cfg) }},
		{"⚡ Quick exec", func() tea.Model { return newQuickExecModel(cfg) }},
		{"💾 Backup", func() tea.Model { return newBackupModel(cfg) }},
		{"👁 Monitor", func() tea.Model { return newMonitorModel(cfg) }},
	}
	return m
}

func (m MenuModel) Init() tea.Cmd { return nil }

func (m MenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.options)-1 {
				m.cursor++
			}
		case "enter":
			return m, switchTo(m.options[m.cursor].make())
		case "q":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m MenuModel) View() string {
	var b string
	b += titleStyle.Render(" NetMon ") + "\n\n"
	for i, opt := range m.options {
		cursor := "  "
		style := dimStyle
		if i == m.cursor {
			cursor = "▸ "
			style = titleStyle
		}
		b += cursor + style.Render(opt.label) + "\n"
	}
	b += "\n" + dimStyle.Render("↑/↓ navigate · enter select · q quit")
	return b
}

// ─── Stub screen (backup / monitor / quick exec) ───

type stubModel struct {
	title string
}

func (m stubModel) Init() tea.Cmd { return nil }
func (m stubModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" || msg.String() == "q" || msg.String() == "b" {
			return m, backToMenu()
		}
	}
	return m, nil
}
func (m stubModel) View() string {
	return titleStyle.Render(m.title) + "\n\n" +
		dimStyle.Render("Coming soon — use CLI mode for now: netmon "+m.title) + "\n\n" +
		dimStyle.Render("esc/b to go back")
}

// ─── Helpers ───

func fmtPortStatus(s string) string {
	switch {
	case s == "err-disabled":
		return errStyle.Render(s)
	case s == "connected":
		return okStyle.Render(s)
	case s == "disabled":
		return warnStyle.Render(s)
	default:
		return s
	}
}

var _ = fmt.Sprintf // keep fmt imported until used
