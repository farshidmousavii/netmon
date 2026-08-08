-- 0002_network_devices_mgmt_ip_unique.down.sql
ALTER TABLE network_devices
    DROP CONSTRAINT IF EXISTS uq_network_devices_mgmt_ip;
