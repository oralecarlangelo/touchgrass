# ADR-0011: Agentless performance diagnostics

- Status: Accepted (2026-10-16)
- Source: `docs/superpowers/specs/2026-10-16-agentless-performance-observability-design.md`

## Context

Operators need to identify slow route families and correlate latency with
container, PostgreSQL, Redis, log-volume, deployment, and runtime signals.
Adding an application SDK, OpenTelemetry pipeline, reverse proxy, or APM agent
would put new code or infrastructure in Ticketnation's request path and expand
the privacy and failure surface.

ADR-0009 remains unchanged: OpenTelemetry support is limited to log
correlation. Touch Grass does not collect spans or meters and does not become
an APM product.

## Decision

Touch Grass collects performance evidence agentlessly from native operational
sources:

- a dedicated, PII-minimized Nginx timing log;
- Docker stats, lifecycle events, and bounded log-volume aggregates;
- read-only PostgreSQL aggregate statistics; and
- read-only Redis aggregate statistics.

Source adapters normalize those inputs into fixed, one-minute aggregate
buckets. The service layer schedules each source independently and provides
bounded global and service-scoped Performance queries. The store layer owns
SQLite cursors, idempotent bucket upserts, rollups, retention, and byte
budgets.

Touch Grass remains outside Ticketnation's request path. Every collector is
independently enabled and fail-open: a missing, stale, or failed source must
not affect Ticketnation or hide historical data from the other sources.

## Privacy boundary

Performance storage contains approved route families and numeric aggregates
only. It must never persist a raw URI, query string, request or response body,
header, cookie, token, IP address, email address, user ID, SQL text or
parameter, or Redis key or value. Unknown, malformed, or sensitive paths are
stored as `other`. Parsing errors contain source position and a safe category,
not the rejected payload.

The UI describes relationships as correlations. Native aggregates cannot
prove source-level or distributed-trace causality.

## Rejected alternatives

- **Ticketnation SDK or OpenTelemetry instrumentation:** rejected because the
  requested design must add no application dependency or tracing pipeline.
- **Touch Grass as a reverse proxy:** rejected because monitoring must not
  become a production request-path dependency.
- **Unbounded raw-log or request capture:** rejected because it creates PII,
  cost, and retention risks while aggregate evidence answers the initial
  operational questions.

## Non-goals

Process profiling, public debug ports, raw SQL capture, and automatic
source-line attribution are excluded. Any bounded profiler requires a separate
ARM64 feasibility, security, privacy, and load decision.

## Consequences

- Collection continues when individual sources fail, while freshness,
  parse/persistence failures, and dropped records remain visible.
- SQLite receives bounded aggregate tables and persisted source cursors rather
  than high-cardinality request data.
- Nginx, PostgreSQL, Redis, Docker, and log collectors can be rolled out and
  disabled independently.
- The Performance workspace supports investigation and drill-down into existing
  Logs, Issues, Deployments, Fleet, and Database views without claiming an
  exact causal trace.
