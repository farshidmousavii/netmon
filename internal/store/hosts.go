package store

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/farshidmousavii/bidar/internal/domain"
)

const hostColumns = `id, hostname, fqdn, ad_domain, ad_ou,
	ad_object_guid::text, ad_object_sid, ad_last_logon_at,
	ad_status, match_status, current_ip, current_mac,
	current_vlan, vlan_source, last_ad_sync_at, last_presence_at`

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
// Presence fields (current_ip, current_mac, last_presence_at) are set by
// network-presence providers (ARP/DHCP/ICMP); AD leaves them NULL.
func (s *Store) InsertHost(ctx context.Context, h *domain.Host) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO hosts (hostname, fqdn, ad_domain, ad_ou,
			ad_object_guid, ad_object_sid, ad_last_logon_at,
			ad_status, match_status, current_ip, current_mac,
			current_vlan, vlan_source, last_ad_sync_at, last_presence_at)
		VALUES ($1, $2, $3, $4, $5::uuid, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id`,
		h.Hostname, h.FQDN, h.ADDomain, h.ADOU,
		h.ADObjectGUID, h.ADObjectSID, h.ADLastLogonAt,
		h.ADStatus, h.MatchStatus, h.CurrentIP, h.CurrentMAC,
		h.CurrentVLAN, h.VLANSrc, h.LastADSyncAt, h.LastPresenceAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert host: %w", err)
	}
	return id, nil
}

// FindHostByIP returns the host currently holding ip, or ErrNotFound.
func (s *Store) FindHostByIP(ctx context.Context, ip netip.Addr) (*domain.Host, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+hostColumns+`
		FROM hosts
		WHERE current_ip = $1::inet`, ip)
	return scanHost(row.Scan)
}

// FindHostByMAC returns the host currently holding mac, or ErrNotFound.
func (s *Store) FindHostByMAC(ctx context.Context, mac net.HardwareAddr) (*domain.Host, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+hostColumns+`
		FROM hosts
		WHERE current_mac = $1::macaddr`, mac)
	return scanHost(row.Scan)
}

// UpdateHostFromPresence refreshes the network-presence fields of an
// existing host from an ARP/DHCP observation. hostname only fills a NULL
// hostname (an AD-assigned name is never overwritten). vlanSource is the
// label to set when vlan is present (ARP passes "arp_svi"; DHCP passes
// nil so an existing label survives). AD fields and match_status are
// untouched.
func (s *Store) UpdateHostFromPresence(ctx context.Context, id int64, hostname *string, ip *netip.Addr, mac *net.HardwareAddr, vlan *int32, vlanSource *string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE hosts SET
			hostname = COALESCE(hosts.hostname, $2),
			current_ip = COALESCE($3::inet, current_ip),
			current_mac = COALESCE($4::macaddr, current_mac),
			current_vlan = COALESCE($5, current_vlan),
			vlan_source = CASE WHEN $5 IS NOT NULL THEN COALESCE($6, vlan_source) ELSE vlan_source END,
			last_presence_at = $7,
			updated_at = now()
		WHERE id = $1`,
		id, hostname, ip, mac, vlan, vlanSource, now)
	if err != nil {
		return fmt.Errorf("update host %d from presence: %w", id, err)
	}
	return nil
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
		&h.CurrentVLAN, &h.VLANSrc, &h.LastADSyncAt, &h.LastPresenceAt,
	)
	if err != nil {
		return nil, notFound(err)
	}
	return &h, nil
}
