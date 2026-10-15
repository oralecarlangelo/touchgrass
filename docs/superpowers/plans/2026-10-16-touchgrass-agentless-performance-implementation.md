# Touch Grass Agentless Performance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a bounded, privacy-safe Performance workspace in Touch Grass using native Nginx, Docker, PostgreSQL, Redis, and log signals without adding an SDK or OpenTelemetry to Ticketnation.

**Architecture:** Source adapters parse infrastructure data into one-minute aggregate models. Domain services schedule collection and answer bounded performance queries; SQLite stores idempotent buckets and persisted cursors; the HTTP and React layers expose global and service-scoped investigation views. Every collector is independently configured and fail-open.

**Tech Stack:** Go 1.26, stdlib `net/http`, SQLite via `modernc.org/sqlite`, Docker Engine API, React 19, TypeScript, Vite, Recharts, Tailwind/shadcn.

**Spec:** `docs/superpowers/specs/2026-10-16-agentless-performance-observability-design.md`

## Global Constraints

- Do not add OpenTelemetry, tracing, an APM agent, or a Touch Grass SDK to Ticketnation.
- Touch Grass must remain outside Ticketnation's request path and must fail open.
- Persist aggregates only; never persist raw URIs, query strings, IPs, headers, cookies, bodies, SQL text/parameters, Redis keys/values, email addresses, or user IDs.
- Keep package direction `http -> service -> store`; SQL stays in `internal/store` and source integrations stay behind narrow interfaces.
- Use fixed HTTP latency buckets `10, 25, 50, 100, 250, 500, 1000, 2500, 5000, +Inf` milliseconds.
- Default retention is 7 days for minute buckets, 30 days for hourly rollups and runtime events, with enforced byte ceilings.
- Every time range, dimension, page size, bucket count, and sort field is validated and capped server-side.
- Every collector has an independent enable condition and reports freshness, parse failures, persistence failures, and dropped records.
- Optional process profiling is excluded from this plan; it requires a separate ARM64 feasibility and security decision.

## Review Focus

- Log rotation or process restart must resume from a persisted file identity/offset without duplicate or silently lost buckets; Task 3 pins this behavior.
- PostgreSQL/Redis counter resets must start a new baseline instead of producing negative or enormous rates; Task 4 pins this behavior.
- Sensitive or unknown HTTP paths must collapse to `other` even when malformed or percent-encoded; Task 3 pins this behavior.
- A stale or failed collector must leave historical dashboards usable and expose its last success/error; Tasks 2 and 6 pin this behavior.
- Concurrent collection, retention, and dashboard reads must remain responsive within the SQLite budget; Tasks 2 and 9 pin this behavior.

## Cross-repository execution order

1. Complete Touch Grass Tasks 1–2 and Ticketnation Tasks 1–6 first; these establish safety, reduce known waste, and create no new telemetry load.
2. Complete Touch Grass Tasks 3–5 alongside Ticketnation's PostgreSQL-statistics and Nginx-source tasks using local fixtures before enabling any production source.
3. Complete Touch Grass Tasks 6–8 after aggregate contracts stabilize.
4. Run both repositories' rollout/evidence tasks one source at a time; do not enable every collector in one deployment.

---

### Task 1: Record the architecture boundary

**Files:**
- Create: `docs/adr/0011-agentless-performance-diagnostics.md`
- Modify: `docs/adr/README.md`
- Modify: `docs/ARCHITECTURE.md`

**Interfaces:**
- Consumes: approved design spec.
- Produces: binding architecture rules for later tasks: no request-path dependency, no raw sensitive data, aggregate storage, source adapters, and independent collector failure.

- [ ] **Step 1: Add the ADR**

Document the selected native-collector approach, rejected proxy/SDK approaches, privacy boundary, storage model, and profiling non-goal. Mark ADR-0009's logs-only boundary as unchanged.

- [ ] **Step 2: Update architecture documentation**

Add the Performance service, source adapters, aggregate tables, scheduler, and global/service UI routes to `docs/ARCHITECTURE.md`; add ADR 0011 to the index.

- [ ] **Step 3: Verify documentation consistency**

Run: `rg -n "APM|OpenTelemetry|raw URI|Performance" docs/ARCHITECTURE.md docs/adr/0009-otel-log-support.md docs/adr/0011-agentless-performance-diagnostics.md`

Expected: Performance is described as agentless aggregation; ADR-0009 still says no spans/metrics/APM; no section claims source-level causality.

- [ ] **Step 4: Commit**

```bash
git add docs/ARCHITECTURE.md docs/adr/README.md docs/adr/0011-agentless-performance-diagnostics.md
git commit -m "docs(observability): record agentless performance architecture"
```

### Task 2: Harden SQLite maintenance and collector state

**Files:**
- Create: `migrations/0016_storage_safety.sql`
- Create: `internal/model/maintenance.go`
- Create: `internal/store/maintenance.go`
- Create: `internal/store/maintenance_test.go`
- Create: `internal/service/maintenance.go`
- Create: `internal/service/maintenance_test.go`
- Modify: `internal/store/db.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/touchgrass/main.go`

**Interfaces:**
- Produces: `model.CollectorCursor`, `store.CursorStore.Get(ctx, source)`, `store.CursorStore.Upsert(ctx, cursor)`, `store.MaintenanceStore.Run(ctx, model.MaintenancePolicy)`, `store.DB.Backup(ctx, destination)`, and `service.Maintenance.Run(ctx)`.
- Produces configuration fields: `MaintenanceInterval` (default `5m`), `SQLiteMaxBytes` (default 1 GiB), `SQLiteBackupDir` (default `./backups/sqlite`), `RetentionPerformance` (default `168h`), and `RetentionPerformanceHourly` (default `720h`).

- [ ] **Step 1: Write failing store tests**

Add tests named `TestCursorStoreRoundTrip`, `TestMaintenanceDeletesInBatches`, `TestMaintenanceCheckpointsWAL`, `TestMaintenanceReportsOverBudget`, and `TestSQLiteBackupIsConsistent`. Assert cursor replacement is idempotent, one pass deletes no more than the configured batch, backup integrity is `ok`, and over-budget state is reported without unbounded deletion.

- [ ] **Step 2: Run the store tests and verify failure**

Run: `go test ./internal/store -run 'Test(CursorStore|Maintenance|SQLiteBackup)'`

Expected: FAIL because the migration, models, and stores do not exist.

- [ ] **Step 3: Add migration and store interfaces**

`0016_storage_safety.sql` creates `collector_cursors(source PRIMARY KEY, identity, offset, last_recorded_at, last_success_at, last_error, dropped_records, updated_at)` and supporting maintenance indexes. Implement:

```go
func (s *CursorStore) Get(ctx context.Context, source string) (model.CollectorCursor, error)
func (s *CursorStore) Upsert(ctx context.Context, cursor model.CollectorCursor) error
func (s *MaintenanceStore) Run(ctx context.Context, policy model.MaintenancePolicy) (model.MaintenanceResult, error)
func (db *DB) Backup(ctx context.Context, destination string) error
```

Use batches of at most 1,000 rows with `DELETE ... WHERE id IN (SELECT id ... LIMIT ?)` operations, `PRAGMA wal_checkpoint(PASSIVE)`, and a consistent SQLite snapshot. Enforce default class ceilings of 512 MiB logs, 256 MiB performance buckets, 128 MiB issues/occurrences, and 64 MiB administrative history inside the 1 GiB total. Reject backup paths outside the configured backup directory at the service boundary.

- [ ] **Step 4: Write and run failing service/config tests**

Test invalid durations, non-positive byte limits, unsafe backup paths, stale maintenance status, cancellation, and a collector error that does not stop the loop.

Run: `go test ./internal/config ./internal/service -run 'Test(Load.*Maintenance|Maintenance)'`

Expected: FAIL before configuration and service wiring exist.

- [ ] **Step 5: Implement maintenance scheduling and wiring**

Add `service.MaintenanceConfig` with stores, policy, interval, backup directory, clock, and logger. `Run(ctx)` performs bounded retention/checkpoint work and periodic backup without blocking application boot. Wire it through `backgroundLoops` in `cmd/touchgrass/main.go`.

- [ ] **Step 6: Verify task**

Run: `go test ./internal/store ./internal/config ./internal/service ./cmd/touchgrass && go test -race ./internal/store ./internal/service`

Expected: PASS, including concurrent read/write maintenance coverage.

- [ ] **Step 7: Commit**

```bash
git add migrations/0016_storage_safety.sql internal/model/maintenance.go internal/store/maintenance.go internal/store/maintenance_test.go internal/store/db.go internal/service/maintenance.go internal/service/maintenance_test.go internal/config/config.go internal/config/config_test.go cmd/touchgrass/main.go
git commit -m "feat(storage): bound retention and persist collector state"
```

### Task 3: Collect privacy-safe Nginx timing aggregates

**Files:**
- Create: `migrations/0017_performance_buckets.sql`
- Create: `internal/model/performance.go`
- Create: `internal/nginxlog/parser.go`
- Create: `internal/nginxlog/parser_test.go`
- Create: `internal/nginxlog/tailer.go`
- Create: `internal/nginxlog/tailer_test.go`
- Create: `internal/nginxlog/testdata/timing.jsonl`
- Create: `internal/store/performance.go`
- Create: `internal/store/performance_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Consumes: persisted cursor API from Task 2.
- Produces: `nginxlog.Parser.Parse(line []byte) (model.HTTPObservation, error)`, `nginxlog.Tailer.Read(ctx) ([]model.HTTPObservation, model.CollectorCursor, error)`, and `store.PerformanceStore.UpsertHTTP(ctx, bucket)`.
- Produces models `HTTPObservation`, `HTTPHistogram`, `HTTPMetricBucket`, `CollectorStatus`, and `PerformanceRange`.

- [ ] **Step 1: Write failing parser/privacy tests**

Cover valid JSON, malformed timing numbers, partial upstream timings, percent-encoded email/token paths, route allowlist matches, unknown routes, query strings, rotation identity changes, truncation, and restart from a persisted offset. Assert output contains only method, route family, status, timing, byte counts, upstream identifier, and timestamp; all sensitive/unknown paths become `other`.

- [ ] **Step 2: Run parser tests and verify failure**

Run: `go test ./internal/nginxlog`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement the parser and tailer**

Use an explicit JSON struct with no fields for raw request metadata. Route normalization is an ordered allowlist; reject control characters and malformed escaping before matching. Tail by file identity plus byte offset, and emit a loss counter when rotation removes unread data.

- [ ] **Step 4: Write failing histogram/store tests**

Assert exact boundary placement for `10,25,50,100,250,500,1000,2500,5000,+Inf`, idempotent minute-bucket upsert, histogram merge, hourly rollup, empty-array reads, and bounded retention.

- [ ] **Step 5: Add aggregate schema and store**

`0017_performance_buckets.sql` creates `http_metric_buckets`, `postgres_metric_buckets`, `postgres_statement_buckets`, `redis_metric_buckets`, `log_volume_buckets`, `performance_hourly_rollups`, `container_events`, and their time/service indexes. Store histogram counters as integer columns and use conflict-safe additive upserts keyed by minute, service, environment, method, route family, upstream, and status class.

- [ ] **Step 6: Add Nginx configuration fields**

Add `NginxTimingLog`, `PerformanceServiceID`, and `PerformanceInterval` (default `30s`); an empty timing-log path disables only this collector. Require an existing `PerformanceServiceID` when the timing log or database/cache collector is enabled, validate that the timing-log path is absolute, and accept only the route-family allowlist embedded in the parser (`health`, `auth`, `public_events`, `account_orders`, `checkout`, `callbacks`, `admin`, `organization`, `static`, `other`).

- [ ] **Step 7: Verify task**

Run: `go test ./internal/nginxlog ./internal/store ./internal/config`

Expected: PASS with no raw path or query value in fixture snapshots.

- [ ] **Step 8: Commit**

```bash
git add migrations/0017_performance_buckets.sql internal/model/performance.go internal/nginxlog internal/store/performance.go internal/store/performance_test.go internal/config/config.go internal/config/config_test.go
git commit -m "feat(performance): collect nginx latency buckets"
```

### Task 4: Collect PostgreSQL and Redis statistics

**Files:**
- Create: `internal/dbstats/runner.go`
- Create: `internal/dbstats/postgres.go`
- Create: `internal/dbstats/postgres_test.go`
- Create: `internal/dbstats/redis.go`
- Create: `internal/dbstats/redis_test.go`
- Create: `internal/service/performance_collector.go`
- Create: `internal/service/performance_collector_test.go`
- Modify: `internal/store/performance.go`
- Modify: `internal/store/performance_test.go`
- Modify: `cmd/touchgrass/main.go`

**Interfaces:**
- Consumes: performance bucket models/store from Task 3.
- Produces: `dbstats.Postgres.Collect(ctx) (model.PostgresSnapshot, error)`, `dbstats.Redis.Collect(ctx) (model.RedisSnapshot, error)`, and `service.PerformanceCollector.Run(ctx)`.
- `PerformanceCollector` consumes narrow `HTTPSource`, `PostgresSource`, `RedisSource`, `LogVolumeSource`, and `PerformanceWriter` interfaces.

- [ ] **Step 1: Write failing PostgreSQL parser tests**

Use fixture output for `pg_stat_database`, aggregate `pg_stat_activity`, table/index statistics, bgwriter, WAL, and optional `pg_stat_statements`. Assert raw query text is ignored, unsupported views degrade per source, and a statistics reset produces a new baseline rather than a negative rate.

- [ ] **Step 2: Write failing Redis parser tests**

Cover `INFO clients,memory,stats,persistence,cpu,commandstats,keyspace` and `SLOWLOG LEN`. Assert unknown commands aggregate to `other`, key names/values never appear, and reset counters start a new baseline.

- [ ] **Step 3: Run source tests and verify failure**

Run: `go test ./internal/dbstats`

Expected: FAIL because source adapters do not exist.

- [ ] **Step 4: Implement source adapters**

Use injected command runners and fixed `docker exec` argument lists. PostgreSQL emits tab-separated numeric rows only; Redis reads named `INFO` sections. Apply source timeouts and return partial snapshots with safe error categories.

- [ ] **Step 5: Write failing collector tests**

Test independent source failure, stale-status retention, persisted last error/success, cancellation, fixed one-minute alignment, idempotent retry, and bounded dropped-record accounting.

- [ ] **Step 6: Implement collector orchestration and wire it**

`Run(ctx)` samples enabled sources every `PerformanceInterval`, computes deltas only against valid baselines, upserts aggregates, and updates `collector_cursors`. One source failure must not block the others or return from the loop.

- [ ] **Step 7: Verify task**

Run: `go test ./internal/dbstats ./internal/service ./internal/store && go test -race ./internal/service`

Expected: PASS; no command includes raw query text or Redis key access.

- [ ] **Step 8: Commit**

```bash
git add internal/dbstats internal/service/performance_collector.go internal/service/performance_collector_test.go internal/store/performance.go internal/store/performance_test.go cmd/touchgrass/main.go
git commit -m "feat(performance): collect postgres and redis pressure"
```

### Task 5: Remove duplicate Docker sampling and persist runtime/log signals

**Files:**
- Create: `internal/docker/events.go`
- Create: `internal/docker/events_test.go`
- Modify: `internal/docker/client.go`
- Modify: `internal/service/resources.go`
- Modify: `internal/service/resources_test.go`
- Modify: `internal/service/fleet.go`
- Modify: `internal/service/fleet_test.go`
- Modify: `internal/service/system.go`
- Modify: `internal/service/system_test.go`
- Modify: `internal/service/log_collector.go`
- Modify: `internal/service/log_collector_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/store/performance.go`
- Modify: `internal/store/performance_test.go`
- Modify: `cmd/touchgrass/main.go`

**Interfaces:**
- Consumes: cursor and performance stores from Tasks 2–3.
- Produces: `docker.EventStream(ctx, since)`, `model.ContainerEvent`, one Docker `Stats` call per container/sample interval, persistent log cursors, minute `LogVolumeBucket` rows, and `HostRoot` configuration (default `/`, `/hostfs` in the container deployment).

- [ ] **Step 1: Write failing sampler tests**

Assert one `Stats` call per container per cycle supplies both service metrics and fleet rows, recreated containers reset baselines, Compose labels determine ownership, and unmanaged containers still appear in Fleet.

- [ ] **Step 2: Refactor sampling minimally**

Collect `docker.Stats` once into a per-cycle map and pass values to focused `recordMetric` and `recordFleetSample` methods. Remove the second stats call without changing existing API output.

- [ ] **Step 3: Write failing event/log durability tests**

Cover start/die/restart/OOM/health events, expected blue-green recreation suppression, event cursor restart, Docker log restart/rotation, duplicate inclusive timestamps, truncation counters, and severity-volume aggregation.

- [ ] **Step 4: Implement events and persistent log state**

Store bounded `container_events`, update collector cursors after successful persistence, and aggregate log count by minute/service/severity. Keep raw-log search behavior unchanged and make cursor persistence failure visible without stopping collection.

- [ ] **Step 5: Correct containerized host-disk reporting**

Add `HostRoot` to config and pass it to System/Fleet disk sampling. Direct binary installs default to `/`; the container deployment mounts the host root read-only at `/hostfs`. When the path is unavailable, report disk as unavailable rather than labeling the Touch Grass container filesystem as host disk.

- [ ] **Step 6: Wire the event loop and verify task**

Register the bounded Docker event loop in `backgroundLoops`; cancellation must close the stream and wait for the goroutine.

Run: `go test ./internal/docker ./internal/service ./internal/store && go test -race ./internal/docker ./internal/service`

Expected: PASS; the mock verifies exactly one stats call per container/cycle.

- [ ] **Step 7: Commit**

```bash
git add internal/docker/events.go internal/docker/events_test.go internal/docker/client.go internal/service/resources.go internal/service/resources_test.go internal/service/fleet.go internal/service/fleet_test.go internal/service/system.go internal/service/system_test.go internal/service/log_collector.go internal/service/log_collector_test.go internal/config/config.go internal/config/config_test.go internal/store/performance.go internal/store/performance_test.go cmd/touchgrass/main.go
git commit -m "feat(signals): persist runtime events and log volume"
```

### Task 6: Add bounded Performance query APIs

**Files:**
- Create: `internal/service/performance.go`
- Create: `internal/service/performance_test.go`
- Create: `internal/http/performance.go`
- Create: `internal/http/performance_test.go`
- Create: `internal/service/performance_alerts.go`
- Create: `internal/service/performance_alerts_test.go`
- Modify: `internal/http/server.go`
- Modify: `internal/http/harness_test.go`
- Modify: `internal/model/metrics.go`
- Modify: `internal/store/rules.go`
- Modify: `cmd/touchgrass/main.go`
- Modify: `docs-site/openapi.yaml`
- Modify: `internal/http/docs_test.go`
- Modify: `web/src/components/RulesScreen.tsx`

**Interfaces:**
- Produces: `Performance.Summary(ctx, query)`, `Timeline(ctx, query)`, `Routes(ctx, query)`, `Postgres(ctx, query)`, `Redis(ctx, query)`, and `CollectorStatus(ctx)`.
- HTTP routes: `GET /api/performance/{summary,timeline,routes,postgres,redis}`, `GET /api/collectors/status`, and service-scoped equivalents below `/api/services/{id}/performance/`.
- Produces alert metrics `http_p95_ms`, `http_error_rate`, `postgres_connection_pct`, and `redis_evictions` through the existing rule/notification model.

- [ ] **Step 1: Write failing service tests**

Test default/maximum ranges, automatic minute/hour rollup selection, comparison-window calculations, percentile extraction from fixed histograms, stale collectors, partial data, unknown service IDs, invalid sort fields, and no-data results as empty slices.

- [ ] **Step 2: Implement the query service**

Define `model.PerformanceQuery` with service ID, UTC start/end, resolution, route family, sort, and limit. Cap minute queries at 7 days, hourly queries at 30 days, points at 1500, and rows at 100. Rank anomalies with neutral “correlates with” wording only.

- [ ] **Step 3: Write failing handler/auth tests**

Cover valid global/service requests, invalid RFC3339/range/filter/limit, unauthenticated requests, viewer read access, unknown services, stale collectors, and internal errors that do not leak SQL/source output.

- [ ] **Step 4: Implement routes and OpenAPI contract**

Handlers parse/validate transport input, call `service.Performance`, and return JSON arrays rather than `null`. Register all routes in `server.go`, wire the service in `cmd/touchgrass/main.go`, and update route-parity documentation.

- [ ] **Step 5: Add performance alert evaluation**

Evaluate only completed minute buckets, reuse the existing sustained-breach state machine, and emit at most one notification per breach episode. Extend Rules UI options with the four bounded metrics; stale/missing source data must not be interpreted as zero or recovery.

- [ ] **Step 6: Verify task**

Run: `go test ./internal/service ./internal/http ./docs-site`

Expected: PASS, including route parity.

- [ ] **Step 7: Commit**

```bash
git add internal/service/performance.go internal/service/performance_test.go internal/service/performance_alerts.go internal/service/performance_alerts_test.go internal/http/performance.go internal/http/performance_test.go internal/http/server.go internal/http/harness_test.go internal/model/metrics.go internal/store/rules.go cmd/touchgrass/main.go docs-site/openapi.yaml internal/http/docs_test.go web/src/components/RulesScreen.tsx
git commit -m "feat(api): expose bounded performance investigations"
```

### Task 7: Build the global Performance workspace

**Files:**
- Create: `web/src/components/PerformanceScreen.tsx`
- Create: `web/src/components/performance/PerformanceSummary.tsx`
- Create: `web/src/components/performance/CorrelationTimeline.tsx`
- Create: `web/src/components/performance/RouteTable.tsx`
- Create: `web/src/components/performance/DependencyPanels.tsx`
- Create: `web/src/components/performance/CollectorStatus.tsx`
- Create: `web/src/lib/performance.ts`
- Create: `web/src/lib/performance.test.ts`
- Modify: `web/package.json`
- Modify: `web/package-lock.json`
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/lib/views.ts`
- Modify: `web/src/lib/hash.ts`
- Modify: `web/src/App.tsx`

**Interfaces:**
- Consumes: Task 6 APIs.
- Produces: `performance` top-level view under Signals and pure helpers `mergeTimelineSeries`, `formatPercentile`, and `collectorState`.

- [ ] **Step 1: Add Vitest and write failing pure-helper tests**

Add `vitest` as a dev dependency and `"test": "vitest run"` to `web/package.json`, then test mismatched bucket timestamps, null sources, stale collectors, invalid timestamps, deploy/event marker ordering, and percentile formatting in `web/src/lib/performance.test.ts`.

- [ ] **Step 2: Add typed API clients**

Define exact wire types for summary, timeline points, route rows, dependency rows, runtime markers, and collector status. Fetch functions accept `PerformanceFilters` and encode only validated UI options.

- [ ] **Step 3: Build the screen states and visual hierarchy**

Implement summary cards, one shared correlation timeline, compact route table, PostgreSQL/Redis panels, collector status, and an investigation feed. Include loading skeletons, empty setup guidance, partial/stale banners, error retry, keyboard-accessible controls, and non-color status labels.

- [ ] **Step 4: Add navigation**

Add `performance` to `ViewId`, hash parsing, Signals navigation, and `App.tsx` dispatch. Preserve existing hashes and viewer read access.

- [ ] **Step 5: Verify task**

Run: `npm run verify --prefix web && npm run build --prefix web`

Expected: TypeScript, ESLint, and production build PASS.

- [ ] **Step 6: Commit**

```bash
git add web/package.json web/package-lock.json web/src/components/PerformanceScreen.tsx web/src/components/performance web/src/lib/performance.ts web/src/lib/performance.test.ts web/src/lib/api.ts web/src/lib/views.ts web/src/lib/hash.ts web/src/App.tsx
git commit -m "feat(web): add performance investigation workspace"
```

### Task 8: Expand the service Metrics tab into Performance

**Files:**
- Create: `web/src/components/service/PerformanceTab.tsx`
- Modify: `web/src/components/service/ServiceWorkspace.tsx`
- Delete: `web/src/components/service/MetricsTab.tsx`
- Modify: `web/src/lib/api.ts`

**Interfaces:**
- Consumes: Task 7 performance components and Task 6 service-scoped APIs.
- Produces: service-scoped Performance tab preserving existing container CPU/memory history while adding route, dependency, log, deploy, and runtime-event correlation.

- [ ] **Step 1: Add service-scoped component tests or pure view-model tests**

Assert service ID is applied to every request, an unknown service shows the existing safe error state, container-only data still renders, and drill-down links retain the selected time range.

- [ ] **Step 2: Implement `PerformanceTab`**

Reuse global components with a fixed service filter. Keep the existing CPU/memory chart and latest-container table, rename the tab and copy to “Performance,” and avoid duplicating global explanatory text.

- [ ] **Step 3: Verify task**

Run: `npm run verify --prefix web && npm run build --prefix web`

Expected: PASS; no import of `MetricsTab` remains.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/service/PerformanceTab.tsx web/src/components/service/ServiceWorkspace.tsx web/src/components/service/MetricsTab.tsx web/src/lib/api.ts
git commit -m "feat(web): connect service performance drilldowns"
```

### Task 9: Package deployment, privacy, and load validation

**Files:**
- Create: `deploy/nginx/timing-log.conf`
- Create: `deploy/nginx/route-families.map`
- Create: `docs/performance-operations.md`
- Create: `scripts/performance-soak.sh`
- Modify: `docker-compose.yml`
- Modify: `README.md`
- Modify: `docs/dogfood-runbook.md`
- Modify: `docs/database-ops.md`

**Interfaces:**
- Consumes: all prior Touch Grass tasks and the linked Ticketnation remediation plan.
- Produces: read-only mounts/config examples, a route-family allowlist, collector rollout/rollback runbook, SQLite recovery steps, and a repeatable overhead/privacy soak.

- [ ] **Step 1: Write configuration fixtures and privacy smoke checks**

The timing log excludes `$request`, `$request_uri`, `$args`, headers, cookies, IP, referrer, and user agent. The smoke script sends paths containing email/token/query fixtures and fails if any appear in SQLite or Touch Grass logs.

- [ ] **Step 2: Add deployment configuration**

Mount the Nginx timing log and route map read-only. Add collector env examples and Docker log rotation. Do not grant new write access to Ticketnation containers, PostgreSQL, Redis, or Nginx configuration.

- [ ] **Step 3: Add operational runbooks**

Document one-source-at-a-time rollout, collector stale/loss diagnosis, WAL checkpoint and backup recovery, database-stat reset behavior, `pg_stat_statements` maintenance-window enablement, independent collector disable flags, and full rollback.

- [ ] **Step 4: Run complete verification**

Run: `make vet && make lint && make test-race && npm run verify --prefix web && make build && ./scripts/performance-soak.sh`

Expected: all commands PASS; the soak reports under 5% Touch Grass CPU overhead, bounded disk growth, responsive API reads, and zero sensitive fixture matches.

- [ ] **Step 5: Commit**

```bash
git add deploy/nginx docs/performance-operations.md scripts/performance-soak.sh docker-compose.yml README.md docs/dogfood-runbook.md docs/database-ops.md
git commit -m "ops(performance): add safe collector rollout"
```

### Task 10: Complete end-to-end review and release evidence

**Files:**
- Create: `docs/superpowers/evidence/2026-10-16-agentless-performance-validation.md`
- Modify: only files required by review findings.

**Interfaces:**
- Consumes: this plan, the Ticketnation remediation plan, and production-like local fixtures.
- Produces: a release evidence document with commands, measured overhead, screenshots, privacy results, rollback proof, and known limitations.

- [ ] **Step 1: Run a clean whole-repository review**

Review migration idempotency, cursor recovery, histogram math, source timeouts, privacy, authorization, bounded queries, goroutine shutdown, accessibility, and documentation parity.

- [ ] **Step 2: Execute failure scenarios**

Stop each source independently, rotate/truncate Nginx logs, recreate containers, reset database statistics, restart Touch Grass mid-read, fill disk to the configured warning threshold, and verify historical data remains usable while status becomes stale/error.

- [ ] **Step 3: Record release evidence**

Capture before/after Touch Grass CPU, memory, SQLite size/latency, collector loss, dashboard API p95, privacy scan output, and all validation commands. State that correlations are not traces or causal proof.

- [ ] **Step 4: Final verification**

Run: `git status --short && make all && make audit && npm run verify --prefix web`

Expected: clean worktree after evidence commit; all checks PASS or any pre-existing advisory is explicitly documented with no new actionable finding.

- [ ] **Step 5: Commit**

```bash
git add docs/superpowers/evidence/2026-10-16-agentless-performance-validation.md
git commit -m "docs(performance): record release validation evidence"
```
