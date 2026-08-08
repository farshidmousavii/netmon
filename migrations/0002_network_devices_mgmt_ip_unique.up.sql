-- 0002_network_devices_mgmt_ip_unique.up.sql
-- mgmt_ip is the matching key idempotent import/upsert logic relies on
-- (bidar import-devices); without a unique constraint two concurrent
-- imports could race and create duplicate rows for the same device.
-- Documented in docs/database-schema.md.
ALTER TABLE network_devices
    ADD CONSTRAINT uq_network_devices_mgmt_ip UNIQUE (mgmt_ip);
