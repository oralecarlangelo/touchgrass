# Fleet monitoring and container security

Every container on the daemon is inventoried — running, paused, exited,
created, restarting, or dead — whether Touch Grass manages it or not. The
authoritative inventory reconciles every 30 seconds and also wakes after
Docker lifecycle events. A separate metrics sampler reads `docker stats`
for running containers plus host CPU, memory, disk, and load. Metrics keep
seven days by default (`TOUCHGRASS_RETENTION_METRICS`); stopped containers
remain in inventory with null live metrics instead of disappearing.

No agent or per-container setup is required. Touch Grass uses the Docker API
read-only for list, inspect, top, diff, stats, logs, and events. It does not
start, stop, exec into, quarantine, or mutate monitored containers.

## Managed and observed containers

**Managed** containers belong to a service row — they get deploys, alert
rules, log collection, and a link to their service page. Everything else is
classified explicitly:

- **Unknown**: newly discovered and not yet reviewed.
- **Approved observed**: reviewed with an image/configuration baseline.
- **Ignored**: excluded by an audited, reasoned operator decision, with an
  expiry unless explicitly made non-expiring.
- **Missing expected**: a managed container that should exist but is absent.

Identity is the Docker container ID, not its reusable name. A complete
reconciliation may mark a container absent; a partial Docker failure never
does. Historical lifecycle rows stay queryable after removal.

Promoting an observed container to managed stays a conscious act. The Manage
flow drafts a service from Compose labels and published ports. Review health,
strategy, and routing before creation; security approval does not promote a
container, and promotion does not approve its security baseline.

## The page

Host stat cards sit above CPU/memory/load charts and the fleet table. The
table shows lifecycle state, classification, ownership, image, CPU, memory,
restart count, and freshness. Open a row for normalized security posture,
baseline state, current findings, and safe operator actions. Stopped rows
show unavailable live metrics as null/“not running”; zero is never invented.

## Security activation and findings

Container security starts disabled. An admin first reviews the complete
inventory and activates monitoring once. Activation does not approve,
ignore, restart, or modify anything. Posture, process, and writable-layer
collectors then create durable finding episodes for:

- risky configuration such as privileged mode, host namespaces, Docker
  socket access, root, mutable filesystems, excess capabilities, missing
  resource limits, exposed ports, and baseline drift;
- kernel-like process names, executables in temp or user-local paths,
  suspicious privilege transitions, and sustained PID spikes;
- added executables and modifications to runtime-sensitive paths; and
- a critical correlation when the same newly added executable is running.

Evidence is bounded metadata only. Environment values, raw command
arguments, arbitrary labels, host mount sources, file contents, client
addresses, and credentials are not stored. Admins can acknowledge, manually
resolve, or suppress a finding; later detection can open a new episode.
Viewers remain read-only.

Collector failures are findings too. **Partial coverage** means existing
findings remain active because absence was not proven. **Stale** means the
source has not succeeded within its freshness window. The Performance
collector cards show last success, safe error category, effective interval,
and dropped records.

## API

- `GET /api/system/containers` — complete current inventory and freshness.
- `GET /api/system/containers/{id}` — lifecycle and safe posture detail.
- `GET /api/system/history?metric=cpu|mem|load&hours=N` — host history,
  bucketed to at most 1500 points (default 24h, max 168h).
- `GET /api/security/summary` and `/api/security/findings` — posture and
  bounded finding episodes.
- `GET/POST/PATCH/DELETE /api/security/policies...` — audited global or
  service policy management; mutations are admin-only.
- `GET /api/onboarding/suggest?container=<name>` and `POST /api/services` —
  promotion draft and creation.

Shapes and bounds are in the [REST API reference](/api/).

## Limits and operating boundary

- Reconciliation is polling plus best-effort event wakeups. A container that
  is created and removed entirely between both can be missed; Docker event
  history is bounded, not a forensic audit log.
- `docker top` and `docker diff` are snapshots. Namespace/rootkit evasion and
  in-memory-only behavior can be invisible. Findings indicate evidence, not
  proof of compromise or attribution.
- The Docker socket is root-equivalent even when mounted `:ro`; restrict host
  and Touch Grass access accordingly.
- Touch Grass has **no automatic containment**. It never kills a process,
  stops a container, edits an image, blocks traffic, or rotates credentials.
  Operators investigate and contain through their existing incident runbook.
- Raw observations default to 168 hours, lifecycle events to 720 hours, and
  resolved finding episodes to 2160 hours. Unresolved findings are preserved
  until an operator or complete recovery resolves them, subject to the total
  SQLite budget.
