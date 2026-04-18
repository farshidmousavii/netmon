package cli

import (
	"log"
	"sync"

	"github.com/farshidmousavii/netmon/internal/backup"
	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/logger"
	"github.com/farshidmousavii/netmon/internal/report"
	"github.com/spf13/cobra"
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Backup device configurations",
	Run:   runBackup,
}

func init() {
	rootCmd.AddCommand(backupCmd)

	// flag for backup
	backupCmd.Flags().BoolVarP(&logToFile, "log", "l", false, "enable file logging")
	backupCmd.Flags().BoolVarP(&jsonOutput, "json", "j", false, "output as JSON")
}

func runBackup(cmd *cobra.Command, args []string) {
	if err := logger.Init(logToFile); err != nil {
		log.Fatal(err)
	}

	if err := config.LoadEnvVars(); err != nil {
		logger.Warning("failed to load .env: %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatal(err)
	}

	reports := make(chan report.DeviceReport, len(cfg.Devices))
	var wg sync.WaitGroup

	logger.Info("Starting backup")

	for _, deviceCfg := range cfg.Devices {
		wg.Add(1)
		go backup.BackupDevice(deviceCfg, cfg, &wg, reports)
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
		report.PrintBackupReport(allReports)
	}

	logger.Info("Backup completed")
}
