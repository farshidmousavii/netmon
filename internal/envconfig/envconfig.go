// Package envconfig is the single source of truth for every BIDAR_*
// environment variable name the project uses. The *FromEnv() constructors
// in internal/db, internal/crypto, and internal/dlog reference these
// constants; .env.example is generated against them; nothing hardcodes a
// BIDAR_* string anywhere else in the Go source (verified by grep in the
// Phase 0 addendum DoD).
package envconfig

const (
	// DatabaseURL is the Postgres connection string (postgres://...).
	// Required by serve, migrate, and import-devices.
	DatabaseURL = "BIDAR_DATABASE_URL"

	// MasterKey is the base64-encoded 32-byte key that encrypts secrets
	// at rest (snmp_profiles, ssh_credentials, dhcp_sources). Generate
	// with `openssl rand -base64 32`; never use the .env.example
	// placeholder in production.
	MasterKey = "BIDAR_MASTER_KEY"

	// LogLevel controls the daemon's slog level:
	// debug | info | warn | error (default info).
	LogLevel = "BIDAR_LOG_LEVEL"

	// TestDatabaseURL points integration tests at a scratch Postgres
	// (test-only; not a runtime configuration variable).
	TestDatabaseURL = "BIDAR_TEST_DATABASE_URL"
)
