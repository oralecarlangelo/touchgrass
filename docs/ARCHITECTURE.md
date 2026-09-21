# touchgrass Architecture

Companion to `BRD-touchgrass.md` (requirements) and
`PROJECT_STRUCTURE.md` (folder standard). Decisions below are binding;
change them via an ADR in `docs/adr/`, not by silent drift.

## System overview

One Go binary + one SQLite file. The binary serves the API, the embedded
UI, SSE streams, and the SDK ingestion endpoint; background loops probe
health and enforce retention.

```
                         ┌──────────────────────────────┐
                         │        touchgrass binary     │
                         │                              │
 browsers ──► :8080 ──►  │  internal/http               │
                         │   ├─ REST API (stdlib mux)   │
 tn-api etc.             │   ├─ SPA (embedded web/dist) │
   │ SDK ──► :8080/ingest│   ├─ SSE (/events)           │
                         │   └─ middleware (auth, log)  │
                         │                              │
                         │  internal/service            │
                         │   ├─ deploy (traffic flips)  │
                         │   ├─ resources (metrics)     │
                         │   ├─ issues (error grouping) │
                         │   ├─ logs (search)           │
                         │   ├─ notify (webhook/email)  │
                         │   └─ audit (append-only)     │
                         │                              │
                         │  internal/store (SQLite)     │
                         │  internal/{docker,probe}     │
                         │  schedulers (probe, retention│
                         └──────┬───────────────┬───────┘
                                │               │
                    Docker socket│               │SQLite file
                    (stats, ps)  │               │(volume-mounted)
                                ▼               ▼
                         containers      touchgrass.db
```

Outbound: deploy engine shells out to service cutover scripts for
cutovers and rollbacks (tn-api's `bluegreen-*.sh` in v1); notifier
POSTs webhooks / sends email.

## Layering and DI (decided)

- **Layers**: `http` (transport: parse, validate, respond) →
  `service` (domain logic, one service per pillar area) →
  `store` (SQLite persistence). `docker`/`probe`/`notify` are
  infrastructure helpers consumed by services, never by handlers directly.
- **DI**: manual constructor injection (`New(...)` in `cmd`), no framework.
  Matches the BRD's narrow-surface constraint; revisit only if wiring
  genuinely hurts (ADR required).
- **Don't**: no business logic in `cmd/` or handlers; no SQL outside
  `store/`; no cross-service imports (services share `model/`, not each
  other).

## HTTP design

- Stdlib `net/http` + Go 1.22 method+pattern ServeMux. No router framework
  in v1 (stdlib-first).
- Route sketch (finalized in technical design):
  - `GET /api/health` — own health (also serves as the uptime probe target)
  - `/api/services`, `/api/services/{id}/metrics`,
    `/api/services/{id}/deploys`, `POST
    /api/services/{id}/cutover`, `POST /api/services/{id}/rollback`,
    `/api/alerts/rules`, `/api/notifications`, `/api/audit` — Sprint 5
    resources, JSON, `[]` never `null`
  - `/api/deploys`, `/api/resources`, `/api/issues`, `/api/logs` — later
    pillar resources
  - `POST /api/ingest` — SDK error reports (per-project API key header)
  - `GET /api/events` — SSE stream (metrics ticks, deploy progress, alerts)
  - `/*` — embedded SPA fallback
- Errors: internal chain with `%w`; boundary translates to
  `{error: <user-safe message>, code: <machine code>}` + status;
  technical detail goes to logs only.
- Middleware: admin auth (session cookie, v1 single credential),
  structured request logging (method, path, status, duration).

## Data

SQLite file (`touchgrass.db`, volume-mounted). Schema areas:

| Area | Holds |
| ---- | ----- |
| services | name, compose project, strategy, health endpoint, ports |
| deploys | per-service cutover/rollback history: SHA, actor, timing, outcome, downtime secs |
| metrics | container rollups (CPU/RAM/disk, restarts, uptime samples) |
| issues | fingerprinted error groups + occurrences |
| logs | collected container log lines (short retention) |
| audit | append-only cutover/rollback/login log |
| settings | alert rules, notification targets, retention caps, keys |

- Migrations: versioned `migrations/*.sql` applied in order by an internal
  runner tracking `schema_migrations`. Forward-only in v1.
- Retention: max-age + max-bytes per area, enforced on a schedule by the
  binary itself. Mandatory (BRD NFR-2), tested with a volume soak.

## Auth (v1)

- UI/API: single admin password from `TOUCHGRASS_ADMIN_PASSWORD`,
  bcrypt-hashed at boot and verified with bcrypt, plus a session cookie.
  Binds localhost by default; public listen requires explicit config.
- Ingestion: per-project API keys (`tg_<random>`), revocable, stored hashed.

## Config (12-factor)

Environment variables only in v1 (no config file, no Viper):

- `TOUCHGRASS_ADDR` (default `127.0.0.1:8080`), `TOUCHGRASS_DB`
  (default `./touchgrass.db`), `TOUCHGRASS_ADMIN_PASSWORD` (required),
  `TOUCHGRASS_DATA_RETENTION_*` overrides.
- Logs to stdout, JSON via `slog`. Graceful shutdown on SIGTERM
  (drain HTTP + stop schedulers).

## CLI

Single binary, subcommands via stdlib `flag` (no Cobra in v1):

- `touchgrass serve` (default) — run the server
- `touchgrass migrate [up|status]` — one-off schema admin
- `touchgrass version` — version + build SHA

## Web embedding and dev mode

- Prod: `web/dist` (Vite build output) embedded via `go:embed`; the Go
  binary serves it. Release flow builds the SPA first, then Go.
- Dev: `make dev` runs API + `vite dev` side by side; the API proxies `/`
  to Vite when `APP_ENV=dev` so HMR works without rebuilds.

## SDK contract (`@touchgrass/node`)

- Captures uncaught exceptions + manual reports with stack traces,
  breadcrumbs, release/SHA tags; batches async over HTTPS+key.
- Zero dependencies. Fail-open: guarded init, no sync IO, try/catch
  around everything; safe to leave enabled with ingestion down.
- PII scrub hooks run client-side before send.

## Distribution

- `touchgrass_<os>_<arch>` binaries via GoReleaser + a Docker image +
  `docker-compose.yml` (binary + volume for the SQLite file).
- Fresh-host install from README in < 30 min (BRD acceptance criterion).

## Errors and observability (internal)

- `slog` JSON everywhere; request middleware on all HTTP; `oops` for
  production errors needing traces/attributes; low-cardinality message
  templates with IDs as attributes (per `golang-error-handling`).
- Own `/api/health` doubles as the canary endpoint for external uptime
  checks.

## Seams left for Phase 8

- `service`/`store` interfaces stay host-agnostic so multi-host agents can
  slot behind them; `docker/` is the seam an agent would remote.
- Tracing/APM, browser/mobile SDKs, RBAC, and secrets management are
  explicitly out — don't half-build them.
