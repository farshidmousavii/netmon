// Package reconcile holds the Phase 1 host-matching rules shared by every
// network-presence provider (ARP, DHCP, and later ICMP). It implements
// docs/architecture.md §Phase 1 reconciliation:
//
//  1. match by objectGUID if already linked (AD only — handled by the AD
//     provider, which has the GUID);
//  2. else match by hostname (case-insensitive) when one is available;
//  3. else match by current IP, then current MAC;
//  4. else insert a fresh host.
//
// Match_status and AD fields are preserved on update — a provider never
// overwrites another provider's evidence. Task 8 (roadmap) wires the
// scheduler onto this same core.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/store"
)

// Host matches a presence observation (ip, mac, optional hostname and
// VLAN) to a hosts row and returns its id, creating the row if needed.
//
// hostname only fills a NULL hostname — an AD-assigned name wins.
// vlanSource labels the inferred VLAN (ARP passes "arp_svi"); nil when
// the observation carries no VLAN.
func Host(ctx context.Context, st *store.Store, hostname *string, ip *netip.Addr, mac *net.HardwareAddr, vlan *int32, vlanSource *string, now time.Time) (int64, error) {
	var existing *domain.Host
	var err error

	// Rule 3a: IP match.
	if ip != nil {
		existing, err = st.FindHostByIP(ctx, *ip)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return 0, fmt.Errorf("find host by ip: %w", err)
		}
	}
	// Rule 2: hostname match (network-presence resolvable names).
	if existing == nil && hostname != nil && *hostname != "" {
		existing, err = st.FindHostByHostname(ctx, *hostname)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return 0, fmt.Errorf("find host by hostname: %w", err)
		}
	}
	// Rule 3b: MAC match.
	if existing == nil && mac != nil {
		existing, err = st.FindHostByMAC(ctx, *mac)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return 0, fmt.Errorf("find host by mac: %w", err)
		}
	}

	if existing != nil {
		if err := st.UpdateHostFromPresence(ctx, existing.ID, hostname, ip, mac, vlan, vlanSource, now); err != nil {
			return 0, err
		}
		return existing.ID, nil
	}

	// Rule 4: fresh host.
	newHost := &domain.Host{
		Hostname:       hostname,
		CurrentIP:      ip,
		CurrentMAC:     mac,
		CurrentVLAN:    vlan,
		VLANSrc:        vlanSource,
		ADStatus:       "unknown",
		MatchStatus:    "matched",
		LastPresenceAt: &now,
	}
	id, err := st.InsertHost(ctx, newHost)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// Observation appends one host_observations row linked to the host
// reconcile.Host returned. source is the provider name (arp | dhcp |
// icmp); detail is provider-specific JSON.
func Observation(ctx context.Context, st *store.Store, hostID int64, source string, hostname *string, ip *netip.Addr, mac *net.HardwareAddr, vlan *int32, detail []byte, now time.Time) error {
	obs := &domain.Observation{
		HostID:     &hostID,
		Source:     source,
		Hostname:   hostname,
		IP:         ip,
		MAC:        mac,
		VLANNumber: vlan,
		Detail:     detail,
		ObservedAt: now,
	}
	return st.InsertObservation(ctx, obs)
}
