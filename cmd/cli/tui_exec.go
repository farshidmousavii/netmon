package cli

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/farshidmousavii/bidar/internal/config"
	"github.com/farshidmousavii/bidar/internal/device"
)

type quickExecModel struct {
	cfg     *config.Config
	picker  *DevicePicker
	input   string
	mode    string // "pick" | "input" | "running" | "done"
	results []taskResult
	scroll  int
	// full output lines (all devices concatenated) for line-based scrolling
	outLines []string
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
					// no selection: fall back to hovered device
					f := m.picker.Filtered()
					if len(f) > 0 && m.picker.Cursor < len(f) {
						name := f[m.picker.Cursor].Name
						m.picker.Selected[name] = true
					}
				}
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
				m.scroll = 0
				m.outLines = nil
				return m, nil
			case "up", "k":
				if m.scroll > 0 {
					m.scroll--
				}
			case "down", "j":
				if m.scroll < len(m.outLines)-1 {
					m.scroll++
				}
			case "pgup", "left":
				m.scroll -= 20
				if m.scroll < 0 {
					m.scroll = 0
				}
			case "pgdown", "right":
				m.scroll += 20
				if m.scroll > len(m.outLines)-1 {
					m.scroll = max(0, len(m.outLines)-1)
				}
			case "home", "g":
				m.scroll = 0
			case "end", "G":
				m.scroll = max(0, len(m.outLines)-1)
			}
		}
	case taskDoneMsg:
		m.mode = "done"
		m.results = msg.results
		// build full output lines (device header + status lines each)
		var lines []string
		for _, r := range m.results {
			if r.err != nil {
				lines = append(lines, fmt.Sprintf("✗ %s — %s", r.name, r.err.Error()))
			} else {
				lines = append(lines, fmt.Sprintf("✓ %s", r.name))
				clean := strings.ReplaceAll(strings.TrimSuffix(r.status, "\n"), "\r", "")
				for _, l := range strings.Split(clean, "\n") {
					lines = append(lines, "  "+l)
				}
			}
			lines = append(lines, "")
		}
		m.outLines = lines
		m.scroll = 0
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
		return out, nil
	})
}

func (m *quickExecModel) View() string {
	switch m.mode {
	case "input":
		var b strings.Builder
		b.WriteString(titleStyle.Render(" Quick Exec ") + "\n\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("%d device(s) selected\n\n", len(m.picker.SelectedDevices()))))
		b.WriteString(accStyle.Render("> "+m.input+"▌") + "\n\n")
		return b.String()
	case "running":
		var b strings.Builder
		b.WriteString(titleStyle.Render(" Quick Exec ") + "\n\n")
		b.WriteString(dimStyle.Render("Running command...") + "\n")
		b.WriteString(spinner() + "\n")
		return b.String()
	case "done":
		var b strings.Builder
		b.WriteString(titleStyle.Render(" Quick Exec Results ") + "\n\n")
		// line-based window: show lines[scroll .. scroll+height)
		height := 25 // visible lines (rough terminal height)
		end := m.scroll + height
		if end > len(m.outLines) {
			end = len(m.outLines)
		}
		if m.scroll >= len(m.outLines) && m.scroll > 0 {
			m.scroll = max(0, len(m.outLines)-1)
			end = len(m.outLines)
		}
		for _, l := range m.outLines[m.scroll:end] {
			if strings.HasPrefix(l, "✗") {
				b.WriteString(errStyle.Render(l) + "\n")
			} else if strings.HasPrefix(l, "✓") {
				b.WriteString(okStyle.Render(l) + "\n")
			} else {
				b.WriteString(dimStyle.Render(l) + "\n")
			}
		}
		// scroll indicator
		total := len(m.outLines)
		pct := 0
		if total > 0 {
			pct = (m.scroll * 100) / total
		}
		if total > height {
			b.WriteString(fmt.Sprintf("\n%s %d%% (%d/%d lines) · ↑/↓ scroll · pgup/pgdn page · g/G top/bottom\n",
				dimStyle.Render("scroll"), pct, m.scroll, total))
		}
		return b.String()
	default:
		return m.picker.View()
	}
}
