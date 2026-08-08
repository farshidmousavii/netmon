package store

import (
	"context"
	"fmt"

	"github.com/farshidmousavii/bidar/internal/domain"
)

// ListEnabledSubnets returns every enabled scan range for the ICMP sweep.
func (s *Store) ListEnabledSubnets(ctx context.Context) ([]domain.Subnet, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, cidr, label, vlan_number
		FROM subnets
		WHERE enabled = true
		ORDER BY cidr`)
	if err != nil {
		return nil, fmt.Errorf("list subnets: %w", err)
	}
	defer rows.Close()

	var subnets []domain.Subnet
	for rows.Next() {
		var sn domain.Subnet
		if err := rows.Scan(&sn.ID, &sn.CIDR, &sn.Label, &sn.VLANNumber); err != nil {
			return nil, fmt.Errorf("scan subnet: %w", err)
		}
		subnets = append(subnets, sn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subnets: %w", err)
	}
	return subnets, nil
}
