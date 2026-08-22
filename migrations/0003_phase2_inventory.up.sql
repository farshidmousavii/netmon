-- 0003_phase2_inventory.up.sql
-- Bidar Phase 2 inventory schema: tables verbatim from
-- docs/database-schema.md §Phase 2. Create order follows FK dependencies:
--   subnets FK constraint (column existed since 0001, constraint added here)
--   -> device_interfaces -> device_vlans, mac_table_current,
--      mac_table_history, neighbors_current, mikrotik_leases

-- Phase 2 tables note (database-schema.md): subnets.building_id existed as
-- a plain column since 0001; the constraint formalizes it now that
-- buildings data is actually maintained.
ALTER TABLE subnets
    ADD CONSTRAINT fk_subnets_building FOREIGN KEY (building_id) REFERENCES buildings(id);

-- One row per physical/logical interface seen on a polled device.
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

-- VLANs announced by the device (Q-BRIDGE/VTP).
CREATE TABLE device_vlans (
    id          BIGSERIAL PRIMARY KEY,
    device_id   bigint NOT NULL REFERENCES network_devices(id),
    vlan_number int NOT NULL,
    name        text,
    UNIQUE (device_id, vlan_number)
);

-- Latest MAC-table state per device; refreshed every poll cycle.
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

-- Append-only MAC-table observations; Phase 3's "where was this host" answer.
CREATE TABLE mac_table_history (
    id           BIGSERIAL PRIMARY KEY,
    device_id    bigint NOT NULL REFERENCES network_devices(id),
    interface_id bigint REFERENCES device_interfaces(id),
    vlan_number  int,
    mac_address  macaddr NOT NULL,
    observed_at  timestamptz NOT NULL
);
CREATE INDEX ON mac_table_history (mac_address, observed_at);

-- LLDP/CDP neighbor links; Phase 3 walks these to find access switches
-- behind uplinks.
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

-- MikroTik-specific evidence — DHCP leases, ARP table, wireless
-- registration table — keyed to the polled device row.
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
