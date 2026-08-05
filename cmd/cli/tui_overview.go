package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/farshidmousavii/bidar/internal/config"
	"github.com/farshidmousavii/bidar/internal/device"
	"github.com/farshidmousavii/bidar/internal/report"
)

// overviewState - cached monitor results (persist across TUI screens)
var overviewState struct {
	results []taskResult
	at      time.Time
}

type overviewModel struct {
	cfg     *config.Config
	running bool
}

func newOverviewModel(cfg *config.Config) *overviewModel {
	return &overviewModel{cfg: cfg}
}

func (m *overviewModel) Init() tea.Cmd { return nil }

func (m *overviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "b", "q":
			return m, backToMenu()
		case "r", "enter":
			m.running = true
			return m, tea.Batch(m.runCheck(), spinnerCmd())
		}
	case taskDoneMsg:
		m.running = false
		overviewState.results = msg.results
		overviewState.at = time.Now()
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

func (m *overviewModel) runCheck() tea.Cmd {
	return RunTaskOnDevices(context.Background(), m.cfg, m.cfg.Devices, func(ctx context.Context, d config.DeviceConfig, cfg *config.Config) (string, error) {
		reports := make(chan report.DeviceReport, 1)
		device.CheckDevice(ctx, d, cfg, reports, true)
		r := <-reports
		if r.Error != nil {
			return "", r.Error
		}
		status := "online"
		if !r.Online {
			status = "offline"
		}
		return status, nil
	})
}

func (m *overviewModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" Overview ") + "\n\n")

	if m.running {
		b.WriteString(dimStyle.Render("Scanning all devices...") + "\n" + spinner() + "\n")
		return b.String()
	}

	if len(overviewState.results) == 0 {
		b.WriteString(dimStyle.Render("No data yet. Press enter to run a full health check.\n\n"))
		return b.String()
	}

	online, offline, fail := 0, 0, 0
	for _, r := range overviewState.results {
		switch {
		case r.err != nil:
			fail++
		case strings.Contains(r.status, "offline"):
			offline++
		default:
			online++
		}
	}

	total := len(overviewState.results)
	b.WriteString(fmt.Sprintf("  %s %d online\n", okStyle.Render("●"), online))
	b.WriteString(fmt.Sprintf("  %s %d offline\n", errStyle.Render("●"), offline))
	b.WriteString(fmt.Sprintf("  %s %d failed\n", warnStyle.Render("●"), fail))
	b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("Total: %d · last check %s",
		total, overviewState.at.Format("15:04:05"))))

	var problems []taskResult
	for _, r := range overviewState.results {
		if r.err != nil || strings.Contains(r.status, "offline") {
			problems = append(problems, r)
		}
	}
	if len(problems) > 0 {
		b.WriteString("\n\n" + sectionStyle.Render("Problem devices") + "\n")
		for _, r := range problems {
			if r.err != nil {
				b.WriteString(fmt.Sprintf("  %s %s — %s\n", errStyle.Render("✗"), deviceStyle.Render(r.name), errStyle.Render(r.err.Error())))
			} else {
				b.WriteString(fmt.Sprintf("  %s %s — offline\n", errStyle.Render("✗"), deviceStyle.Render(r.name)))
			}
		}
	}

	return b.String()
}
