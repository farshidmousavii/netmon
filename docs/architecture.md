# Architecture — Phased Design

This document explains the *why* behind each phase. For task lists see `roadmap.md`; for tables see `database-schema.md`.

## Guiding principles (carried over from the earlier design review)

1. Evidence, not truth. Every provider (AD, network-scan, SNMP, MikroTik) reports *observations*. The system derives current state from observations — it never lets one provider silently overwrite another's evidence.
2. Confidence over false precision. When evidence is weak or conflicting, say so in the UI/API rather than guessing.
3. One binary, one database. No Redis, no message broker, no Kubernetes, no microservices — until real, measured scale demands it (see Phase 5 notes).
4. Every phase must stand on its own and be genuinely useful before the next phase starts.

---

## Merging with the existing Bidar CLI (monitor / backup / exec / diff)

Bidar already exists as a CLI tool (formerly netmon): `monitor` (ping + SNMP health check), `backup`/`diff` (config backup and comparison), `exec` (bulk SSH command execution), `init` (YAML/CSV config setup), and a config-management **`tui`** (device CRUD against `config.yaml`, live SSH checks). This project merges into that repository rather than living alongside it as a second tool. A full codebase audit (2026-08-07) informed the decisions below — see `roadmap.md` §Phase 0 for the resulting task list.

**What merges:**
- One `bidar` binary, one CLI (cobra — already in use). Existing subcommands are unchanged in behavior, with one rename: the existing `tui` becomes **`bidar tui-config`** (it manages `config.yaml` and holds SSH credentials in-process — that's a different tool than the Phase 5 TUI). New subcommands are added: `bidar serve` (runs the daemon — Phases 1–7), `bidar migrate` (DB migrations), `bidar import-devices` (one-time import of an existing `config.yaml`/`devices.csv` into `network_devices`/`snmp_profiles`/`ssh_credentials`).
- `internal/snmp` (already exists — `SnmpWalk`, single SNMPv2c GET, string-typed, no walk/v3/context support today) stays untouched for CLI compatibility. Phase 1's ARP collector and Phase 2's SNMP provider add new functions to the same package (table walk, v3 auth, context, typed varbinds) — additive only, never a rewrite of `SnmpWalk`. The audit also found an off-by-one bug in `ParseVendorSNMP` (reads the enterprise-ID octet at the wrong index) — fix it as an isolated, tested commit in Phase 0, separate from the new functions.
- `network_devices` (new, Postgres) becomes the canonical device list. The existing YAML/CSV config format remains a supported input via `bidar import-devices` (strictly one-directional, file → DB — it never rewrites `config.yaml`) and stays available as-is for `monitor`/`backup`/`exec`/`tui-config`.
- Credential encryption for DB-stored secrets is handled by a new `internal/crypto` package, keyed by the `BIDAR_MASTER_KEY` environment variable. This does **not** extend to the legacy YAML/CSV credential files — those remain plaintext-on-disk, operator-owned and gitignored, exactly as today. `bidar serve` validates `BIDAR_MASTER_KEY` at startup (fails fast, before anything else) even before any Phase 1+ provider actually needs it — the daemon's whole contract depends on encryption being possible, so a missing key should be an immediate, unambiguous startup failure, not a mid-operation one later.
- The daemon gets its own `log/slog`-based logger, injected via constructor. The existing `internal/logger` (custom, color-based, package-level global) is left untouched — do not route daemon logging through it, and do not refactor it to slog as part of this merge.

**What stays separate — this is the important part:**
- `exec`, `backup`, and `tui-config` are **read-write**: they SSH into devices and can change configuration. The daemon (Phases 1–7) is **read-only by design** — SNMP reads, ARP reads, DHCP lease reads, LDAP reads, agent reports. It never sends a device a command that changes anything.
- Credentials follow the same split: `snmp_profiles` (read-only community/v3 keys) is separate from `ssh_credentials` (read-write, used only by `exec`/`backup`/`tui-config`). The daemon never touches `ssh_credentials`, and daemon code (`internal/providers/*`) must never import `internal/device` (the existing SSH/exec package) — verified one-directional today (`internal/device` imports `internal/snmp`; the reverse is not true, and must stay that way).
- This isn't just tidiness: it bounds the blast radius of a bug. A mistake in a passive, unattended, always-on poller should never be able to reach a code path that can push configuration to production switches.

---

## Production deployment (Docker Compose)

Local development so far has used a developer-run Postgres container (Docker) alongside a locally-built `bidar` binary — fine for building/testing individual packages, but production needs the whole stack (Postgres + `bidar serve`, with migrations applied first) to come up together, reproducibly, on a clean host.

- **`Dockerfile`**: multi-stage build. A Go build stage (pinned to the `go.mod` Go version) produces the `bidar` binary; the final stage is a small runtime image (alpine or distroless) containing just that binary. The same image runs either `bidar serve` or `bidar migrate` — which one is decided by the command passed in compose, not by two different images.
- **`docker-compose.yml`**: three services —
  1. `postgres` — official image, a named volume for the data directory (production data must survive container recreation), a healthcheck (`pg_isready`).
  2. `migrate` — the `bidar` image running `bidar migrate`, `depends_on: postgres: condition: service_healthy`, runs once and exits.
  3. `bidar` (the daemon, `bidar serve`) — `depends_on: migrate: condition: service_completed_successfully` and `postgres: condition: service_healthy`, so it never starts against a database that isn't ready or migrated.
- **`.env.example`**: every `BIDAR_*` var compose needs, with safe placeholder values — `BIDAR_MASTER_KEY` must be a real generated secret in production (e.g. `openssl rand -base64 32`), never the placeholder.
- **Env var names, centralized**: every `BIDAR_*` name is defined exactly once, in `internal/envconfig`, as an exported string constant. `internal/db`, `internal/crypto`, and `internal/dlog` reference those constants in their own `*FromEnv()` constructors rather than each hardcoding its own copy of the string — this is the single source of truth both the Go code and `.env.example` are checked against, so the two can't silently drift apart.

---

## Phase 1 — AD connectivity + independent network presence

### Problem this phase solves
"Who/what exists, and is it currently up?" — answered from two *independent* sources, so the system keeps working even if AD is unreachable, and so non-domain devices still show up.

### Components

**AD Provider**
- Connects to AD over LDAP(S) with a read-only service account.
- Periodically (daily, configurable) pulls computer objects: hostname, DN, OU, domain, `objectGUID`, `objectSid`, `dNSHostName`, `lastLogonTimestamp`.
- Failure mode: if AD is unreachable, the provider marks itself unhealthy and does **not** block anything else — it's one evidence source among several, never a hard dependency.

**Network Presence Provider (works without AD)**

Independently determines which IPs/hosts are alive on the LAN right now, without relying on AD. This is **not one mechanism** — a single gateway's ARP table misses other VLANs, and a single DHCP server misses leases handed out elsewhere. It's three independent, complementary collectors, all feeding `host_observations`:

1. **ARP collector (SNMP, from *all* core/L3 switches)** — queries `ipNetToPhysicalTable`/`ipNetToMediaTable` (IP-MIB) on every configured core device. Unlike the MAC table (`BRIDGE-MIB`, which is per-VLAN and needs community-string indexing on Cisco), ARP is a Layer-3 function: querying a core switch's ARP table returns entries across *all* its SVIs/VLANs in one query. If there are multiple cores (HSRP/VRRP pair, or one per site/building), each is configured as its own source — evidence from all of them merges in `host_observations`, the same way every other source does. ARP entries age out (Cisco default ~4h), so this alone isn't enough for fast "is it up right now" — that's what the ICMP collector is for.
2. **DHCP lease collector (multiple sources, by type)** — a configurable list of DHCP sources, each with a `type` (`windows`, `mikrotik`, `isc`, ...), each with its own connection method (Windows DHCP via PowerShell/WMI, MikroTik via RouterOS API, ISC via lease file, etc.). Do not assume a single DHCP server — most enterprise networks have several, often of different kinds. **For `type = mikrotik`, this collector calls into a shared `internal/providers/mikrotik` RouterOS API client package** — the same package Phase 2 later extends with ARP/wireless-registration/interface polling. There is exactly one MikroTik client in the codebase, used narrowly here (DHCP leases only) and more fully from Phase 2 on — never two separate implementations talking to the same device.
3. **ICMP sweep (fallback/freshness layer)** — periodic ping sweep of configured `subnets`. Cheapest and fastest way to refresh "still up" between ARP/DHCP snapshots; also catches hosts that don't yet have a fresh ARP/DHCP entry.

This provider is what keeps the system useful when AD is down, and what lets non-domain devices appear at all. Because ARP querying needs SNMP, `network_devices` and `snmp_profiles` (originally scoped as Phase 2 tables) are pulled forward into Phase 1 — but only used narrowly here, for ARP reads against devices flagged as core/L3. Phase 2 reuses the same `network_devices` rows and adds full interface/VLAN/MAC-table polling on top; nothing here is redone.

### VLAN — inferred in Phase 1, verified in Phase 3

VLAN is tracked from day one, at two levels of confidence — never presented as more certain than it is:

- **Inferred (Phase 1, immediate)**: if a subnet is configured with its `vlan_number` (most subnets map 1:1 to a VLAN), any host matched to that subnet gets that VLAN. The ARP collector adds a second inferred source for free — each core switch's ARP table is queried per-SVI, and each SVI *is* a VLAN interface, so the VLAN comes along with every ARP entry at no extra cost.
- **Verified (Phase 3, authoritative)**: once switch MAC-table correlation exists, it overwrites the inferred value with the actual VLAN tag observed on the specific access port the host is connected to — this is `hosts.vlan_source = 'switch_verified'`, and it's what `endpoint_location_current.vlan_number` always reflects.

`hosts.vlan_source` tells you which kind of answer you're looking at (`subnet_config` / `arp_svi` / `switch_verified`) — the UI/API should surface this, not just the number, so "which VLAN" never reads as more certain than the evidence actually is.

### Reconciliation (kept intentionally simple in Phase 1)
- A `hosts` table holds one row per known device.
- Matching logic — deliberately simple, **not** the full Identity Service from the earlier design docs:
  1. Match by AD `objectGUID` if already linked.
  2. Else match by hostname (case-insensitive) if AD reports `dNSHostName` and network-presence reports a resolvable hostname.
  3. Else treat as a weak/candidate match by IP+MAC — flagged `needs_review`, never auto-merged.
- No automatic merge/split workflow yet. Ambiguous matches sit in a manual review queue (`hosts.match_status = 'needs_review'`). The full Identity Service (aliases, conflict resolution, priority evidence chain) is deferred to Phase 5.

### What Phase 1 delivers
- A `hosts` list: hostname, AD OU/domain (if known), current IP, last seen, up/down, evidence source(s).
- Fully functional even with AD down — network-presence-only hosts still show up, flagged `ad_status = unknown`.

---

## Phase 2 — SNMP polling

### Problem this phase solves
Discover the network devices themselves (Cisco switches + MikroTik routers) and their live interface/VLAN/neighbor state. This phase does **not** map endpoints to ports yet — that's Phase 3, kept deliberately separate so this phase's data can be trusted on its own first.

### Components

**SNMP Provider (Cisco)**
- SNMPv3 preferred, v2c fallback, configured per-device (`snmp_profiles`).
- Polls: system info, interfaces (`IF-MIB`/`ifXTable`), VLANs, MAC table (`BRIDGE-MIB`/`Q-BRIDGE-MIB`), LLDP/CDP neighbors.
- Poll interval configurable per device, default 10 minutes.

**MikroTik Provider (extends the client built in Phase 1)**
- Phase 1's DHCP collector already built an `internal/providers/mikrotik` RouterOS API client for lease reads. Phase 2 extends that same package — it does not add a second, separate MikroTik integration.
- Adds: ARP table read, wireless registration table read, and SNMP polling for interface/uptime stats (RouterOS API doesn't cover everything SNMP does).
- Result: one MikroTik client package, growing in capability phase by phase, always talking to the device one way.

### Polling architecture (no broker, Postgres-backed)
- A `discovery_jobs` table holds scheduled/queued/running/failed jobs.
- A scheduler goroutine enqueues due jobs; a worker pool (configurable size, default 5) claims jobs via a single `UPDATE ... WHERE id = ... AND status = 'queued' RETURNING ...` statement, which acts as the lease.
- Retry with exponential backoff. After N consecutive failures a device is marked unhealthy and polling frequency backs off (circuit breaker) — visible via the device's `last_error`/`consecutive_failures` fields.

### What Phase 2 delivers
- Full switch/router inventory: interfaces, VLANs, MAC tables, LLDP/CDP neighbors — current + history.
- Poll health visible per device (last success, last error, consecutive failures).
- Still no endpoint-to-port mapping — verify this phase's data is trustworthy before Phase 3 consumes it.

---

## Phase 3 — Correlation / Mapping

### Problem this phase solves
Given (a) hosts from Phase 1 and (b) switch MAC tables + neighbor data from Phase 2, determine which switch port each host is connected to — with a confidence level, never presented as absolute fact.

### Algorithm (simplified from the fuller design, same core logic)
1. Normalize all MAC addresses to one canonical form (lowercase, no separators) everywhere in the system.
2. For each host's known MAC(s), find recent MAC-table observations within a freshness window (default: strong <15 min, weak <24h, ignore beyond that).
3. Classify each candidate port — access / trunk / uplink — using LLDP/CDP neighbor type, VLAN count, and MAC count on the port.
4. Suppress uplink/core ports as a host's "location" unless there is no better evidence — instead walk the LLDP/CDP topology link to find the actual access switch.
5. Score candidates: recent + access port + single-MAC port = high confidence; shared/trunk/stale = medium; uplink-only/very stale/conflicting = low.
6. Write the winning candidate to `endpoint_location_current` with `confidence` (`high`/`medium`/`low`) and `location_quality` (`direct_access_port`/`shared_access_port`/`behind_uplink`/`unknown`). Write every observation to `endpoint_location_history`. Also update `hosts.current_vlan` / `hosts.vlan_source = 'switch_verified'` for the host — this is what upgrades Phase 1's inferred VLAN to an authoritative one.

### What Phase 3 delivers
- Host detail view: "connected to SW-ACC-04, port Gi1/0/12, VLAN 20, confidence: high, last seen 3 min ago."
- Honest "unknown" or "behind uplink, exact port unknown" states instead of a false guess.

---

## Phase 4 — REST API

### Problem this phase solves
Expose everything built in Phases 1–3 over HTTP, so a frontend, scripts, or you personally via curl can query it.

### Design
- Plain REST, `/api/v1/...`, JSON — no GraphQL, unjustified complexity for this domain.
- Read-heavy: `/hosts`, `/hosts/{id}`, `/devices`, `/devices/{id}`, `/devices/{id}/mac-table`, `/search?q=...`.
- A handful of admin/write endpoints for config (subnets, SNMP profiles, resolving the Phase 1 `needs_review` queue).
- Auth: start with a single static API token (env var) for personal use. Do not build full RBAC/session/user management until Phase 5 needs multiple users.

### What Phase 4 delivers
- A working API reachable from a browser, curl, or a future frontend — the system becomes usable outside the database directly.

---

## Phase 5 — TUI

### Problem this phase solves
Getting to a daily-usable "who/what is connected right now" view without waiting for a full Web dashboard (Phase 7) — and dogfooding the Phase 4 API in the process.

### Design
- `bidar tui` — the *new* TUI added in this phase, built with `bubbletea`/`lipgloss` (already in `go.mod` from the existing TUI — reuse, don't reintroduce). The existing config-management TUI was renamed `bidar tui-config` in Phase 0 and is unaffected by this phase.
- The TUI is a **client of the Phase 4 REST API over HTTP** (using the same bearer-token auth), not a direct database client. This keeps one code path for "read the system's state" — the same one the future Web dashboard and any scripts use — and lets the TUI work against a `bidar serve` instance running on a different machine, not just locally.
- Read-only. No `exec`/`backup` actions launchable from the TUI in this phase — keep the same read-only/read-write boundary that separates the daemon from the existing SSH-based commands (see §Merging with the existing Bidar CLI above). A "jump to `bidar exec`" convenience can be considered later, but the TUI process itself should never hold SSH credentials.
- Views: a searchable/filterable host list (hostname, IP, MAC, VLAN, last seen, AD status, confidence), a host detail pane (hardware from `host_hardware` where an agent exists, location from `endpoint_location_current` where correlation has run), and a device list showing SNMP/RouterOS poll health.
- Refresh: periodic polling of the API (a few seconds), not a push/streaming connection — matches the daemon's own polling cadence, no need to over-build this.

### What Phase 5 delivers
- A live, usable terminal view of the whole system without a browser or any frontend build tooling — the fastest path to real day-to-day value from everything built in Phases 1–4.

---

## Phase 6 — Endpoint Agent (cross-platform: Windows, Debian/Ubuntu, RHEL/Fedora, macOS)

### Problem this phase solves
Phases 1–4 tell you a host exists, is online, and (from Phase 3) roughly where it's physically connected — but not its actual specs. The original ask was "who, where, **and with what specs**" — CPU, RAM, GPU, disk, BIOS/serial, OS version/build, currently logged-in user. That level of detail is only reliably available from something running *on* the endpoint — and the fleet isn't Windows-only, so the agent has to cover Linux and macOS from the start, not as a bolt-on later.

### Why this comes after the REST API, not before
The agent needs an authenticated HTTP endpoint to report to. Rather than building a one-off listener, this phase adds ingestion endpoints on top of the router/auth middleware/JSON handling Phase 4 already built. Building the agent any earlier would mean building (and later throwing away) a throwaway HTTP layer.

### One agent, one wire protocol, per-OS collection

The enrollment/heartbeat/inventory protocol, the transport (HTTPS + machine token), and the normalized data shape sent to the backend are identical across all four targets. Only the *local collection* code differs. Structure this as:

- `internal/agent/collector` — a `Collector` interface returning one normalized struct (manufacturer, model, BIOS vendor/version/serial, CPU summary, RAM total, disk summary, GPU summary, OS name/edition/version/build, logged-in user, last boot).
- `internal/agent/collector/collector_windows.go` (`//go:build windows`) — PowerShell/CIM (`Get-CimInstance Win32_*`). Do not reimplement WMI in pure Go.
- `internal/agent/collector/collector_linux.go` (`//go:build linux`) — reads `/sys/class/dmi/id/*` for BIOS/serial/manufacturer (root or a narrowly-scoped sudo rule may be required for some fields), `/proc/cpuinfo`, `/proc/meminfo`, `lsblk`/`/sys/block` for disk, `/etc/os-release` for distro/version. Debian/Ubuntu and RHEL/Fedora/CentOS both expose the same `/sys`/`/proc` interfaces — the only real difference between them is the packaging format for *distributing* the agent (`.deb` vs `.rpm`), not the collection code.
- `internal/agent/collector/collector_darwin.go` (`//go:build darwin`) — `system_profiler SPHardwareDataType`, `sysctl`, `ioctl`/`ioreg` for serial and model. Note some fields may need Full Disk Access / elevated permission depending on macOS version — flag this during pilot, don't assume it'll just work.
- Go cross-compiles cleanly for all four targets from one codebase — this is one binary per OS/arch, not four different agents.

Go binary reports over HTTPS to: `POST /api/v1/agents/enroll`, `POST /api/v1/agents/heartbeat`, `POST /api/v1/agents/inventory`.

### Agent authentication (lifecycle, not just "send a token")
1. Admin issues a one-time enrollment token (via the API or a CLI command).
2. Agent installs, presents the enrollment token once, receives a machine-specific agent token in return.
3. Backend stores only the hash of the agent token — never the plaintext.
4. Agent uses its token over TLS for every subsequent call.
5. Tokens can be rotated or revoked from the admin side; a revoked agent's submissions are rejected.

### Relationship to Phase 1 evidence
The agent is additive, not a replacement — it becomes the richest evidence source for a host, but AD/ARP/DHCP/ICMP evidence keeps a host visible even where the agent isn't installed or has stopped reporting. Agent heartbeat is also itself strong "is this online" evidence, on top of what Phase 1 already provides.

### Deployment (operational concern, not a blocker for the MVP) — differs per OS
- **Windows**: GPO startup script or MSI push, using the enrollment-token mechanism above.
- **Linux (Debian/Ubuntu, RHEL/Fedora)**: ship as `.deb`/`.rpm` with a `systemd` unit; both distro families use `systemd`, so the unit file is shared even though the package format isn't. Rollout can very likely piggyback on the Ansible setup you already run for Ubuntu server patching, rather than building a separate deployment mechanism from scratch.
- **macOS**: `launchd` plist + `.pkg` installer, or manual install for a small fleet.
- Non-domain/unmanaged machines on any OS: manual install with a manually issued enrollment token.
- Start with a handful of pilot machines across at least two of the four targets; broader rollout is a Phase 5 rollout task, not a Phase 5 code task.

### What Phase 5 delivers
- Host detail view gains real hardware/OS specs and logged-in user, sourced from the agent where installed — regardless of OS.
- Agent health visible (last heartbeat, last inventory, agent version, OS/platform) per host.
- Enrollment tokens can be issued and revoked.

---

## Phase 7 — everything else (only after 1–6 are solid and in daily use)

Deferred by design, not forgotten:
- Formal Identity Service (aliases, merge/split, conflict resolution) — replaces Phase 1's simple matching if that stops being sufficient.
- Explicit lifecycle states (online/offline/stale/archived) with hysteresis.
- Event model / audit log as a first-class table (outbox pattern, for future webhooks/alerts).
- Real-time layer: SNMP traps (MAC notification, linkUp/linkDown) and/or syslog ingestion — Phase 2's polling alone is **not** real-time (5–15 min lag by design).
- Multi-user auth (LDAP bind or SSO), RBAC.
- Dashboard/topology UI (React + Cytoscape.js, per the original design docs).
- Retention/partitioning for history tables once volume is an actually measured problem — not before.

Do not pull work from this list forward without explicitly updating `roadmap.md` first.

---

## Decisions made

1. **Network-presence mechanism — resolved**: not a single mechanism. Three collectors run in parallel: SNMP ARP reads from *all* core/L3 switches, DHCP lease reads from *all* configured DHCP sources (multi-type), and ICMP sweep as a fast freshness fallback. See §Phase 1 above.

## A note on open-sourcing this project

This project is meant to be published as open source, not built only for one environment. That means: **the number of core switches, the number/type of DHCP servers, subnet lists, credentials, and building names are deployment data — rows in `network_devices` / `dhcp_sources` / `subnets` — never hardcoded in code, migrations, or these docs.** A given deployment (e.g. three Cisco cores plus a Windows and a MikroTik DHCP server) is configured after install, the same way any number of cores or DHCP sources of any type would be. Keep this in mind while implementing: if a piece of code only works for exactly one core switch or assumes a specific vendor pairing, that's a bug, not a shortcut.

Practical open-source housekeeping to do before/around Phase 1, separate from the technical work above:
- Pick a license (MIT/Apache-2.0 are the common defaults for this kind of tool) and add a `LICENSE` file.
- Write a human-facing `README.md` (separate from `AGENTS.md`, which is agent-facing) — what the project does, quick start, link to `docs/`.
- Avoid committing any real environment data (IPs, hostnames, credentials) as examples — use placeholder/example values in docs and sample config.

## Open decisions to make before/during Phase 1

Deliberately left as choices rather than decided here — record the answer as deployment configuration (not in this file) once chosen for a given deployment:

1. **Which devices count as "core/L3" for the ARP collector** — needs an explicit list per deployment.
2. **DHCP source inventory** — enumerate every DHCP server in the environment and its type, per deployment.
3. **AD service account credential source** — env var is fine for a single-operator system; per-deployment.
4. **Subnet list source** — a small `subnets` table from day one is recommended over hardcoded config, since more will be added over time.
