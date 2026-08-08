package store

import (
	"context"
	"fmt"

	"github.com/farshidmousavii/bidar/internal/domain"
)

// ListDHCPSources returns every enabled DHCP lease evidence source.
// Queried fresh on every Run, so configuration changes take effect next
// cycle without restart.
func (s *Store) ListDHCPSources(ctx context.Context) ([]domain.DHCPSource, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, source_type, enabled, connection_config, credential_enc
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
		if err := rows.Scan(&src.ID, &src.Name, &src.SourceType, &src.Enabled, &src.ConnectionConfig, &src.CredentialEnc); err != nil {
			return nil, fmt.Errorf("scan dhcp source: %w", err)
		}
		sources = append(sources, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dhcp sources: %w", err)
	}
	return sources, nil
}

// ListAllDHCPSources returns every source, enabled or not — the admin
// CLI's full picture.
func (s *Store) ListAllDHCPSources(ctx context.Context) ([]domain.DHCPSource, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, source_type, enabled, connection_config, credential_enc
		FROM dhcp_sources
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list all dhcp sources: %w", err)
	}
	defer rows.Close()

	var sources []domain.DHCPSource
	for rows.Next() {
		var src domain.DHCPSource
		if err := rows.Scan(&src.ID, &src.Name, &src.SourceType, &src.Enabled, &src.ConnectionConfig, &src.CredentialEnc); err != nil {
			return nil, fmt.Errorf("scan dhcp source: %w", err)
		}
		sources = append(sources, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dhcp sources: %w", err)
	}
	return sources, nil
}

// SetDHCPSourcePath sets connection_config.path for a windows source
// (Phase 1's only file-backed type), preserving any other keys in
// connection_config (e.g. host). Non-windows sources are rejected with a
// clear error rather than silently no-op'd.
func (s *Store) SetDHCPSourcePath(ctx context.Context, name, path string) (int64, error) {
	var id int64
	var sourceType string
	err := s.pool.QueryRow(ctx, `
		SELECT id, source_type FROM dhcp_sources WHERE name = $1`, name).Scan(&id, &sourceType)
	if err != nil {
		return 0, notFound(err)
	}
	if sourceType != "windows" {
		return 0, fmt.Errorf("source %q is type %q; only windows sources use a lease-export file path in Phase 1", name, sourceType)
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE dhcp_sources
		SET connection_config = jsonb_set(coalesce(connection_config, '{}'::jsonb), '{path}', to_jsonb($2::text))
		WHERE id = $1`, id, path)
	if err != nil {
		return 0, fmt.Errorf("set dhcp source path: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return 0, fmt.Errorf("%w", ErrNotFound)
	}
	return id, nil
}
