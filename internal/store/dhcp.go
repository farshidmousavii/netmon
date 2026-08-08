package store

import (
	"context"
	"fmt"

	"github.com/farshidmousavii/bidar/internal/domain"
)

// ListDHCPSources returns every enabled DHCP lease evidence source.
func (s *Store) ListDHCPSources(ctx context.Context) ([]domain.DHCPSource, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, source_type, connection_config, credential_enc
		FROM dhcp_sources
		WHERE enabled = true
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list dhcp sources: %w", err)
	}
	defer rows.Close()

	var sources []domain.DHCPSource
	for rows.Next() {
		var src domain.DHCPSource
		if err := rows.Scan(&src.ID, &src.Name, &src.SourceType, &src.ConnectionConfig, &src.CredentialEnc); err != nil {
			return nil, fmt.Errorf("scan dhcp source: %w", err)
		}
		sources = append(sources, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dhcp sources: %w", err)
	}
	return sources, nil
}
