-- 0005_routeros_port.up.sql
-- Per-device RouterOS API port (Phase 2 task 5c). Nullable with a
-- default: NULL/absent means "use 8728", so the provider falls back
-- without special-casing.

ALTER TABLE network_devices
    ADD COLUMN routeros_port int DEFAULT 8728;
