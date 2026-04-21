package cli

import (
	"log"

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

	ctx := cmd.Context()

	if err := logger.Init(logToFile); err != nil {
		log.Fatal(err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatal(err)
	}

	logger.Info("Starting backup")

	allReports := RunOnDevices(ctx, cfg, backup.BackupDevice, "backup")

	if jsonOutput {
		if err := report.ReportToJson(allReports); err != nil {
			log.Fatal(err)
		}
	} else {
		report.PrintBackupReport(allReports)
	}

	logger.Info("Backup completed")
}
