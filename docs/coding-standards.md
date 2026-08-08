# Coding Standards

## Go style
- `gofmt -s` and `golangci-lint run` must pass before commit. No exceptions, no `//nolint` without a comment explaining why.
- Standard project layout: `cmd/`, `internal/` — nothing importable from outside the module lives outside `internal/`.
- Constructors take explicit dependencies (DB pool, logger, config struct) — no globals, no `init()` magic beyond flag/env parsing.
- Errors: always wrap with context — `fmt.Errorf("polling device %d: %w", deviceID, err)`. Never `_ = err`. Sentinel errors (`errors.Is` targets) live next to the type they belong to.
- Context: every function that does I/O takes `ctx context.Context` as its first argument and respects cancellation/timeout.
- Logging: `log/slog`, structured fields, not string concatenation — `logger.Error("snmp poll failed", "device_id", id, "err", err)`.

## Package boundaries
- `internal/providers/<name>` — one provider, one package. A provider exposes a small interface (roughly: `Run(ctx) (Result, error)`, `Name() string`, `Health() Health`). Providers do not import each other.
- `internal/agent/collector` — platform-specific code lives behind Go build tags (`collector_windows.go`, `collector_linux.go`, `collector_darwin.go`), all implementing the same `Collector` interface. Never branch on `runtime.GOOS` at runtime inside shared code when a build tag can do it at compile time instead.
- `internal/domain` — plain types shared across packages (Host, Device, Observation, Location). No I/O in this package.
- `internal/store` — all SQL lives here. Handlers and providers call `store.*`, never raw SQL themselves.
- `internal/api` — HTTP only: routing, request parsing, response encoding, auth middleware. No business logic — delegate to domain/store.

## Migrations
- `golang-migrate`, numbered sequentially (`0001_..`, `0002_..`), always with both `.up.sql` and `.down.sql`.
- Never edit a migration file that has already run anywhere, including your own dev DB — write a new migration instead.
- Every migration is reviewed for: does it need a new index for a query we already know we'll run? Add it in the same migration.

## Testing
- Providers: unit-test the parsing/normalization logic against recorded fixture data (a captured SNMP walk, a sample LDAP response) — `go test ./...` should not require live network access.
- Store layer: integration tests against a real Postgres, gated behind an env var (e.g. `BIDAR_TEST_DATABASE_URL`) that's unset by default — `go test ./...` must pass with zero external dependencies, and the integration tests run only when that var is set (CI, or a developer running one explicitly).
- API handlers: table-driven tests per endpoint covering the success path plus the 2–3 most likely error paths (bad input, not found, auth failure).
- No target coverage percentage — test what's likely to break or silently produce wrong data (correlation/confidence logic especially).

## Config
- All runtime config via environment variables, loaded once at startup into a typed `Config` struct (`internal/config`). No config file for MVP.
- Secrets (AD service account password, SNMP community/v3 keys, MikroTik API password) are never logged, never returned in API responses, and encrypted at rest using a key from `BIDAR_MASTER_KEY` (env var, base64-encoded 32 bytes — see `internal/crypto`).

## Commit conventions
- Small, phase-scoped commits. Prefix with the phase or area when useful: `phase1: add AD provider LDAP query`, `schema: add mac_table_current`.
- Do not mix schema migration + feature code + unrelated refactor in one commit.

## When in doubt
- Prefer the boring, explicit solution over the clever one.
- If a task seems to require a new external dependency, a new top-level package, or work that belongs to a later phase — stop and flag it instead of proceeding.
