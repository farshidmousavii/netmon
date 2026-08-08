package cli

import (
	"context"
	"fmt"
	"log"

	"github.com/farshidmousavii/bidar/internal/db"
	"github.com/farshidmousavii/bidar/internal/logger"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Apply database migrations",
	Long: `Apply pending golang-migrate migrations (embedded in the binary)
to the Postgres database in BIDAR_DATABASE_URL. Safe to run repeatedly —
already-applied migrations are a no-op.

Environment-configured (not the --config flag):
  BIDAR_DATABASE_URL   Postgres connection string (required)`,
	Run: runMigrate,
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}

func runMigrate(cmd *cobra.Command, args []string) {
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	version, err := migrateSchema(cmd.Context())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Migrations applied. Schema version: %d\n", version)
}

// migrateSchema applies all pending migrations and returns the resulting
// schema version. Separated from runMigrate so tests can call it directly.
func migrateSchema(ctx context.Context) (uint, error) {
	databaseURL, err := db.DatabaseURLFromEnv()
	if err != nil {
		return 0, err
	}

	if err := db.Migrate(ctx, databaseURL); err != nil {
		return 0, err
	}

	// Report the resulting version (golang-migrate's schema_migrations
	// bookkeeping is authoritative).
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		return 0, err
	}
	defer pool.Close()

	version, dirty, err := db.SchemaVersion(ctx, pool)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	if dirty {
		return 0, fmt.Errorf("schema is dirty at version %d: fix the failed migration and force-version", version)
	}
	return version, nil
}
