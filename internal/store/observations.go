package store

import (
	"context"
	"fmt"

	"github.com/farshidmousavii/bidar/internal/domain"
)

// InsertObservation appends one raw evidence row (host_observations).
// host_id is set when the observation was already matched to a host;
// otherwise NULL until reconciliation links it.
func (s *Store) InsertObservation(ctx context.Context, o *domain.Observation) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO host_observations (host_id, source, hostname, ip, mac, vlan_number, detail, observed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		o.HostID, o.Source, o.Hostname, o.IP, o.MAC, o.VLANNumber, o.Detail, o.ObservedAt)
	if err != nil {
		return fmt.Errorf("insert observation: %w", err)
	}
	return nil
}
