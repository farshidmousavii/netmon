package store

// neighbors_current persistence for the Phase 2 SNMP provider.

import (
	"context"
	"fmt"
	"time"

	"github.com/farshidmousavii/bidar/internal/domain"
)

// ReplaceDeviceNeighbors writes one polled device's LLDP/CDP neighbor
// set, replacing whatever the previous poll saw — "current" semantics.
// The table has no natural unique key (the same remote may appear on
// several ports), so replacement is delete-then-insert in one
// transaction. A failed walk must not call this: it would wipe good data.
func (s *Store) ReplaceDeviceNeighbors(ctx context.Context, deviceID int64, neighbors []domain.Neighbor, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin neighbor sync: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM neighbors_current WHERE device_id = $1`, deviceID); err != nil {
		return fmt.Errorf("clear old neighbors: %w", err)
	}

	const q = `
		INSERT INTO neighbors_current
		    (device_id, local_interface_id, protocol, remote_system_name,
		     remote_port_id, remote_mgmt_ip, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	for _, n := range neighbors {
		if _, err := tx.Exec(ctx, q,
			deviceID, n.LocalInterfaceID, n.Protocol, n.RemoteSystemName,
			n.RemotePortID, n.RemoteMgmtIP, now,
		); err != nil {
			return fmt.Errorf("insert neighbor: %w", err)
		}
	}

	return tx.Commit(ctx)
}
