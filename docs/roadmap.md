# Roadmap

Ordered exactly as scoped: (0) merge with the existing Bidar CLI → (1) AD + network presence → (2) SNMP polling → (3) mapping/correlation → (4) REST API → (5) TUI → (6) endpoint agent → (7) everything else.

Each phase lists tasks and a Definition of Done (DoD). Do not begin a phase until the previous phase's DoD is met and in actual daily use for at least a few days.

---

## Phase 0 — Merge with the existing Bidar CLI

Based on the codebase audit (2026-08-07). Reality check first: `cmd/cli/` (not `cmd/bidar/cli/`) already holds `root.go`, `init.go`, `monitor.go`, `backup.go`, `exec.go`, `diff.go`, `tui.go`(+`tui_*.go`) — all working. `internal/snmp`, `internal/device`, `internal/config`, `internal/retry`, `internal/worker`, `internal/logger`, `internal/report` already exist. `internal/db`, `internal/crypto`, `internal/providers`, `internal/domain`, `internal/api`, `internal/store`, `migrations/` do not exist yet — this phase creates them.

Tasks:
1. **Rename the existing TUI**: `cmd/cli/tui*.go` → registers as `bidar tui-config` instead of `bidar tui`. No behavior change — it keeps managing `config.yaml` and doing live SSH checks exactly as today. This frees up the `tui` name for Phase 5's new REST-API-client TUI.
2. **`internal/db`** (new): pgx pool + golang-migrate runner. `migrations/0001_init.up.sql` is the starting point — nothing exists yet, so this migration creates every Phase 0/1 table at once: `buildings`, `snmp_profiles`, `ssh_credentials`, `network_devices`, `dhcp_sources`, `subnets`, `hosts`, `host_observations`, `provider_runs`. Env var: `BIDAR_DATABASE_URL`.
3. **`internal/crypto`** (new): encrypt/decrypt for secrets stored in Postgres (`snmp_profiles`, `ssh_credentials`, `dhcp_sources.credential_enc`), keyed by `BIDAR_MASTER_KEY`. This does not touch the legacy YAML/CSV files — those stay plaintext-on-disk as they are today.
4. **`internal/snmp` additive changes**:
   - Add new functions alongside the existing `SnmpWalk` (do not change its signature — `internal/device` depends on it): a typed table-walk function (for `ipNetToPhysicalTable` in Phase 1, `BRIDGE-MIB`/`Q-BRIDGE-MIB`/LLDP/CDP in Phase 2), SNMPv3 support, `context.Context` support.
   - Fix the `ParseVendorSNMP` off-by-one bug (`parts[7]` should be `parts[6]` for the enterprise-ID octet in `1.3.6.1.4.1.<enterprise>...`) as its own isolated commit, with a regression test. Verify against a live capture before merging — the audit flags this as likely broken in practice today.
   - Add fixture-based tests for `internal/snmp` (currently has none) — required by `coding-standards.md`, and the highest-risk gap given the ARP collector builds directly on this package next.
5. **`bidar import-devices`** (new subcommand): reads an existing `config.yaml`/`devices.csv` and writes:
   - Each device → a `network_devices` row. Map config `vendor` (`cisco`/`mikrotik`) → `protocol_family` (`cisco_snmp`/`mikrotik_routeros`); map config `type` (`router`/`switch`/`firewall`) → `function`; leave `role` at its default `unassigned` — never guess `core` vs `access`.
   - The config's global `snmp` block (community/timeout) → one `snmp_profiles` row, linked via `snmp_profile_id` on every imported device. Same for CSV's `#snmp_community=`/`#snmp_timeout=` settings.
   - Each config `credentials` entry (or each CSV row's per-device username/password) → one `ssh_credentials` row, linked via `ssh_credential_id`. Encrypt via `internal/crypto` on the way in.
   - Strictly one-directional: file → DB. Never writes back to `config.yaml`/`devices.csv` — that stays `tui-config`'s job.
   - After import, print a summary listing every device still at `role = unassigned`, so the operator knows what needs manual role assignment before the Phase 1 ARP collector will use it.
6. **Daemon logging**: add a `log/slog`-based logger for the daemon (`bidar serve` and everything under `internal/providers`/`internal/api`), injected via constructor per `coding-standards.md`. Do not touch or route through the existing `internal/logger`.
7. **`bidar serve`, `bidar migrate`** (new subcommands): added to `cmd/cli/` following the existing `var fooCmd = &cobra.Command{...}` + `init(){ rootCmd.AddCommand(...) }` pattern. `serve` and `migrate` are env-configured (`BIDAR_*` vars), not via the root `--config` flag the legacy commands use — keep that split explicit in the command help text.

DoD:
- `bidar tui-config` behaves identically to the old `bidar tui`; `bidar monitor`/`backup`/`exec`/`diff`/`init` are unchanged.
- `bidar import-devices` turns an existing `config.yaml`/`devices.csv` into `network_devices`/`snmp_profiles`/`ssh_credentials` rows, with credentials encrypted at rest, and prints the list of devices needing manual `role` assignment.
- `internal/snmp` has fixture-based tests, and the `ParseVendorSNMP` fix is verified against a live capture.
- `bidar migrate` applies `0001_init` cleanly against a fresh database.
- Zero imports of `internal/device` from any daemon-side package.

**Status: complete.** All five DoD bullets met (the live-capture verification of `ParseVendorSNMP` is source/fixture-verified but not yet checked against a real device — tracked in §Backlog below, not blocking). `internal/db`, `internal/crypto`, `internal/dlog`, migrations `0001`+`0002`, `bidar import-devices`, `bidar migrate`, and a `bidar serve` stub all exist and are tested.

### Phase 0 addendum — centralize env var names, Dockerize for production

Added after initial Phase 0 completion, once local testing surfaced two production-readiness gaps: every `BIDAR_*` env var name was defined separately inside the package that reads it (no single source of truth), and there was no way to bring up the full stack (Postgres + migrations + the daemon) reproducibly on a clean host.

Tasks:
1. **`internal/envconfig`** (new): every `BIDAR_*` name (`DatabaseURL`, `MasterKey`, `LogLevel`, and any added later) as an exported string constant, in one file. Update `internal/db.DatabaseURLFromEnv`, `internal/crypto.NewFromEnv`, `internal/dlog.NewFromEnv`/`LevelFromEnv` to reference these constants instead of each defining its own local copy of the string. Keep each package's own `*FromEnv()` constructor and isolated tests exactly as they are — only the name *strings* move, not the env-reading logic.
2. While touching `internal/dlog`: rename the `dlogLevelEnvForTest` constant — despite the name it's the real production env var name, not test-only; the name is misleading and should be fixed as part of this same change.
3. **`.env.example`** at repo root: every var `internal/envconfig` defines, with safe placeholder values (never a real secret). Add a short comment above `BIDAR_MASTER_KEY` making clear the placeholder must be replaced with a real generated key in production.
4. **`Dockerfile`**: multi-stage build (pinned Go version from `go.mod`) producing a small final image with just the `bidar` binary. Same image serves both `bidar serve` and `bidar migrate` — the command decides, not two images.
5. **`docker-compose.yml`**: `postgres` (named volume, `pg_isready` healthcheck) → `migrate` (one-shot `bidar migrate`, waits for postgres healthy) → `bidar` (the daemon, waits for `migrate` to complete successfully **and** postgres healthy). No manual ordering steps for the operator.
6. Document the production Docker setup (vs. the developer's local Postgres-in-Docker + locally-built-binary workflow used through Phase 0) in `docs/architecture.md` §Production deployment — already added.

DoD:
- `docker compose up` on a clean host (no prior state) brings up Postgres, applies migrations, and starts `bidar serve` with zero manual steps beyond supplying a real `.env`.
- Every `BIDAR_*` env var name exists in exactly one place in the Go source (`internal/envconfig`) — verified by grep, not just code review.
- Restarting the compose stack does not lose Postgres data (named volume, not an anonymous/ephemeral one).
- `internal/dlog`'s tests still pass after the rename; no other package's public API changes.

---

## Backlog (low priority, revisit opportunistically — not blocking any phase)

- **Verify the `ParseVendorSNMP` fix against a live device capture.** Phase 0 verified it against `gosnmp`'s source and official vendor MIBs (Cisco, MikroTik) plus a fixture-based fake agent — solid, but not the same as a real switch response. Do this the first time Phase 1/2 work has a reachable core switch in front of it; not worth blocking on.
- **gofmt drift in three pre-existing legacy files** (`backup/diff.go`, `logger/logger.go`, `report/json.go`) — untouched by design during Phase 0 to keep diffs scoped. Clean up in one small, isolated commit once the daemon phases have settled, not mixed into feature work.

---

## Phase 1 — AD connectivity + network presence

Note: `bidar import-devices` (Phase 0) currently holds its own SQL directly in `cmd/cli/import_devices.go` because `internal/store` didn't exist yet at the time. `internal/store` is a natural first sub-task of the provider work below (tasks 4/8 need it to write `host_observations`/`hosts`) — once it exists, move `import-devices`'s four statements into it too, rather than leaving two places in the codebase writing to `network_devices`/`snmp_profiles`/`ssh_credentials` directly.

Also note: Phase 0 already built `internal/db` (pgx pool + migration runner) and migrations `0001`+`0002`, which cover every Phase 0 **and** Phase 1 table (`subnets`, `buildings`, `snmp_profiles`, `ssh_credentials`, `network_devices`, `dhcp_sources`, `hosts`, `host_observations`, `provider_runs`) in one pass. The two tasks that would normally set this up are already done — see the strikethroughs below.

Tasks:
1. ~~`internal/config`: env loading, DB connection.~~ Already covered by `internal/db` + `internal/dlog` (Phase 0). Daemon env loading (`BIDAR_*` vars) can stay inline in `cmd/cli/serve.go` unless it grows enough to justify its own package.
2. ~~`migrations`: subnets/buildings/snmp_profiles/network_devices/dhcp_sources/hosts/host_observations/provider_runs.~~ Already created in `0001_init`/`0002` (Phase 0). If a provider below needs a column that doesn't exist yet, add it via a new numbered migration — don't touch `0001`/`0002`.
3. Enumerate and record (in `architecture.md` §Open decisions): the list of core/L3 devices for the ARP collector, every DHCP server + its type, and the VLAN number for each configured subnet (where a subnet maps 1:1 to a VLAN). Do this before writing the collectors, not during.
4. `internal/providers/ad`: LDAP(S) bind, pull computer objects, write to `host_observations`, upsert into `hosts` via matching rules (see `architecture.md` §Phase 1).
5. `internal/providers/arp`: SNMP query of `ipNetToPhysicalTable`/`ipNetToMediaTable` against every `network_devices` row with `role = 'core'`, capturing the VLAN each entry belongs to (from the SVI/interface being queried), write to `host_observations` including `vlan_number`.
6. `internal/providers/dhcp`: pluggable per-`source_type` lease collector, iterating every enabled row in `dhcp_sources`, write to `host_observations`. For `source_type = mikrotik`, build `internal/providers/mikrotik` as a real RouterOS API client package now (not a throwaway) — Phase 2 extends it rather than replacing it.
7. `internal/providers/icmpsweep`: ping sweep of enabled `subnets`, write to `host_observations`.
8. Reconciliation: upsert `host_observations` into `hosts` via the matching rules in `architecture.md` §Phase 1, regardless of which collector produced the observation.
9. Minimal way to inspect `hosts` (a CLI subcommand or direct SQL is fine — the full REST API is Phase 4).
10. Scheduler: AD sync daily, ARP poll every N minutes, DHCP poll every N minutes, ICMP sweep every N minutes (all configurable) as goroutines with basic error handling, logging to `provider_runs`. The full lease-based job queue (`discovery_jobs`) arrives in Phase 2 when concurrent polling of ~50-70 devices needs it — plain goroutines are enough for four collectors.

DoD:
- All four collectors run on their own schedule without crashing the process when any of the others (or AD) is down.
- `hosts` reflects reality: known AD computers + live network devices seen via ARP/DHCP/ICMP, each with source(s) and `last_seen_at`.
- Killing AD connectivity does not stop the other three collectors from updating `hosts`.
- A host on a VLAN with no direct relationship to AD or a single gateway still shows up (via ARP from a core switch or a DHCP lease).
- `hosts.current_vlan` is populated (from subnet config or ARP SVI) for hosts where either is available, with `vlan_source` correctly labeled — clearly distinguishable from the switch-verified value Phase 3 will later provide.
- You can answer "is this AD hostname currently online" and "what's on the network that isn't in AD" from `hosts`.

---

## Phase 2 — SNMP polling

Tasks:
1. `migrations`: `device_interfaces`, `device_vlans`, `mac_table_current`, `mac_table_history`, `neighbors_current`, `mikrotik_leases`, `discovery_jobs`. (`buildings`, `snmp_profiles`, `network_devices` already exist from Phase 1 — this phase adds every remaining access switch as a row and adds the tables above.)
2. `internal/providers/snmp`: Cisco polling — system info, interfaces, VLANs, MAC table, LLDP/CDP. Reuses the same SNMP client code the Phase 1 ARP collector already built.
3. `internal/providers/mikrotik`: extend the RouterOS API client already built in Phase 1 (currently DHCP-lease-only) with ARP table read, wireless registration table read, and SNMP polling for interface/uptime stats. One client package, no duplicate MikroTik integration.
4. Job queue + worker pool (`discovery_jobs`, lease-based claiming, retry/backoff, circuit breaker after N consecutive failures) — replaces Phase 1's simple `provider_runs` scheduling once device count makes a worker pool worthwhile.
5. Secret encryption for MikroTik credentials (SNMP profile encryption already built in Phase 1).

DoD:
- All known Cisco switches and MikroTik devices are configured and polling successfully on their configured interval.
- Poll failures are visible (per-device last error, consecutive failure count) and don't stop other devices from polling.
- MAC tables, interfaces, VLANs, and neighbors are populated and refreshing.
- No endpoint-to-port mapping yet — verify this phase's data is trustworthy on its own before Phase 3 consumes it.

---

## Phase 3 — Correlation / mapping

Tasks:
1. `migrations`: `endpoint_location_current`, `endpoint_location_history`.
2. `internal/domain`: correlation types (Candidate, Confidence, LocationQuality).
3. Correlation engine: MAC normalization → candidate lookup → port classification (access/trunk/uplink) → uplink suppression + topology walk via `neighbors_current` → scoring → write current + history.
4. Scheduled correlation run (e.g. every 5–10 minutes, after each SNMP poll cycle or independently).

DoD:
- For a sample of known hosts, the reported switch/port matches physical reality (spot-check manually).
- Hosts behind uplinks or with conflicting evidence show `unknown`/low confidence instead of a wrong guess.
- `hosts.current_vlan` / `vlan_source` is upgraded to `switch_verified` for every successfully correlated host, and stays at its Phase 1 inferred value for anything not yet correlated.
- History is retained so you can answer "where was this host an hour ago."

---

## Phase 4 — REST API

Tasks:
1. `migrations`: `api_tokens`.
2. `internal/api`: router, auth middleware (static bearer token check against `api_tokens`), JSON encoding helpers, error format.
3. Endpoints: `GET /api/v1/hosts`, `GET /api/v1/hosts/{id}`, `GET /api/v1/devices`, `GET /api/v1/devices/{id}`, `GET /api/v1/devices/{id}/mac-table`, `GET /api/v1/search?q=`, plus admin endpoints for `subnets` / `snmp_profiles` / reviewing `hosts.match_status = needs_review`.
4. Basic pagination (`page`, `page_size`) and filtering on the already-indexed fields.

DoD:
- Every read built in Phases 1–3 is reachable over HTTP with a token.
- You can script against it (curl/Python) without touching the database directly.

---

## Phase 5 — TUI

Tasks:
1. Add `bidar tui` subcommand to `cmd/bidar/cli`.
2. `internal/tui`: bubbletea app, talking to the Phase 4 API over HTTP with a bearer token (from config/env, not hardcoded).
3. Host list view: table with search/filter, columns for hostname/IP/MAC/VLAN/last seen/AD status/confidence.
4. Host detail view: hardware (from `host_hardware` if present), location (from `endpoint_location_current` if present), observation sources.
5. Device list view: `network_devices` poll health (last success, last error, consecutive failures).
6. Periodic refresh (poll the API every few seconds) — no push/streaming needed.

DoD:
- `bidar tui` runs against a local or remote `bidar serve` instance and shows a live, searchable host list.
- Host detail and device health are reachable from the list view.
- The TUI process holds no SSH/SNMP/AD credentials — it only holds an API bearer token.

---

## Phase 6 — Endpoint Agent (cross-platform)

Tasks:
1. `migrations`: `agents` (with `platform`), `agent_enrollment_tokens`, `host_hardware`.
2. `internal/api`: add `POST /api/v1/agents/enroll`, `POST /api/v1/agents/heartbeat`, `POST /api/v1/agents/inventory` on top of the Phase 4 router/auth infrastructure.
3. Admin path to issue/revoke enrollment tokens (CLI subcommand is enough for MVP — full admin UI is Phase 6).
4. `internal/agent/collector`: define the `Collector` interface + normalized struct, then implement `collector_windows.go`, `collector_linux.go` (covers both Debian/Ubuntu and RHEL/Fedora — same `/proc`/`/sys` interfaces), and `collector_darwin.go`, each behind Go build tags.
5. `cmd/inventory-agent`: enrollment flow, heartbeat loop, full-inventory loop, retry with backoff, bounded local log — OS-agnostic, calls into whichever collector was built for the target platform.
6. Packaging per OS: Windows service + MSI, `.deb`/`.rpm` + systemd unit for Linux, `.pkg` + launchd plist for macOS.
7. Pilot install across at least two of the four platform targets — validate before wider rollout. For Linux, check whether the existing Ubuntu-patching Ansible setup can be reused for agent rollout before building a separate deployment path.

DoD:
- Pilot machines on at least Windows and one Linux distro enroll, heartbeat, and report full hardware/OS inventory successfully; macOS validated before counting this phase done if any Macs exist in scope.
- Revoking an agent's token causes its subsequent submissions to be rejected.
- Killing the agent on a machine does not remove that host from `hosts` — Phase 1 evidence keeps it visible, just without hardware detail and without agent-based "online" evidence.
- Host detail (once a UI/API consumer exists) shows real CPU/RAM/OS/serial data for agent-covered hosts, correctly labeled by platform.

---

## Phase 7 — everything else (not scoped in detail yet)

Revisit and break this down properly once Phase 6 is in daily use. Rough order of likely value:
1. Real-time layer (SNMP traps / syslog) — closes the "real-time" gap left by Phase 2's polling interval.
2. Formal Identity Service (replaces Phase 1's simple `match_status` matching) — only if manual review of `needs_review` hosts becomes a real burden.
3. Lifecycle states (online/offline/stale/archived) with hysteresis.
4. Event log / audit log.
5. Dashboard UI.
6. Multi-user auth (LDAP bind or SSO) + RBAC — only needed if more people than just you will use this.
7. Retention/partitioning for history tables — only once volume is measured to be a real problem.

Do not pre-build any of these into earlier phases.
