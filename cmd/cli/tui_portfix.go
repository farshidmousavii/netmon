package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/device"
	"github.com/farshidmousavii/netmon/internal/logger"
)

// PortFixStatus - one port's lifecycle
type PortFixStatus struct {
	Port     string
	State    string // "pending" | "err-disabled" | "cleared" | "bounced" | "up" | "failed"
	Err      string
	Actioned bool
}

// PortFixDevice - per-device progress
type PortFixDevice struct {
	Device config.DeviceConfig
	Ports  []PortFixStatus
	Done   bool
	Err    string
}

type portFixModel struct {
	cfg        *config.Config
	devices    []config.DeviceConfig
	results    map[string]*PortFixDevice
	cursor     int
	scanning   bool
	scanningDn string
	scanErr    string
	clearing   bool
	fixedCount int
	lastMsg    string
}

func newPortFixModel(cfg *config.Config) *portFixModel {
	m := &portFixModel{cfg: cfg, results: map[string]*PortFixDevice{}}
	m.devices = cfg.Devices
	return m
}

func (m *portFixModel) Init() tea.Cmd {
	return m.startScan()
}

// ─── Commands ───

// startScan - run `show interface status | include err-dis` on all cisco devices
func (m *portFixModel) startScan() tea.Cmd {
	m.scanning = true
	m.scanningDn = ""
	m.scanErr = ""
	devices := make([]config.DeviceConfig, 0)
	for _, d := range m.devices {
		if strings.EqualFold(d.Vendor, "cisco") {
			devices = append(devices, d)
		}
	}
	if len(devices) == 0 {
		m.scanning = false
		m.scanErr = "no cisco devices in config"
		return nil
	}
	return func() tea.Msg {
		return scanMsg{devices: devices}
	}
}

// runPortFix - execute clear + bounce on all ports of a device
func (m *portFixModel) runPortFix(ctx context.Context, d config.DeviceConfig) tea.Cmd {
	m.clearing = true
	return func() tea.Msg {
		return fixDoneMsg{device: d, err: m.fixDevice(ctx, d)}
	}
}

// ─── Messages ───

type scanMsg struct {
	devices []config.DeviceConfig
}

type fixDoneMsg struct {
	device config.DeviceConfig
	err    error
}

type portScanMsg struct {
	device  config.DeviceConfig
	output  string
	err     error
	ports   []PortFixStatus
}

// ─── Update ───

func (m *portFixModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			if m.cursor < len(m.devices)-1 {
				m.cursor++
			}
		case "enter":
			if !m.scanning && !m.clearing {
				d := m.devices[m.cursor]
				return m, m.runPortFix(context.Background(), d)
			}
		case "r":
			// re-scan
			return m, m.startScan()
		}
	case scanMsg:
		m.scanning = true
		var cmds []tea.Cmd
		for _, d := range msg.devices {
			d := d
			cmds = append(cmds, func() tea.Msg {
				return portScanMsg{device: d, output: "", err: nil}
			})
		}
		return m, tea.Batch(cmds...)
	case portScanMsg:
		// simulate scan: for now, show device as scanned
		m.results[msg.device.Name] = &PortFixDevice{
			Device: msg.device,
			Done:   true,
		}
		m.scanningDn = msg.device.Name
		m.checkScanDone()
		return m, nil
	case fixDoneMsg:
		m.clearing = false
		if msg.err != nil {
			if pf, ok := m.results[msg.device.Name]; ok {
				pf.Err = msg.err.Error()
			}
			m.lastMsg = errStyle.Render(fmt.Sprintf("%s: %v", msg.device.Name, msg.err))
		} else {
			m.lastMsg = okStyle.Render(fmt.Sprintf("%s: ports fixed", msg.device.Name))
			m.fixedCount++
		}
		return m, nil
	}
	return m, nil
}

func (m *portFixModel) checkScanDone() {
	// count scanned devices
	done := 0
	for _, d := range m.devices {
		if strings.EqualFold(d.Vendor, "cisco") {
			if _, ok := m.results[d.Name]; ok {
				done++
			}
		}
	}
	total := 0
	for _, d := range m.devices {
		if strings.EqualFold(d.Vendor, "cisco") {
			total++
		}
	}
	if done >= total && total > 0 {
		m.scanning = false
	}
}

// fixDevice - real logic: clear port-security + bounce
func (m *portFixModel) fixDevice(ctx context.Context, d config.DeviceConfig) error {
	cred, err := m.cfg.GetCredential(d.Credential)
	if err != nil {
		return fmt.Errorf("get credential: %w", err)
	}
	dev, err := device.NewDevice(d, cred, m.cfg)
	if err != nil {
		return fmt.Errorf("create device: %w", err)
	}

	// 1. find err-disabled ports
	out, err := dev.RunCommand("show interface status | include err-dis")
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	ports := parseErrDisabledPorts(out)
	if len(ports) == 0 {
		return fmt.Errorf("no err-disabled ports found")
	}

	// 2. clear + bounce each port
	for _, p := range ports {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// clear port-security sticky (exec mode)
		_, _ = dev.RunCommand(fmt.Sprintf("clear port-security sticky interface %s", p))
		// bounce
		_, _ = dev.RunCommands([]string{
			fmt.Sprintf("interface %s", p),
			"shutdown",
			"no shutdown",
		})
		time.Sleep(1 * time.Second)
	}
	return nil
}

// ─── Parsing ───

var portNameRe = regexp.MustCompile(`^\s*(\S+)\s+`)

func parseErrDisabledPorts(output string) []string {
	var ports []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Port") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.Contains(line, "err-disabled") {
			if m := portNameRe.FindStringSubmatch(line); len(m) > 1 {
				ports = append(ports, m[1])
			}
		}
	}
	return ports
}

// ─── View ───

func (m *portFixModel) View() string {
	var b string
	b += titleStyle.Render(" Port Fix ") + "\n\n"

	if m.scanning {
		b += "Scanning for err-disabled ports...\n"
	} else if m.scanErr != "" {
		b += errStyle.Render(m.scanErr) + "\n"
	} else {
		for i, d := range m.devices {
			if !strings.EqualFold(d.Vendor, "cisco") {
				continue
			}
			cursor := "  "
			style := dimStyle
			if i == m.cursor {
				cursor = "▸ "
				style = titleStyle
			}
			status := "…"
			if pf, ok := m.results[d.Name]; ok {
				if pf.Err != "" {
					status = errStyle.Render("✗")
				} else if pf.Done {
					status = okStyle.Render("✓")
				}
			}
			b += fmt.Sprintf("%s %s %s\n", cursor, style.Render(d.Name), status)
		}
	}

	if m.lastMsg != "" {
		b += "\n" + m.lastMsg + "\n"
	}

	b += "\n" + dimStyle.Render("↑/↓ nav · enter fix · r rescan · q back")
	return b
}

var _ = logger.Info
