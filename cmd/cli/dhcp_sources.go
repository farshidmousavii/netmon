package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/db"
	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/envconfig"
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

var dhcpSourcesAddCmd = &cobra.Command{
	Use:   "add <name> <windows|mikrotik|isc|other>",
	Short: "Add a DHCP source",
	Long: `Add a DHCP lease evidence source.

  windows:  --path <file>  lease-export file (as seen inside the daemon
                           container; mount the SMB share into the
                           container first, e.g. at /mnt/dhcp). Optional
                           here — set later with set-path.
  mikrotik: --host <addr> --username <user> --password <pass>
                           RouterOS API credentials (password is
                           encrypted at rest with BIDAR_MASTER_KEY).
  isc/other: no extra fields (not collected in Phase 1).

The daemon picks the new source up on its next poll cycle — no restart.`,
	Args: cobra.ExactArgs(2),
	Run:  runDHCPSourcesAdd,
}

func init() {
	rootCmd.AddCommand(dhcpSourcesCmd)
	dhcpSourcesCmd.AddCommand(dhcpSourcesListCmd)
	dhcpSourcesCmd.AddCommand(dhcpSourcesSetPathCmd)
	dhcpSourcesCmd.AddCommand(dhcpSourcesAddCmd)

	dhcpSourcesAddCmd.Flags().String("host", "", "RouterOS API host (mikrotik)")
	dhcpSourcesAddCmd.Flags().String("username", "", "RouterOS API username (mikrotik)")
	dhcpSourcesAddCmd.Flags().String("password", "", "RouterOS API password (mikrotik; encrypted at rest)")
	dhcpSourcesAddCmd.Flags().String("path", "", "lease-export file path (windows)")
}

func runDHCPSourcesAdd(cmd *cobra.Command, args []string) {
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	name := strings.TrimSpace(args[0])
	sourceType := strings.TrimSpace(args[1])
	if !store.ValidDHCPSourceTypes[sourceType] {
		log.Fatalf("invalid source type %q (use windows, mikrotik, isc or other)", sourceType)
	}

	host, _ := cmd.Flags().GetString("host")
	username, _ := cmd.Flags().GetString("username")
	password, _ := cmd.Flags().GetString("password")
	path, _ := cmd.Flags().GetString("path")

	cfg := map[string]any{}
	switch sourceType {
	case "windows":
		if path != "" {
			cfg["path"] = path
		}
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

// dhcpMountPoint is where docker-compose.yml mounts the DHCP share
// inside the daemon container.
const dhcpMountPoint = "/mnt/dhcp"

func runDHCPSourcesSetPath(cmd *cobra.Command, args []string) {
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	name := strings.TrimSpace(args[0])
	path := strings.TrimSpace(args[1])
	if path == "" {
		log.Fatal("path cannot be empty")
	}

	// Accept both the container-internal form (/mnt/dhcp/...) and the
	// host/Windows form (\\server\share\... or Z:\...) the operator
	// actually sees; the latter is translated against BIDAR_DHCP_SHARE_SRC.
	path, warn := translateDHCPPath(os.Getenv(envconfig.DHCPShareSrc), dhcpMountPoint, path)
	if warn != "" {
		logger.Warning("%s", warn)
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

// translateDHCPPath maps the path an operator types into the path the
// daemon container can actually read:
//
//   - already container-internal (starts with mountPoint) -> unchanged;
//   - starts with the configured share src (shareSrc) -> rewritten to
//     mountPoint/<relative>, handling Windows separators and UNC/drive
//     letter forms;
//   - anything else -> stored as-is, with a warning when it looks like a
//     Windows path that doesn't match the configured share (the daemon
//     can only read mounted paths).
func translateDHCPPath(shareSrc, mountPoint, given string) (string, string) {
	g := normalizeWinPath(given)
	if mountPoint != "" && (g == mountPoint || strings.HasPrefix(g, mountPoint+"/")) {
		return g, ""
	}

	src := normalizeWinPath(shareSrc)
	if src == "" || src == "." || src == ".." || strings.HasPrefix(src, "./") {
		// No meaningful share configured: keep the path verbatim.
		return given, ""
	}

	lowerG, lowerSrc := strings.ToLower(g), strings.ToLower(src)
	switch {
	case lowerG == lowerSrc:
		return mountPoint, ""
	case strings.HasPrefix(lowerG, lowerSrc+"/"):
		return mountPoint + "/" + g[len(src)+1:], ""
	case looksLikeWindowsPath(g):
		return given, fmt.Sprintf("path %q does not start with the configured share %q (mounted at %s); the daemon can only read mounted paths",
			given, shareSrc, mountPoint)
	default:
		return given, ""
	}
}

// normalizeWinPath converts Windows separators to forward slashes.
func normalizeWinPath(p string) string {
	return strings.TrimRight(strings.ReplaceAll(p, "\\", "/"), "/")
}

// looksLikeWindowsPath reports whether p is a UNC path (//server/share)
// or a drive-letter path (Z:/...).
func looksLikeWindowsPath(p string) bool {
	return strings.HasPrefix(p, "//") || (len(p) >= 2 && p[1] == ':')
}
