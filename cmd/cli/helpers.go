package cli

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/device"
	"github.com/farshidmousavii/netmon/internal/logger"
	"github.com/farshidmousavii/netmon/internal/report"
	execWorker "github.com/farshidmousavii/netmon/internal/worker"
)

// DeviceWorker - A function that runs on every device
type DeviceWorker func(ctx context.Context, deviceCfg config.DeviceConfig, cfg *config.Config, reports chan<- report.DeviceReport)

// RunOnDevices - Run worker on all devices with graceful shutdown
func RunOnDevicesWithPool(ctx context.Context, cfg *config.Config, devices []config.DeviceConfig, worker DeviceWorker, operationName string) []report.DeviceReport {

	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 50 {
		concurrency = 50
	}

	reports := make(chan report.DeviceReport, len(devices))
	progress := make(chan string, 100)

	totalDevices := len(devices)
	var completedCount int32

	// Progress printer
	progressDone := make(chan struct{})
	go func() {
		for msg := range progress {
			fmt.Println(msg)
		}
		close(progressDone)
	}()

	logger.Info("Starting %s with concurrency=%d", operationName, concurrency)

	// Worker pool
	pool := execWorker.NewPool(ctx, concurrency)

	for _, deviceCfg := range devices {
		if ctx.Err() != nil {
			progress <- fmt.Sprintf("⚠ %s cancelled before processing all devices", operationName)
			break
		}

		dcfg := deviceCfg // copy for closure

		// Submit job
		err := pool.Submit(func(ctx context.Context) error {
			defer func() {
				count := atomic.AddInt32(&completedCount, 1)
				progress <- fmt.Sprintf("[%d/%d] ✓ Completed: %s (%s)", count, totalDevices, dcfg.Name, dcfg.IP)
			}()

			progress <- fmt.Sprintf("→ Starting: %s (%s)", dcfg.Name, dcfg.IP)

			// run worker function
			worker(ctx, dcfg, cfg, reports)

			return nil
		})

		if err != nil {
			progress <- fmt.Sprintf("✗ Failed to submit: %s", deviceCfg.Name)
		}
	}

	// Wait for completion
	pool.Close()

	close(reports)
	close(progress)
	<-progressDone

	logger.Info("%s completed", operationName)

	// Collect results
	var allReports []report.DeviceReport
	for r := range reports {
		allReports = append(allReports, r)
	}

	return allReports
}

// ExecDeviceWorker - A function that runs on each device for exec
type ExecDeviceWorker func(ctx context.Context, deviceCfg config.DeviceConfig, cfg *config.Config, results chan<- device.ExecResult)

// RunExecWithPool - Running exec on all devices with worker pool
func RunExecWithPool(ctx context.Context, cfg *config.Config, devices []config.DeviceConfig, worker ExecDeviceWorker, operationName string) []device.ExecResult {
	// Validation concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 50 {
		concurrency = 50
	}

	results := make(chan device.ExecResult, len(devices))
	progress := make(chan string, 100)

	totalDevices := len(devices)
	var completedCount int32

	// Progress printer
	progressDone := make(chan struct{})
	go func() {
		for msg := range progress {
			fmt.Println(msg)
		}
		close(progressDone)
	}()

	logger.Info("Starting %s with concurrency=%d", operationName, concurrency)

	// Worker pool
	pool := execWorker.NewPool(ctx, concurrency)

	for _, deviceCfg := range devices {
		if ctx.Err() != nil {
			progress <- fmt.Sprintf("⚠ %s cancelled before processing all devices", operationName)
			break
		}

		dcfg := deviceCfg // copy for closure

		// Submit job
		err := pool.Submit(func(ctx context.Context) error {
			defer func() {
				count := atomic.AddInt32(&completedCount, 1)
				progress <- fmt.Sprintf("[%d/%d] ✓ Completed: %s (%s)", count, totalDevices, dcfg.Name, dcfg.IP)
			}()

			progress <- fmt.Sprintf("→ Starting: %s (%s)", dcfg.Name, dcfg.IP)

			// run worker function
			worker(ctx, dcfg, cfg, results)

			return nil
		})

		if err != nil {
			progress <- fmt.Sprintf("✗ Failed to submit: %s", deviceCfg.Name)
		}
	}

	// Wait for completion
	pool.Close()

	close(results)
	close(progress)
	<-progressDone

	logger.Info("%s completed", operationName)

	// Collect results
	var allResults []device.ExecResult
	for r := range results {
		allResults = append(allResults, r)
	}

	return allResults
}
