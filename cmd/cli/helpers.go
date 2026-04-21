package cli

import (
	"context"
	"sync"

	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/logger"
	"github.com/farshidmousavii/netmon/internal/report"
)

// DeviceWorker - A function that runs on every device
type DeviceWorker func(ctx context.Context, deviceCfg config.DeviceConfig, cfg *config.Config, reports chan<- report.DeviceReport)

// RunOnDevices - Run worker on all devices with graceful shutdown
func RunOnDevices(ctx context.Context, cfg *config.Config, worker DeviceWorker, operationName string) []report.DeviceReport {
	reports := make(chan report.DeviceReport, len(cfg.Devices))
	var wg sync.WaitGroup

	logger.Info("Starting %s", operationName)

	for _, deviceCfg := range cfg.Devices {
		select {
		case <-ctx.Done():
			logger.Warning("%s cancelled before processing all devices", operationName)
			close(reports)
			return collectReports(reports)
		default:
		}

		wg.Add(1)
		go func(dcfg config.DeviceConfig) {
			defer wg.Done()
			worker(ctx, dcfg, cfg, reports)
		}(deviceCfg)
	}

	// Wait for all goroutines
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// all done
	case <-ctx.Done():
		logger.Warning("%s interrupted - waiting for active operations to complete...", operationName)
		<-done
	}

	close(reports)

	logger.Info("%s completed", operationName)
	return collectReports(reports)
}

func collectReports(reports <-chan report.DeviceReport) []report.DeviceReport {
	var allReports []report.DeviceReport
	for r := range reports {
		allReports = append(allReports, r)
	}
	return allReports
}
