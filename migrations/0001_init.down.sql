-- 0001_init.down.sql
-- Drops everything 0001_init.up.sql created, in reverse dependency order:
-- children first, then the tables they reference.

DROP TABLE IF EXISTS provider_runs;
DROP TABLE IF EXISTS host_observations;
DROP TABLE IF EXISTS hosts;
DROP TABLE IF EXISTS dhcp_sources;
DROP TABLE IF EXISTS network_devices;
DROP TABLE IF EXISTS subnets;
DROP TABLE IF EXISTS ssh_credentials;
DROP TABLE IF EXISTS snmp_profiles;
DROP TABLE IF EXISTS buildings;
