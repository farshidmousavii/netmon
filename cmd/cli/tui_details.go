package cli

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/farshidmousavii/bidar/internal/config"
	"github.com/farshidmousavii/bidar/internal/device"
	"github.com/farshidmousavii/bidar/internal/report"
)

// deviceDetailsModel - key/value view of one device with live status.
type deviceDetailsModel struct {
	cfg    *config.Config
	dev    config.DeviceConfig
	status string // online / offline / unknown
	err    error
}

func newDeviceDetailsModel(cfg *config.Config, dev config.DeviceConfig) *deviceDetailsModel {
	return &deviceDetailsModel{cfg: cfg, dev: dev, status: "unknown"}
}

func (m *deviceDetailsModel) Init() tea.Cmd {
	return m.check()
}

func (m *deviceDetailsModel) check() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		reports := make(chan report.DeviceReport, 1)
		device.CheckDevice(ctx, m.dev, m.cfg, reports, true)
		r := <-reports
		if r.Error != nil {
			return deviceStatusMsg{name: m.dev.Name, status: "error", err: r.Error}
		}
		status := "online"
		if !r.Online {
			status = "offline"
		}
		return deviceStatusMsg{name: m.dev.Name, status: status}
	}
}

type deviceStatusMsg struct {
	name   string
	status string
	err    error
}

func (m *deviceDetailsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case deviceStatusMsg:
		m.status = v.status
		m.err = v.err
		return m, nil
	case tea.KeyMsg:
		switch v.String() {
		case "esc", "q":
			return m, backToMenu()
		case "r":
			return m, m.check()
		case "e":
			// quick exec on this device
			return m, switchTo(newQuickExecModel(m.cfg))
		case "b":
			// backup this device
			return m, switchTo(newBackupModel(m.cfg))
		}
	}
	return m, nil
}

func (m *deviceDetailsModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" Device Details ") + "\n\n")

	// status dot
	dot := warnStyle.Render("●")
	switch m.status {
	case "online":
		dot = okStyle.Render("●")
	case "offline", "error":
		dot = errStyle.Render("●")
	}

	rows := [][2]string{
		{"Hostname", m.dev.Name},
		{"Status", dot + " " + m.status},
		{"IP", m.dev.IP},
		{"Vendor", m.dev.Vendor},
		{"Credential", m.dev.Credential},
		{"Port", m.dev.Port},
	}
	if m.err != nil {
		rows = append(rows, [2]string{"Error", m.err.Error()})
	}

	labelW := 14
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("  %s %s\n",
			dimStyle.Render(fmt.Sprintf("%-*s", labelW, r[0]+":")),
			textStyle.Render(r[1])))
	}

	return b.String()
}
