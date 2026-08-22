package store

// mac_table_current / mac_table_history persistence for the Phase 2 SNMP
// provider.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/farshidmousavii/bidar/internal/domain"
)

// SyncDeviceMACTable writes one polled device's MAC table.
//
// current is upserted per (device_id, vlan_number, mac_address):
// first_seen_at survives updates, last_seen_at refreshes. history stays
// transition-based — a row is appended only when the MAC is new in
// current or its access port changed — so "where was this host" reads
// stay meaningful and the table doesn't grow by the full MAC count every
// poll. Freshness between transitions lives in current.last_seen_at.
// (A VLAN change is itself a new key, so it lands in history via the
// new-row path automatically.)
func (s *Store) SyncDeviceMACTable(ctx context.Context, deviceID int64, entries []domain.MACTableEntry, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin mac sync: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const priorLocation = `
		SELECT interface_id FROM mac_table_current
		WHERE device_id = $1 AND vlan_number IS NOT DISTINCT FROM $2 AND mac_address = $3`

	const upsert = `
		INSERT INTO mac_table_current
		    (device_id, interface_id, vlan_number, mac_address, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (device_id, vlan_number, mac_address) DO UPDATE SET
		    interface_id = EXCLUDED.interface_id,
		    last_seen_at = EXCLUDED.last_seen_at
		RETURNING (xmax = 0) AS inserted`

	const insertHistory = `
		INSERT INTO mac_table_history
		    (device_id, interface_id, vlan_number, mac_address, observed_at)
		VALUES ($1, $2, $3, $4, $5)`

	for _, e := range entries {
		// Pre-upsert location: needed to detect a port move before the
		// upsert overwrites it.
		var priorIface *int64
		priorErr := tx.QueryRow(ctx, priorLocation, deviceID, e.VLANNumber, e.MAC).Scan(&priorIface)
		hasPrior := priorErr == nil
		if priorErr != nil && !errors.Is(priorErr, pgx.ErrNoRows) {
			return fmt.Errorf("read prior location of mac %s: %w", e.MAC, priorErr)
		}

		var inserted bool
		if err := tx.QueryRow(ctx, upsert,
			deviceID, e.InterfaceID, e.VLANNumber, e.MAC, now,
		).Scan(&inserted); err != nil {
			return fmt.Errorf("upsert mac %s: %w", e.MAC, err)
		}

		moved := !inserted && hasPrior && !ptrEqualI64(priorIface, e.InterfaceID)
		if !(inserted || moved) {
			continue
		}
		if _, err := tx.Exec(ctx, insertHistory,
			deviceID, e.InterfaceID, e.VLANNumber, e.MAC, now); err != nil {
			return fmt.Errorf("insert mac history %s: %w", e.MAC, err)
		}
	}

	return tx.Commit(ctx)
}

func ptrEqualI64(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
