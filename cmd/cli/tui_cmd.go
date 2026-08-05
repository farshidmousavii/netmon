package cli

import (
	"log"

	"github.com/farshidmousavii/bidar/internal/config"
	"github.com/farshidmousavii/bidar/internal/logger"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Interactive terminal UI",
	Long: `Launch the interactive terminal user interface for Bidar.
Browse devices, scan for err-disabled ports, fix port-security violations.`,
	Run: runTUICmd,
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

func runTUICmd(cmd *cobra.Command, args []string) {
	ctx := cmd.Context()
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	if err := runTUIEngine(ctx, cfg); err != nil {
		log.Fatalf("TUI error: %v", err)
	}
}
