package cli

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	rootCmd = &cobra.Command{
		Use:   "netmon",
		Short: "Network Monitoring Tool",
		Long: `Network Device Monitor - Monitor and backup Cisco/Mikrotik devices
    
Run without subcommand to start monitoring, or use:
  netmon monitor   - Start monitoring
  netmon diff      - Compare backup files
  netmon init      - Initialize config files`,
		Run: func(cmd *cobra.Command, args []string) {
			monitorCmd.Run(cmd, args)
		},
	}

	// Global flags
	configPath string
	logToFile  bool
	skipBackup bool
	skipSNMP   bool
	jsonOutput bool
)

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {

	rootCmd.PersistentFlags().StringVar(&configPath, "config", "config.yaml", "path to config file")

	rootCmd.Flags().BoolVarP(&logToFile, "log", "l", false, "enable file logging")
	rootCmd.Flags().BoolVar(&skipBackup, "skip-backup", false, "skip backup")
	rootCmd.Flags().BoolVar(&skipSNMP, "skip-snmp", false, "skip SNMP")
	rootCmd.Flags().BoolVarP(&jsonOutput, "json", "j", false, "output as JSON")
}
