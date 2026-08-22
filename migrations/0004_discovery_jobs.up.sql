-- 0004_discovery_jobs.up.sql
-- Bidar Phase 2 polling queue: generalized job/poll table verbatim from
-- docs/database-schema.md §Phase 2. Replaces Phase 1's simple per-provider
-- scheduling for device-level polls once concurrent polling of ~50-70
-- devices makes a worker pool worthwhile. Jobs are claimed by a single
-- UPDATE ... WHERE ... RETURNING statement, which acts as the lease —
-- all state lives here, so the queue is resumable across restarts.

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
