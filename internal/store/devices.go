package store

import (
	"context"
	"fmt"

	"github.com/farshidmousavii/bidar/internal/domain"
)

// ListCoreDevices returns every enabled device the ARP collector polls:
// role='core' (Phase 1 scope — access devices are Phase 2).
func (s *Store) ListCoreDevices(ctx context.Context) ([]domain.Device, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, mgmt_ip, protocol_family, role, snmp_profile_id
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
		if err := rows.Scan(&d.ID, &d.Name, &d.MgmtIP, &d.ProtocolFamily, &d.Role, &d.SNMPProfileID); err != nil {
			return nil, fmt.Errorf("scan core device: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate core devices: %w", err)
	}
	return devices, nil
}

// GetSNMPProfile returns the profile a device polls with, or ErrNotFound.
func (s *Store) GetSNMPProfile(ctx context.Context, id int64) (*domain.SNMPProfile, error) {
	var p domain.SNMPProfile
	err := s.pool.QueryRow(ctx, `
		SELECT id, version, community_encrypted, timeout_ms, retries
		FROM snmp_profiles
		WHERE id = $1`, id).Scan(&p.ID, &p.Version, &p.CommunityEncrypted, &p.TimeoutMS, &p.Retries)
	if err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}
