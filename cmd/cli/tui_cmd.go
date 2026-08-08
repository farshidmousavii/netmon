package cli

import (
	"log"

	"github.com/farshidmousavii/bidar/internal/config"
	"github.com/farshidmousavii/bidar/internal/logger"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui-config",
	Short: "Manage device config interactively (config-file TUI)",
	Long: `Manage config.yaml/devices.csv and run live SSH checks against devices.

This is the config-file-based terminal UI (device CRUD against the YAML/CSV
config, live SSH checks). It reads and writes config.yaml and holds SSH
credentials in-process. The name was changed from "tui" to "tui-config" in
Phase 0 to free up the "tui" name for the Phase 5 REST-API-client TUI.`,
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
