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
	ID             int64
	Hostname       *string
	FQDN           *string
	ADDomain       *string
	ADOU           *string
	ADObjectGUID   *string // canonical UUID string form
	ADObjectSID    *string // SDDL form, e.g. S-1-5-21-...
	ADLastLogonAt  *time.Time
	ADStatus       string // known | not_in_ad | unknown
	MatchStatus    string // matched | needs_review
	CurrentIP      *netip.Addr
	CurrentMAC     *net.HardwareAddr
	CurrentVLAN    *int32
	VLANSrc        *string // subnet_config | arp_svi | switch_verified
	LastADSyncAt   *time.Time
	LastPresenceAt *time.Time
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

// Device is one row of network_devices — the canonical device list shared
// by the ARP collector (role=core) and Phase 2 polling. PollIntervalSec
// drives Phase 2's discovery_jobs enqueue cadence.
type Device struct {
	ID                  int64
	Name                string
	MgmtIP              netip.Addr
	ProtocolFamily      string
	Role                string
	Enabled             bool
	SNMPProfileID       *int64
	PollIntervalSec     int
	RouterOSUsername    string // MikroTik API auth (Phase 2 device polling)
	RouterOSPasswordEnc []byte // encrypted at rest; decrypt via internal/crypto
}

// SNMPProfile is one row of snmp_profiles — read-only SNMP credentials,
// encrypted at rest. The v3 fields exist since migration 0001; they are
// consumed from Phase 2 on via snmp.ConfigFromProfile.
type SNMPProfile struct {
	ID                 int64
	Version            string // v2c | v3
	CommunityEncrypted []byte
	V3Username         string
	V3AuthProtocol     string
	V3AuthKeyEncrypted []byte
	V3PrivProtocol     string
	V3PrivKeyEncrypted []byte
	TimeoutMS          int
	Retries            int
}

// DeviceInterface is one row of device_interfaces — an interface seen on a
// polled device. PortRole is deliberately absent here: it is Phase 3's
// correlation output, never written by the polling providers.
type DeviceInterface struct {
	IfIndex      int
	IfName       *string
	IfDesc       *string
	MAC          *net.HardwareAddr
	AdminStatus  string // text form of ifAdminStatus ("up"/"down"/...)
	OperStatus   string
	PVID         *int32
	LastChangeAt *time.Time
}

// DeviceVLAN is one row of device_vlans — a VLAN announced by a device.
type DeviceVLAN struct {
	VlanNumber int32
	Name       *string
}

// MACTableEntry is one MAC-table observation for SyncDeviceMACTable.
// InterfaceID links to device_interfaces (nil when the port is unknown);
// VLANNumber is nil for VLAN-unaware tables.
type MACTableEntry struct {
	InterfaceID *int64
	VLANNumber  *int32
	MAC         net.HardwareAddr
}

// Neighbor is one LLDP/CDP neighbor link for ReplaceDeviceNeighbors.
type Neighbor struct {
	LocalInterfaceID *int64
	Protocol         string // lldp | cdp
	RemoteSystemName *string
	RemotePortID     *string
	RemoteMgmtIP     *netip.Addr
}

// MikrotikEvidence is one mikrotik_leases row — RouterOS-sourced evidence
// (dhcp_lease | arp | wireless_reg) keyed to the polled device.
type MikrotikEvidence struct {
	MAC       net.HardwareAddr
	IP        *netip.Addr
	Hostname  *string
	Interface *string
}

// DiscoveryJob is one row of discovery_jobs — the Postgres-backed poll
// queue. All state lives in the table so the worker pool is resumable
// across restarts.
type DiscoveryJob struct {
	ID             int64
	Provider       string
	TargetType     string
	TargetID       *int64
	Status         string // queued | running | succeeded | failed
	Attempt        int
	LeaseOwner     *string
	LeaseExpiresAt *time.Time
	ScheduledAt    time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	ErrorMessage   *string
}

// DHCPSource is one row of dhcp_sources — a lease evidence source, by
// type (windows | mikrotik | isc | other). ConnectionConfig is
// type-specific JSON (windows: {"path": ...}; mikrotik: {"host": ...,
// "username": ...}); CredentialEnc is the encrypted secret if the type
// needs one (none for the windows file-export method).
type DHCPSource struct {
	ID               int64
	Name             string
	SourceType       string
	Enabled          bool
	ConnectionConfig []byte // jsonb
	CredentialEnc    []byte
}

// Subnet is one row of subnets — a configured range the ICMP sweep scans.
type Subnet struct {
	ID         int64
	CIDR       netip.Prefix
	Label      *string
	VLANNumber *int32
}
