package store

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/farshidmousavii/bidar/internal/domain"
)

// ValidDeviceRoles are the only allowed network_devices.role values.
var ValidDeviceRoles = map[string]bool{
	"core":       true,
	"access":     true,
	"unassigned": true,
}

// ListDevices returns every device, optionally filtered by role. Unlike
// ListCoreDevices it does not filter on enabled — the admin CLI needs the
// full picture.
func (s *Store) ListDevices(ctx context.Context, role *string) ([]domain.Device, error) {
	query := `
		SELECT id, name, mgmt_ip, protocol_family, role, enabled, snmp_profile_id
		FROM network_devices`
	var args []any
	if role != nil {
		query += ` WHERE role = $1`
		args = append(args, *role)
	}
	query += ` ORDER BY name`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	var devices []domain.Device
	for rows.Next() {
		var d domain.Device
		if err := rows.Scan(&d.ID, &d.Name, &d.MgmtIP, &d.ProtocolFamily, &d.Role, &d.Enabled, &d.SNMPProfileID); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate devices: %w", err)
	}
	return devices, nil
}

// SetDeviceRole updates a device's role, matching by exact name
// (case-insensitive) or by mgmt_ip. Matching by name must be unambiguous
// — if several rows share the name, the caller is told to use mgmt_ip.
// The same query the ARP provider runs each cycle (ListCoreDevices)
// reflects the change immediately, so a live daemon picks it up on the
// next poll with no restart.
func (s *Store) SetDeviceRole(ctx context.Context, nameOrIP, role string) (int64, error) {
	if !ValidDeviceRoles[role] {
		return 0, fmt.Errorf("invalid role %q (use core, access or unassigned)", role)
	}
	id, err := s.resolveDeviceID(ctx, nameOrIP)
	if err != nil {
		return 0, err
	}
	return s.updateDeviceRole(ctx, id, role)
}

// resolveDeviceID matches a device by exact name (case-insensitive,
// must be unambiguous) or by mgmt_ip — the shared lookup behind the
// admin CLI's per-device setters.
func (s *Store) resolveDeviceID(ctx context.Context, nameOrIP string) (int64, error) {
	// Exact-name match first (must be unique).
	rows, err := s.pool.Query(ctx, `SELECT id, name FROM network_devices WHERE lower(name) = lower($1)`, nameOrIP)
	if err != nil {
		return 0, fmt.Errorf("look up device by name: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan device: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate name matches: %w", err)
	}
	if len(ids) > 1 {
		return 0, fmt.Errorf("name %q matches %d devices; use the mgmt_ip instead", nameOrIP, len(ids))
	}
	if len(ids) == 1 {
		return ids[0], nil
	}

	// Fall back to mgmt_ip.
	ip, err := netip.ParseAddr(nameOrIP)
	if err != nil {
		return 0, fmt.Errorf("%w: no device named %q", ErrNotFound, nameOrIP)
	}
	var id int64
	err = s.pool.QueryRow(ctx, `SELECT id FROM network_devices WHERE mgmt_ip = $1::inet`, ip).Scan(&id)
	if err != nil {
		return 0, notFound(err)
	}
	return id, nil
}

// GetDeviceByID returns one device by id, or ErrNotFound. Same column
// set as ListEnabledDevicesByFamily — the queue executors' loader.
func (s *Store) GetDeviceByID(ctx context.Context, id int64) (*domain.Device, error) {
	var d domain.Device
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, mgmt_ip, coalesce(function, ''), protocol_family, role, enabled, snmp_profile_id, poll_interval_sec,
		       coalesce(routeros_username, ''), routeros_password_enc,
		       coalesce(routeros_port, 8728),
		       last_poll_at, consecutive_failures
		FROM network_devices WHERE id = $1`, id).Scan(&d.ID, &d.Name, &d.MgmtIP, &d.Function, &d.ProtocolFamily, &d.Role,
		&d.Enabled, &d.SNMPProfileID, &d.PollIntervalSec,
		&d.RouterOSUsername, &d.RouterOSPasswordEnc, &d.RouterOSPort,
		&d.LastPollAt, &d.ConsecutiveFailures)
	if err != nil {
		return nil, notFound(err)
	}
	return &d, nil
}

// SetDeviceEnabled enables or disables a device by name-or-mgmt_ip.
// Disabled devices are skipped by every poller (the Phase 2 queue and
// the ARP collector both filter on enabled) without deleting any of
// their history — the right answer for decommissioned equipment.
func (s *Store) SetDeviceEnabled(ctx context.Context, nameOrIP string, enabled bool) (int64, error) {
	id, err := s.resolveDeviceID(ctx, nameOrIP)
	if err != nil {
		return 0, err
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE network_devices SET enabled = $2, updated_at = now() WHERE id = $1`, id, enabled)
	if err != nil {
		return 0, fmt.Errorf("set device enabled: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return 0, fmt.Errorf("%w", ErrNotFound)
	}
	return id, nil
}

// SetDeviceRouterOSAuth stores a MikroTik device's RouterOS API
// credentials (password already encrypted by the caller) and optionally
// its API port (nil leaves the stored value unchanged). Only
// mikrotik_routeros devices accept RouterOS credentials.
func (s *Store) SetDeviceRouterOSAuth(ctx context.Context, nameOrIP, username string, passwordEnc []byte, port *int32) (int64, error) {
	if strings.TrimSpace(username) == "" {
		return 0, fmt.Errorf("routeros username cannot be empty")
	}
	id, err := s.resolveDeviceID(ctx, nameOrIP)
	if err != nil {
		return 0, err
	}
	var family string
	if err := s.pool.QueryRow(ctx,
		`SELECT protocol_family FROM network_devices WHERE id = $1`, id).Scan(&family); err != nil {
		return 0, fmt.Errorf("load device: %w", err)
	}
	if family != "mikrotik_routeros" {
		return 0, fmt.Errorf("device %q is %s, not mikrotik_routeros — RouterOS credentials only apply to MikroTik devices", nameOrIP, family)
	}

	if port != nil {
		_, err = s.pool.Exec(ctx, `
			UPDATE network_devices
			SET routeros_username = $2, routeros_password_enc = $3, routeros_port = $4, updated_at = now()
			WHERE id = $1`, id, username, passwordEnc, *port)
	} else {
		_, err = s.pool.Exec(ctx, `
			UPDATE network_devices
			SET routeros_username = $2, routeros_password_enc = $3, updated_at = now()
			WHERE id = $1`, id, username, passwordEnc)
	}
	if err != nil {
		return 0, fmt.Errorf("set routeros auth: %w", err)
	}
	return id, nil
}

func (s *Store) updateDeviceRole(ctx context.Context, id int64, role string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE network_devices SET role = $2, updated_at = now() WHERE id = $1`, id, role)
	if err != nil {
		return 0, fmt.Errorf("set role: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return 0, fmt.Errorf("%w", ErrNotFound)
	}
	return id, nil
}
func (s *Store) ListCoreDevices(ctx context.Context) ([]domain.Device, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, mgmt_ip, protocol_family, role, enabled, snmp_profile_id
		FROM network_devices
		WHERE role = 'core' AND enabled = true
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list core devices: %w", err)
	}
	defer rows.Close()

	var devices []domain.Device
	for rows.Next() {
		var d domain.Device
		if err := rows.Scan(&d.ID, &d.Name, &d.MgmtIP, &d.ProtocolFamily, &d.Role, &d.Enabled, &d.SNMPProfileID); err != nil {
			return nil, fmt.Errorf("scan core device: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate core devices: %w", err)
	}
	return devices, nil
}

// ListEnabledDevicesByFamily returns every enabled device of one
// protocol_family (cisco_snmp | mikrotik_routeros) with its poll interval
// — the Phase 2 enqueue loop's source of "what is due". Role plays no part:
// it is an ARP-collector-only concept.
func (s *Store) ListEnabledDevicesByFamily(ctx context.Context, family string) ([]domain.Device, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, mgmt_ip, coalesce(function, ''), protocol_family, role, enabled, snmp_profile_id, poll_interval_sec,
		       coalesce(routeros_username, ''), routeros_password_enc,
		       coalesce(routeros_port, 8728),
		       last_poll_at, consecutive_failures
		FROM network_devices
		WHERE protocol_family = $1 AND enabled = true
		ORDER BY name`, family)
	if err != nil {
		return nil, fmt.Errorf("list %s devices: %w", family, err)
	}
	defer rows.Close()

	var devices []domain.Device
	for rows.Next() {
		var d domain.Device
		if err := rows.Scan(&d.ID, &d.Name, &d.MgmtIP, &d.Function, &d.ProtocolFamily, &d.Role,
			&d.Enabled, &d.SNMPProfileID, &d.PollIntervalSec,
			&d.RouterOSUsername, &d.RouterOSPasswordEnc, &d.RouterOSPort,
			&d.LastPollAt, &d.ConsecutiveFailures); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate devices: %w", err)
	}
	return devices, nil
}

// UpdateDevicePollHealth records one poll outcome for a device. A success
// resets consecutive_failures and refreshes last_seen_at; a failure stores
// the error and increments the counter (the circuit breaker's input).
// last_poll_at updates either way.
func (s *Store) UpdateDevicePollHealth(ctx context.Context, id int64, pollErr *string, at time.Time) error {
	var tag pgconn.CommandTag
	var err error
	if pollErr == nil {
		tag, err = s.pool.Exec(ctx, `
			UPDATE network_devices
			SET last_poll_at = $2, last_seen_at = $2, last_error = NULL, consecutive_failures = 0,
			    updated_at = now()
			WHERE id = $1`, id, at)
	} else {
		tag, err = s.pool.Exec(ctx, `
			UPDATE network_devices
			SET last_poll_at = $2, last_error = $3, consecutive_failures = consecutive_failures + 1,
			    updated_at = now()
			WHERE id = $1`, id, at, *pollErr)
	}
	if err != nil {
		return fmt.Errorf("update device poll health: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w", ErrNotFound)
	}
	return nil
}

// UpdateDeviceFirmwareVersion stores a polled device's reported firmware
// string (parsed from sysDescr by the SNMP provider). Best-effort
// enrichment — never overwrites with an empty value.
func (s *Store) UpdateDeviceFirmwareVersion(ctx context.Context, id int64, version string) error {
	if strings.TrimSpace(version) == "" {
		return nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE network_devices SET firmware_version = $2, updated_at = now() WHERE id = $1`,
		id, version)
	if err != nil {
		return fmt.Errorf("update device firmware: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w", ErrNotFound)
	}
	return nil
}

// GetSNMPProfile returns the profile a device polls with — including the
// v3 USM fields (ciphertext; decrypt via internal/crypto before handing to
// snmp.ConfigFromProfile) — or ErrNotFound.
func (s *Store) GetSNMPProfile(ctx context.Context, id int64) (*domain.SNMPProfile, error) {
	var p domain.SNMPProfile
	err := s.pool.QueryRow(ctx, `
		SELECT id, version, community_encrypted,
		       coalesce(v3_username, ''), coalesce(v3_auth_protocol, ''),
		       v3_auth_key_encrypted,
		       coalesce(v3_priv_protocol, ''),
		       v3_priv_key_encrypted,
		       timeout_ms, retries
		FROM snmp_profiles
		WHERE id = $1`, id).Scan(&p.ID, &p.Version, &p.CommunityEncrypted,
		&p.V3Username, &p.V3AuthProtocol, &p.V3AuthKeyEncrypted,
		&p.V3PrivProtocol, &p.V3PrivKeyEncrypted,
		&p.TimeoutMS, &p.Retries)
	if err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}
