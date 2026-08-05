package cli

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/farshidmousavii/bidar/internal/backup"
	"github.com/farshidmousavii/bidar/internal/config"
	"github.com/farshidmousavii/bidar/internal/report"
)

type backupModel struct {
	cfg     *config.Config
	picker  *DevicePicker
	running bool
	results []taskResult
	done    bool
}

func newBackupModel(cfg *config.Config) *backupModel {
	m := &backupModel{cfg: cfg, picker: NewDevicePicker(cfg.Devices, "Backup Devices")}
	m.picker.Confirm = "backup"
	return m
}

func (m *backupModel) Init() tea.Cmd { return nil }

func (m *backupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			return m, tea.Batch(m.runBackup(sel), spinnerCmd())
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

func (m *backupModel) runBackup(devices []config.DeviceConfig) tea.Cmd {
	return RunTaskOnDevices(context.Background(), m.cfg, devices, func(ctx context.Context, d config.DeviceConfig, cfg *config.Config) (string, error) {
		reports := make(chan report.DeviceReport, 1)
		backup.BackupDevice(ctx, d, cfg, reports)
		r := <-reports
		if r.Error != nil {
			return "", r.Error
		}
		return r.BackupPath, nil
	})
}

func (m *backupModel) View() string {
	if m.running {
		var b strings.Builder
		b.WriteString(titleStyle.Render(" Backup ") + "\n\n")
		b.WriteString(dimStyle.Render("Backing up "+fmt.Sprint(len(m.results))+"/"+fmt.Sprint(len(m.picker.SelectedDevices()))+" ...") + "\n")
		b.WriteString(spinner() + "\n")
		return b.String()
	}
	if m.done {
		var b strings.Builder
		b.WriteString(titleStyle.Render(" Backup Results ") + "\n\n")
		ok, fail := 0, 0
		for _, r := range m.results {
			if r.err != nil {
				fail++
				b.WriteString(fmt.Sprintf("  %s %s — %s\n", errStyle.Render("✗"), deviceStyle.Render(r.name), errStyle.Render(r.err.Error())))
			} else {
				ok++
				b.WriteString(fmt.Sprintf("  %s %s → %s\n", okStyle.Render("✓"), deviceStyle.Render(r.name), dimStyle.Render(r.status)))
			}
		}
		b.WriteString("\n" + fmt.Sprintf("%s %d ok · %s %d failed",
			okStyle.Render("✓"), ok, errStyle.Render("✗"), fail))
		return b.String()
	}
	return m.picker.View()
}

// spinner - simple animation frames (braille)
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var spinnerIndex int

func spinner() string {
	return spinnerFrames[spinnerIndex%len(spinnerFrames)]
}
