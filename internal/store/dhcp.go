package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

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

// ValidDHCPSourceTypes are the schema-recognized dhcp_sources.source_type
// values. windows/cisco are recognized but deliberately unimplemented in
// Phase 1 — the admin CLI rejects them at add time and the DHCP provider
// fails them clearly if a row ever exists (e.g. inserted via SQL).
var ValidDHCPSourceTypes = map[string]bool{
	"windows":  true,
	"mikrotik": true,
	"cisco":    true,
	"isc":      true,
	"other":    true,
}

// AddDHCPSource inserts a new source. Duplicate names are rejected with a
// clear error (the schema has no unique constraint on name).
func (s *Store) AddDHCPSource(ctx context.Context, name, sourceType string, connectionConfig []byte, credentialEnc []byte) (int64, error) {
	if !ValidDHCPSourceTypes[sourceType] {
		return 0, fmt.Errorf("invalid source_type %q (recognized types: windows, mikrotik, cisco, isc, other)", sourceType)
	}
	if strings.TrimSpace(name) == "" {
		return 0, fmt.Errorf("source name cannot be empty")
	}

	var existing int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM dhcp_sources WHERE name = $1`, name).Scan(&existing)
	if err == nil {
		return 0, fmt.Errorf("source %q already exists (id %d)", name, existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("look up dhcp source: %w", err)
	}

	var id int64
	err = s.pool.QueryRow(ctx, `
		INSERT INTO dhcp_sources (name, source_type, connection_config, credential_enc)
		VALUES ($1, $2, $3::jsonb, $4)
		RETURNING id`, name, sourceType, connectionConfig, credentialEnc).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert dhcp source: %w", err)
	}
	return id, nil
}
