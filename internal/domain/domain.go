// Package domain holds the core types shared across providers, store, and
// api: plain data, no I/O (per docs/coding-standards.md). Columns map 1:1
// to tables in docs/database-schema.md.
package domain

import (
	"net"
	"net/netip"
	"time"
)

// Host is one row of the hosts table — the reconciled "device" identity.
// Pointer fields are NULL-able columns; only the AD-sourced subset is
// populated by the Phase 1 AD provider (network-presence fields fill in
// from the ARP/DHCP/ICMP collectors).
type Host struct {
	ID            int64
	Hostname      *string
	FQDN          *string
	ADDomain      *string
	ADOU          *string
	ADObjectGUID  *string // canonical UUID string form
	ADObjectSID   *string // SDDL form, e.g. S-1-5-21-...
	ADLastLogonAt *time.Time
	ADStatus      string // known | not_in_ad | unknown
	MatchStatus   string // matched | needs_review
	CurrentIP     *netip.Addr
	CurrentMAC    *net.HardwareAddr
	LastADSyncAt  *time.Time
}

// Observation is one row of host_observations — raw evidence from a single
// provider run; hosts is a derived projection.
type Observation struct {
	HostID     *int64
	Source     string // ad | arp | dhcp | icmp
	Hostname   *string
	IP         *netip.Addr
	MAC        *net.HardwareAddr
	VLANNumber *int32
	Detail     []byte // jsonb, provider-specific
	ObservedAt time.Time
}
