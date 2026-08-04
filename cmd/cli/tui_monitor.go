package cli

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/device"
	"github.com/farshidmousavii/netmon/internal/report"
)

type monitorModel struct {
	cfg     *config.Config
	picker  *DevicePicker
	running bool
	results []taskResult
	done    bool
}

func newMonitorModel(cfg *config.Config) *monitorModel {
	m := &monitorModel{cfg: cfg, picker: NewDevicePicker(cfg.Devices, "Monitor Devices")}
	m.picker.Confirm = "monitor"
	return m
}

func (m *monitorModel) Init() tea.Cmd { return nil }

func (m *monitorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.running {
			if msg.String() == "esc" || msg.String() == "q" {
				return m, backToMenu()
			}
			return m, nil
		}
		if msg.String() == "?" {
			return m, switchTo(helpModel{parent: m, keys: pickerHelpKeys})
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
			return m, tea.Batch(m.runMonitor(sel), spinnerCmd())
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

func (m *monitorModel) runMonitor(devices []config.DeviceConfig) tea.Cmd {
	return RunTaskOnDevices(context.Background(), m.cfg, devices, func(ctx context.Context, d config.DeviceConfig, cfg *config.Config) (string, error) {
		reports := make(chan report.DeviceReport, 1)
		device.CheckDevice(ctx, d, cfg, reports, true) // skip backup in TUI monitor
		r := <-reports
		if r.Error != nil {
			return "", r.Error
		}
		status := "online"
		if !r.Online {
			status = "offline"
		}
		info := r.SNMPInfo
		if info.Hostname != "" {
			status += " · " + info.Hostname + " · " + info.Vendor
		}
		return status, nil
	})
}

func (m *monitorModel) View() string {
	if m.running {
		var b strings.Builder
		b.WriteString(titleStyle.Render(" Monitor ") + "\n\n")
		b.WriteString(dimStyle.Render("Checking "+fmt.Sprint(len(m.picker.SelectedDevices()))+" devices...") + "\n")
		b.WriteString(spinner() + "\n")
		b.WriteString("\n" + renderFooter("esc", "cancel"))
		return b.String()
	}
	if m.done {
		var b strings.Builder
		b.WriteString(titleStyle.Render(" Monitor Results ") + "\n\n")
		online, offline, fail := 0, 0, 0
		for _, r := range m.results {
			switch {
			case r.err != nil:
				fail++
				b.WriteString(fmt.Sprintf("  %s %s — %s\n", errStyle.Render("✗"), deviceStyle.Render(r.name), errStyle.Render(r.err.Error())))
			case strings.Contains(r.status, "offline"):
				offline++
				b.WriteString(fmt.Sprintf("  %s %s — %s\n", errStyle.Render("✗"), deviceStyle.Render(r.name), "offline"))
			default:
				online++
				b.WriteString(fmt.Sprintf("  %s %s — %s\n", okStyle.Render("✓"), deviceStyle.Render(r.name), dimStyle.Render(r.status)))
			}
		}
		b.WriteString("\n" + fmt.Sprintf("%s %d online · %s %d offline · %s %d failed",
			okStyle.Render("✓"), online, errStyle.Render("✗"), offline, warnStyle.Render("?"), fail))
		b.WriteString("\n" + renderFooter("esc/b", "back", "r", "re-run"))
		return b.String()
	}
	return m.picker.View()
}
