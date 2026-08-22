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

// ListDeviceVLANNumbers returns the VLAN numbers last seen on a device —
// the fallback loop list for BRIDGE-MIB @vlan walks when the current
// poll discovered none.
func (s *Store) ListDeviceVLANNumbers(ctx context.Context, deviceID int64) ([]int32, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT vlan_number FROM device_vlans WHERE device_id = $1 ORDER BY vlan_number`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("list device vlans: %w", err)
	}
	defer rows.Close()

	var out []int32
	for rows.Next() {
		var n int32
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scan vlan number: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
