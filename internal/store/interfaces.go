package store

// device_interfaces persistence for the Phase 2 SNMP provider.

import (
	"context"
	"fmt"
	"time"

	"github.com/farshidmousavii/bidar/internal/domain"
)

// UpsertDeviceInterfaces writes one polled device's interface table.
//
// Rows are keyed by (device_id, if_index); existing rows keep their id —
// mac_table_current.interface_id and neighbors_current.local_interface_id
// reference them — and their port_role, which belongs to Phase 3's
// correlation engine and is never written by polling.
//
// Returns if_index -> interface id so callers can link MAC-table and
// neighbor rows.
func (s *Store) UpsertDeviceInterfaces(ctx context.Context, deviceID int64, ifaces []domain.DeviceInterface, now time.Time) (map[int]int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin interface sync: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		INSERT INTO device_interfaces
		    (device_id, if_index, if_name, if_desc, mac_address,
		     admin_status, oper_status, pvid, last_change_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (device_id, if_index) DO UPDATE SET
		    if_name        = EXCLUDED.if_name,
		    if_desc        = EXCLUDED.if_desc,
		    mac_address    = EXCLUDED.mac_address,
		    admin_status   = EXCLUDED.admin_status,
		    oper_status    = EXCLUDED.oper_status,
		    pvid           = EXCLUDED.pvid,
		    last_change_at = EXCLUDED.last_change_at,
		    last_seen_at   = EXCLUDED.last_seen_at
		RETURNING id`

	ids := make(map[int]int64, len(ifaces))
	for _, i := range ifaces {
		var id int64
		if err := tx.QueryRow(ctx, q,
			deviceID, i.IfIndex, i.IfName, i.IfDesc, i.MAC,
			i.AdminStatus, i.OperStatus, i.PVID, i.LastChangeAt, now,
		).Scan(&id); err != nil {
			return nil, fmt.Errorf("upsert interface %d: %w", i.IfIndex, err)
		}
		ids[i.IfIndex] = id
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit interface sync: %w", err)
	}
	return ids, nil
}
