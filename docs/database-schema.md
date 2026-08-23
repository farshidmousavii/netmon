# Database Schema — by Phase

Postgres. Migrations live in `/migrations`, one file per change, golang-migrate naming (`0001_init.up.sql` / `.down.sql`).

Conventions: `snake_case`, plural table names, `id BIGSERIAL PRIMARY KEY` unless noted, `created_at timestamptz not null default now()`, `updated_at timestamptz` where the row is ever updated after creation.

---

## Phase 1 tables

### subnets
Configured network ranges the network-presence provider scans.

```sql
CREATE TABLE subnets (
    id           BIGSERIAL PRIMARY KEY,
    cidr         inet NOT NULL,
    label        text,
    vlan_number  int,              -- if this subnet maps 1:1 to a VLAN, set it — gives an immediate (unverified) VLAN answer in Phase 1
    building_id  bigint,           -- FK added once buildings table exists (Phase 2)
    enabled      boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now()
);
```

### buildings
Pulled forward from Phase 2 — small table, no cost to have early, and `network_devices` below wants an optional reference to it.

```sql
CREATE TABLE buildings (
    id    BIGSERIAL PRIMARY KEY,
    code  text NOT NULL UNIQUE,
    name  text NOT NULL
);
```

### snmp_profiles
Pulled forward from Phase 2 — needed now because the ARP collector talks SNMP to core/L3 switches. Phase 2 reuses this table unchanged for full switch polling. **Read-only credentials only** — see `ssh_credentials` below for the separate, read-write path used by the existing `exec`/`backup` CLI commands.

```sql
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
```

### ssh_credentials
Read-write credentials, used only by the existing `exec`/`backup`/`tui-config` CLI commands (`internal/device`) — **never** referenced by anything under `internal/providers`. Kept as a separate table from `snmp_profiles` deliberately, so the daemon's dependency graph has no path to a credential that can change device state.

```sql
CREATE TABLE ssh_credentials (
    id                  BIGSERIAL PRIMARY KEY,
    name                text NOT NULL,
    username            text NOT NULL,
    password_encrypted  bytea,          -- one of password or private_key, not both
    private_key_encrypted bytea,
    port                int NOT NULL DEFAULT 22,
    enabled             boolean NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now()
);
```

`network_devices.ssh_credential_id` (nullable FK, added below) links a device to one of these — only relevant to `exec`/`backup`, ignored by the daemon.

### network_devices
Pulled forward from Phase 2, for the same reason as `snmp_profiles`. In Phase 1 only rows flagged `role = 'core'` are actively polled (for ARP). Phase 2 adds `device_interfaces` / `device_vlans` / `mac_table_current` etc. referencing these same rows — no re-creation, no rework.

```sql
CREATE TABLE network_devices (
    id                     BIGSERIAL PRIMARY KEY,
    name                   text NOT NULL,
    protocol_family        text NOT NULL,      -- cisco_snmp | mikrotik_routeros — which provider polls this device
    function               text,               -- free text: switch | router | firewall | ... (informational, from config's `type` field on import)
    role                   text NOT NULL DEFAULT 'unassigned',  -- core | access | unassigned — must be explicitly set; 'unassigned' devices are not polled by the ARP collector
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
    routeros_port          int DEFAULT 8728, -- API port; NULL/absent reads as 8728 (migration 0005)
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
ALTER TABLE network_devices ADD CONSTRAINT uq_network_devices_mgmt_ip UNIQUE (mgmt_ip);
```

The unique constraint on `mgmt_ip` was added in migration `0002` (Phase 0, discovered while implementing `bidar import-devices`) — it's the matching key idempotent import/upsert logic relies on, and without it two concurrent imports could race and create duplicate rows for the same device.

`protocol_family` replaces an earlier, more ambiguous `device_type` field — it exists purely to answer "which provider talks to this device," separate from `vendor` (free-text, e.g. populated later from SNMP `sysObjectID` detection) and `function` (informational only). `bidar import-devices` maps the existing config's `vendor` field (`cisco`/`mikrotik`) to `protocol_family` (`cisco_snmp`/`mikrotik_routeros`) and its `type` field (`router`/`switch`/`firewall`) to `function` — but never sets `role`; that stays `unassigned` until an operator explicitly assigns `core`/`access`, so an import can never silently leave the ARP collector polling nothing.

### dhcp_sources
A DHCP lease evidence source. `mikrotik` is the only implemented type in Phase 1 — `windows` and `cisco` are recognized but deliberately unimplemented (a clean error if configured, never a silent no-op); see `architecture.md` §Phase 1 and §Backlog for why. `isc`/`other` are accepted placeholders for future use, not built against.

```sql
CREATE TABLE dhcp_sources (
    id                 BIGSERIAL PRIMARY KEY,
    name               text NOT NULL,
    source_type        text NOT NULL,   -- mikrotik (implemented) | windows | cisco (unimplemented) | isc | other (placeholders)
    connection_config  jsonb NOT NULL,  -- type-specific: host, auth method, etc.
    credential_enc     bytea,           -- encrypted secret if the type needs one
    enabled            boolean NOT NULL DEFAULT true,
    last_poll_at       timestamptz,
    last_error         text,
    created_at         timestamptz NOT NULL DEFAULT now()
);
```

### hosts
The core "device" table — one row per known device, populated/updated by any provider.

```sql
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
```

### host_observations
Raw evidence log — every provider writes here; `hosts` is a derived projection of this table.

```sql
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
```

### provider_runs
One generic run log covering all four Phase 1 collectors (AD, ARP, DHCP, ICMP). Simple log, not a queue — Phase 1's collectors run on plain scheduled goroutines, not a worker pool. The full lease-based queue (`discovery_jobs`) arrives in Phase 2 once concurrent polling of ~50-70 devices makes that necessary.

```sql
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
```

---

## Phase 2 tables

`buildings`, `snmp_profiles`, and `network_devices` already exist from Phase 1 (needed there for the ARP collector). Phase 2 adds full switch/router polling on top of the same `network_devices` rows — plus every access switch that wasn't a Phase 1 "core" device now gets added as a row too.

```sql
ALTER TABLE subnets
    ADD CONSTRAINT fk_subnets_building FOREIGN KEY (building_id) REFERENCES buildings(id);
```

### device_interfaces
```sql
CREATE TABLE device_interfaces (
    id             BIGSERIAL PRIMARY KEY,
    device_id      bigint NOT NULL REFERENCES network_devices(id),
    if_index       int NOT NULL,
    if_name        text,
    if_desc        text,
    mac_address    macaddr,
    admin_status   text,
    oper_status    text,
    port_role      text NOT NULL DEFAULT 'unknown',  -- access | trunk | uplink | unknown
    pvid           int,
    last_change_at timestamptz,
    last_seen_at   timestamptz,
    UNIQUE (device_id, if_index)
);
```

### device_vlans
```sql
CREATE TABLE device_vlans (
    id          BIGSERIAL PRIMARY KEY,
    device_id   bigint NOT NULL REFERENCES network_devices(id),
    vlan_number int NOT NULL,
    name        text,
    UNIQUE (device_id, vlan_number)
);
```

### mac_table_current / mac_table_history
```sql
CREATE TABLE mac_table_current (
    id            BIGSERIAL PRIMARY KEY,
    device_id     bigint NOT NULL REFERENCES network_devices(id),
    interface_id  bigint REFERENCES device_interfaces(id),
    vlan_number   int,
    mac_address   macaddr NOT NULL,
    first_seen_at timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    UNIQUE (device_id, vlan_number, mac_address)
);
CREATE INDEX ON mac_table_current (mac_address);

CREATE TABLE mac_table_history (
    id           BIGSERIAL PRIMARY KEY,
    device_id    bigint NOT NULL REFERENCES network_devices(id),
    interface_id bigint REFERENCES device_interfaces(id),
    vlan_number  int,
    mac_address  macaddr NOT NULL,
    observed_at  timestamptz NOT NULL
);
CREATE INDEX ON mac_table_history (mac_address, observed_at);
```

### neighbors_current (LLDP/CDP)
```sql
CREATE TABLE neighbors_current (
    id                  BIGSERIAL PRIMARY KEY,
    device_id           bigint NOT NULL REFERENCES network_devices(id),
    local_interface_id  bigint REFERENCES device_interfaces(id),
    protocol            text NOT NULL,   -- lldp | cdp
    remote_system_name  text,
    remote_port_id      text,
    remote_mgmt_ip      inet,
    last_seen_at        timestamptz NOT NULL
);
```

### mikrotik_leases
MikroTik-specific evidence — DHCP leases, ARP table, wireless registration table.

```sql
CREATE TABLE mikrotik_leases (
    id           BIGSERIAL PRIMARY KEY,
    device_id    bigint NOT NULL REFERENCES network_devices(id),
    source       text NOT NULL,   -- dhcp_lease | arp | wireless_reg
    mac_address  macaddr NOT NULL,
    ip_address   inet,
    hostname     text,
    interface    text,
    observed_at  timestamptz NOT NULL
);
CREATE INDEX ON mikrotik_leases (mac_address);
```

### discovery_jobs
Generalized job/poll queue used from Phase 2 onward.

```sql
CREATE TABLE discovery_jobs (
    id               BIGSERIAL PRIMARY KEY,
    provider         text NOT NULL,     -- snmp | mikrotik | ad | network_presence
    target_type      text NOT NULL,     -- device | subnet | domain
    target_id        bigint,
    status           text NOT NULL DEFAULT 'queued',  -- queued|running|succeeded|failed
    attempt          int NOT NULL DEFAULT 0,
    lease_owner      text,
    lease_expires_at timestamptz,
    scheduled_at     timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    finished_at      timestamptz,
    error_message    text
);
CREATE INDEX ON discovery_jobs (status, scheduled_at);
```

---

## Phase 3 tables

Once correlation runs, it also updates `hosts.current_vlan` / `hosts.vlan_source = 'switch_verified'` for any host it successfully locates — overwriting the Phase 1 inferred value with the actual VLAN tag from the switch port. Hosts with no successful correlation keep their Phase 1 inferred value (or `null` if none was available).

### endpoint_location_current / endpoint_location_history
```sql
CREATE TABLE endpoint_location_current (
    host_id           bigint PRIMARY KEY REFERENCES hosts(id),
    mac_address       macaddr,
    device_id         bigint REFERENCES network_devices(id),
    interface_id      bigint REFERENCES device_interfaces(id),
    vlan_number       int,
    building_id       bigint REFERENCES buildings(id),
    confidence        text NOT NULL,        -- high | medium | low
    location_quality  text NOT NULL,        -- direct_access_port | shared_access_port | behind_uplink | unknown
    first_seen_at     timestamptz NOT NULL,
    last_seen_at      timestamptz NOT NULL
);

CREATE TABLE endpoint_location_history (
    id                BIGSERIAL PRIMARY KEY,
    host_id           bigint NOT NULL REFERENCES hosts(id),
    mac_address       macaddr,
    device_id         bigint REFERENCES network_devices(id),
    interface_id      bigint REFERENCES device_interfaces(id),
    vlan_number       int,
    confidence        text NOT NULL,
    location_quality  text NOT NULL,
    observed_at       timestamptz NOT NULL
);
CREATE INDEX ON endpoint_location_history (host_id, observed_at);
```

---

## Phase 4 tables

### api_tokens
```sql
CREATE TABLE api_tokens (
    id           BIGSERIAL PRIMARY KEY,
    name         text NOT NULL,
    token_hash   text NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);
```

Nothing else is required for Phase 4 — it's a read layer over the Phase 1–3 tables plus this one auth table.

---

## Phase 5 tables

None — the TUI is a pure client of the Phase 4 REST API, no new schema.

## Phase 6 tables

### agents
Endpoint agent lifecycle — one row per installed agent, any OS.

```sql
CREATE TABLE agents (
    id                 BIGSERIAL PRIMARY KEY,
    host_id            bigint NOT NULL REFERENCES hosts(id),
    platform           text NOT NULL,   -- windows | linux_debian | linux_redhat | macos
    agent_token_hash   text NOT NULL UNIQUE,
    agent_version      text,
    enrolled_at        timestamptz NOT NULL DEFAULT now(),
    last_heartbeat_at  timestamptz,
    last_inventory_at  timestamptz,
    revoked_at         timestamptz
);
```

### agent_enrollment_tokens
One-time tokens issued by an admin for a new agent to enroll with.

```sql
CREATE TABLE agent_enrollment_tokens (
    id           BIGSERIAL PRIMARY KEY,
    token_hash   text NOT NULL UNIQUE,
    label        text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    used_at      timestamptz,
    expires_at   timestamptz
);
```

### host_hardware
Normalized hardware/OS detail reported by the agent — kept separate from `hosts` since it's agent-sourced and optional (a host with no agent has no row here).

```sql
CREATE TABLE host_hardware (
    host_id           bigint PRIMARY KEY REFERENCES hosts(id),
    manufacturer      text,
    model             text,
    bios_vendor       text,
    bios_version      text,
    bios_serial       text,
    cpu_summary       text,
    ram_mb            int,
    storage_summary   text,
    gpu_summary       text,
    os_name           text,
    os_edition        text,
    os_version        text,
    os_build          text,
    last_logged_in_user text,
    last_boot_at      timestamptz,
    updated_at        timestamptz NOT NULL DEFAULT now()
);
```

Nothing else is required for Phase 5 — inventory ingestion is `agents` + `host_hardware`, both hung off the same `hosts` row that Phase 1 already created.

---

## Phase 7 (sketch only — do not create until actually needed)

- `events` — outbox-style: `entity_type`, `entity_id`, `event_type`, `before`, `after`, `occurred_at`.
- `identities` + `identity_aliases` + `identity_conflicts` — full Identity Service, replacing Phase 1's simple `hosts.match_status` field.
- `users`, `sessions`, `audit_logs` — multi-user auth.
- Lifecycle fields on `hosts` and `network_devices`: `lifecycle_state`, `lifecycle_reason`, `lifecycle_updated_at`.
- `snmp_traps_received` / `syslog_events` — the real-time layer.
