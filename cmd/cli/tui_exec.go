package cli

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/device"
)

type quickExecModel struct {
	cfg     *config.Config
	picker  *DevicePicker
	input   string
	mode    string // "pick" | "input" | "running" | "done"
	results []taskResult
}

func newQuickExecModel(cfg *config.Config) *quickExecModel {
	return &quickExecModel{
		cfg:    cfg,
		picker: NewDevicePicker(cfg.Devices, "Quick Exec — select devices"),
		mode:   "pick",
	}
}

func (m *quickExecModel) Init() tea.Cmd { return nil }

func (m *quickExecModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.mode {
		case "pick":
			handled, back, confirm := m.picker.HandleKey(msg)
			if back {
				return m, backToMenu()
			}
			if confirm {
				if len(m.picker.SelectedDevices()) == 0 {
					return m, nil
				}
				m.mode = "input"
				m.input = ""
				return m, nil
			}
			if handled {
				return m, nil
			}
		case "input":
			switch msg.String() {
			case "enter":
				cmd := strings.TrimSpace(m.input)
				if cmd == "" {
					return m, nil
				}
				m.mode = "running"
				m.results = nil
				return m, tea.Batch(m.runExec(m.picker.SelectedDevices(), cmd), spinnerCmd())
			case "esc", "q":
				m.mode = "pick"
				return m, nil
			case "backspace":
				if len(m.input) > 0 {
					m.input = m.input[:len(m.input)-1]
				}
			default:
				if len(msg.String()) == 1 {
					m.input += msg.String()
				}
			}
		case "running":
			if msg.String() == "esc" || msg.String() == "q" {
				return m, backToMenu()
			}
		case "done":
			switch msg.String() {
			case "esc", "b", "q":
				return m, backToMenu()
			case "r":
				m.mode = "pick"
				m.results = nil
				return m, nil
			}
		}
	case taskDoneMsg:
		m.mode = "done"
		m.results = msg.results
		return m, nil
	case spinnerTick:
		spinnerIndex++
		if m.mode == "running" {
			return m, spinnerCmd()
		}
		return m, nil
	}
	return m, nil
}

func (m *quickExecModel) runExec(devices []config.DeviceConfig, cmd string) tea.Cmd {
	return RunTaskOnDevices(context.Background(), m.cfg, devices, func(ctx context.Context, d config.DeviceConfig, cfg *config.Config) (string, error) {
		cred, err := cfg.GetCredential(d.Credential)
		if err != nil {
			return "", fmt.Errorf("get credential: %w", err)
		}
		dev, err := device.NewDevice(d, cred, cfg)
		if err != nil {
			return "", fmt.Errorf("create device: %w", err)
		}
		out, err := dev.RunCommand(cmd)
		if err != nil {
			return "", err
		}
		// truncate long output for TUI display
		if len(out) > 400 {
			out = out[:400] + "...\n[truncated]"
		}
		return out, nil
	})
}

func (m *quickExecModel) View() string {
	switch m.mode {
	case "input":
		return titleStyle.Render(" Quick Exec — command ") + "\n\n" +
			dimStyle.Render(fmt.Sprintf("devices: %d selected\n\n", len(m.picker.SelectedDevices()))) +
			titleStyle.Render("> "+m.input+"▌") + "\n\n" +
			dimStyle.Render("enter run · esc back")
	case "running":
		return titleStyle.Render(" Quick Exec ") + "\n\n" +
			dimStyle.Render("Running command...") + "\n" +
			spinner() + "\n\n" +
			dimStyle.Render("esc to cancel")
	case "done":
		var b strings.Builder
		b.WriteString(titleStyle.Render(" Quick Exec Results ") + "\n\n")
		for _, r := range m.results {
			if r.err != nil {
				b.WriteString(fmt.Sprintf("  %s %s — %s\n", errStyle.Render("✗"), deviceStyle.Render(r.name), r.err.Error()))
			} else {
				b.WriteString(fmt.Sprintf("  %s %s\n%s\n\n", okStyle.Render("✓"), deviceStyle.Render(r.name), dimStyle.Render(r.status)))
			}
		}
		b.WriteString(dimStyle.Render("esc/b back · r re-run"))
		return b.String()
	default:
		return m.picker.View()
	}
}
