package cli

import (
	"log"
	"sync"

	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/device"
	"github.com/farshidmousavii/netmon/internal/logger"
	"github.com/farshidmousavii/netmon/internal/report"
	"github.com/spf13/cobra"
)

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Run network monitoring",
	Run:   runMonitor,
}

func init() {
	rootCmd.AddCommand(monitorCmd)

	monitorCmd.Flags().BoolVarP(&logToFile, "log", "l", false, "enable file logging")
	monitorCmd.Flags().BoolVar(&skipBackup, "skip-backup", false, "skip backup")
	monitorCmd.Flags().BoolVar(&skipSNMP, "skip-snmp", false, "skip SNMP")
	monitorCmd.Flags().BoolVarP(&jsonOutput, "json", "j", false, "output as JSON")
}

func runMonitor(cmd *cobra.Command, args []string) {
	if err := logger.Init(logToFile); err != nil {
		log.Fatal(err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatal(err)
	}

	if skipSNMP {
		cfg.SNMP = nil
	}

	reports := make(chan report.DeviceReport, len(cfg.Devices))
	var wg sync.WaitGroup

	logger.Info("Starting network monitor")

	for _, deviceCfg := range cfg.Devices {
		wg.Add(1)
		go device.CheckDevice(deviceCfg, cfg, &wg, reports, skipBackup)
	}

	wg.Wait()
	close(reports)

	var allReports []report.DeviceReport
	for r := range reports {
		allReports = append(allReports, r)
	}

	if jsonOutput {
		if err := report.ReportToJson(allReports); err != nil {
			log.Fatal(err)
		}
	} else {
		report.PrintMonitorReport(allReports)
	}

	logger.Info("Network monitor completed")
}
