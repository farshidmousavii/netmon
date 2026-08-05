package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/farshidmousavii/bidar/internal/config"
	"github.com/farshidmousavii/bidar/internal/device"
	"github.com/farshidmousavii/bidar/internal/report"
)

// healthModel - dashboard health cards + recent problems.
// Reuses overviewState cache when fresh; can force a re-scan.
type healthModel struct {
	cfg     *config.Config
	running bool
}

func newHealthModel(cfg *config.Config) *healthModel {
	return &healthModel{cfg: cfg}
}

func (m *healthModel) Init() tea.Cmd { return nil }

func (m *healthModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m *healthModel) runCheck() tea.Cmd {
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

func (m *healthModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" Dashboard ") + "\n\n")

	if m.running {
		b.WriteString(dimStyle.Render("Scanning all devices...") + "\n" + spinner() + "\n")
		return b.String()
	}

	if len(overviewState.results) == 0 {
		b.WriteString(dimStyle.Render("No health data yet. Press enter to run a full check.\n\n"))
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

	// health cards (side by side)
	card := func(label string, count int, style lipgloss.Style) string {
		return lipgloss.NewStyle().
			Width(22).Padding(0, 1).Border(lipgloss.RoundedBorder()).
			BorderForeground(colSubtle).
			Render(fmt.Sprintf("%s\n%s", dimStyle.Render(label), style.Render(fmt.Sprintf("%d", count))))
	}

	b.WriteString(
		lipgloss.JoinHorizontal(lipgloss.Top,
			card("Online", online, okStyle),
			card("Offline", offline, errStyle),
			card("Failed", fail, warnStyle),
			card("Total", total, textStyle),
		) + "\n")

	b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("Last check: %s", overviewState.at.Format("15:04:05"))))

	// problem devices
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
