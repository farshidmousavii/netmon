package store

// device_vlans persistence for the Phase 2 SNMP provider.

import (
	"context"
	"fmt"
	"time"

	"github.com/farshidmousavii/bidar/internal/domain"
)

// UpsertDeviceVLANs writes one polled device's VLAN list, keyed by
// (device_id, vlan_number).
func (s *Store) UpsertDeviceVLANs(ctx context.Context, deviceID int64, vlans []domain.DeviceVLAN, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin vlan sync: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		INSERT INTO device_vlans (device_id, vlan_number, name)
		VALUES ($1, $2, $3)
		ON CONFLICT (device_id, vlan_number) DO UPDATE SET
		    name = EXCLUDED.name`

	for _, v := range vlans {
		if _, err := tx.Exec(ctx, q, deviceID, v.VlanNumber, v.Name); err != nil {
			return fmt.Errorf("upsert vlan %d: %w", v.VlanNumber, err)
		}
	}

	return tx.Commit(ctx)
}
