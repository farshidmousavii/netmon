package cli

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/farshidmousavii/bidar/internal/config"
)

// ─── Semantic color palette (Catppuccin Mocha-inspired) ───
// Color = meaning, not decoration. Max 4 hues + neutrals.
// All tokens map to ANSI 256 so they survive any terminal theme.

var (
	// neutrals
	colText   = lipgloss.Color("252") // light gray text
	colMuted  = lipgloss.Color("245") // dim secondary
	colSubtle = lipgloss.Color("240") // borders, separators
	colBg     = lipgloss.Color("235") // deep background

	// semantic hues
	colPrimary = lipgloss.Color("75")  // blue - info, active, links
	colSuccess = lipgloss.Color("114") // green - ok, online
	colError   = lipgloss.Color("203") // red - fail, offline
	colWarning = lipgloss.Color("214") // amber - pending, warn
	colAccent  = lipgloss.Color("141") // purple - selection, focus
)

// ─── Shared styles (immutable, package-level) ───

var (
	// title bar: bold accent on dark pill
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colAccent).
			Padding(0, 1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colSubtle).
			BorderBottom(true)

	// section header
	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colPrimary).
			MarginBottom(1)

	// footer hint bar
	footerStyle = lipgloss.NewStyle().
			Foreground(colMuted).
			MarginTop(1)

	// key hint highlight
	keyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colPrimary)

	// status colors
	okStyle   = lipgloss.NewStyle().Foreground(colSuccess)
	errStyle  = lipgloss.NewStyle().Foreground(colError)
	warnStyle = lipgloss.NewStyle().Foreground(colWarning)
	dimStyle  = lipgloss.NewStyle().Foreground(colMuted)
	textStyle = lipgloss.NewStyle().Foreground(colText)
	accStyle  = lipgloss.NewStyle().Bold(true).Foreground(colAccent)

	// device name emphasis
	deviceStyle = lipgloss.NewStyle().Bold(true).Foreground(colPrimary)
	portStyle   = lipgloss.NewStyle().Bold(true).Foreground(colWarning)
)

// renderKey - format a key as highlighted hint
func renderKey(k string) string {
	return keyStyle.Render(k)
}

// renderFooter - consistent footer with key hints
func renderFooter(hints ...string) string {
	var parts []string
	for i, h := range hints {
		if i%2 == 0 {
			parts = append(parts, renderKey(h))
		} else {
			parts = append(parts, dimStyle.Render(h))
		}
	}
	return footerStyle.Render(strings.Join(parts, " "))
}

// ─── Help model (toggled with ?) ───

type helpModel struct {
	parent tea.Model
	keys   [][2]string // key, action
}

func (m helpModel) Init() tea.Cmd { return nil }

func (m helpModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		if k.String() == "?" || k.String() == "esc" || k.String() == "q" || k.String() == "b" {
			return m.parent, nil // back to parent
		}
	}
	return m, nil
}

func (m helpModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" Help ") + "\n\n")
	for _, kv := range m.keys {
		b.WriteString("  " + renderKey(kv[0]) + dimStyle.Render("  "+kv[1]) + "\n")
	}
	b.WriteString("\n" + renderFooter("?", "help", "esc", "back", "q", "quit"))
	return b.String()
}

// runTUIEngine - main entry for TUI mode
func runTUIEngine(ctx context.Context, cfg *config.Config) error {
	tuiProgram = tea.NewProgram(newRootModel(cfg), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))
	_, err := tuiProgram.Run()
	return err
}

// tuiProgram - active bubbletea program, used to stream progress messages
// from worker goroutines (port fix steps) into the event loop.
var tuiProgram *tea.Program

// ─── Root model ───

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
	return func() tea.Msg { return switchMsg{model: nil} }
}

// banner - ASCII art logo (small, single color)
var banner = []string{
	" _    _    _          ",
	"| |__(_)__| |__ _ _ _ ",
	"| '_ \\ / _` / _` | '_|",
	"|_.__/_\\__,_\\__,_|_|",
}

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
		{"Overview", func() tea.Model { return newOverviewModel(cfg) }},
		{"Device list", func() tea.Model { return newDeviceListModel(cfg) }},
		{"Port fix", func() tea.Model { return newPortFixModel(cfg) }},
		{"Quick exec", func() tea.Model { return newQuickExecModel(cfg) }},
		{"Backup", func() tea.Model { return newBackupModel(cfg) }},
		{"Monitor", func() tea.Model { return newMonitorModel(cfg) }},
	}
	return m
}

func (m MenuModel) Init() tea.Cmd { return nil }

func (m MenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		e := tea.MouseEvent(msg)
		if e.Button == tea.MouseButtonLeft && e.Action == tea.MouseActionPress {
			// menu items start at Y=8 (banner 4 + blank + title + blank)
			idx := e.Y - 8
			if idx >= 0 && idx < len(m.options) {
				m.cursor = idx
				return m, switchTo(m.options[m.cursor].make())
			}
		}
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
		case "g", "home":
			m.cursor = 0
		case "G", "end":
			m.cursor = len(m.options) - 1
		case "enter":
			return m, switchTo(m.options[m.cursor].make())
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?":
			return m, switchTo(helpModel{parent: m, keys: menuKeys})
		}
	}
	return m, nil
}

var menuKeys = [][2]string{
	{"↑/k ↓/j", "navigate"},
	{"enter", "open screen"},
	{"g/G", "top/bottom"},
	{"?", "help"},
	{"q", "quit"},
}

func (m MenuModel) View() string {
	var b strings.Builder

	// ASCII art banner
	bannerStyle := lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	for _, line := range banner {
		b.WriteString(bannerStyle.Render(line) + "\n")
	}
	// small menu title under the banner
	b.WriteString("\n" + dimStyle.Render("─ main menu ─") + "\n\n")

	for i, opt := range m.options {
		cursor := "  "
		style := dimStyle
		if i == m.cursor {
			cursor = "▸ "
			style = accStyle
		}
		b.WriteString(cursor + style.Render(opt.label) + "\n")
	}

	b.WriteString("\n" + renderFooter(
		"↑/↓", "navigate", "enter", "open", "g/G", "top/bottom", "?", "help", "q", "quit"))
	return b.String()
}
