# Agentless Performance Observability for Touch Grass

**Date:** 2026-10-16
**Status:** Approved architecture; implementation plan pending
**Owners:** Touch Grass / Ticketnation platform engineering

## Summary

Touch Grass will become the operational view for finding production slowdowns and bottlenecks without adding OpenTelemetry, an APM agent, or a permanent observability SDK to Ticketnation. It will collect bounded, privacy-safe signals from infrastructure the platform already controls: Nginx timing logs, Docker statistics and lifecycle events, PostgreSQL statistics, Redis statistics, and existing application logs.

The result is a new **Performance** workspace that aligns request latency, traffic, errors, resource pressure, database activity, cache behavior, logs, deployments, and incidents on one timeline. It will help operators identify likely bottlenecks and move directly into existing logs, issues, and deployment details. It will not claim distributed-trace-level causality or exact source-line attribution.

This work also includes targeted Ticketnation fixes already supported by production evidence: missing authorization-table indexes, redundant account-order diagnostic queries, excessive and privacy-sensitive logging, duplicate Sentry interception, and shallow health checks.

## Goals

- Reveal whether slowdowns correlate with HTTP routes, PostgreSQL, Redis, container CPU or memory, deployments, restarts, or error bursts.
- Keep Ticketnation free of new observability SDKs and OpenTelemetry instrumentation.
- Keep Touch Grass out of the serving path so its failure cannot affect customer traffic.
- Store only bounded aggregates and privacy-safe identifiers.
- Make collection overhead, data retention, and disk growth predictable.
- Turn confirmed findings into small, measurable Ticketnation performance fixes.
- Preserve the existing Touch Grass model: one Go binary, SQLite, agentless collection, and the `http -> service -> store` dependency direction.

## Non-goals

- Distributed tracing, spans, baggage, or OpenTelemetry in Ticketnation.
- Adding a Touch Grass SDK or permanent APM agent to Ticketnation.
- Making Touch Grass a reverse proxy or other traffic dependency.
- Capturing request bodies, query parameters, cookies, authorization headers, client IP addresses, raw SQL parameters, Redis keys, or user identifiers.
- Promising exact source-level CPU attribution from Docker metrics.
- Uploading heap snapshots, process dumps, or unrestricted profiler output.
- Kubernetes or multi-host orchestration in this release.
- Replacing specialist incident tooling or making automatic causal claims.

## Design principles

1. **Agentless first.** Prefer existing operating-system, proxy, container, database, and cache interfaces.
2. **Aggregate before persistence.** Persist minute buckets and bounded dimensions, not raw request events.
3. **Privacy by construction.** Sensitive fields are excluded at the source; redaction is a second line of defense.
4. **Operationally optional.** Collection failures reduce visibility but never interrupt Ticketnation or Touch Grass control-plane actions.
5. **Evidence before optimization.** Dashboard correlations point investigators toward a cause; a focused measurement confirms it.
6. **Smallest correct fixes.** Ticketnation changes remain independent, reviewable, and measurable.

## Considered approaches

### A. Native collectors and time-bucket correlation — selected

Touch Grass reads dedicated Nginx timing logs, Docker statistics and events, PostgreSQL statistics, Redis `INFO`, and existing logs. It aggregates these signals into aligned time buckets and renders them together.

This approach provides useful production visibility without changing Ticketnation's runtime or request path. It cannot reconstruct a distributed trace, but it can identify high-value correlations with low operational risk.

### B. Touch Grass as a reverse proxy — rejected

Proxying Ticketnation traffic through Touch Grass would provide accurate request measurements but would make the control plane a production dependency and enlarge its security and reliability scope. This conflicts with the agentless control-plane architecture.

### C. Application SDK or OpenTelemetry — rejected by product direction

Application instrumentation provides the strongest route-template, span, and code-level fidelity. It also adds runtime coupling and maintenance that the product explicitly does not want. This design therefore does not add it.

### D. Privileged on-demand profiler — optional later

A bounded, administrator-triggered profiler may be useful after infrastructure evidence narrows a problem to one process. It must be disabled by default, proven on the production ARM64 environment, avoid the Node Inspector, keep artifacts outside SQLite, and use strict access and retention controls. It is not required for the first release.

## User experience

### Navigation

- Add **Performance** under the existing **Signals** navigation group.
- Expand the service workspace's current **Metrics** area into a **Performance** tab.
- Preserve links to existing Logs, Issues, Deployments, Fleet, and Database views.

### Global Performance page

The global page answers, “What is slow or unhealthy now, and what changed?” It contains:

1. **Health summary**
   - Requests per minute
   - p50, p95, and p99 response time
   - Error rate
   - Slow route families
   - Database and Redis pressure indicators
   - Collector freshness and dropped-data warnings

2. **Correlation timeline**
   - Request count and latency percentiles
   - HTTP 4xx and 5xx volume
   - Container CPU and memory
   - PostgreSQL load and statement time
   - Redis memory, hit ratio, evictions, and blocked clients
   - Application error/log volume
   - Deployment, restart, OOM, and health transition markers

3. **Route family table**
   - Method and approved route family
   - Request volume
   - p50, p95, and p99 latency
   - Error percentage
   - Upstream connect, header, and response timing
   - Change from the preceding comparison window

4. **Database and cache panels**
   - PostgreSQL connection pressure, commits/rollbacks, deadlocks, tuple activity, cache hit indicators, WAL and checkpoint activity
   - Top PostgreSQL query fingerprints after `pg_stat_statements` is enabled
   - Redis memory utilization, connected and blocked clients, hit/miss ratio, evictions, expirations, command rates, persistence health, and slow-log count

5. **Investigation feed**
   - Ranked anomalies with confidence wording such as “correlates with” rather than “caused by”
   - Links to the matching time range in logs, issues, deployment details, database health, and container history

### Service Performance tab

The service view scopes the same model to one service. It presents the route families, containers, logs, deployments, and dependencies associated with that service. The primary workflow is:

1. Select a slow time range.
2. Compare route latency with resource and dependency pressure.
3. Open the related logs, deployment, database fingerprint, or runtime event.
4. Record or link an issue when investigation is needed.

### Empty, stale, and degraded states

- A collector that has never run explains the required setup and permissions.
- A stale collector shows its last successful sample and last error without hiding older data.
- `pg_stat_statements` panels clearly state that statement-level data requires a maintenance-window setup.
- Unknown or sensitive HTTP paths appear as `other`, never as a raw path.
- Missing signals do not prevent the rest of the page from rendering.

## Collection architecture

### Nginx HTTP timing collector

Nginx emits a dedicated, PII-minimized JSON timing log to a host file mounted read-only into Touch Grass. The record includes only:

- Timestamp and generated request ID
- HTTP method
- Status and upstream status
- Approved route family
- Request duration
- Upstream connect, header, and response durations
- Response and request byte counts
- Upstream address identifier
- Connection request count

The format must not include the raw request line, raw URI, query string, authorization data, cookies, referrer, user agent, or client IP. Route families are produced by an allowlisted Nginx `map` or a bounded Touch Grass normalization table. Unknown and sensitive paths collapse to `other`.

Touch Grass tails this file using a persisted file identity and offset. Rotation handling recognizes inode changes and resumes without re-reading the full file. The collector parses and aggregates records in memory before storing minute buckets.

This route-family method is deliberately approximate. Perfect framework route-template identification is unavailable without application help.

### Docker collector

Unify existing resource and fleet collection so a managed container is sampled once per interval. Continue recording CPU, memory, network, and block I/O. Add Docker lifecycle events for:

- Start, stop, die, restart
- OOM
- Health status changes
- Container recreation

Use Docker Compose labels and configured service identity instead of ephemeral container IDs. Reset counter baselines when a container is recreated. Suppress expected blue-green replacement noise when the deployment controller can identify it.

Persist collector cursors and loss counters. A restart must not silently reset the apparent history or hide dropped logs/events.

### PostgreSQL collector

The zero-restart collector reads aggregate statistics from:

- `pg_stat_database`
- Aggregate `pg_stat_activity`
- `pg_stat_user_tables`
- `pg_stat_user_indexes`
- `pg_stat_bgwriter`
- `pg_stat_wal` when available

It stores counts, rates, and deltas. It does not store active SQL text, parameters, application names that can identify a user, or connection addresses.

`pg_stat_statements` is a separate phase because enabling it requires `shared_preload_libraries`, a database restart, and `CREATE EXTENSION`. After controlled enablement, Touch Grass stores the query ID plus execution, block, row, and call deltas. Raw SQL text is omitted in the initial release. A later privacy review may allow normalized and tightly truncated fingerprints.

The collector detects statistics resets and counter wrap/restart conditions so rates are not calculated across invalid baselines.

### Redis collector

Use read-only administrative access to collect safe values from:

- `INFO clients`
- `INFO memory`
- `INFO stats`
- `INFO persistence`
- `INFO cpu`
- `INFO commandstats`
- `INFO keyspace`
- `SLOWLOG LEN`

Do not use `MONITOR`, `KEYS`, unrestricted `CLIENT LIST`, or raw slow-log arguments. Do not store key names or values. Command statistics are bounded to a known command set plus `other`.

### Existing logs

Continue using existing Docker and application-log ingestion for correlation and drill-down. Add per-service log-volume and severity buckets so the Performance page does not scan raw log lines to draw timelines.

Before increasing collection, fix log durability and cost:

- Persist Docker log cursors and loss counters.
- Avoid re-reading only the latest fixed-size tail as the primary cursor strategy.
- Enforce Docker log rotation.
- Add byte-based retention as well as age-based retention.
- Avoid storing known high-volume routine messages when an aggregate counter is sufficient.

### Optional bounded profiler

This is gated behind a separate implementation decision and feasibility test. If delivered, it must:

- Be administrator-triggered and disabled by default.
- Run for a short fixed duration with one active capture per service.
- Avoid the Node Inspector and public debug ports.
- Store encrypted or permission-restricted artifacts outside SQLite.
- Automatically delete artifacts after a short retention period.
- Record an audit event for start, completion, download, and deletion.
- Refuse capture when load, disk, or permission checks fail.

The UI must label profiler output as sampled evidence, not a guaranteed source of causality.

## Storage design

### Existing SQLite hardening

Before adding new data classes:

- Add byte limits per data class and a total database budget.
- Delete expired rows in bounded batches rather than one large transaction.
- Establish WAL checkpoint thresholds and expose checkpoint health.
- Provide an online SQLite backup procedure and recovery test.
- Keep large log and profiler artifacts outside SQLite.
- Verify the current single-connection configuration under concurrent collection and dashboard reads; increase connections only if WAL-safe load tests justify it.
- Add collector freshness, parse-error, dropped-record, and persistence-error metrics to Touch Grass itself.

### New aggregate tables

A new migration introduces bounded bucket tables:

- `http_metric_buckets`
- `postgres_metric_buckets`
- `postgres_statement_buckets`
- `redis_metric_buckets`
- `log_volume_buckets`
- `container_events`
- `collector_cursors`

Each metric bucket uses a one-minute UTC start time, service ID, environment, collector source, and a strictly bounded set of dimensions. Unique indexes make upserts idempotent. The store layer owns all SQL.

HTTP latency uses mergeable fixed histogram counters with these initial millisecond boundaries:

`10, 25, 50, 100, 250, 500, 1000, 2500, 5000, +Inf`

Histogram counts are stored as integer columns to keep reads stable and aggregation explicit. Bucket records also store request count, error count, total duration, and upstream timing totals.

Default retention:

- Minute buckets: 7 days
- Hourly rollups: 30 days
- Container events and collector errors: 30 days
- Raw logs: existing policy, amended with byte caps
- Optional profiler artifacts: no more than 24 hours unless explicitly retained for an active incident

Retention remains configurable within hard safety ceilings.

## API and service boundaries

Follow Touch Grass layering:

- HTTP handlers validate parameters, enforce permissions, and serialize responses.
- A performance service performs range validation, rollup selection, comparison-window calculation, and anomaly ranking.
- Collector services normalize and aggregate signals.
- Store packages own bucket upserts, range queries, rollups, cursors, and retention SQL.

Initial read APIs:

- `GET /api/performance/summary`
- `GET /api/performance/timeline`
- `GET /api/performance/routes`
- `GET /api/performance/postgres`
- `GET /api/performance/redis`
- `GET /api/services/:id/performance/*`
- `GET /api/collectors/status`

All endpoints require existing authenticated admin access. Time ranges, service IDs, route-family filters, sort fields, page size, and maximum bucket count are validated and capped server-side. The API returns aggregates only.

## Privacy and security

- New collectors use least-privilege read-only credentials.
- Database and Redis credentials remain in the existing secret mechanism, never Touch Grass UI or logs.
- Route families come from an allowlist; unrecognized paths become `other`.
- No raw SQL, SQL parameters, Redis keys, request bodies, query strings, cookies, tokens, IPs, email addresses, or user IDs enter the performance tables.
- Collector parsing failures log only source, offset, and a safe error category—not the rejected payload.
- New setup actions and any profile action are audited.
- Profile artifacts, if introduced, are permission-restricted, encrypted where practical, and stored outside SQLite.
- Touch Grass remains bound and exposed according to the existing control-plane security model.

## Confirmed Ticketnation improvements

These changes are independent of the collectors and are included because current code and production statistics already identify concrete waste.

### Authorization lookup indexes

Every authenticated request currently loads user, organization, roles, and permissions. Production statistics show the `UserRole` join table repeatedly scanned without a supporting user lookup index.

Add reviewed constraints and indexes:

- `UserRole`: uniqueness for `(userId, roleId)` and an index supporting role-side access where required.
- `RolePermission`: equivalent uniqueness and indexes for its actual access paths.

Validate duplicates before creating uniqueness constraints. Use `EXPLAIN (ANALYZE, BUFFERS)` before and after the migration, then compare authorization-request latency in Touch Grass.

### Account order diagnostics

The account-order list performs its required total query plus up to five serial diagnostic count queries and verbose per-order logging. Remove production diagnostics from the request path. Keep only the required count and page query, with focused error logging.

### Logging hygiene and volume

- Remove per-request CORS information logs.
- Reduce repetitive PublicEvents, AccountOrders, MainGateway, and PayMongo routine logs.
- Remove or redact email addresses, push tokens, complete order payloads, Redis URLs, raw external URLs, and unnecessary stacks.
- Preserve structured warnings and errors that support action.

### Error reporting

Normalize Sentry integration to one exception pipeline. The current interceptor is registered in more than one place while initialization is unclear. The change must establish one initialization path, one capture path, environment filtering, and PII-safe context.

### Dependency health

Replace the static `/health` response with bounded checks for the API process, PostgreSQL, and Redis. Expose readiness separately from liveness so a temporary dependency failure does not create a restart loop.

### Secret and build-context remediation

Credential-shaped environment files are tracked or included in the API build context. Before expanding operational access:

- Determine which credentials remain active and rotate them.
- Remove secret-bearing files from version control and Docker build context.
- Review image layers and repository history for required remediation.
- Replace tracked values with templates and secret-store references.

No secret values are copied into this document or Touch Grass.

## Delivery phases

### Phase 0 — Security and storage safety

- Rotate and remediate tracked Ticketnation credentials.
- Add SQLite byte budgets, bounded retention, WAL checkpoint policy, backup and recovery procedure.
- Persist collector cursors and data-loss counters.
- Configure Docker log rotation and verify host mount semantics.
- Confirm read-only Nginx, PostgreSQL, Redis, and Docker access.

### Phase 1 — Confirmed Ticketnation fixes

- Add authorization-table constraints and indexes after duplicate checks.
- Remove account-order diagnostic queries.
- Reduce noisy and sensitive logging.
- Normalize Sentry handling.
- Add liveness and readiness checks.
- Measure each change against a pre-change baseline.

### Phase 2 — Native collectors

- Add dedicated Nginx timing log and tailer.
- Unify Docker sampling and add lifecycle events.
- Add zero-restart PostgreSQL aggregate collection.
- Add Redis statistics collection.
- Add log-volume aggregation.

### Phase 3 — Performance storage, API, and UI

- Add aggregate bucket migration and store methods.
- Add performance and collector-status services and APIs.
- Build global Performance view and service Performance tab.
- Add correlation markers and drill-down links.
- Add freshness, loss, storage, and collector-overhead warnings.

### Phase 4 — PostgreSQL query fingerprints

- Schedule the required database restart.
- Enable and validate `pg_stat_statements` with conservative settings.
- Add statement-delta collection and top-fingerprint UI.
- Verify collection overhead and statement reset handling.

### Phase 5 — Optional profiling feasibility

- Prove a bounded approach on the production ARM64 environment.
- Perform privacy, security, and load tests.
- Ship only if the benefit exceeds the operational and security cost.

### Phase 6 — Production validation

- Roll out collectors one source at a time.
- Compare Touch Grass CPU, memory, SQLite latency, and disk growth before and after each source.
- Simulate rotation, restarts, collector failure, counter resets, and disk pressure.
- Validate the end-to-end investigation workflow on a known slow request.
- Document rollback and incident runbooks.

## Acceptance criteria

- Ticketnation has no new OpenTelemetry, tracing, APM, or Touch Grass SDK dependency.
- Stopping Touch Grass has no effect on Ticketnation request handling.
- Operators can view request volume, error rate, and p50/p95/p99 latency by approved route family.
- A single time range can align HTTP, CPU, memory, PostgreSQL, Redis, logs, lifecycle events, and deployments.
- PostgreSQL statement fingerprints are visible after the extension phase without storing raw SQL.
- Redis memory, cache efficiency, eviction, blocked-client, command, and persistence trends are visible.
- Unknown or sensitive routes are stored only as `other`; privacy tests prove excluded fields do not persist.
- Retention keeps SQLite and external artifacts within configured byte ceilings.
- Dashboard reads and collector writes remain responsive during representative load.
- Collector failures and dropped data are visible and recoverable without affecting customer traffic.
- Authorization lookup plans use the intended index and materially reduce scanned rows.
- Account-order listing executes only the required pagination queries.
- Liveness and readiness accurately distinguish process health from dependency availability.
- Touch Grass collection overhead remains below an internal target of 5% CPU under representative load, with bounded memory and disk usage.
- Optional profiling remains disabled by default and exposes no debug port.
- Go tests, frontend tests, linting, static analysis, and Ticketnation validation pass before rollout.

## Test strategy

### Collector tests

- Golden parsing tests for Nginx, PostgreSQL, Redis, and Docker inputs.
- Privacy fixtures containing query strings, emails, tokens, IPs, and SQL values prove they are dropped.
- Rotation, truncation, restart, counter-reset, malformed-record, and permission-denied cases.
- Histogram merge and percentile correctness tests.
- Idempotent cursor and bucket-upsert tests.

### Store and API tests

- Migration and rollback coverage where supported.
- Unique bucket keys, rollup correctness, range caps, pagination, and comparison windows.
- Bounded retention and byte-budget enforcement.
- Authorization and invalid-filter tests.
- Concurrent collector-write and dashboard-read load tests.

### UI tests

- Complete, empty, stale, partial, error, and lost-data states.
- Correlation marker and drill-down behavior.
- Accessible labels, keyboard operation, color contrast, and non-color status indicators.
- Responsive behavior on supported admin viewport sizes.

### Ticketnation validation

- Duplicate audit before authorization constraints.
- Before/after query plans and latency comparison.
- Account-order query-count regression test.
- Log redaction and volume tests.
- Liveness/readiness dependency-state tests.
- Sentry single-capture regression test.

## Rollback

- Every collector has an independent enable flag and can be disabled without schema rollback.
- Nginx keeps serving if the timing log cannot be written; logging failure is alerted separately.
- The UI tolerates missing buckets and collector gaps.
- Ticketnation performance changes ship in separate commits and migrations with explicit rollback steps.
- `pg_stat_statements` is enabled only in a scheduled change with a database rollback procedure.
- Optional profiling has a global kill switch and is absent from the default production path.

## Documentation updates required during implementation

- Add an ADR describing agentless performance diagnostics and its no-tracing boundary.
- Update `docs/ARCHITECTURE.md` with collectors, storage budgets, and the Performance view.
- Update deployment documentation with mounts, read-only credentials, rotation, and rollback.
- Add operator runbooks for collector gaps, disk pressure, SQLite recovery, PostgreSQL statistics resets, Redis pressure, and performance investigations.
- Document the route-family allowlist and privacy review procedure.

## Open implementation decisions

These decisions are intentionally deferred to implementation planning, where repository details and benchmarks can resolve them without changing the approved architecture:

- Exact SQLite byte ceilings and per-table allocation.
- Whether minute-to-hour rollups are synchronous with retention or handled by a separate bounded job.
- The initial Nginx route-family allowlist and ownership process.
- The safest ARM64 profiling mechanism, if profiling proceeds at all.
- Whether PostgreSQL query text remains permanently excluded or a future privacy-reviewed normalized form is useful.
