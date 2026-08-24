# AGENTS.md — Guide for AI Coding Agents (Bidar)

This file tells any AI coding agent (Claude Code, Cursor, etc.) working on this repository how to build, extend, and reason about this project. Read this before writing any code.

## 0. Bidar merges two things — know which one you're touching

This repo is the merge of two previously separate tools:
- **The existing Bidar CLI** (`monitor`, `backup`, `exec`, `diff`, `init`) — on-demand, human-invoked, SSH-based. `exec`/`backup` **change device state** (can push config, save running-config, etc.). Read-write by nature.
- **The new inventory daemon** (Phases 1–6 below) — unattended, continuous, passive. It only ever reads (SNMP/ARP/DHCP/AD/agent reports). It must never gain the ability to change a device.

Both live in one `bidar` binary as CLI subcommands (`bidar monitor`, `bidar exec`, ..., `bidar serve` for the daemon), and share core packages (`internal/snmp`, and the `network_devices` table as the one canonical device list) — but their credential paths stay separate. SNMP profiles (read-only) and SSH credentials (read-write, used only by `exec`/`backup`) are different tables. **Daemon code (`internal/providers/*`) must never import anything from the SSH/exec code path.** If a task seems to require that, stop and flag it — it's very likely a boundary violation, not a legitimate need.

## 1. What this project is

A lightweight, self-hosted network & asset inventory system. Built to run on a small-to-medium enterprise network (reference scale: a few hundred endpoints, tens of Cisco/MikroTik network devices), and designed to be released as open source — so every environment-specific detail (which devices are "core," how many DHCP servers exist, subnet lists, credentials) is **deployment configuration in the database or environment, never hardcoded in code or docs.** The goal: answer "who/what is connected to the network right now, and where, and with what specs" — first from Active Directory + live network presence, then enriched with SNMP switch-port mapping and a lightweight endpoint agent.

This is **not** a commercial CMDB. Do not add complexity (microservices, Kubernetes, Redis, message brokers, dynamic plugin systems) unless a phase below explicitly calls for it.

## 2. Tech stack (fixed — do not change without discussion)

- Language: Go (standard library first; external deps only when justified — see §6)
- Database: PostgreSQL
- Migrations: golang-migrate (SQL files, up/down)
- API: REST, `net/http` (stdlib `ServeMux`, or `chi` only if routing needs grow)
- Config: environment variables (`.env` for local dev), no config server
- Logging: structured logging via `log/slog`
- Deployment target: single Linux VM, single binary + systemd service

## 3. Repository layout

```
/cmd/bidar/main.go       → entrypoint (signal handling + context, already daemon-shaped)
/cmd/cli/                → command definitions — existing: root.go, init.go, monitor.go, backup.go, exec.go, diff.go — new (Phase 0+): serve.go, migrate.go, import_devices.go. The config-file TUI (tui.go/tui_*.go, briefly renamed `tui-config`) was removed entirely rather than extended — Phase 5's REST-API-client TUI is a separate, unrelated tool and there was no value maintaining two.
/internal/config/       → existing YAML/CSV loader (LoadConfig, Validate, Save) — stays as-is for the legacy CLI path
/internal/db/           → NEW (Phase 0) — pgx pool, migrations runner
/internal/crypto/       → NEW (Phase 0) — encrypt/decrypt for DB-stored secrets, keyed by BIDAR_MASTER_KEY. Legacy YAML/CSV credentials are NOT touched by this — they stay plaintext-on-disk, operator-owned, gitignored.
/internal/providers/    → NEW — one package per daemon evidence source (ad, arp, dhcp, icmpsweep, snmp, mikrotik, ...) — read-only, never imports internal/device
/internal/snmp/         → EXISTING (snmp.go, types.go) — SnmpWalk (single GET, v2c only, string-typed) stays untouched for CLI compatibility. Phase 1/2 add new functions alongside it (typed walk, v3, context) — do not modify SnmpWalk's signature.
/internal/device/        → EXISTING — SSH client + Cisco/MikroTik command execution, used only by `exec`/`backup`/`monitor`. Imports internal/snmp (one-directional; snmp does not import device). Daemon code must never import this package.
/internal/retry/         → EXISTING — reused as-is wherever retry-with-backoff is needed
/internal/worker/        → EXISTING — check whether Phase 2's job worker pool can build on this before writing a new one
/internal/logger/        → EXISTING — custom color logger for the CLI, left untouched. The daemon gets its own slog-based logger, injected via constructor, not routed through this package.
/internal/report/        → EXISTING — CLI's terminal/JSON report printer, left untouched. The daemon's JSON responses come from internal/api, not this package.
/internal/agent/collector/ → NEW (Phase 6) — agent-side hardware/OS collection, one file per OS build tag
/internal/tui/            → NEW (Phase 5) — the REST-API-client TUI (`bidar tui`). The only TUI in the project — the earlier config-file TUI (`tui-config`) was removed rather than kept alongside it.
/internal/domain/       → NEW — core types: Host, Device, Observation, Location — no I/O here
/internal/api/          → NEW — HTTP handlers, routing, middleware
/internal/store/        → NEW — all SQL queries, one file per table/aggregate
/migrations/            → NEW — golang-migrate SQL files, starting at 0001_init.up.sql
/cmd/inventory-agent/    → NEW (Phase 6) — endpoint agent entrypoint, cross-compiled per OS, separate binary
/docs/                  → architecture, schema, roadmap, standards (this folder's siblings)
```

This reflects an actual codebase audit (2026-08-07), not an assumption — `cmd/cli/` is correct (not `cmd/bidar/cli/`), and `monitor`/`backup`/`exec`/`diff`/`init`/`tui` already exist and work, along with `internal/retry` and `internal/worker`. Nothing marked NEW above should already exist; if it does, re-run the audit before proceeding.

Do not introduce a `services/` vs `microservices/` split. This is one binary.

## 4. Build order — follow this exact sequence

The project is built in phases. **Do not start phase N+1 work until phase N's Definition of Done (`docs/roadmap.md`) is met and in actual daily use.** Each phase must be usable on its own.

1. **Phase 0 — Merge with the existing Bidar CLI** (shared SNMP client, `network_devices` as canonical device list, `ssh_credentials` split from `snmp_profiles`, `bidar import-devices`)
2. **Phase 1 — AD connectivity + live network presence** (works even if AD is unreachable)
3. **Phase 2 — SNMP polling** of Cisco switches and MikroTik devices
4. **Phase 3 — Correlation/mapping** (endpoint → switch → port, with confidence)
5. **Phase 4 — REST API** exposing phases 1–3
6. **Phase 5 — TUI** (`bidar tui`, a terminal client of the Phase 4 API — the fastest path to a daily-usable live view)
7. **Phase 6 — Endpoint Agent** (cross-platform Go agent — Windows, Debian/Ubuntu, RHEL/Fedora, macOS — rich hardware/OS inventory, reports over the Phase 4 API)
8. **Phase 7 — everything else** (Web dashboard/topology, lifecycle states, events, real-time via traps/syslog, formal identity/merge tooling)

Full rationale: `docs/architecture.md`. Schema per phase: `docs/database-schema.md`. Task breakdown + Definition of Done: `docs/roadmap.md`.

## 5. Non-negotiable design rules

- **No single component is ever the source of truth for "does this device exist."** AD, network-presence scan, and (later) SNMP MAC tables are independent *evidence providers*. The system reconciles them — it never lets one silently overwrite another's evidence.
- **Never present a guess as fact.** If confidence is low or evidence conflicts, store and surface that explicitly — don't pick a "best guess" and show it as certain.
- **Every provider is isolated.** Its own config, its own failure handling. One provider failing must never crash the daemon or block the others.
- **All discovery/poll jobs must be resumable from the database**, not from in-memory state — the process can restart at any time.
- **No credentials in plaintext in the database.** SNMP communities/v3 keys, AD service-account password, MikroTik API password — encrypted at the application layer before insert, never logged, never returned in API responses.
- **No real infrastructure data in git, ever — not even in docs.** Real IPs, hostnames, subnet ranges, device names, DHCP server addresses, VLAN-to-subnet mappings for an actual deployment belong in the database (`network_devices`, `subnets`, `dhcp_sources`) only. `docs/architecture.md`'s "Decisions made" section records the *shape* of a decision ("3 Cisco cores, one per building" / "11 subnets" / "3 DHCP sources") — never the addresses themselves. If a task's output would put a real IP, hostname, or subnet CIDR into a file under `docs/`, `README.md`, a commit message, or any other tracked file, stop and write it to the database instead. This already happened once (real subnet data committed to `docs/`, caught and scrubbed from git history) — treat that as the standard this rule exists to prevent from recurring.
- **Idempotency:** any ingestion path or provider run must be safe to run twice with the same input.

## 6. External dependencies — ask before adding

Currently approved:
- `github.com/jackc/pgx/v5` — Postgres driver (not yet in go.mod)
- `github.com/golang-migrate/migrate/v4` — migrations (not yet in go.mod)
- `github.com/gosnmp/gosnmp` — already in go.mod (v1.43.2), used by `internal/snmp`. Add new typed/walk/v3 functions to that package rather than a second SNMP client.
- `github.com/go-ldap/ldap/v3` — AD/LDAP client (Phase 1, not yet in go.mod)
- `github.com/spf13/cobra` — already in go.mod, existing CLI framework; new subcommands (`serve`, `migrate`, `import-devices`) follow its existing `var fooCmd = &cobra.Command{...}` + `init(){ rootCmd.AddCommand(...) }` pattern
- `golang.org/x/crypto` — already in go.mod (SSH); reuse for the new `internal/crypto` package if it fits, otherwise a minimal justified addition
- `github.com/go-chi/chi/v5` — only if stdlib routing becomes unwieldy in Phase 4
- `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/lipgloss` — removed from go.mod along with the config-file TUI; re-add them when Phase 5 builds the new `bidar tui`. `bubbles` was never used — do not add it unless Phase 5 genuinely needs it.

Environment variables use a `BIDAR_` prefix: `BIDAR_DATABASE_URL`, `BIDAR_MASTER_KEY` (for `internal/crypto`), etc. — pin every new one in `docs/roadmap.md` Phase 0 as it's introduced.

Do not add an ORM. Do not add a job-queue library — the job queue is a Postgres table (see `docs/architecture.md` §Polling architecture).

## 7. Coding conventions

Full detail: `docs/coding-standards.md`. Summary:
- `gofmt` + `golangci-lint` clean, no exceptions without an explained `//nolint`.
- Errors wrapped with context (`fmt.Errorf("...: %w", err)`), never swallowed.
- No package-level mutable state except what's injected via constructor (DB pool, logger).
- One SQL migration per logical change; never edit a migration already applied anywhere.
- Table names: `snake_case`, plural (`hosts`, `device_interfaces`).

## 8. What Farshid wants from the agent

- Write real, runnable Go — not pseudocode — unless a sketch is explicitly requested.
- He reviews and edits code himself; prefer proposing an approach and a first draft over a large unreviewed dump. Flag design trade-offs explicitly rather than silently picking one.
- Keep each phase's surface area small. If a task starts pulling in a later phase's concepts, stop and flag it instead of quietly expanding scope.
- Ask before adding a new external dependency or a new top-level package.

## 9. Where to look first

- `docs/architecture.md` — why the system is shaped this way, phase by phase
- `docs/database-schema.md` — tables, grouped by phase
- `docs/roadmap.md` — task breakdown and Definition of Done per phase
- `docs/coding-standards.md` — Go conventions, migration workflow, testing
