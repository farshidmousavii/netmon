package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
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

var dhcpSourcesAddCmd = &cobra.Command{
	Use:   "add <name> <mikrotik|isc|other>",
	Short: "Add a DHCP source",
	Long: `Add a DHCP lease evidence source.

  mikrotik: --host <addr> --username <user> --password <pass>
                           RouterOS API credentials (password is
                           encrypted at rest with BIDAR_MASTER_KEY).
  isc/other: no extra fields (accepted placeholders, not collected
                           in Phase 1).

windows and cisco are recognized schema types but deliberately
unimplemented in Phase 1 (only mikrotik is) — this command rejects them
with a clear error rather than creating a source that can never poll.
See docs/architecture.md §Phase 1 for the reasoning.

The daemon picks the new source up on its next poll cycle — no restart.`,
	Args: cobra.ExactArgs(2),
	Run:  runDHCPSourcesAdd,
}

func init() {
	rootCmd.AddCommand(dhcpSourcesCmd)
	dhcpSourcesCmd.AddCommand(dhcpSourcesListCmd)
	dhcpSourcesCmd.AddCommand(dhcpSourcesAddCmd)

	dhcpSourcesAddCmd.Flags().String("host", "", "RouterOS API host (mikrotik)")
	dhcpSourcesAddCmd.Flags().String("username", "", "RouterOS API username (mikrotik)")
	dhcpSourcesAddCmd.Flags().String("password", "", "RouterOS API password (mikrotik; encrypted at rest)")
}

func runDHCPSourcesAdd(cmd *cobra.Command, args []string) {
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	name := strings.TrimSpace(args[0])
	sourceType := strings.TrimSpace(args[1])
	if !store.ValidDHCPSourceTypes[sourceType] {
		log.Fatalf("invalid source type %q (use mikrotik, isc or other)", sourceType)
	}
	if sourceType == "windows" || sourceType == "cisco" {
		log.Fatalf("source_type %q is not implemented in Phase 1 (only mikrotik is supported); see docs/architecture.md", sourceType)
	}

	host, _ := cmd.Flags().GetString("host")
	username, _ := cmd.Flags().GetString("username")
	password, _ := cmd.Flags().GetString("password")

	cfg := map[string]any{}
	switch sourceType {
	case "mikrotik":
		if host == "" || username == "" || password == "" {
			log.Fatal("mikrotik sources need --host, --username and --password")
		}
		cfg["host"] = host
		cfg["username"] = username
	default:
		// isc/other: no extra fields in Phase 1.
	}
	connConfig, err := json.Marshal(cfg)
	if err != nil {
		log.Fatal(err)
	}

	// Only mikrotik carries a credential to encrypt.
	var credEnc []byte
	if password != "" {
		enc, err := crypto.NewFromEnv()
		if err != nil {
			log.Fatal(err)
		}
		credEnc, err = enc.Encrypt([]byte(password))
		if err != nil {
			log.Fatal(err)
		}
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

	id, err := store.New(pool).AddDHCPSource(ctx, name, sourceType, connConfig, credEnc)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("source %d (%s, %s) added\n", id, name, sourceType)
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

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
