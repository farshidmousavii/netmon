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

	// ADURL is the Active Directory LDAP(S) endpoint for the AD provider
	// (Phase 1), e.g. ldaps://<dc-hostname>.
	ADURL = "BIDAR_AD_URL"

	// ADBindDN is the read-only service account DN used to bind to AD.
	ADBindDN = "BIDAR_AD_BIND_DN"

	// ADBindPassword is the service account password (env var, never
	// logged or committed).
	ADBindPassword = "BIDAR_AD_BIND_PASSWORD"

	// ADBaseDN is the search base for computer objects, e.g.
	// "DC=corp,DC=local".
	ADBaseDN = "BIDAR_AD_BASE_DN"

	// ADInterval is how often the AD provider syncs (Go duration,
	// default 24h).
	ADInterval = "BIDAR_AD_INTERVAL"

	// ARPInterval is how often the ARP collector polls core devices
	// (Go duration, default 5m).
	ARPInterval = "BIDAR_ARP_INTERVAL"

	// DHCPInterval is how often the DHCP collector polls its sources
	// (Go duration, default 5m).
	DHCPInterval = "BIDAR_DHCP_INTERVAL"

	// JobWorkers is the Phase 2 polling queue's worker count
	// (discovery_jobs consumers).
	JobWorkers = "BIDAR_JOB_WORKERS"

	// EnqueueInterval is how often the queue's enqueue pass runs — it
	// checks every enabled device against its poll interval and the
	// circuit-breaker backoff, enqueuing due ones.
	EnqueueInterval = "BIDAR_ENQUEUE_INTERVAL"

	// JobLease bounds one claimed job: a worker crashing mid-poll leaves
	// the lease to expire, making the job claimable again.
	JobLease = "BIDAR_JOB_LEASE"

	// ICMPInterval is how often the ICMP sweep runs (Go duration,
	// default 5m).
	ICMPInterval = "BIDAR_ICMP_INTERVAL"

	// TestDatabaseURL points integration tests at a scratch Postgres
	// (test-only; not a runtime configuration variable).
	TestDatabaseURL = "BIDAR_TEST_DATABASE_URL"
)
