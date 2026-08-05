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
	tuiProgram = tea.NewProgram(newRootModel(cfg), tea.WithAltScreen(), tea.WithContext(ctx))
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

// navItem - one screen inside a domain
type navItem struct {
	label string
	make  func() tea.Model
}

// navDomain - one domain column (left panel). Items = right panel.
type navDomain struct {
	label string
	items []navItem
}

// domains - domain-based menu structure (lazygit-style).
// Only domains that have items are shown.
var domains = []navDomain{
	{
		label: "Dashboard",
		items: []navItem{
			{"Overview", func() tea.Model { return newOverviewModel(cfgGlobal) }},
		},
	},
	{
		label: "Assets",
		items: []navItem{
			{"Devices", func() tea.Model { return newDeviceListModel(cfgGlobal) }},
		},
	},
	{
		label: "Network",
		items: []navItem{
			{"Switches", func() tea.Model { return newDeviceListModelFiltered(cfgGlobal, "cisco") }},
		},
	},
	{
		label: "Operations",
		items: []navItem{
			{"Quick Exec", func() tea.Model { return newQuickExecModel(cfgGlobal) }},
			{"Port Fix", func() tea.Model { return newPortFixModel(cfgGlobal) }},
			{"Backup", func() tea.Model { return newBackupModel(cfgGlobal) }},
		},
	},
	{
		label: "Monitoring",
		items: []navItem{
			{"Status", func() tea.Model { return newMonitorModel(cfgGlobal) }},
		},
	},
}

// cfgGlobal - config shared by nav factories (set at startup)
var cfgGlobal *config.Config

// NavModel - two-panel navigation: domains left, items right.
type NavModel struct {
	cfg      *config.Config
	dCursor  int // domain cursor (left panel)
	iCursor  int // item cursor (right panel)
	focus    int // 0 = left, 1 = right
	selected int // domain whose items are shown (-1 = none)
}

func newMenuModel(cfg *config.Config) *NavModel {
	cfgGlobal = cfg
	return &NavModel{cfg: cfg, selected: -1}
}

func (m NavModel) Init() tea.Cmd { return nil }

func (m *NavModel) currentDomain() *navDomain {
	if m.selected < 0 || m.selected >= len(domains) {
		return nil
	}
	return &domains[m.selected]
}

func (m NavModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?":
			return m, switchTo(helpModel{parent: m, keys: menuKeys})
		case "up", "k":
			if m.focus == 0 {
				if m.dCursor > 0 {
					m.dCursor--
					m.iCursor = 0
				}
			} else {
				if m.iCursor > 0 {
					m.iCursor--
				}
			}
		case "down", "j":
			if m.focus == 0 {
				if m.dCursor < len(domains)-1 {
					m.dCursor++
					m.iCursor = 0
				}
			} else {
				if d := m.currentDomain(); d != nil && m.iCursor < len(d.items)-1 {
					m.iCursor++
				}
			}
		case "left", "h":
			if m.focus == 1 {
				m.focus = 0
			}
		case "right", "l", "enter", "tab":
			if m.focus == 0 {
				// show items of hovered domain, focus right
				m.selected = m.dCursor
				m.focus = 1
				m.iCursor = 0
			} else if m.focus == 1 {
				if msg.String() == "tab" {
					m.focus = 0
					break
				}
				// open the screen
				if d := m.currentDomain(); d != nil && m.iCursor < len(d.items) {
					return m, switchTo(d.items[m.iCursor].make())
				}
			}
		case "esc", "b":
			if m.focus == 1 {
				m.focus = 0
			} else {
				m.selected = -1
			}
		case "g", "home":
			if m.focus == 0 {
				m.dCursor = 0
				m.iCursor = 0
			} else {
				m.iCursor = 0
			}
		case "G", "end":
			if m.focus == 0 {
				m.dCursor = len(domains) - 1
				m.iCursor = 0
			} else {
				if d := m.currentDomain(); d != nil {
					m.iCursor = len(d.items) - 1
				}
			}
		}
	}
	return m, nil
}

var menuKeys = [][2]string{
	{"↑/k ↓/j", "navigate"},
	{"→/l / enter", "select domain / open item"},
	{"tab", "switch panel"},
	{"←/h / esc", "back to domains"},
	{"g/G", "top/bottom"},
	{"?", "help"},
	{"q", "quit"},
}

func (m NavModel) View() string {
	var b strings.Builder

	// banner
	bannerStyle := lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	for _, line := range banner {
		b.WriteString(bannerStyle.Render(line) + "\n")
	}
	b.WriteString("\n")

	// two-panel layout: domains left, items right
	domainCol := lipgloss.NewStyle().Width(20).Padding(0, 1)
	itemCol := lipgloss.NewStyle().Width(30).Padding(0, 1)

	var lb, rb strings.Builder
	for i, d := range domains {
		style := dimStyle
		marker := "  "
		if i == m.dCursor {
			style = accStyle
			marker = "▸ "
		}
		lb.WriteString(marker + style.Render(d.label) + "\n")
	}

	if d := m.currentDomain(); d != nil {
		// right panel header
		rb.WriteString(dimStyle.Render(d.label) + "\n\n")
		for i, item := range d.items {
			style := dimStyle
			marker := "  "
			if i == m.iCursor && m.focus == 1 {
				style = accStyle
				marker = "▸ "
			}
			rb.WriteString(marker + style.Render(item.label) + "\n")
		}
	} else {
		rb.WriteString(dimStyle.Render("→ select a domain") + "\n")
	}

	// panels side by side
	if m.focus == 0 {
		b.WriteString(accStyle.Render(domainCol.Render(lb.String())))
		b.WriteString(dimStyle.Render(itemCol.Render(rb.String())))
	} else {
		b.WriteString(dimStyle.Render(domainCol.Render(lb.String())))
		b.WriteString(accStyle.Render(itemCol.Render(rb.String())))
	}

	b.WriteString("\n" + renderFooter(
		"↑/↓", "navigate", "→/enter", "open", "tab", "switch panel", "←/esc", "back", "?", "help", "q", "quit"))
	return b.String()
}
