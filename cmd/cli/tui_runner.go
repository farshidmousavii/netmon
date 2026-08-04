package cli

import (
	"context"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/worker"
)

// DeviceTask - one unit of work on one device. Returns a status line.
type DeviceTask func(ctx context.Context, d config.DeviceConfig, cfg *config.Config) (status string, err error)

// taskDoneMsg - all devices finished
type taskDoneMsg struct {
	results []taskResult
}

type taskResult struct {
	name   string
	status string
	err    error
}

// spinnerTick - animation frame
type spinnerTick time.Time

// RunTaskOnDevices - run task on devices in parallel, stream spinner, return done msg.
func RunTaskOnDevices(ctx context.Context, cfg *config.Config, devices []config.DeviceConfig, task DeviceTask) tea.Cmd {
	return func() tea.Msg {
		results := make([]taskResult, 0, len(devices))
		var mu sync.Mutex

		workers := len(devices)
		if workers < 1 {
			workers = 1
		}
		if workers > 10 {
			workers = 10
		}
		pool := worker.NewPool(ctx, workers)

		var wg sync.WaitGroup
		for _, d := range devices {
			d := d
			wg.Add(1)
			if err := pool.Submit(func(ctx context.Context) error {
				defer wg.Done()
				status, err := task(ctx, d, cfg)
				mu.Lock()
				results = append(results, taskResult{name: d.Name, status: status, err: err})
				mu.Unlock()
				return nil
			}); err != nil {
				wg.Done()
			}
		}
		wg.Wait()
		pool.Close()

		return taskDoneMsg{results: results}
	}
}

// spinnerCmd - emit periodic tick for spinner animation while running
func spinnerCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg { return spinnerTick(t) })
}
