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

type portFixModel struct {
	cfg     *config.Config
	picker  *DevicePicker
	running bool
	results []taskResult
	done    bool
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
		picker: NewDevicePicker(cisco, "Port Fix — select switches"),
	}
}

func (m *portFixModel) Init() tea.Cmd { return nil }

func (m *portFixModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.running {
			if msg.String() == "esc" || msg.String() == "q" {
				return m, backToMenu()
			}
			return m, nil
		}
		handled, back, confirm := m.picker.HandleKey(msg)
		if back {
			return m, backToMenu()
		}
		if confirm {
			sel := m.picker.SelectedDevices()
			if len(sel) == 0 {
				return m, nil
			}
			m.running = true
			m.done = false
			m.results = nil
			return m, tea.Batch(m.runFix(sel), spinnerCmd())
		}
		if handled {
			return m, nil
		}
	case taskDoneMsg:
		m.running = false
		m.done = true
		m.results = msg.results
		return m, nil
	case spinnerTick:
		spinnerIndex++
		if m.running {
			return m, spinnerCmd()
		}
		return m, nil
	}
	return m, nil
}

// fixDevice - clear port-security sticky + bounce, verify up
func fixDevice(ctx context.Context, d config.DeviceConfig, cfg *config.Config) (string, error) {
	cred, err := cfg.GetCredential(d.Credential)
	if err != nil {
		return "", fmt.Errorf("get credential: %w", err)
	}
	dev, err := device.NewDevice(d, cred, cfg)
	if err != nil {
		return "", fmt.Errorf("create device: %w", err)
	}

	// 1. find err-disabled ports
	out, err := dev.RunCommand("show interface status | include err-dis")
	if err != nil {
		return "", fmt.Errorf("scan: %w", err)
	}
	ports := parseErrDisabledPorts(out)
	if len(ports) == 0 {
		return "", fmt.Errorf("no err-disabled ports found")
	}

	// 2. clear + bounce each port, then verify
	var fixed []string
	var failed []string
	for _, p := range ports {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// clear port-security sticky (exec mode)
		if _, err := dev.RunCommand(fmt.Sprintf("clear port-security sticky interface %s", p)); err != nil {
			failed = append(failed, p+": clear failed")
			continue
		}
		// bounce
		if _, err := dev.RunCommands([]string{
			fmt.Sprintf("interface %s", p),
			"shutdown",
			"no shutdown",
		}); err != nil {
			failed = append(failed, p+": bounce failed")
			continue
		}
		time.Sleep(1 * time.Second)
		// verify
		ver, err := dev.RunCommand(fmt.Sprintf("show interface %s", p))
		if err == nil && strings.Contains(ver, "up") && !strings.Contains(ver, "down") {
			fixed = append(fixed, p)
		} else {
			failed = append(failed, p+": verify failed")
		}
	}

	summary := fmt.Sprintf("fixed %d/%d ports: %s", len(fixed), len(fixed)+len(failed), strings.Join(fixed, ", "))
	if len(failed) > 0 {
		return summary, fmt.Errorf("failed: %s", strings.Join(failed, "; "))
	}
	return summary, nil
}

func (m *portFixModel) runFix(devices []config.DeviceConfig) tea.Cmd {
	return RunTaskOnDevices(context.Background(), m.cfg, devices, fixDevice)
}

func (m *portFixModel) View() string {
	if m.running {
		return titleStyle.Render(" Port Fix ") + "\n\n" +
			dimStyle.Render("Fixing err-disabled ports...") + "\n" +
			spinner() + "\n\n" +
			dimStyle.Render("esc to cancel")
	}
	if m.done {
		var b strings.Builder
		b.WriteString(titleStyle.Render(" Port Fix Results ") + "\n\n")
		ok, fail := 0, 0
		for _, r := range m.results {
			if r.err != nil {
				fail++
				b.WriteString(fmt.Sprintf("  %s %s — %s (%s)\n", errStyle.Render("✗"), deviceStyle.Render(r.name), r.err.Error(), dimStyle.Render(r.status)))
			} else {
				ok++
				b.WriteString(fmt.Sprintf("  %s %s — %s\n", okStyle.Render("✓"), deviceStyle.Render(r.name), r.status))
			}
		}
		b.WriteString("\n" + fmt.Sprintf("%s %d ok · %s %d failed",
			okStyle.Render("✓"), ok, errStyle.Render("✗"), fail))
		b.WriteString("\n\n" + dimStyle.Render("esc/b back · r re-run"))
		return b.String()
	}
	return m.picker.View()
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
