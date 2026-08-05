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

// banner - ASCII art logo (small, single color)
var banner = []string{
	" _    _    _          ",
	"| |__(_)__| |__ _ _ _ ",
	"| '_ \\ / _` / _` | '_|",
	"|_.__/_\\__,_\\__,_|_|",
}

// cfgGlobal - config shared by screen factories (set at startup)
var cfgGlobal *config.Config

// ─── Shell layout: header + left nav + content + status bar ───

// shellItem - one screen inside a domain (right panel content)
type shellItem struct {
	label string
	make  func() tea.Model
}

// shellDomain - one domain column (left nav, always visible)
type shellDomain struct {
	label string
	items []shellItem
}

// domains - domain structure. Left nav shows ALL domains (empty ones
// render as placeholders); items are only the real screens.
var domains = []shellDomain{
	{
		label: "Dashboard",
		items: []shellItem{
			{"Overview", func() tea.Model { return newOverviewModel(cfgGlobal) }},
		},
	},
	{
		label: "Assets",
		items: []shellItem{
			{"Devices", func() tea.Model { return newDeviceListModel(cfgGlobal) }},
		},
	},
	{
		label: "Network",
		items: []shellItem{
			{"Switches", func() tea.Model { return newSwitchListModel(cfgGlobal) }},
		},
	},
	{
		label: "Discovery",
		items: []shellItem{},
	},
	{
		label: "Operations",
		items: []shellItem{
			{"Quick Exec", func() tea.Model { return newQuickExecModel(cfgGlobal) }},
			{"Port Fix", func() tea.Model { return newPortFixModel(cfgGlobal) }},
			{"Backup", func() tea.Model { return newBackupModel(cfgGlobal) }},
		},
	},
	{
		label: "Monitoring",
		items: []shellItem{
			{"Status", func() tea.Model { return newMonitorModel(cfgGlobal) }},
		},
	},
	{
		label: "Reports",
		items: []shellItem{},
	},
	{
		label: "Settings",
		items: []shellItem{},
	},
}

// ShellModel - the app shell: persistent left nav + content area.
type ShellModel struct {
	cfg      *config.Config
	dCursor  int
	iCursor  int
	focus    int // 0 = nav, 1 = content
	selected int // domain with items shown (-1 = none)
	content  tea.Model
	width    int
	height   int
}

func newRootModel(cfg *config.Config) *ShellModel {
	cfgGlobal = cfg
	return &ShellModel{cfg: cfg, selected: -1}
}

func (m ShellModel) Init() tea.Cmd { return nil }

func (m *ShellModel) currentDomain() *shellDomain {
	if m.selected < 0 || m.selected >= len(domains) {
		return nil
	}
	return &domains[m.selected]
}

func (m *ShellModel) openItem() tea.Cmd {
	if d := m.currentDomain(); d != nil && m.iCursor < len(d.items) {
		m.content = d.items[m.iCursor].make()
		m.focus = 1
		return m.content.Init()
	}
	return nil
}

func (m ShellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// content screen active (full-screen mode): pass through
	if m.content != nil {
		switch v := msg.(type) {
		case switchMsg:
			// nil = back to shell; model = nested screen (e.g. Device Details)
			if v.model == nil {
				m.content = nil
			} else {
				m.content = v.model
				return m, m.content.Init()
			}
			return m, nil
		case tea.KeyMsg:
			k := v.String()
			if k == "esc" || k == "q" || k == "b" {
				m.content = nil
				return m, nil
			}
		}
		updated, cmd := m.content.Update(msg)
		m.content = updated
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
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
					return m, m.openItem()
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
		default:
			// 1-8 jump to domain
			if len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '8' {
				idx := int(msg.String()[0] - '1')
				if idx < len(domains) {
					m.dCursor = idx
					m.iCursor = 0
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
	{"1-8", "jump to domain"},
	{"g/G", "top/bottom"},
	{"?", "help"},
	{"q", "quit"},
}

// headerBar - banner + version + user
func (m ShellModel) headerBar() string {
	var b strings.Builder
	bannerStyle := lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	for _, line := range banner {
		b.WriteString(bannerStyle.Render(line) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// statusBar - bottom bar: context shortcuts left, global right
func (m ShellModel) statusBar() string {
	left := "↑/↓ nav · →/enter open · esc back"
	right := "? help · q quit"
	barStyle := lipgloss.NewStyle().Foreground(colMuted).Background(colBg)
	return barStyle.Render(" " + left + strings.Repeat(" ", max(0, m.width-len(left)-len(right)-2)) + right + " ")
}

func (m ShellModel) View() string {
	var b strings.Builder

	// header
	b.WriteString(m.headerBar())

	// if a content screen is open, show it full-width below header
	if m.content != nil {
		b.WriteString(m.content.View())
		b.WriteString("\n" + m.statusBar())
		return b.String()
	}

	// left nav column (always visible)
	navWidth := 18
	navStyle := lipgloss.NewStyle().Width(navWidth).Padding(0, 1)

	var nav strings.Builder
	nav.WriteString(dimStyle.Render(" MODULES ") + "\n\n")
	for i, d := range domains {
		style := dimStyle
		marker := "  "
		if i == m.dCursor {
			style = accStyle
			marker = "▸ "
		}
		nav.WriteString(marker + style.Render(d.label) + "\n")
	}

	// content panel: items of selected domain, or hint
	var content strings.Builder
	if d := m.currentDomain(); d != nil && len(d.items) > 0 {
		content.WriteString(accStyle.Render(" "+d.label+" ") + "\n\n")
		for i, item := range d.items {
			style := dimStyle
			marker := "  "
			if i == m.iCursor && m.focus == 1 {
				style = accStyle
				marker = "▸ "
			}
			content.WriteString(marker + style.Render(item.label) + "\n")
		}
	} else if m.selected >= 0 {
		content.WriteString(dimStyle.Render(" "+domains[m.selected].label+" — coming soon") + "\n")
	} else {
		content.WriteString(dimStyle.Render("→ select a module") + "\n")
	}

	// panels side by side
	if m.focus == 0 {
		b.WriteString(accStyle.Render(navStyle.Render(nav.String())))
	} else {
		b.WriteString(dimStyle.Render(navStyle.Render(nav.String())))
	}
	b.WriteString(content.String())

	b.WriteString("\n" + m.statusBar())
	return b.String()
}

// ─── Shared messages ───

type switchMsg struct{ model tea.Model }

func switchTo(model tea.Model) tea.Cmd {
	return func() tea.Msg { return switchMsg{model: model} }
}

func backToMenu() tea.Cmd {
	return func() tea.Msg { return switchMsg{model: nil} }
}
