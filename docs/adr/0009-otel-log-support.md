# ADR-0009: OTel log support in the Node SDK

- Status: Accepted (2026-09-22)
- Source: S20 logging SDK; overrides `docs/ARCHITECTURE.md`
  ("Tracing/APM explicitly out")

## Context

The architecture doc banned tracing/APM to keep v1 narrow. But
application logs without trace correlation are half a story: the
console already links errors to surrounding log lines, and
trace/span ids are the natural join key for services that run
OpenTelemetry. Teams asked for Sentry-style `logger.info` parity,
which implies OTel severity mapping at minimum.

## Decision

The Node SDK supports OpenTelemetry for **logs only**:

- `logger` auto-captures the active span's trace/span ids when
  `@opentelemetry/api` is present (fully guarded lookup — no
  package, no span, or any throw means unattributed logs, never a
  crash).
- `@touchgrass/node/otel` ships an OTel logs-SDK exporter so
  OTel-native apps can skip `logger` entirely.
- Severity uses the OTel 1–24 scale end to end (SDK → ingest →
  `sdk_logs` → console).

No tracing, no metrics, no APM: touchgrass never collects spans or
meters, and `@opentelemetry/api` stays an optional peer — the SDK
keeps its zero-dependency, fail-open contract.

## Consequences

- `POST /api/ingest/logs` accepts OTel-shaped batches
  (trace_id/span_id/severity_number) alongside the logger shape.
- Ingest validates the OTel fields like any other untrusted
  payload (size caps, level enum); the threat model covers SDK
  log contents.
- Full tracing/APM stays out: a future span pipeline needs its
  own ADR.
