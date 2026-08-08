package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/db"
	"github.com/farshidmousavii/bidar/internal/dlog"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the inventory daemon",
	Long: `Run the inventory daemon. This Phase 0 stub proves the daemon shape
end to end: environment configuration, database connectivity, schema
version check, and graceful shutdown on SIGINT/SIGTERM. The actual
providers (AD, ARP, DHCP, ICMP) arrive in Phase 1.

Environment (the daemon is env-configured, not --config):
  BIDAR_DATABASE_URL   Postgres connection string (required)
  BIDAR_MASTER_KEY     base64 32-byte key (required; validated at startup)
  BIDAR_LOG_LEVEL      debug|info|warn|error (default info)

The schema must already be migrated with ` + "`bidar migrate`" + ` — serve
never applies migrations itself and fails clearly if the schema is
missing, dirty, or at the wrong version.`,
	Run: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) {
	if err := serve(cmd.Context()); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

// serve runs the daemon until ctx is cancelled (SIGINT/SIGTERM via
// cmd/bidar/main.go's signal wiring). Every configuration input is
// validated BEFORE the startup message is logged, so a misconfigured
// daemon fails loud and immediately instead of hanging ambiguously.
func serve(ctx context.Context) error {
	logger, err := dlog.NewFromEnv()
	if err != nil {
		return err
	}

	databaseURL, err := db.DatabaseURLFromEnv()
	if err != nil {
		return err
	}

	// Master key: validated at startup even though this stub reads no
	// encrypted data. Every Phase 1+ provider that touches snmp_profiles /
	// dhcp_sources needs it, and a daemon that comes up without its key
	// would fail mid-operation later — failing now is cheaper. Nothing is
	// held beyond this check.
	if _, err := crypto.NewFromEnv(); err != nil {
		return err
	}

	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	version, err := db.EnsureSchemaUpToDate(ctx, pool)
	if err != nil {
		return err
	}

	level, err := dlog.LevelFromEnv()
	if err != nil {
		return err
	}
	logger.Info("starting",
		"log_level", level.String(),
		"database_reachable", true,
		"schema_version", version,
	)

	// Block until SIGINT/SIGTERM cancels the root context.
	<-ctx.Done()

	logger.Info("shutting down", "reason", ctx.Err().Error())
	return nil
}
