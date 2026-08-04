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

// fixDevice - clear ALL sticky + bounce ONLY selected ports, verify each
func fixPorts(ctx context.Context, d config.DeviceConfig, cfg *config.Config, ports []string) (string, error) {
	cred, err := cfg.GetCredential(d.Credential)
	if err != nil {
		return "", fmt.Errorf("get credential: %w", err)
	}
	dev, err := device.NewDevice(d, cred, cfg)
	if err != nil {
		return "", fmt.Errorf("create device: %w", err)
	}

	// 1. clear sticky on ALL ports (port moved 1->2: stale sticky on port 1)
	if _, err := dev.RunCommand("clear port-security sticky"); err != nil {
		return "", fmt.Errorf("clear all sticky: %w", err)
	}

	// 2. bounce ONLY selected ports
	var fixed []string
	var failed []string
	for _, p := range ports {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
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
			return titleStyle.Render(" Port Fix ") + "\n\n" +
				dimStyle.Render("Scanning ports...") + "\n" +
				spinner() + "\n\n" +
				dimStyle.Render("esc to cancel")
		}
		return m.ppicker.View()
	case phaseRunning:
		return titleStyle.Render(" Port Fix ") + "\n\n" +
			dimStyle.Render("Fixing ports...") + "\n" +
			spinner() + "\n\n" +
			dimStyle.Render("esc to cancel")
	case phaseDone:
		var b strings.Builder
		b.WriteString(titleStyle.Render(" Port Fix Results ") + "\n\n")
		for _, r := range m.results {
			if r.err != nil {
				b.WriteString(fmt.Sprintf("  %s %s — %s\n", errStyle.Render("✗"), deviceStyle.Render(r.name), r.err.Error()))
			} else {
				b.WriteString(fmt.Sprintf("  %s %s — %s\n", okStyle.Render("✓"), deviceStyle.Render(r.name), r.status))
			}
		}
		b.WriteString("\n" + dimStyle.Render("esc/b back · r re-run"))
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
