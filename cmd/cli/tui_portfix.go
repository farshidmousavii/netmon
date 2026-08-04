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
)

// fixPhase - workflow state
type fixPhase int

const (
	phasePickSwitch fixPhase = iota
	phasePickPort
	phaseRunning
	phaseDone
)

type portFixModel struct {
	cfg     *config.Config
	picker  *DevicePicker
	ppicker *PortPicker
	phase   fixPhase
	results []taskResult
}

func newPortFixModel(cfg *config.Config) *portFixModel {
	var cisco []config.DeviceConfig
	for _, d := range cfg.Devices {
		if strings.EqualFold(d.Vendor, "cisco") {
			cisco = append(cisco, d)
		}
	}
	return &portFixModel{
		cfg:    cfg,
		picker: NewDevicePicker(cisco, "Port Fix — select switch"),
		phase:  phasePickSwitch,
	}
}

func (m *portFixModel) Init() tea.Cmd { return nil }

func (m *portFixModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.phase {
		case phasePickSwitch:
			handled, back, confirm := m.picker.HandleKey(msg)
			if back {
				return m, backToMenu()
			}
			if confirm {
				// enter on a switch = pick it (even if not space-selected)
				sel := m.picker.SelectedDevices()
				if len(sel) == 0 {
					f := m.picker.Filtered()
					if len(f) > 0 && m.picker.Cursor < len(f) {
						sel = []config.DeviceConfig{f[m.picker.Cursor]}
					}
				}
				if len(sel) != 1 {
					return m, nil
				}
				// scan ports of this switch
				m.phase = phasePickPort
				m.ppicker = nil
				return m, m.scanPorts(sel[0])
			}
			if handled {
				return m, nil
			}
		case phasePickPort:
			if m.ppicker == nil {
				// still scanning
				if msg.String() == "esc" || msg.String() == "q" {
					m.phase = phasePickSwitch
					return m, nil
				}
				return m, nil
			}
			handled, back, confirm := m.ppicker.HandleKey(msg)
			if back {
				m.phase = phasePickSwitch
				return m, nil
			}
			if confirm {
				ports := m.ppicker.SelectedPorts()
				if len(ports) == 0 {
					return m, nil
				}
				m.phase = phaseRunning
				m.results = nil
				dev := m.picker.SelectedDevices()[0]
				return m, tea.Batch(m.runFix(dev, ports), spinnerCmd())
			}
			if handled {
				return m, nil
			}
		case phaseRunning:
			if msg.String() == "esc" || msg.String() == "q" {
				return m, backToMenu()
			}
		case phaseDone:
			switch msg.String() {
			case "esc", "b", "q":
				return m, backToMenu()
			case "r":
				m.phase = phasePickSwitch
				m.results = nil
				return m, nil
			}
		}
	case portScanMsg:
		m.ppicker = NewPortPicker(msg.ports, "Select ports on "+msg.device)
		if len(msg.ports) == 0 {
			// no err-disabled ports
			m.phase = phasePickSwitch
			return m, nil
		}
		return m, nil
	case taskDoneMsg:
		m.phase = phaseDone
		m.results = msg.results
		return m, nil
	case spinnerTick:
		spinnerIndex++
		if m.phase == phaseRunning {
			return m, spinnerCmd()
		}
		return m, nil
	}
	return m, nil
}

// portScanMsg - ports found on one switch
type portScanMsg struct {
	device string
	ports  []string
	err    error
}

func (m *portFixModel) scanPorts(d config.DeviceConfig) tea.Cmd {
	return func() tea.Msg {
		cred, err := m.cfg.GetCredential(d.Credential)
		if err != nil {
			return portScanMsg{device: d.Name, err: err}
		}
		dev, err := device.NewDevice(d, cred, m.cfg)
		if err != nil {
			return portScanMsg{device: d.Name, err: err}
		}
		out, err := dev.RunCommand("show interface status | include err-dis")
		if err != nil {
			return portScanMsg{device: d.Name, err: err}
		}
		return portScanMsg{device: d.Name, ports: parseErrDisabledPorts(out)}
	}
}

// findStickyPort - find which interface holds the violating MAC.
// Returns the interface name, or "" if not determinable.
func findStickyPort(dev device.Device, errPort string) (string, error) {
	// 1. get violating MAC from the err-disabled port
	ifaceOut, err := dev.RunCommand(fmt.Sprintf("show port-security interface %s", errPort))
	if err != nil {
		return "", fmt.Errorf("show port-security interface: %w", err)
	}
	mac := parseLastSourceAddress(ifaceOut)
	if mac == "" {
		return "", nil // no MAC info available
	}

	// 2. find which port holds that MAC (whole switch table)
	addrOut, err := dev.RunCommand("show port-security address")
	if err != nil {
		return "", fmt.Errorf("show port-security address: %w", err)
	}
	port := findPortForMac(addrOut, mac)
	return port, nil
}

// parseLastSourceAddress - extract "Last Source Address" MAC from
// `show port-security interface` output. Format:
//   Last Source Address:Vlan : 0050.7966.6800:1
var lastSourceRe = regexp.MustCompile(`Last\s+Source\s+Address:?Vlan\s*:\s*([0-9a-fA-F]{4}\.[0-9a-fA-F]{4}\.[0-9a-fA-F]{4})`)

func parseLastSourceAddress(output string) string {
	if m := lastSourceRe.FindStringSubmatch(output); len(m) > 1 {
		return strings.ToLower(m[1])
	}
	return ""
}

// findPortForMac - find port column for given MAC in `show port-security address`.
// Table rows:  Vlan  Mac Address  Type  Ports  Remaining Age
func findPortForMac(output, mac string) string {
	mac = strings.ToLower(mac)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if strings.ToLower(fields[1]) == mac {
			return fields[3] // Ports column
		}
	}
	return ""
}

// fixPorts - clear ONLY the sticky port holding the violating MAC,
// then bounce selected ports. If MAC not found -> error, user decides.
func fixPorts(ctx context.Context, d config.DeviceConfig, cfg *config.Config, ports []string) (string, error) {
	cred, err := cfg.GetCredential(d.Credential)
	if err != nil {
		return "", fmt.Errorf("get credential: %w", err)
	}
	dev, err := device.NewDevice(d, cred, cfg)
	if err != nil {
		return "", fmt.Errorf("create device: %w", err)
	}

	var fixed []string
	var failed []string

	for _, p := range ports {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		// find the interface holding the violating MAC (port move 1->2 case)
		stickyPort, err := findStickyPort(dev, p)
		if err != nil {
			failed = append(failed, p+": find sticky failed: "+err.Error())
			continue
		}

		if stickyPort == "" {
			// cannot determine -> safety: refuse, ask user
			failed = append(failed, p+": cannot find violating MAC (sticky port unknown)")
			continue
		}

		// clear ONLY that sticky interface
		if _, err := dev.RunCommand(fmt.Sprintf("clear port-security sticky interface %s", stickyPort)); err != nil {
			failed = append(failed, p+": clear sticky "+stickyPort+" failed")
			continue
		}

		// bounce the selected port
		if _, err := dev.RunCommands([]string{
			fmt.Sprintf("interface %s", p),
			"shutdown",
			"no shutdown",
		}); err != nil {
			failed = append(failed, p+": bounce failed")
			continue
		}
		time.Sleep(1 * time.Second)

		// verify up
		ver, err := dev.RunCommand(fmt.Sprintf("show interface %s", p))
		if err == nil && strings.Contains(ver, "up") && !strings.Contains(ver, "down") {
			fixed = append(fixed, p)
		} else {
			failed = append(failed, p+": verify failed")
		}
	}

	summary := fmt.Sprintf("fixed %d/%d: %s", len(fixed), len(fixed)+len(failed), strings.Join(fixed, ", "))
	if len(failed) > 0 {
		return summary, fmt.Errorf("failed: %s", strings.Join(failed, "; "))
	}
	return summary, nil
}

func (m *portFixModel) runFix(d config.DeviceConfig, ports []string) tea.Cmd {
	return RunTaskOnDevices(context.Background(), m.cfg, []config.DeviceConfig{d}, func(ctx context.Context, dev config.DeviceConfig, cfg *config.Config) (string, error) {
		return fixPorts(ctx, dev, cfg, ports)
	})
}

func (m *portFixModel) View() string {
	switch m.phase {
	case phasePickPort:
		if m.ppicker == nil {
			var b strings.Builder
			b.WriteString(titleStyle.Render(" Port Fix ") + "\n\n")
			b.WriteString(dimStyle.Render("Scanning for err-disabled ports...") + "\n")
			b.WriteString(spinner() + "\n")
			b.WriteString("\n" + renderFooter("esc", "cancel"))
			return b.String()
		}
		return m.ppicker.View()
	case phaseRunning:
		var b strings.Builder
		b.WriteString(titleStyle.Render(" Port Fix ") + "\n\n")
		b.WriteString(dimStyle.Render("Fixing ports...") + "\n")
		b.WriteString(spinner() + "\n")
		b.WriteString("\n" + renderFooter("esc", "cancel"))
		return b.String()
	case phaseDone:
		var b strings.Builder
		b.WriteString(titleStyle.Render(" Port Fix Results ") + "\n\n")
		ok, fail := 0, 0
		for _, r := range m.results {
			if r.err != nil {
				fail++
				b.WriteString(fmt.Sprintf("  %s %s — %s\n", errStyle.Render("✗"), deviceStyle.Render(r.name), errStyle.Render(r.err.Error())))
			} else {
				ok++
				b.WriteString(fmt.Sprintf("  %s %s — %s\n", okStyle.Render("✓"), deviceStyle.Render(r.name), dimStyle.Render(r.status)))
			}
		}
		b.WriteString("\n" + fmt.Sprintf("%s %d ok · %s %d failed",
			okStyle.Render("✓"), ok, errStyle.Render("✗"), fail))
		b.WriteString("\n" + renderFooter("esc/b", "back", "r", "re-run"))
		return b.String()
	default:
		return m.picker.View()
	}
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
