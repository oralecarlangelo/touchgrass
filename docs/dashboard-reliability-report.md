# Dashboard reliability assessment

Status: deployed and verified, 27 November 2026.

## Observed production behavior

The overview shows loaded host and notification data while managed services and
the services list remain loading. The services page subsequently shows HTTP 502.

Production journal and reverse-proxy records agree on the affected requests:

- `/api/services` takes approximately 10.1–10.5 seconds repeatedly.
- The application records HTTP 200, while nginx records an upstream connection
  closing before response headers at matching timestamps.
- The HTTP server has a 10-second write timeout. Handler completion after that
  deadline does not prove that the response reached the browser.
- `/api/system` sometimes takes approximately 10 seconds and also fails at the
  proxy. Several system requests appear together during subsequent refreshes.

## Intended changes and acceptance criteria

Source tracing confirms that services reads synchronously list Docker containers
and probe configured health endpoints. The Docker call allows 10 seconds; health
probes allow 5 seconds, with services processed sequentially. System reads also
call Docker Info synchronously. These calls share a two-permit Docker guard with
background collectors. The existing nginx target watcher does not supply a
cached service health view.

The implementation therefore uses separate inventory and system heartbeat loops;
the existing deployment target watcher keeps its independent cadence. Request
handlers read snapshots and authorized service definitions. This removes remote
dependency latency from the browser request without hiding freshness failures.

1. Collect expensive service and host observations on a bounded background
   schedule. Browser reads must not start Docker or external health probes.
2. Return the latest snapshot promptly, with explicit initial, stale, and failed
   collection states. Preserve the last successful observation on failure.
3. Share simultaneous frontend requests within the authenticated session, cancel
   obsolete work, and prevent old responses from replacing newer state.
4. Keep independent dashboard sections visible. Refresh failures must provide
   retry without discarding previously loaded data; freshness must be visible.
5. Correlate requests with safe identifiers and record duration, delivery errors,
   and background collection outcomes without secrets or unbounded log payloads.
6. Verify delayed and failed dependency behavior, authenticated production request
   latency, request counts, and rendered loading/error states after deployment.

## Verification record

The implementation adds independent inventory and system heartbeats using the
configured metrics interval (30 seconds by default). Browser reads return cached
observations plus freshness metadata. Service authorization still limits the
database query. Cold observations are unknown, failed collections preserve prior
data, and observations age into stale state.

The dashboard exposes independent error/retry states and preserves previously
loaded rows on refresh failure. A single coordinator shares overlapping service
reads and rejects responses after cancellation or an authentication/scope change.
Host, notification, and fleet sections settle independently.

HTTP diagnostics now generate `X-Request-ID` and log the same identifier, first
final status, bytes accepted by the response writer, write failures, cancellation,
and elapsed time. Slow response starts warn; promptly started event streams do
not warn solely because they remain open. Heartbeats log duration and collection
outcomes. Accepted bytes are not proof of delivery to the browser.

Verified locally: full Go suite, go vet, affected-test command, new-code lint
(zero issues), frontend type/lint checks, and the ARM64 preview build. The web
suite has 144 passing tests including independent section settlement, request sharing, cancellation, initial
failure, preserved stale rows, and degraded freshness. Race checks passed for
service, HTTP, command, and model packages. An existing uptime test now compares
against the actual sampling interval instead of assuming setup takes under ten
seconds. Production results are recorded below.

Rendered browser validation used the built UI with a generic authenticated fixture:
system data delayed 2.5 seconds and fleet data delayed 4 seconds. Services and
notifications appeared before either delayed response. Initial requests were one
each for session, services, system, and fleet; notifications had two intentional
consumers (the navigation bell and dashboard). One manual refresh issued one
additional service request. A controlled HTTP 502 preserved the service row/count
and showed retry alerts on both Services and Overview. No unexpected browser
runtime errors were observed.

Screenshot evidence: `touchgrass-dashboard-stale-services.png` in the chat's
visualization artifacts. This fixture verifies UI behavior, not production speed.

An authenticated public-endpoint baseline immediately before deployment reproduced
the failure: `/api/services` returned HTTP 502 after 13,011 ms; `/api/system`
returned HTTP 200 in 62 ms. This sample complements the repeated approximately
10-second service durations in the application journal; it is not a percentile.

## Backup retention

Production initially retained seven uncompressed SQLite snapshots (about 13 GB
including one older compressed archive) and fourteen PostgreSQL dumps (6.9 GB).
The requested reduction uses three SQLite snapshots and seven PostgreSQL dumps.
Daily scheduling remains unchanged. The older compressed archive and deployment
rollback artifacts are retained separately. The newest PostgreSQL dump passed
SHA-256 verification and `pg_restore --list`; this is an archive check, not a full
restore rehearsal. The newest SQLite snapshot passed `PRAGMA quick_check`.
Pruning removed four old SQLite snapshots and seven old PostgreSQL dumps,
reclaiming 11,046,125,814 bytes (10.3 GiB). Three SQLite snapshots and seven dumps
remain. Root disk usage fell from 63% to 58%, with approximately 83 GB available.

## Production deployment and measurements

Deployed `0.9.0-dashboard.1`, source commit `89c985e`, at 10:03 UTC. Local and
uploaded ARM64 binary SHA-256 matched. The previous binary and environment are
retained in `backups/releases/pre-dashboard-20261127`; existing database rollback
artifacts remain. The systemd service restarted successfully.

Ten authenticated public-URL reads per endpoint across two heartbeat cycles from the EC2 host all returned
HTTP 200: services 13–15 ms and system 13–15 ms. All returned complete, non-stale
snapshots and unique `X-Request-ID` headers. Repeated reads shared the same
observation timestamp rather than triggering collection. Initial background
system collection took 837 ms and service collection 1,289 ms, isolated from
browser request latency. These are small-sample checks, not p95 measurements or
an end-user geographic latency benchmark. The next heartbeat ran without intervening
service/system reads, updated the observation timestamps, and completed in 16 ms
(system) and 285 ms (services). The process has zero automatic restarts. A returned
request ID was matched to its request journal entry with no write error. The
publicly served JavaScript bundle SHA-256 matches the final local build. Running
process environment confirms backup retention of three SQLite and seven database
backups, not merely edited configuration on disk.

## Limits and follow-up

- Observations refresh every 30 seconds; manual browser refresh reads the latest
  snapshot. A failed or delayed collector can leave older data, explicitly marked
  degraded/stale. The nginx deployment target watcher retains its 5-second cadence.
- This removes the observed synchronous-read timeout path, not every possible
  infrastructure cause of HTTP 502.
- The production frontend bundle still raises Vite's existing large-chunk warning
  (about 335 KB gzip); route-level code splitting is a separate optimization.
- Retention trades historical restore depth for disk space. Compressed archive
  and release rollback copies are outside normal snapshot-count pruning.
