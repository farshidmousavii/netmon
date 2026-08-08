-- 0001_init.up.sql
-- Bidar Phase 0/1 schema: every table from docs/database-schema.md §Phase 1.
-- Create order follows FK dependencies:
--   buildings (no deps) -> snmp_profiles, ssh_credentials (no deps)
--   -> subnets, network_devices (reference buildings/snmp_profiles/ssh_credentials)
--   -> dhcp_sources, hosts -> host_observations (references hosts) -> provider_runs

-- Pulled forward from Phase 2: small table, network_devices references it.
CREATE TABLE buildings (
    id    BIGSERIAL PRIMARY KEY,
    code  text NOT NULL UNIQUE,
    name  text NOT NULL
);

-- Read-only SNMP credentials. Kept separate from ssh_credentials so the
-- daemon's dependency graph has no path to a credential that can change
-- device state.
CREATE TABLE snmp_profiles (
    id                    BIGSERIAL PRIMARY KEY,
    name                  text NOT NULL,
    version               text NOT NULL,   -- v2c | v3
    community_encrypted   bytea,
    v3_username           text,
    v3_auth_protocol      text,
    v3_auth_key_encrypted bytea,
    v3_priv_protocol      text,
    v3_priv_key_encrypted bytea,
    timeout_ms            int NOT NULL DEFAULT 2000,
    retries               int NOT NULL DEFAULT 2,
    enabled               boolean NOT NULL DEFAULT true
);

-- Read-write credentials, used only by the existing exec/backup/tui-config
-- CLI commands (internal/device) -- never referenced by internal/providers.
CREATE TABLE ssh_credentials (
    id                    BIGSERIAL PRIMARY KEY,
    name                  text NOT NULL,
    username              text NOT NULL,
    password_encrypted    bytea,           -- one of password or private_key, not both
    private_key_encrypted bytea,
    port                  int NOT NULL DEFAULT 22,
    enabled               boolean NOT NULL DEFAULT true,
    created_at            timestamptz NOT NULL DEFAULT now()
);

-- Configured network ranges the network-presence provider scans.
-- building_id FK is added in Phase 2 (see database-schema.md Phase 2):
--   ALTER TABLE subnets ADD CONSTRAINT fk_subnets_building
--       FOREIGN KEY (building_id) REFERENCES buildings(id);
CREATE TABLE subnets (
    id           BIGSERIAL PRIMARY KEY,
    cidr         inet NOT NULL,
    label        text,
    vlan_number  int,              -- if this subnet maps 1:1 to a VLAN, set it -- gives an immediate (unverified) VLAN answer in Phase 1
    building_id  bigint,           -- FK added once buildings table exists (Phase 2)
    enabled      boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- Canonical device list (Phase 1: only role='core' rows are polled for ARP;
-- Phase 2 polls everything). protocol_family answers "which provider talks
-- to this device"; function is informational; role must be set explicitly --
-- 'unassigned' devices are never polled by the ARP collector.
CREATE TABLE network_devices (
    id                     BIGSERIAL PRIMARY KEY,
    name                   text NOT NULL,
    protocol_family        text NOT NULL,      -- cisco_snmp | mikrotik_routeros -- which provider polls this device
    function               text,               -- free text: switch | router | firewall | ... (informational, from config's `type` field on import)
    role                   text NOT NULL DEFAULT 'unassigned',  -- core | access | unassigned -- must be explicitly set; 'unassigned' devices are not polled by the ARP collector
    mgmt_ip                inet NOT NULL,
    vendor                 text,
    model                  text,
    serial_number          text,
    firmware_version       text,
    building_id            bigint REFERENCES buildings(id),
    snmp_profile_id        bigint REFERENCES snmp_profiles(id),
    ssh_credential_id      bigint REFERENCES ssh_credentials(id),  -- only used by exec/backup/tui-config, never by the daemon
    routeros_username      text,             -- MikroTik API auth (used from Phase 2 on)
    routeros_password_enc  bytea,
    poll_interval_sec      int NOT NULL DEFAULT 600,
    enabled                boolean NOT NULL DEFAULT true,
    last_poll_at           timestamptz,
    last_seen_at           timestamptz,
    last_error             text,
    consecutive_failures   int NOT NULL DEFAULT 0,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz
);
CREATE INDEX ON network_devices (role) WHERE enabled = true;

-- A DHCP lease evidence source. Do not assume one DHCP server -- list every
-- one, by type.
CREATE TABLE dhcp_sources (
    id                 BIGSERIAL PRIMARY KEY,
    name               text NOT NULL,
    source_type        text NOT NULL,   -- windows | mikrotik | isc | other
    connection_config  jsonb NOT NULL,  -- type-specific: host, auth method, path, etc.
    credential_enc     bytea,           -- encrypted secret if the type needs one
    enabled            boolean NOT NULL DEFAULT true,
    last_poll_at       timestamptz,
    last_error         text,
    created_at         timestamptz NOT NULL DEFAULT now()
);

-- Core "device" table -- one row per known device, populated/updated by any
-- provider.
CREATE TABLE hosts (
    id                 BIGSERIAL PRIMARY KEY,
    hostname           text,
    fqdn               text,
    ad_domain          text,
    ad_ou              text,
    ad_object_guid     uuid,
    ad_object_sid      text,
    ad_last_logon_at   timestamptz,
    current_ip         inet,
    current_mac        macaddr,
    current_vlan       int,
    vlan_source        text,               -- subnet_config | arp_svi | switch_verified
    ad_status          text NOT NULL DEFAULT 'unknown',   -- known | not_in_ad | unknown
    match_status       text NOT NULL DEFAULT 'matched',   -- matched | needs_review
    first_seen_at      timestamptz NOT NULL DEFAULT now(),
    last_seen_at       timestamptz,
    last_ad_sync_at    timestamptz,
    last_presence_at   timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz
);

CREATE INDEX ON hosts (lower(hostname));
CREATE INDEX ON hosts (current_ip);
CREATE INDEX ON hosts (current_mac);
CREATE INDEX ON hosts (match_status) WHERE match_status = 'needs_review';

-- Raw evidence log -- every provider writes here; hosts is a derived
-- projection of this table.
CREATE TABLE host_observations (
    id           BIGSERIAL PRIMARY KEY,
    host_id      bigint REFERENCES hosts(id),   -- nullable until matched
    source       text NOT NULL,                 -- ad | arp | dhcp | icmp
    hostname     text,
    ip           inet,
    mac          macaddr,
    vlan_number  int,                           -- set by arp (from the SVI queried) or dhcp (if the source maps to one VLAN)
    detail       jsonb,
    observed_at  timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ON host_observations (host_id);
CREATE INDEX ON host_observations (source, observed_at);

-- One generic run log covering all four Phase 1 collectors (AD, ARP, DHCP,
-- ICMP). Simple log, not a queue -- the lease-based discovery_jobs queue
-- arrives in Phase 2.
CREATE TABLE provider_runs (
    id            BIGSERIAL PRIMARY KEY,
    provider      text NOT NULL,     -- ad | arp | dhcp | icmp
    target_type   text,              -- domain | device | dhcp_source | subnet (nullable: AD sync has no target)
    target_id     bigint,
    status        text NOT NULL,     -- running | succeeded | failed
    items_found   int,
    error_message text,
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz
);
CREATE INDEX ON provider_runs (provider, started_at);
