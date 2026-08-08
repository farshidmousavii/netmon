package store

import (
	"context"
	"fmt"

	"github.com/farshidmousavii/bidar/internal/domain"
)

const hostColumns = `id, hostname, fqdn, ad_domain, ad_ou,
	ad_object_guid::text, ad_object_sid, ad_last_logon_at,
	ad_status, match_status, current_ip, current_mac, last_ad_sync_at`

// FindHostByGUID returns the host already linked to an AD objectGUID, or
// ErrNotFound. Matching rule 1 (architecture.md §Phase 1): GUID match
// first, if already linked.
func (s *Store) FindHostByGUID(ctx context.Context, guid string) (*domain.Host, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+hostColumns+`
		FROM hosts
		WHERE ad_object_guid = $1::uuid`, guid)
	return scanHost(row.Scan)
}

// FindHostByHostname returns the host with the given hostname
// (case-insensitive), or ErrNotFound. Matching rule 2: hostname match when
// AD reports dNSHostName and network presence reports a resolvable name.
func (s *Store) FindHostByHostname(ctx context.Context, hostname string) (*domain.Host, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+hostColumns+`
		FROM hosts
		WHERE lower(hostname) = lower($1)`, hostname)
	return scanHost(row.Scan)
}

// InsertHost creates a new host row and returns its id. Used when neither
// the GUID nor the hostname matches an existing host (rule 3: a fresh AD
// identity with no conflicting evidence — no needs_review flag needed).
func (s *Store) InsertHost(ctx context.Context, h *domain.Host) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO hosts (hostname, fqdn, ad_domain, ad_ou,
			ad_object_guid, ad_object_sid, ad_last_logon_at,
			ad_status, match_status, last_ad_sync_at)
		VALUES ($1, $2, $3, $4, $5::uuid, $6, $7, $8, $9, $10)
		RETURNING id`,
		h.Hostname, h.FQDN, h.ADDomain, h.ADOU,
		h.ADObjectGUID, h.ADObjectSID, h.ADLastLogonAt,
		h.ADStatus, h.MatchStatus, h.LastADSyncAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert host: %w", err)
	}
	return id, nil
}

// UpdateHostFromAD refreshes the AD-sourced fields of an existing host.
// MatchStatus is deliberately NOT touched — an operator's needs_review
// flag must survive re-syncs.
func (s *Store) UpdateHostFromAD(ctx context.Context, id int64, h *domain.Host) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE hosts SET
			hostname = $1, fqdn = $2, ad_domain = $3, ad_ou = $4,
			ad_object_guid = $5::uuid, ad_object_sid = $6,
			ad_last_logon_at = $7, ad_status = $8, last_ad_sync_at = $9,
			updated_at = now()
		WHERE id = $10`,
		h.Hostname, h.FQDN, h.ADDomain, h.ADOU,
		h.ADObjectGUID, h.ADObjectSID, h.ADLastLogonAt,
		h.ADStatus, h.LastADSyncAt, id)
	if err != nil {
		return fmt.Errorf("update host %d from AD: %w", id, err)
	}
	return nil
}

// scanHost maps a hosts row into a domain.Host. It takes a closure so the
// callers can pass either QueryRow or Rows.Scan.
func scanHost(scan func(dest ...any) error) (*domain.Host, error) {
	var h domain.Host
	err := scan(
		&h.ID, &h.Hostname, &h.FQDN, &h.ADDomain, &h.ADOU,
		&h.ADObjectGUID, &h.ADObjectSID, &h.ADLastLogonAt,
		&h.ADStatus, &h.MatchStatus, &h.CurrentIP, &h.CurrentMAC,
		&h.LastADSyncAt,
	)
	if err != nil {
		return nil, notFound(err)
	}
	return &h, nil
}
