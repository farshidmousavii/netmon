package cli

import (
	"context"
	"os"

	"github.com/spf13/cobra"
)

var (
	configPath string
	logToFile  bool
	skipBackup bool
	skipSNMP   bool
	jsonOutput bool
)

var (
	rootCmd = &cobra.Command{
		Use:   "netmon-cli",
		Short: "Network device monitoring and management tool",
		Long: `NetMon is a CLI tool for monitoring and managing network devices.
It supports Cisco and MikroTik devices with features like health checks,
configuration backups, and bulk command execution.`}
)

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {

	rootCmd.PersistentFlags().StringVar(&configPath, "config", "config.yaml", "path to config file(config.yaml or devices.csv)")

}

func ExecuteContext(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}
