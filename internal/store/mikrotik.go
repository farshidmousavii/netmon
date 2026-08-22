package store

// mikrotik_leases persistence for the Phase 2 MikroTik provider.

import (
	"context"
	"fmt"
	"time"

	"github.com/farshidmousavii/bidar/internal/domain"
)

// ReplaceMikrotikEvidence writes one polled device's evidence of one
// source type (dhcp_lease | arp | wireless_reg), replacing the previous
// snapshot of that same source — "current" semantics per source. Other
// sources' rows for the device are untouched. Like neighbors, a failed
// read must not call this: it would wipe good data.
func (s *Store) ReplaceMikrotikEvidence(ctx context.Context, deviceID int64, source string, rows []domain.MikrotikEvidence, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin mikrotik sync: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		DELETE FROM mikrotik_leases WHERE device_id = $1 AND source = $2`,
		deviceID, source); err != nil {
		return fmt.Errorf("clear old %s rows: %w", source, err)
	}

	const q = `
		INSERT INTO mikrotik_leases
		    (device_id, source, mac_address, ip_address, hostname, interface, observed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	for _, r := range rows {
		if _, err := tx.Exec(ctx, q,
			deviceID, source, r.MAC, r.IP, r.Hostname, r.Interface, now,
		); err != nil {
			return fmt.Errorf("insert %s row: %w", source, err)
		}
	}

	return tx.Commit(ctx)
}
