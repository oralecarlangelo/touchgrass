# touchgrass Architecture

Companion to `BRD-touchgrass.md` (requirements) and
`PROJECT_STRUCTURE.md` (folder standard). Decisions below are binding;
change them via an ADR in `docs/adr/`, not by silent drift.

## System overview

One Go binary + one SQLite file. The binary serves the API, the embedded
UI and docs site, SSE streams, and the SDK ingestion endpoints;
background loops sample containers, probe health, collect logs, and
enforce retention.

```
                         ┌──────────────────────────────┐
                         │        touchgrass binary     │
                         │                              │
 browsers ──► :8080 ──►  │  internal/http               │
                         │   ├─ REST API (stdlib mux)   │
 managed apps            │   ├─ SPA (embedded web/dist) │
   │ SDK ──► :8080/ingest│   ├─ docs (embedded site)    │
                         │   ├─ SSE (/events)           │
                         │   └─ middleware (auth, log)  │
                         │                              │
                         │  internal/service            │
                         │   ├─ deploy/cutover (deploys)│
                         │   ├─ resources (metrics)     │
                         │   ├─ issues (error grouping) │
                         │   ├─ logs + sdk_logs (search)│
                         │   ├─ fleet/system (sampling) │
                         │   ├─ database (pg/redis)     │
                         │   ├─ performance (correlate) │
                         │   ├─ security (episodes)    │
                         │   ├─ onboarding (promote)    │
                         │   ├─ notify (webhook/email)  │
                         │   └─ audit (append-only)     │
                         │                              │
                         │  internal/store (SQLite)     │
                         │  source adapters             │
                         │   ├─ nginxlog (HTTP timing)  │
                         │   ├─ dbstats (pg/redis)      │
                         │   └─ docker/probe            │
                         │  schedulers (sample, probe,  │
                         │   collect, roll up, retain)  │
                         └──────┬───────────────┬───────┘
                                │               │
                    Docker socket│               │SQLite file
                    (stats, ps)  │               │(volume-mounted)
                                ▼               ▼
                         containers      touchgrass.db
```

Outbound: the deploy engine shells out to service-configured cutover scripts
for cutovers and rollbacks. Repository examples may include a Ticketnation
blue-green adapter, but the engine has no Ticketnation-specific branch.
Notification providers POST webhooks or send email through generic contracts.

## Project-agnostic boundary (decided)

Touch Grass core models deployments, services, signals, incidents, and
notification channels without knowing which application it monitors.
Ticketnation is the first production dogfood environment, not a product
dependency.

- Service identity, compose ownership, health URLs, cutover commands, source
  locations, and routing dimensions are data or configuration.
- Core packages must not branch on service IDs, customer names, domains,
  repository names, or specific email/cloud providers.
- Infrastructure integrations enter through narrow adapters. Standards-based
  contracts such as Docker, HTTP, SMTP, signed webhooks, PostgreSQL, Redis,
  and JSONL are preferred over project APIs.
- A provider-specific adapter may add capabilities but cannot become required
  for alert generation, storage, delivery routing, or dashboard rendering.
- Monitored services remain outside the Touch Grass trust and availability
  boundary. Failure or removal of a monitored project cannot prevent Touch
  Grass from starting or managing other services.
- Examples and fixtures may use Ticketnation names, but reusable behavior must
  also be covered by a generic fixture and must work unchanged when the service
  record is renamed.

Adding a project-specific exception to core behavior requires an ADR that
explains why configuration or an adapter is insufficient. Deployment overlays
and private runbooks do not require an ADR because they are not product logic.

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
  other); no project/customer checks in core packages.
- Performance source adapters accept native Nginx, Docker, PostgreSQL,
  Redis, and log signals through narrow interfaces. They normalize data;
  services schedule collection and correlate aggregates; stores own cursors,
  buckets, rollups, and retention.

## HTTP design

- Stdlib `net/http` + Go 1.22 method+pattern ServeMux. No router framework
  in v1 (stdlib-first).
- Resources (authoritative list: `docs-site/openapi.yaml`, verified at
  100% route parity — re-check with every new route): `GET /api/health`
  (also the uptime probe
  target); services (CRUD incl. `DELETE`, metrics, deploys, deploy,
  cutover, rollback); onboarding suggest; alert rules; notifications;
  audit; users (admin-only account CRUD); issues (+ occurrences,
  rules, per-issue logs); container logs
  (+ stats, context); SDK logs ingest; API keys; fleet/system
  (containers, host history); databases (health, backups, restore,
  jobs); docker images (+ prune); `POST /api/ingest` (SDK error
  reports, per-project API key header); `POST /api/ingest/logs` (SDK
  structured-log batches, same key model); `GET /api/events` (SSE:
  metrics ticks, deploy progress, alerts); `/docs` (embedded docs
  site); global Performance summary/timeline/routes/PostgreSQL/Redis and
  collector-status reads; service-scoped Performance reads; `/*` (embedded
  SPA fallback). JSON everywhere, `[]` never `null`. Performance ranges,
  dimensions, sort fields, bucket counts, and page sizes are capped and
  validated at the HTTP/service boundary.
- Errors: internal chain with `%w`; boundary translates to
  `{error: <user-safe message>, code: <machine code>}` + status;
  technical detail goes to logs only.
- Middleware: multi-user auth (session cookie, admin/viewer roles),
  structured request logging (method, path, status, duration).

## Data

SQLite file (`touchgrass.db`, volume-mounted). Schema areas:

| Area | Holds |
| ---- | ----- |
| services | name, compose project, strategy, health endpoint, ports |
| deploys | per-service cutover/rollback history: SHA, actor, timing, outcome, downtime secs |
| deploy_probes | per-run public-URL samples behind the downtime math |
| metrics | per-service container rollups (CPU/RAM/disk, restarts, uptime samples) |
| host/container_samples | fleet-wide samples for every container + host CPU/mem/load history |
| issues | fingerprinted error groups + occurrences (+ releases, issue rules) |
| log_lines | collected container log lines (short retention) |
| sdk_logs | SDK structured logs: level, severity, attrs, trace/span, release |
| performance buckets | one-minute HTTP, PostgreSQL, Redis, and log-volume aggregates plus bounded hourly rollups |
| collector_cursors | persisted source identity/offset, freshness, safe errors, and dropped-record counters |
| container_events | bounded Docker lifecycle, OOM, restart, and health-transition history |
| container_instances | all-state Docker identity, ownership, classification, safe posture, and presence history |
| security observations/findings | bounded scan metadata plus durable episodes, policies, baselines, and ignores |
| db_jobs | backup/restore job rows (dumps live on disk, not in SQLite) |
| audit | append-only action log (cutover, rollback, deploy, login, db_*, service_*, user_*) |
| users | managed login accounts: username, bcrypt hash, role, disabled flag |
| settings | alert rules, notifications, API keys, retention caps |

- Migrations: versioned `migrations/*.sql` applied in order by an internal
  runner tracking `schema_migrations`. Forward-only in v1.
- Retention: max-age + max-bytes per area, enforced on a schedule by the
  binary itself. Minute Performance buckets default to seven days; hourly
  rollups and runtime events default to 30 days. Deletion is batched and the
  total SQLite budget plus per-class byte ceilings are enforced. Mandatory
  (BRD NFR-2), tested with a volume soak and online backup/recovery checks.

## All-state inventory and container security

The authoritative inventory lists every Docker lifecycle state and stores
identity by container ID. Complete reconciliations infer absence; partial
reconciliations never do. Docker events reduce update latency but do not
replace polling. Live metrics remain a separate running-container stream, so
stopped rows correctly carry null metrics.

Security monitoring is native and read-only. Migrations leave activation off
so a migrated backup can be validated without collection; a successful
`touchgrass serve` startup activates posture, process, and writable-layer
collectors as `system:startup`. Each collector normalizes data before
the service layer: inspect excludes environment/arbitrary labels/host sources,
top keeps only executable identity, and diff keeps path operations without
contents. The security service fingerprints `(container, rule, baseline)` into
durable episodes, records bounded evidence, and emits notifications only on
open/recovery transitions. Partial coverage cannot resolve prior evidence.

Collectors expose last success, coverage, safe error category, effective
interval, and staleness through the shared collector-status projection. Raw
observations (168h) and container events (720h) trim before resolved findings
(2160h); unresolved findings remain durable within the global SQLite budget.
No detector path calls Docker mutation APIs or automatic containment.

Release gates are the generic compromise replay/privacy test, the 100-container
soak (`make test-security-soak`), full unit/race/integration/web validation,
and a pre-0019 online-backup migration rehearsal. Rollback restores the
untouched backup and previous binary; forward migrations are never reversed in
place.

## Agentless Performance diagnostics

ADR-0011 defines the boundary. A dedicated Nginx timing log, Docker stats and
events, read-only PostgreSQL statistics, read-only Redis statistics, and
existing log ingestion feed independent collectors. They store approved route
families and numeric aggregates only—never a raw URI, query string, SQL text,
Redis key, request payload, address, or user identifier. Unknown or sensitive
paths become `other`.

The Performance service selects bounded minute or hourly ranges and aligns
HTTP latency/error volume with resource, dependency, log, deployment, and
runtime markers. It describes evidence as correlation, not source-level
causality. A failed or stale collector reports its own status while historical
data and the other collectors remain available.

The web application exposes **Performance** under **Signals** and a
service-scoped **Performance** tab that reuses the same aggregate contracts.
Both views link to existing Logs, Issues, Deployments, Fleet, and Database
workflows for investigation.

## Auth (v1)

- UI/API: managed users with two global roles — `admin` (full
  power) and `viewer` (read-only: every `GET` plus login/logout;
  every mutating route answers 403). Passwords are bcrypt-hashed
  in SQLite; sessions are server-side random tokens bound to a
  username, with role and disabled state re-resolved per request.
  `TOUCHGRASS_ADMIN_PASSWORD` seeds the bootstrap admin on first
  boot only (ignored once an enabled admin exists). A
  last-enabled-admin guard rejects orphaning demotes, disables,
  and deletes. Binds localhost by default; public listen requires
  explicit config.
- Ingestion: per-project API keys (`tg_<random>`), revocable, stored hashed.

## Config (12-factor)

Environment variables only in v1 (no config file, no Viper):

- `TOUCHGRASS_ADDR` (default `127.0.0.1:8080`), `TOUCHGRASS_DB`
  (default `./touchgrass.db`), `TOUCHGRASS_ADMIN_PASSWORD` (required),
  `TOUCHGRASS_DATA_RETENTION_*` overrides.
- Logs to stdout, JSON via `slog`. Graceful shutdown on SIGTERM
  (drain HTTP + stop schedulers).
- Performance, maintenance, and each native source have independent enable
  conditions and bounded intervals. Empty source configuration disables only
  that collector; it never blocks application boot.

## CLI

Single binary, subcommands via stdlib `flag` (no Cobra in v1):

- `touchgrass serve` (default) — run the server
- `touchgrass migrate [up|status]` — one-off schema admin
- `touchgrass version` — version + build SHA

## Web embedding and dev mode

- Prod: `web/dist` (Vite build output) and `docs-site/dist` (guides +
  API reference) embedded via `go:embed`; the Go binary serves both.
  Release flow builds the SPA and the docs site first (`make build`
  does all three), then Go.
- Dev: `make dev` runs API + `vite dev` side by side; the API proxies `/`
  to Vite when `APP_ENV=dev` so HMR works without rebuilds.

## SDK contract (`@touchgrass/node`)

- Captures uncaught exceptions + manual reports with stack traces,
  breadcrumbs, release/SHA tags; batches async over HTTPS+key.
- `logger` ships structured app logs (levels, attributes, release)
  to `POST /api/ingest/logs`, queryable beside container logs.
- OTel logs only (ADR-0009): auto-captured trace/span ids when
  `@opentelemetry/api` is present, OTel 1–24 severity end to end,
  plus an `@touchgrass/node/otel` exporter for OTel-native apps.
  No spans, no metrics, no APM.
- Zero dependencies (OTel API is an optional peer). Fail-open:
  guarded init, no sync IO, try/catch around everything; safe to
  leave enabled with ingestion down.
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
- Collector status records last success, last safe error category, parse and
  persistence failures, and dropped records. These internal signals must not
  include rejected payload contents.

## Seams left for Phase 8

- `service`/`store` interfaces stay host-agnostic so multi-host agents can
  slot behind them; `docker/` is the seam an agent would remote.
- Tracing/APM stays out except OTel log correlation (ADR-0009);
  browser/mobile SDKs, per-service RBAC scoping, SSO, and secrets
  management are explicitly out — don't half-build them. The
  admin/viewer split (ADR-0010) is the whole role model.
