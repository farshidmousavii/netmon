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

	// shell frame styles
	frameStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colSubtle)
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colAccent)
	statusBarStyle = lipgloss.NewStyle().
			Foreground(colMuted)
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

// banner - ASCII art logo (big)
var banner = []string{
	" ____  _     _            ",
	"| __ )(_) __| | __ _ _ __ ",
	"|  _ \\| |/ _` |/ _` | '__|",
	"| |_) | | (_| | (_| | |   ",
	"|____/|_|\\__,_|\\__,_|_|   ",
}

// bannerVersion - ASCII "v0.1"
var bannerVersion = []string{
	"     __   _ ",
	"__ __/  \\ / |",
	"\\ V / () || |",
	" \\_/ \\__(_)_|",
}

// bannerAuthor - ASCII "Farshid Mousavi"
var bannerAuthor = []string{
	" ___            _    _    _   __  __                       _ ",
	"| __|_ _ _ _ __| |_ (_)__| | |  \\/  |___ _  _ ___ __ ___ _(_)",
	"| _/ _` | '_(_-< ' \\| / _` | | |\\/| / _ \\ || (_-</ _` \\ V / |",
	"|_|\\__,_|_| /__/_||_|_\\__,_| |_|  |_\\___/\\_,_/__/\\__,_|\\_/|_|",
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
			{"Health", func() tea.Model { return newHealthModel(cfgGlobal) }},
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
	// breadcrumb trail (current location)
	crumbDomain string
	crumbItem   string
	crumbScreen string
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
		m.crumbDomain = d.label
		m.crumbItem = d.items[m.iCursor].label
		m.crumbScreen = ""
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
				m.crumbScreen = ""
			} else {
				m.content = v.model
				// breadcrumb for nested screens
				switch v.model.(type) {
				case *deviceDetailsModel:
					m.crumbScreen = "Device Details"
				case *switchDetailsModel:
					m.crumbScreen = "Switch Details"
				case *quickExecModel:
					m.crumbScreen = "Quick Exec"
				case *backupModel:
					m.crumbScreen = "Backup"
				case *portFixModel:
					m.crumbScreen = "Port Fix"
				default:
					m.crumbScreen = ""
				}
				return m, m.content.Init()
			}
			return m, nil
		case tea.KeyMsg:
			k := v.String()
			// global back: b/esc/q close the screen and return to nav
			if k == "esc" || k == "q" || k == "b" {
				// but q quits when at nav level... handled below
				m.content = nil
				m.crumbScreen = ""
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

// headerBar - big ASCII banner + version + author + user, framed
func (m ShellModel) headerBar() string {
	var b strings.Builder
	bannerStyle := lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	dimBanner := lipgloss.NewStyle().Foreground(colMuted).Bold(true)

	// big BIDAR banner
	for _, line := range banner {
		b.WriteString(bannerStyle.Render(line) + "\n")
	}
	// side by side: version + author (pad version to author width)
	version := strings.Join(bannerVersion, "\n")
	author := strings.Join(bannerAuthor, "\n")
	verW := lipgloss.Width(version)
	authW := lipgloss.Width(author)
	if verW < authW {
		version = lipgloss.NewStyle().Width(authW).Render(version)
	}
	b.WriteString(dimBanner.Render(version) + bannerStyle.Render(author) + "\n")
	b.WriteString("\n")
	return b.String()
}

// statusBar - bottom bar: global shortcuts, always the same everywhere
func (m ShellModel) statusBar() string {
	left := renderKey("b") + dimStyle.Render(" back ") +
		renderKey("r") + dimStyle.Render(" refresh ") +
		renderKey("/") + dimStyle.Render(" search ") +
		renderKey("?") + dimStyle.Render(" help ")
	right := renderKey("q") + dimStyle.Render(" quit ")
	bar := " " + left + strings.Repeat(" ", max(0, m.width-len(left)-len(right)-6)) + right
	barStyle := lipgloss.NewStyle().
		Foreground(colText).
		Background(colBg).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colSubtle)
	return barStyle.Render(bar)
}

// frame - wrap content in a rounded border box
func (m ShellModel) frame(inner string) string {
	inner = strings.TrimSuffix(inner, "\n")
	return frameStyle.Width(max(0, m.width-2)).Render(inner)
}

// breadcrumb - current location trail, e.g. "Modules / Assets / Devices"
func (m ShellModel) breadcrumb() string {
	parts := []string{"Modules"}
	if m.crumbDomain != "" {
		parts = append(parts, m.crumbDomain)
	}
	if m.crumbItem != "" {
		parts = append(parts, m.crumbItem)
	}
	if m.crumbScreen != "" {
		parts = append(parts, m.crumbScreen)
	}
	for i, p := range parts {
		if i == len(parts)-1 {
			parts[i] = accStyle.Render(p)
		} else {
			parts[i] = dimStyle.Render(p)
		}
	}
	return strings.Join(parts, dimStyle.Render(" / "))
}

func (m ShellModel) View() string {
	var b strings.Builder

	// header (always visible, framed)
	b.WriteString(m.headerBar())
	b.WriteString(strings.Repeat("─", max(0, m.width)) + "\n")

	// content screen active: full-width below header
	if m.content != nil {
		b.WriteString(" " + m.breadcrumb() + "\n")
		b.WriteString(dimStyle.Render(strings.Repeat("─", max(0, m.width))) + "\n")
		b.WriteString(m.content.View())
		b.WriteString("\n" + m.statusBar())
		return b.String()
	}

	// breadcrumb above the main frame
	b.WriteString(" " + m.breadcrumb() + "\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", max(0, m.width))) + "\n")

	// panels side by side with a vertical separator.
	// Build nav column as plain lines (fixed width), then interleave.
	navWidth := 20
	var navLines []string
	navLines = append(navLines, headerStyle.Render(" Modules "))
	navLines = append(navLines, "")
	for i, d := range domains {
		style := dimStyle
		marker := "  "
		if i == m.dCursor {
			style = accStyle
			marker = "▸ "
		}
		navLines = append(navLines, marker+style.Render(d.label))
	}

	var contentLines []string
	if d := m.currentDomain(); d != nil && len(d.items) > 0 {
		contentLines = append(contentLines, headerStyle.Render(" "+d.label+" "))
		contentLines = append(contentLines, dimStyle.Render(strings.Repeat("─", max(0, m.width-navWidth-12))))
		contentLines = append(contentLines, "")
		for i, item := range d.items {
			style := dimStyle
			marker := "  "
			if i == m.iCursor && m.focus == 1 {
				style = accStyle
				marker = "▸ "
			}
			contentLines = append(contentLines, marker+style.Render(item.label))
		}
	} else if m.selected >= 0 {
		contentLines = append(contentLines, headerStyle.Render(" "+domains[m.selected].label+" "))
		contentLines = append(contentLines, dimStyle.Render(strings.Repeat("─", max(0, m.width-navWidth-12))))
		contentLines = append(contentLines, "")
		contentLines = append(contentLines, dimStyle.Render("  Coming soon"))
	} else {
		contentLines = append(contentLines, dimStyle.Render("→ select a module"))
	}

	// pad shorter column so separator spans full height
	for len(contentLines) < len(navLines) {
		contentLines = append(contentLines, "")
	}
	for len(navLines) < len(contentLines) {
		navLines = append(navLines, "")
	}

	leftStyle := dimStyle
	if m.focus == 0 {
		leftStyle = accStyle
	}
	sep := dimStyle.Render("│")
	var main strings.Builder
	for i := range navLines {
		main.WriteString(leftStyle.Render(lipgloss.NewStyle().Width(navWidth).Padding(0, 1).Render(navLines[i])))
		main.WriteString(sep)
		main.WriteString(contentLines[i] + "\n")
	}
	b.WriteString(m.frame(strings.TrimSuffix(main.String(), "\n")))
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
