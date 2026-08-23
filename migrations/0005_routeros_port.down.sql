-- 0005_routeros_port.down.sql
-- Reverse of 0005_routeros_port.up.sql.

ALTER TABLE network_devices DROP COLUMN IF EXISTS routeros_port;
