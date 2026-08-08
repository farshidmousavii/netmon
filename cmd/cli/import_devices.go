package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/farshidmousavii/bidar/internal/config"
	"github.com/farshidmousavii/bidar/internal/crypto"
	"github.com/farshidmousavii/bidar/internal/db"
	"github.com/farshidmousavii/bidar/internal/logger"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

var importDevicesCmd = &cobra.Command{
	Use:   "import-devices",
	Short: "Import config.yaml/devices.csv devices into the database",
	Long: `Import devices from an existing config.yaml or devices.csv into the
Postgres canonical device list (network_devices, snmp_profiles,
ssh_credentials).

Reads the same file format monitor/backup/exec use (auto-detected by
extension), writes rows via BIDAR_DATABASE_URL, and encrypts credentials
with BIDAR_MASTER_KEY (base64 32-byte key).

Strictly one-directional: the source file is never modified.

Devices are imported with role='unassigned' and are not polled by the
Phase 1 ARP collector until an operator explicitly assigns role='core'.`,
	Run: runImportDevices,
}

func init() {
	rootCmd.AddCommand(importDevicesCmd)
}

// importSummary is what runImportDevices prints; kept separate from the
// side-effecting import so tests can assert on it without parsing stdout.
type importSummary struct {
	DevicesImported int
	Unassigned      []string // "name (ip)" of every device at role=unassigned
}

func runImportDevices(cmd *cobra.Command, args []string) {
	if err := logger.Init(false); err != nil {
		log.Fatal(err)
	}

	summary, err := importDevices(cmd.Context(), configPath)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println()
	fmt.Println("Import complete.")
	fmt.Printf("Devices imported: %d\n", summary.DevicesImported)
	fmt.Printf("Devices at role=unassigned: %d\n", len(summary.Unassigned))
	for _, d := range summary.Unassigned {
		fmt.Printf("  - %s\n", d)
	}
	if len(summary.Unassigned) > 0 {
		fmt.Println()
		fmt.Println("Note: Phase 1's ARP collector only polls devices with role=core.")
		fmt.Println("Assign core/access manually (UPDATE network_devices SET role='core' ...) ")
		fmt.Println("before Phase 1; re-running this import will not change roles.")
	}
}

// importDevices loads configPath via the existing YAML/CSV parser, writes
// network_devices / snmp_profiles / ssh_credentials rows, and returns the
// post-import state for the summary.
//
// Idempotency: devices match on mgmt_ip (update existing rows, preserving
// their role and poll-health fields); snmp_profiles and ssh_credentials
// match on name (reused, never overwritten). See the report on Task 5 for
// the rationale.
func importDevices(ctx context.Context, configPath string) (*importSummary, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", configPath, err)
	}

	// Master key before anything else: fail before touching the database.
	enc, err := crypto.NewFromEnv()
	if err != nil {
		return nil, err
	}

	databaseURL, err := db.DatabaseURLFromEnv()
	if err != nil {
		return nil, err
	}
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	// The config's global SNMP block -> exactly one snmp_profiles row.
	var snmpProfileID *int64
	if cfg.SNMP != nil {
		id, err := upsertSNMPProfile(ctx, pool, enc, profileNameFor(configPath), cfg.SNMP)
		if err != nil {
			return nil, err
		}
		snmpProfileID = &id
	}

	// Each credential entry -> one ssh_credentials row, deduplicated by
	// name (several devices sharing a YAML credential share one row).
	credIDs := make(map[string]int64, len(cfg.Credentials))
	for name := range cfg.Credentials {
		cred := cfg.Credentials[name]
		id, err := upsertSSHCredential(ctx, pool, enc, name, cred, credentialPort(cfg, name))
		if err != nil {
			return nil, fmt.Errorf("credential %q: %w", name, err)
		}
		credIDs[name] = id
	}

	imported := 0
	for _, d := range cfg.Devices {
		family, err := protocolFamily(d.Vendor)
		if err != nil {
			return nil, fmt.Errorf("device %q: %w", d.Name, err)
		}
		if err := upsertDevice(ctx, pool, d, family, snmpProfileID, credIDs[d.Credential]); err != nil {
			return nil, fmt.Errorf("device %q: %w", d.Name, err)
		}
		imported++
	}

	unassigned, err := listUnassigned(ctx, pool)
	if err != nil {
		return nil, err
	}

	return &importSummary{DevicesImported: imported, Unassigned: unassigned}, nil
}

// protocolFamily maps the config's vendor field to network_devices.
// protocol_family (database-schema.md): cisco -> cisco_snmp,
// mikrotik -> mikrotik_routeros.
func protocolFamily(vendor string) (string, error) {
	switch strings.ToLower(vendor) {
	case "cisco":
		return "cisco_snmp", nil
	case "mikrotik":
		return "mikrotik_routeros", nil
	default:
		return "", fmt.Errorf("unsupported vendor %q (import maps cisco/mikrotik only)", vendor)
	}
}

// profileNameFor makes the single imported SNMP profile traceable back to
// its source file: "imported-config.yaml", "imported-devices.csv", ...
func profileNameFor(configPath string) string {
	return "imported-" + filepath.Base(configPath)
}

// credentialPort returns the port to store on a shared ssh_credentials row:
// the port of the first device referencing the credential. The CLI's
// exec/backup path actually reads the port from the per-device config, so
// this column is informational; per-device port differences stay per-device.
func credentialPort(cfg *config.Config, name string) int {
	for _, d := range cfg.Devices {
		if d.Credential == name {
			if p, err := strconv.Atoi(d.Port); err == nil && p > 0 && p <= 65535 {
				return p
			}
			return 22
		}
	}
	return 22
}

// upsertSNMPProfile reuses the profile when one with the same name exists
// (never overwrites it — an operator may have tuned timeout/retries or
// rotated the community after a previous import), otherwise inserts one.
func upsertSNMPProfile(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, name string, snmpCfg *config.SNMPConfig) (int64, error) {
	var id int64
	err := pool.QueryRow(ctx, `SELECT id FROM snmp_profiles WHERE name = $1`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("look up snmp profile: %w", err)
	}

	communityEnc, err := enc.Encrypt([]byte(snmpCfg.Community))
	if err != nil {
		return 0, fmt.Errorf("encrypt snmp community: %w", err)
	}
	err = pool.QueryRow(ctx, `
		INSERT INTO snmp_profiles (name, version, community_encrypted, timeout_ms)
		VALUES ($1, 'v2c', $2, $3)
		RETURNING id`,
		name, communityEnc, snmpCfg.Timeout*1000).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert snmp profile: %w", err)
	}
	return id, nil
}

// upsertSSHCredential: same reuse-by-name policy as upsertSNMPProfile.
func upsertSSHCredential(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, name string, cred config.CredentialInfo, port int) (int64, error) {
	var id int64
	err := pool.QueryRow(ctx, `SELECT id FROM ssh_credentials WHERE name = $1`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("look up ssh credential: %w", err)
	}

	passwordEnc, err := enc.Encrypt([]byte(cred.Password))
	if err != nil {
		return 0, fmt.Errorf("encrypt ssh password: %w", err)
	}
	err = pool.QueryRow(ctx, `
		INSERT INTO ssh_credentials (name, username, password_encrypted, port)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		name, cred.Username, passwordEnc, port).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert ssh credential: %w", err)
	}
	return id, nil
}

// upsertDevice inserts a network_devices row or updates the existing row
// with the same mgmt_ip. role and the poll-health fields are never touched
// by an update, so manual core/access assignment survives re-imports.
func upsertDevice(ctx context.Context, pool *pgxpool.Pool, d config.DeviceConfig, family string, snmpProfileID *int64, credID int64) error {
	var existing int64
	err := pool.QueryRow(ctx, `SELECT id FROM network_devices WHERE mgmt_ip = $1::inet`, d.IP).Scan(&existing)
	switch {
	case err == nil:
		_, err = pool.Exec(ctx, `
			UPDATE network_devices SET
				name = $1,
				protocol_family = $2,
				function = NULLIF($3, ''),
				enabled = true,
				snmp_profile_id = $4,
				ssh_credential_id = $5,
				updated_at = now()
			WHERE id = $6`,
			d.Name, family, d.Type, snmpProfileID, credID, existing)
		if err != nil {
			return fmt.Errorf("update device: %w", err)
		}
		return nil
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("look up device: %w", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO network_devices (name, protocol_family, function, role, mgmt_ip, enabled, snmp_profile_id, ssh_credential_id)
		VALUES ($1, $2, NULLIF($3, ''), 'unassigned', $4::inet, true, $5, $6)`,
		d.Name, family, d.Type, d.IP, snmpProfileID, credID)
	if err != nil {
		return fmt.Errorf("insert device: %w", err)
	}
	return nil
}

func listUnassigned(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT name, host(mgmt_ip) FROM network_devices
		WHERE role = 'unassigned'
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list unassigned devices: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name, ip string
		if err := rows.Scan(&name, &ip); err != nil {
			return nil, fmt.Errorf("scan unassigned device: %w", err)
		}
		out = append(out, fmt.Sprintf("%s (%s)", name, ip))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unassigned devices: %w", err)
	}
	return out, nil
}
