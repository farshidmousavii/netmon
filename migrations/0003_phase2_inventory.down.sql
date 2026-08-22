-- 0003_phase2_inventory.down.sql
-- Reverse of 0003_phase2_inventory.up.sql: drop children before the
-- tables their FKs point at, then the subnets constraint.

DROP TABLE IF EXISTS mikrotik_leases;
DROP TABLE IF EXISTS neighbors_current;
DROP TABLE IF EXISTS mac_table_history;
DROP TABLE IF EXISTS mac_table_current;
DROP TABLE IF EXISTS device_vlans;
DROP TABLE IF EXISTS device_interfaces;

ALTER TABLE subnets DROP CONSTRAINT IF EXISTS fk_subnets_building;
