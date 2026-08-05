package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/farshidmousavii/bidar/internal/config"
)

// deviceListMsg - holds the loaded device list (simulated async load)
type deviceListMsg struct {
	devices []config.DeviceConfig
	err     error
}

type deviceListModel struct {
	cfg        *config.Config
	devices    []config.DeviceConfig
	loading    bool
	err        error
	cursor     int
	offset     int
	pageSize   int
	query      string
	filtered   []config.DeviceConfig
	filtering  bool // in filter input mode
	configPath string
	// form state (add/edit)
	formMode   string // "", "add", "edit", "confirm-delete"
	formField  int
	formFields []string // current field values
	formErr    string
}

// formLabels - device form fields in order
var formLabels = []string{"Name", "IP", "Vendor (cisco/mikrotik)", "Credential", "Port (22)"}

func newDeviceListModel(cfg *config.Config) *deviceListModel {
	return newDeviceListModelFiltered(cfg, "")
}

// newDeviceListModelFiltered - device list with an initial filter query
// (e.g. "cisco" for Switches). User can change/clear it freely afterwards.
func newDeviceListModelFiltered(cfg *config.Config, initialFilter string) *deviceListModel {
	m := &deviceListModel{cfg: cfg, pageSize: 15, loading: true, configPath: configPath}
	m.load()
	if initialFilter != "" {
		m.query = initialFilter
		m.applyFilter()
	}
	return m
}

func (m *deviceListModel) startForm(mode string) {
	m.formMode = mode
	m.formField = 0
	m.formErr = ""
	if mode == "edit" && len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		d := m.filtered[m.cursor]
		m.formFields = []string{d.Name, d.IP, d.Vendor, d.Credential, d.Port}
	} else {
		m.formFields = []string{"", "", "cisco", "", "22"}
	}
}

// formDevice - build DeviceConfig from form fields (validate)
func (m *deviceListModel) formDevice() (config.DeviceConfig, error) {
	d := config.DeviceConfig{
		Name:       strings.TrimSpace(m.formFields[0]),
		IP:         strings.TrimSpace(m.formFields[1]),
		Vendor:     strings.ToLower(strings.TrimSpace(m.formFields[2])),
		Credential: strings.TrimSpace(m.formFields[3]),
		Port:       strings.TrimSpace(m.formFields[4]),
	}
	if d.Name == "" || d.IP == "" {
		return d, fmt.Errorf("name and IP are required")
	}
	if d.Vendor != "cisco" && d.Vendor != "mikrotik" {
		return d, fmt.Errorf("vendor must be cisco or mikrotik")
	}
	if d.Credential == "" {
		d.Credential = "default"
	}
	if d.Port == "" {
		d.Port = "22"
	}
	return d, nil
}

func (m *deviceListModel) saveDevice(d config.DeviceConfig, isEdit bool, oldName string) error {
	if isEdit {
		for i := range m.cfg.Devices {
			if m.cfg.Devices[i].Name == oldName {
				m.cfg.Devices[i] = d
				break
			}
		}
	} else {
		m.cfg.Devices = append(m.cfg.Devices, d)
	}
	if err := m.cfg.Save(m.configPath); err != nil {
		return err
	}
	m.load() // refresh from cfg
	m.applyFilter()
	return nil
}

func (m *deviceListModel) deleteDevice() error {
	if len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
		return fmt.Errorf("no device selected")
	}
	name := m.filtered[m.cursor].Name
	for i := range m.cfg.Devices {
		if m.cfg.Devices[i].Name == name {
			m.cfg.Devices = append(m.cfg.Devices[:i], m.cfg.Devices[i+1:]...)
			break
		}
	}
	if err := m.cfg.Save(m.configPath); err != nil {
		return err
	}
	m.load()
	m.applyFilter()
	return nil
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
	} else {
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
	}
	// reset viewport to top whenever filter changes
	m.cursor = 0
	m.offset = 0
}

func (m deviceListModel) Init() tea.Cmd { return nil }

func (m deviceListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// form mode: handle input
		if m.formMode == "confirm-delete" {
			switch msg.String() {
			case "y", "Y":
				if err := m.deleteDevice(); err != nil {
					m.formErr = err.Error()
				}
				m.formMode = ""
				return m, nil
			case "n", "N", "esc", "q":
				m.formMode = ""
				return m, nil
			}
			return m, nil
		}
		if m.formMode == "add" || m.formMode == "edit" {
			switch msg.String() {
			case "esc", "q":
				m.formMode = ""
				return m, nil
			case "tab", "enter":
				if m.formField < len(m.formFields)-1 {
					m.formField++
				} else {
					// save on last field
					d, err := m.formDevice()
					if err != nil {
						m.formErr = err.Error()
						return m, nil
					}
					oldName := ""
					if m.formMode == "edit" && len(m.filtered) > 0 && m.cursor < len(m.filtered) {
						oldName = m.filtered[m.cursor].Name
					}
					if err := m.saveDevice(d, m.formMode == "edit", oldName); err != nil {
						m.formErr = err.Error()
						return m, nil
					}
					m.formMode = ""
					return m, nil
				}
			case "shift+tab":
				if m.formField > 0 {
					m.formField--
				}
			case "backspace":
				if len(m.formFields[m.formField]) > 0 {
					m.formFields[m.formField] = m.formFields[m.formField][:len(m.formFields[m.formField])-1]
				}
			case "ctrl+c":
				return m, tea.Quit
			default:
				if len(msg.String()) == 1 {
					m.formFields[m.formField] += msg.String()
				}
			}
			return m, nil
		}
		// filter input mode: everything goes to the query
		if m.filtering {
			switch msg.String() {
			case "enter", "esc", "/":
				m.filtering = false
			case "backspace":
				if len(m.query) > 0 {
					m.query = m.query[:len(m.query)-1]
					m.applyFilter()
				}
			case "ctrl+c":
				return m, tea.Quit
			default:
				if len(msg.String()) == 1 {
					m.query += msg.String()
					m.applyFilter()
				}
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "esc", "b":
			return m, backToMenu()
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				// scroll up if cursor left the window
				if m.cursor < m.offset {
					m.offset = m.cursor
				}
			}
		case "down", "j":
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
				// scroll down if cursor passed window end
				end := m.offset + m.pageSize
				if end > len(m.filtered) {
					end = len(m.filtered)
				}
				if m.cursor >= end {
					m.offset++
				}
			}
		case "pgup", "left":
			m.offset -= m.pageSize
			if m.offset < 0 {
				m.offset = 0
			}
			m.cursor = m.offset
		case "pgdown", "right":
			if len(m.filtered) > 0 && m.offset+m.pageSize < len(m.filtered) {
				m.offset += m.pageSize
				m.cursor = m.offset
			}
		case "g", "home":
			m.cursor = 0
			m.offset = 0
		case "G", "end":
			if len(m.filtered) > 0 {
				m.cursor = len(m.filtered) - 1
				m.offset = max(0, len(m.filtered)-m.pageSize)
			}
		case "/":
			// enter filter input mode
			m.filtering = true
			return m, nil
		case "a":
			m.startForm("add")
			return m, nil
		case "e":
			if len(m.filtered) > 0 {
				m.startForm("edit")
			}
			return m, nil
		case "d":
			if len(m.filtered) > 0 {
				m.formMode = "confirm-delete"
				m.formErr = ""
			}
			return m, nil
		default:
			// ignore stray chars when not filtering
		}
	}
	return m, nil
}

func (m deviceListModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" Device List ") + "\n\n")

	// form view (add/edit)
	if m.formMode == "add" || m.formMode == "edit" {
		b.WriteString(accStyle.Render(" "+m.formMode+" device ") + "\n\n")
		for i, label := range formLabels {
			cursor := "  "
			if i == m.formField {
				cursor = "▸ "
			}
			val := m.formFields[i]
			if i == m.formField {
				val += "▌"
			}
			b.WriteString(fmt.Sprintf("%s %-28s %s\n", cursor, dimStyle.Render(label), accStyle.Render(val)))
		}
		if m.formErr != "" {
			b.WriteString("\n" + errStyle.Render("✗ "+m.formErr) + "\n")
		}
		b.WriteString("\n" + renderFooter("tab/enter", "next · save", "shift+tab", "prev", "esc", "cancel"))
		return b.String()
	}

	// delete confirmation
	if m.formMode == "confirm-delete" {
		name := ""
		if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
			name = m.filtered[m.cursor].Name
		}
		b.WriteString(warnStyle.Render("Delete device "+name+"? This edits config.yaml (backup saved).") + "\n\n")
		b.WriteString(renderFooter("y", "delete", "n/esc", "cancel"))
		return b.String()
	}

	// filter input bar — shown whenever filtering
	if m.filtering {
		b.WriteString(accStyle.Render("/ "+m.query+"▌") + "  " +
			dimStyle.Render("filter (enter done · esc cancel)") + "\n\n")
	}

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
	b.WriteString("\n" + renderFooter("↑/↓", "nav", "←/→", "page", "/", "filter", "a", "add", "e", "edit", "d", "del", "esc", "back"))
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
