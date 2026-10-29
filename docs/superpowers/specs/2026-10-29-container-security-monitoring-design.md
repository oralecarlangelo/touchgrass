# Automatic Container Discovery and Runtime Security Monitoring

**Date:** 2026-10-29

**Status:** Approved in conversation; awaiting written-spec review

**Owners:** Touch Grass maintainers

## Summary

Touch Grass will discover every Docker container on an enrolled host, preserve
its lifecycle even when it is stopped or short-lived, and evaluate fleet-wide
security signals independently of whether the container has been promoted to a
managed service. The existing managed-service model remains responsible for
deploys, health probes, application logs, and service alert rules. A new
container inventory and security-finding model covers managed and unmanaged
containers alike.

The first release is alert-first. It records bounded evidence, creates durable
findings, shows fleet risk in the dashboard, and emits in-app security
notifications. It does not automatically stop, restart, disconnect, delete, or
execute commands inside monitored containers. Optional containment and a
separate host sensor are future capabilities that require their own approval.

The design stays project-agnostic. It uses Docker identities, Compose labels,
operator-approved baselines, and generic policies; it contains no customer,
repository, service, route, or provider-specific behavior.

## Problem statement

Touch Grass currently samples all *running* containers, but Docker list calls
use `All: false`. Stopped containers are absent, containers that live for less
than the sampling interval may never appear, and a recreated container can be
conflated with an older container that reused its name. Docker lifecycle events
are retained for performance correlation, but they do not maintain the
authoritative inventory or produce security findings.

The dashboard's Fleet health card is derived from configured services. An
unmanaged or unknown container can therefore be compromised while every
managed service is shown as healthy. Unmanaged containers currently receive
resource metrics only and do not page anyone. Touch Grass also has no
container-process inventory, approved image/configuration baseline, or
writable-layer integrity checks.

The desired outcome is:

1. Every current Docker container is visible regardless of state.
2. Short-lived containers leave a durable lifecycle record.
3. Unknown, missing, unhealthy, drifted, or suspicious containers affect the
   fleet posture and can create findings.
4. Runtime evidence such as suspicious processes and added executables is
   correlated into actionable incidents.
5. Service deploy management remains an explicit operator decision.
6. Monitoring failure is visible and never silently renders a healthy state.

## Goals

- Reconcile the complete Docker inventory at boot and on a bounded interval.
- Consume Docker events for low-latency lifecycle updates and recover from
  missed events through reconciliation.
- Separate managed-service health, observed-container state, and security
  posture in the data model and dashboard.
- Apply fleet-security policies to managed and unmanaged containers.
- Detect image/configuration drift, risky container configuration, suspicious
  process behavior, and high-risk writable-layer changes.
- Correlate multiple weak signals into higher-confidence findings.
- Deduplicate repeated observations into finding episodes with acknowledgement,
  resolution, suppression, and recovery.
- Preserve bounded, privacy-safe evidence without storing file contents,
  environment variables, arbitrary labels, or full command lines.
- Reuse existing authentication, authorization, notification, retention,
  audit, SSE, and SQLite patterns.
- Keep collection bounded in CPU, memory, storage, concurrency, and time.

## Non-goals

- Automatic containment in the first release.
- Deleting a container, image, or forensic evidence automatically.
- Replacing Falco, Tetragon, or a full eBPF runtime-security product.
- Guaranteeing capture of every millisecond-lived process with polling alone.
- Virus scanning, malware signature distribution, or uploading binaries.
- Reading file contents from monitored containers.
- Persisting complete process arguments, environment variables, arbitrary
  container labels, or secret-bearing Docker inspection fields.
- Making an unmanaged container a deploy-managed service automatically.
- Multi-host remote agents in this release.
- Exposing a generic Docker API proxy or arbitrary Docker exec endpoint.

## Considered approaches

### A. Change Docker listing to `All: true` only

This fixes stopped-container visibility with minimal code. It does not capture
short-lived containers reliably, model identity across recreation, detect
runtime compromise, or alert on unmanaged containers. It is necessary but not
sufficient.

### B. Complete reconciliation plus Docker events and bounded security scans — selected

A periodic `All: true` reconciliation establishes authoritative current state.
Docker events provide fast lifecycle changes between reconciliations. Bounded
inspection, process, and writable-layer collectors feed a deterministic finding
engine. This fits the current single-host architecture and can detect the
observed incident class without requiring an application SDK.

### C. Host sensor with eBPF from the first release

A root-owned sensor can observe process execution continuously and remove the
Docker socket from the web/control-plane container. It gives the strongest
coverage but substantially expands installation, privilege, packaging,
cross-platform, and support requirements. The first release will preserve a
narrow collector boundary so a sensor can replace local Docker adapters later.

## High-level architecture

```text
 Docker Engine
   |-- complete list (All: true) -------+
   |-- event stream --------------------+--> Inventory reconciler
   |-- inspect/top/diff ----------------+        |
                                                   v
                                         Current inventory + history
                                                   |
 Operator baselines + generic policies ------------+
                                                   v
                                            Finding engine
                                             |    |    |
                                             |    |    +--> bounded evidence metadata
                                             |    +------> in-app notification + SSE
                                             +-----------> dashboard/API
```

The event stream is an accelerator, not the source of truth. Reconciliation
repairs missed events, daemon restarts, cursor gaps, and collector downtime.
The database is the durable source of Touch Grass inventory and finding state.

## Container identity and classification

Docker container ID is the instance identity. Container name is display data
and must not be a primary key because names are reused after recreation.

Each inventory row has one classification:

- `managed`: associated with an existing Touch Grass service.
- `approved_observed`: intentionally present but not deploy-managed.
- `unknown`: discovered after monitoring activation and not approved.
- `ignored`: explicitly excluded from security policy by an administrator,
  with a required reason and audit entry.
- `missing_expected`: an approved or managed instance/group expected by policy
  but absent beyond its grace period.

Classification and runtime state are separate. A managed container can be
stopped or critical; an unknown container can be healthy according to Docker
but still require review.

Initial enrollment must not silently approve a compromised host. The first
reconciliation imports existing containers as `unknown` with an
`existing_at_activation` marker. The UI offers a bulk review workflow, but each
approval records the actor, approved image ID/configuration hash, timestamp,
and optional reason. New containers appearing after activation create a warning
immediately unless a matching approved policy identifies them.

## Inventory collection

### Full reconciliation

- Run at process start and every 30 seconds by default.
- Call Docker container list with `All: true` and bounded response time.
- Inspect all new or changed container instances.
- Collect live resource statistics only for running containers.
- Represent unavailable CPU/memory statistics as null, never zero.
- Mark containers missing from a successful full reconciliation as destroyed or
  absent only after checking lifecycle state and a short race grace period.
- Preserve stopped and destroyed instances through the configured retention
  window.
- Record inventory freshness and the last successful complete reconciliation.

### Event stream

Collect bounded container events for:

- create, start, restart, pause, unpause;
- health transitions;
- OOM, die, stop, kill;
- rename, destroy, and remove.

An event updates known state immediately and schedules a coalesced
reconciliation. The collector keeps a durable cursor where Docker supports it.
Duplicate events are idempotent. Gaps, cursor expiration, or stream errors mark
the event collector degraded and trigger a full reconciliation.

Exec lifecycle events may be counted as a signal but their commands and
arguments are never persisted. An exec event alone is informational unless a
policy or correlated process observation raises its severity.

### Lifecycle edge cases

- If a container disappears between list and inspect, treat it as a race and
  reconcile again instead of failing the cycle.
- A new ID with an existing name is a new instance and closes the prior
  instance's active lifecycle.
- Rename updates display identity without changing instance identity.
- Paused, restarting, dead, and removing states remain visible.
- Collector failure keeps last-known state but marks it stale. Stale inventory
  can never be summarized as healthy.

## Baselines and policies

Baselines are explicit and revisioned. A baseline may match by approved image
digest, Compose project/service labels, selected configuration fingerprint,
or an administrator-approved instance. Core behavior cannot match a customer
name or hard-coded service ID.

The configuration fingerprint includes only security-relevant normalized
fields:

- image ID/digest and entrypoint/command hash;
- user, read-only-root setting, privileged flag, and added capabilities;
- PID/network/IPC modes;
- published port bindings;
- mount destinations and mount types, with sensitive source paths redacted;
- restart policy and configured PID/memory/CPU limits;
- selected standard Compose labels.

Arbitrary environment variables, label values, secrets, and raw mount source
paths are excluded. A baseline update requires administrator authority and an
audit entry showing the safe before/after fingerprint.

Policies support host-wide defaults plus narrower selectors based on generic
image, Compose, or service metadata. More-specific policies override defaults.
An ignored policy must have an expiry or explicit non-expiring choice and a
human-readable reason.

## Security posture checks

The posture scanner evaluates new and changed containers for:

- privileged mode;
- Docker socket or equivalent daemon-control mounts;
- host PID, network, or IPC namespace;
- unexpected added Linux capabilities;
- root execution when policy requires non-root;
- writable root filesystem when policy requires read-only;
- missing `no-new-privileges` where observable;
- missing PID, memory, or CPU limits;
- ports exposed on all host interfaces;
- mutable image tags such as `latest`;
- image ID/digest differing from the approved baseline;
- unexpected entrypoint, command, networks, or mount fingerprint.

Risky configuration is not automatically proof of compromise. Findings state
whether a rule represents posture risk, runtime anomaly, integrity drift, or a
correlated incident.

## Runtime integrity collection

### Process observations

For running containers, collect a bounded process projection using Docker's
read-only process API. The collector processes the response in memory and
persists only:

- PID and parent PID when available;
- user and normalized command name;
- normalized executable path only when the source exposes it reliably;
- first/last observed timestamps;
- process-count aggregate;
- a hash of the bounded normalized projection.

Full argument strings and environment variables are not persisted. Values are
length-capped and control characters are rejected.

Rules include:

- a kernel-thread-like name inside an application container;
- an executable path under `/tmp`, `/dev/shm`, or a user's `.local/bin` or
  `.local/sbin` directory;
- a process running from a path added to the writable layer;
- a deleted or replaced executable when observable;
- a sustained process-count increase against the approved rolling baseline;
- an unexpected process parent/child relationship defined by policy.

Process polling cannot guarantee capture of extremely short-lived execution.
The UI and documentation state this limitation plainly.

### Writable-layer observations

Use Docker's container diff API without reading file contents. Run a diff:

- after a container starts;
- when its image or configuration changes;
- every ten minutes by default for running containers;
- on demand internally when another high-severity signal needs correlation.

Persist aggregate counts and only bounded paths relevant to findings. High-risk
patterns include added executables in temporary/user-local locations,
modifications to approved application runtime/server files, persistence files,
and unexpected cron or SSH locations. Policies can allow expected writable
paths such as caches and upload directories.

The collector never reads, copies, hashes, or uploads file contents. A future
forensic export may reference an operator-owned host path but remains outside
this release.

### Correlation

Individual observations have conservative severity. Correlation raises
confidence when signals overlap within the same container instance and time
window. Examples:

| Signal | Base severity |
| --- | --- |
| New unknown container | Warning |
| Unexpected privileged or Docker-socket container | Critical |
| Image digest drift | High |
| Kernel-like process name inside a container | High |
| Process from a high-risk writable path | High |
| Added executable in a high-risk writable path | High |
| Added executable observed running | Critical |
| Application runtime modification plus suspicious child process | Critical |
| Sustained process-count spike | Warning or High by policy |

The engine must explain which rules contributed to a correlated finding. A
finding cannot become critical solely from opaque anomaly scoring.

## Finding lifecycle

A security finding contains:

- stable fingerprint and rule ID;
- container instance ID, safe display name, and optional service ID;
- category, severity, confidence, and human-readable summary;
- first seen, last seen, occurrence count, and last evaluated time;
- open, acknowledged, resolved, or suppressed status;
- contributing observation types;
- approved image/configuration fingerprint and observed fingerprint;
- bounded evidence metadata and recommended response;
- acknowledgement, suppression, and resolution actor/reason metadata.

Repeated observations update the existing open episode. A resolved condition
closes the episode after rule-specific hysteresis. If it returns later, a new
episode opens with the same fingerprint family. Suppression never deletes the
history and every mutation is audited.

Critical findings and configured high-severity findings create a dedicated
`security` in-app notification and SSE event. When the separately designed
provider-neutral notification outbox is implemented, the same bounded event
envelope can route to SMTP and signed webhooks without changes to the finding
engine.

## Data model

The implementation adds or evolves conceptual storage areas for:

- `container_instances`: durable identity, lifecycle, classification,
  ownership, image/configuration fingerprints, first/last seen, state, health,
  and freshness.
- `container_events`: idempotent lifecycle events including container ID and
  safe normalized action metadata.
- `container_baselines`: approved selectors, image/configuration fingerprints,
  policy revision, actor, reason, and timestamps.
- `container_security_observations`: bounded posture/process/layer observations
  used for correlation and short retention.
- `security_findings`: durable deduplicated finding episodes and workflow state.
- `security_evidence`: safe metadata or hashes referenced by a finding; never
  file contents or secrets.

Existing `container_samples` remains the time-series resource store and is
keyed to container instance identity in the upgraded model. Migrations are
forward-only and preserve existing history. Existing name-only samples are
imported as legacy history without pretending that the original Docker ID is
known.

Exact migration SQL requires normal repository review and migration tests; this
design defines behavior and invariants rather than embedding schema SQL.

## Service and package boundaries

- `internal/docker`: narrow list-all, inspect, process-list, diff, stats, and
  event adapters. No finding policy or persistence logic.
- `internal/model`: container identity, classification, observation, policy,
  and finding contracts.
- `internal/service`: inventory reconciliation, bounded scanner scheduling,
  policy evaluation, correlation, finding lifecycle, notifications, and audit.
- `internal/store`: SQLite persistence and transactional finding episode
  updates.
- `internal/http`: validation, authorization, pagination, filtering, and safe
  response projection.
- `web`: fleet posture, container details, review workflows, and findings.

Interfaces are defined where consumed and remain narrow enough for a future
host-sensor implementation to replace the local Docker adapter.

## API design

Existing endpoints remain backward compatible. Additive fields extend
`GET /api/system/containers` with instance ID, lifecycle state, health,
classification, risk summary, image identity, first/last seen, and freshness.

New read endpoints:

- `GET /api/system/containers/{id}`
- `GET /api/security/summary`
- `GET /api/security/findings`
- `GET /api/security/findings/{id}`
- `GET /api/security/policies`

Administrator-only mutations:

- `POST /api/system/containers/{id}/approve`
- `POST /api/system/containers/{id}/ignore`
- `POST /api/security/findings/{id}/acknowledge`
- `POST /api/security/findings/{id}/resolve`
- `POST /api/security/findings/{id}/suppress`
- `POST /api/security/policies`
- `PATCH /api/security/policies/{id}`
- `DELETE /api/security/policies/{id}`

List endpoints use bounded pagination and allowlisted sort/filter fields.
Responses never expose full commands, environment variables, arbitrary labels,
raw Docker inspection output, file contents, or host-sensitive mount sources.
All mutations use existing server-side role checks and append-only audit logs.

## Dashboard experience

### Overview

Replace the current service-derived Fleet health card with three explicit
summaries:

- Managed services: healthy/total.
- Containers: running, stopped, unknown, missing.
- Security posture: critical/high/open findings and collector freshness.

A critical finding or stale security collector visibly degrades the overall
posture even when every managed service is healthy.

### Fleet page

The fleet page gains filters for All, Running, Stopped, Unknown, Drifted, and
Risky. Rows show classification, lifecycle/health state, image identity, risk,
first/last seen, freshness, and ownership. CPU and memory are blank for states
where Docker cannot provide live statistics.

The existing Manage action remains separate from Approve observed. Approving a
container acknowledges its presence and baseline; managing it additionally
enables Touch Grass deploy/application workflows.

### Container detail

The detail view shows:

- identity, image, lifecycle, Compose ownership, and safe configuration;
- resource history and lifecycle timeline;
- approved versus observed fingerprint;
- posture checks and current findings;
- bounded process and writable-layer summaries;
- approval, ignore, and finding workflow history.

### Security findings

Add a Security view under Signals with severity/status filters, concise
evidence, affected containers, first/last seen, acknowledgement state, and
recommended response. The UI never promises automatic remediation. Every
status is conveyed by text/icon as well as color, and tables remain usable by
keyboard and on narrow screens.

## Scheduling, concurrency, and limits

- One reconciliation owner; event notifications coalesce into a bounded wakeup
  channel instead of starting an unbounded goroutine per event.
- Container inspection and process collection use a small configurable worker
  limit with per-call timeouts.
- Stats run only for running containers and continue to be collected once per
  interval for both service and fleet views.
- Process scans default to 30 seconds for running containers.
- Layer diffs default to ten minutes and event-triggered scans.
- Cancellation propagates from the top-level service context. Every worker has
  a clear exit and is awaited during shutdown.
- One failing container does not fail the full cycle; a failed complete list
  does prevent the reconciliation from claiming completeness.
- Observation, event, and finding writes are batched and covered by existing
  age/byte retention enforcement.

Operational validation targets on a 100-container fixture:

- complete reconciliation p95 under five seconds;
- event-to-inventory update under five seconds;
- no unbounded goroutine or queue growth;
- average added CPU no more than two percent of one core;
- added steady-state RSS no more than 64 MiB;
- storage remains inside configured SQLite limits.

If a target cannot be met, collection must degrade explicitly by increasing
scan intervals or disabling the expensive scanner with a visible degraded
status; it must not silently drop coverage.

## Security and privacy

- Docker access remains root-equivalent. The API stays localhost-bound behind
  authenticated TLS termination as documented today.
- Docker operations in this feature are read-only. No exec, stop, restart,
  network mutation, file copy, or image/container deletion is added.
- Raw Docker objects do not cross the adapter boundary or reach the browser.
- Process arguments, environment variables, arbitrary labels, raw file
  contents, and secret-bearing mount sources are never persisted.
- Logs use stable low-cardinality messages and safe IDs; no evidence values are
  interpolated into message templates.
- Findings and evidence inherit database file protection, authentication,
  retention, and backup controls.
- Viewer users can read bounded findings but cannot approve, ignore,
  acknowledge, resolve, suppress, or edit policy.
- A later host sensor must expose a narrow telemetry protocol, not the Docker
  socket or arbitrary command execution. Removing the socket from the
  control-plane container requires a separate ADR and implementation spec.

## Failure behavior

- Docker unavailable: retain last-known data, mark all Docker-derived status
  stale, create one deduplicated collector incident, and retry with backoff.
- Event stream unavailable: reconciliation continues; event freshness is
  degraded independently.
- Process or diff unavailable for one container: inventory and stats continue;
  the container shows partial security coverage.
- SQLite write failure: do not claim that a finding or reconciliation was
  persisted; log once at the owning boundary and expose collector degradation.
- Rule evaluation failure: preserve existing findings and mark evaluation
  stale rather than resolving anything.
- Touch Grass restart: resume cursors, reconcile immediately, and reopen only
  conditions still present.
- Clock anomalies: lifecycle ordering uses Docker event time plus deterministic
  IDs; receipt time is stored separately where needed.

## Testing strategy

### Unit tests

- `All: true` list mapping for running, stopped, paused, restarting, and dead
  containers.
- Identity and name-reuse behavior.
- Event projection, idempotency, cursor recovery, and coalescing.
- Configuration fingerprint stability and exclusion of sensitive fields.
- Classification precedence and activation-time behavior.
- Every posture, process, diff, and correlation rule with false-positive cases.
- Finding deduplication, hysteresis, recovery, acknowledgement, suppression,
  and audit behavior.
- Bounded values, pagination, authorization, and user-safe API projections.
- Collector cancellation, bounded concurrency, timeout, and partial failure.

### Docker integration tests

- A stopped container appears in the inventory with null live statistics.
- A short-lived create/start/die/destroy sequence leaves lifecycle history even
  when it falls between reconciliations.
- A missed event is repaired by reconciliation.
- Recreating a container with the same name produces a new instance.
- A generic web-container fixture with an added executable under a user-local
  path and a kernel-like process name produces a correlated critical finding.
- Approved cache/temp writes do not produce a high-severity finding.
- No host process outside the container is attributed to the container.
- Secrets included in environment variables, labels, or process arguments do
  not appear in SQLite, API responses, logs, or test artifacts.

### Store and migration tests

- Upgrade from the current production schema with existing fleet samples and
  container events.
- Transactional finding episode updates under concurrent observations.
- Retention of resolved findings versus trimming of raw observations.
- Database size enforcement and online backup/recovery.

### Web tests

- Fleet/service/security counts cannot mask each other.
- Filters and responsive tables for every lifecycle/classification state.
- Accessible severity, stale, partial-coverage, and null-metric states.
- Admin-only approval and finding workflow controls.
- Existing Manage workflow remains distinct and functional.

### Operational validation

- Run in shadow mode on a non-production host with representative containers.
- Measure reconciliation latency, event delay, CPU, RSS, Docker API load, and
  SQLite growth.
- Restart Docker and Touch Grass independently and verify recovery.
- Block Docker access and verify visible staleness without false recovery.
- Replay the generic compromised-container fixture and verify one critical
  incident rather than a notification storm.

## Rollout

### Phase 1: authoritative inventory

Ship list-all reconciliation, durable instance identity, complete lifecycle
events, stopped-container UI, and collector freshness. Run without security
notifications while operators review the imported inventory.

### Phase 2: baseline and fleet alerts

Enable approval/ignore workflows, unknown and missing-container findings,
restart/OOM/unhealthy findings, and corrected dashboard health semantics.

### Phase 3: posture and image/configuration drift

Enable safe Docker inspection rules and approved fingerprints. Begin with
warnings in shadow mode, review false positives, then activate severity routing.

### Phase 4: runtime integrity

Enable bounded process and writable-layer scans, deterministic correlation,
incident workflows, and the generic compromised-container replay fixture.

### Phase 5: outbound delivery integration

Route critical/high security event envelopes through the separately reviewed
provider-neutral SMTP/webhook outbox when that subsystem is available. In-app
notifications remain authoritative if outbound delivery is degraded.

Automatic containment and host-sensor/eBPF work are not part of these phases.
They require separate design approval after alert quality and operational cost
are proven.

## Acceptance criteria

1. Every container returned by `docker ps -a` appears within 60 seconds,
   including stopped containers.
2. Supported Docker events update inventory within five seconds and survive
   Touch Grass restart without duplication.
3. A short-lived container leaves lifecycle history even when absent from the
   next full list.
4. Name reuse never merges two Docker container instances.
5. Unknown and missing expected containers produce deduplicated findings.
6. Managed-service health cannot hide an unknown, stale, or critical fleet
   condition.
7. Image/configuration drift and risky privilege settings are explainable in
   the UI without exposing secrets.
8. The generic compromised-container fixture produces one correlated critical
   finding from suspicious process and writable-layer evidence.
9. Full process arguments, environment variables, arbitrary labels, and file
   contents never enter persistence, logs, API responses, or the UI.
10. A collector outage preserves last-known data, marks it stale, and creates a
    deduplicated collector incident.
11. The feature meets bounded concurrency, performance, storage, retention,
    authentication, authorization, accessibility, and audit requirements.
12. Existing deploy, service, performance, log, and onboarding behavior remains
    backward compatible.

## Decisions recorded

- Alert-first is the approved first-release behavior.
- Complete reconciliation plus Docker events is the inventory model.
- Security monitoring applies to managed and unmanaged containers.
- Operator approval, not first observation, establishes a trusted baseline.
- Correlated deterministic rules, not opaque anomaly scoring, control critical
  severity.
- The current control plane may implement read-only Docker scanning first, but
  its interfaces must permit later replacement by a root-owned host sensor.
- Automatic containment, arbitrary exec, and destructive Docker actions remain
  out of scope.
