package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/farshidmousavii/bidar/internal/db"
	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/logger"
	"github.com/farshidmousavii/bidar/internal/store"
	"github.com/spf13/cobra"
)

var dhcpSourcesCmd = &cobra.Command{
	Use:   "dhcp-sources",
	Short: "Manage DHCP lease sources",
	Long: `Manage the DHCP lease evidence sources (dhcp_sources).

Environment-configured (not the --config flag):
  BIDAR_DATABASE_URL   Postgres connection string (required)`,
}

var dhcpSourcesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List DHCP sources",
	Run:   runDHCPSourcesList,
}

var dhcpSourcesSetPathCmd = &cobra.Command{
	Use:   "set-path <name> <path>",
	Short: "Set a windows source's lease-export file path",
	Long: `Set connection_config.path for a source_type = 'windows' source:
the JSON lease-export file produced by scripts/export-dhcp-leases.ps1 and
made reachable to the daemon (e.g. an SMB mount). Other keys in
connection_config are preserved.

Takes effect on the next scheduled poll cycle — no daemon restart needed.`,
	Args: cobra.ExactArgs(2),
	Run:  runDHCPSourcesSetPath,
}

func init() {
	rootCmd.AddCommand(dhcpSourcesCmd)
	dhcpSourcesCmd.AddCommand(dhcpSourcesListCmd)
	dhcpSourcesCmd.AddCommand(dhcpSourcesSetPathCmd)
}

func runDHCPSourcesList(cmd *cobra.Command, args []string) {
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	databaseURL, err := db.DatabaseURLFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	sources, err := store.New(pool).ListAllDHCPSources(ctx)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%-28s %-10s %-8s %-24s %s\n", "NAME", "TYPE", "ENABLED", "STATUS", "CONFIG")
	for _, src := range sources {
		fmt.Printf("%-28s %-10s %-8s %-24s %s\n",
			src.Name, src.SourceType, yesNo(src.Enabled), dhcpStatus(src), string(src.ConnectionConfig))
	}
	fmt.Printf("\n%d source(s)\n", len(sources))
}

// dhcpStatus summarises whether connection_config looks usable for the
// source's type — the "is this configured" answer at a glance.
func dhcpStatus(src domain.DHCPSource) string {
	var cfg map[string]any
	if err := json.Unmarshal(src.ConnectionConfig, &cfg); err != nil {
		return "unparsable config"
	}
	switch src.SourceType {
	case "windows":
		if p, _ := cfg["path"].(string); p != "" {
			return "ok"
		}
		return "missing path"
	case "mikrotik":
		if h, _ := cfg["host"].(string); h != "" {
			if u, _ := cfg["username"].(string); u != "" {
				return "ok"
			}
			return "missing username"
		}
		return "missing host"
	default:
		return "n/a"
	}
}

func runDHCPSourcesSetPath(cmd *cobra.Command, args []string) {
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	name := strings.TrimSpace(args[0])
	path := strings.TrimSpace(args[1])
	if path == "" {
		log.Fatal("path cannot be empty")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	databaseURL, err := db.DatabaseURLFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	id, err := store.New(pool).SetDHCPSourcePath(ctx, name, path)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("source %d (%s): path -> %s\n", id, name, path)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
