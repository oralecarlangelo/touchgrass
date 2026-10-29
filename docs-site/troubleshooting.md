# Troubleshooting

## No issues appearing

1. Check the SDK is initialized: `init` with a bad endpoint or key
   yields a disabled client and never throws — exactly backwards from
   what you want while debugging. Log the return or call `flush()`
   and check the boolean.
2. From the app's network, not yours: containerized apps cannot reach
   `127.0.0.1` on the host. Use the public base URL.
3. Check the key: revoked or mistyped keys get silent `401`s by
   design. Mint a fresh key and retry.
4. Check sampling: at `sample_rate` below 1 most reports drop. The
   ingest response's `sampled` flag tells you per report.

## Issues appear but group wrong

Two bugs in one issue means the fingerprint templates match too
aggressively for your messages; one bug in many issues means a high-
cardinality token (a hash, a path) survived normalization. Open an
issue upstream with both messages — fingerprint rules are shared
code, not config.

## Counts dropped after a deploy

You probably shipped a release tag change with a message change: same
bug, new fingerprint, new issue. The old issue's releases list shows
where it stopped. This is working as designed — releases exist to
draw exactly that line.

## 413 from ingest

One report exceeded 1MB — usually a giant breadcrumb trail or a
stringified object in the message. Trim client-side; the cap is not
configurable.

## 401 from ingest

Unknown, mistyped, or revoked key. The server logs the key prefix
only, so compare prefixes in the Keys screen.

## No SDK logs appearing

1. Check the tab: application logs live under the **Application**
   source in the Logs view (`source=sdk`), not Containers.
2. Check the client: like error capture, `logger` needs an
   initialized client — a bad endpoint or key yields a disabled
   client that never throws. Await `flush()` and check the boolean.
3. Flush before exit: logs ride the 1s batch loop, so short-lived
   scripts must `await flush()` (or `close()`) or the batch dies
   with the process.
4. Check the level filter and the search text — then the key, same
   silent-`401` story as error ingest.

## 400 from ingest/logs

One invalid item rejects the whole batch; the response's `index`
points at it. Usual suspects: a level outside
trace/debug/info/warn/error/fatal, an empty or >8KB body, a
`trace_id` that isn't 32 lowercase hex, an attribute `type` outside
string/integer/double/boolean, or more than 1000 items.

## Empty trace_id on OTel records

Span context flows from each record's context, not the exporter —
with no async tracking there is no active span to attach. Full
setups (the OTel NodeSDK) get it automatically; minimal scripts
must pass it explicitly:
`emit({ body, context: trace.setSpan(context.active(), span) })`.
See [SDK logging](/sdk-logging).

## PII in occurrences

Add the pattern to the SDK `scrub` list, redeploy the app, and
confirm on the next occurrence. Scrubbing is client-side and
forward-only — already-stored payloads need retention to age out
(`TOUCHGRASS_RETENTION_ERRORS`).

## A container is missing from the fleet table

Inventory reconciles all Docker lifecycle states every 30 seconds and after
best-effort event wakeups. Check **Fleet → freshness** first. A partial
reconciliation never marks old rows absent; `docker_inventory_unavailable`
means the last complete snapshot is being preserved. Verify the Docker socket
mount and permissions, then compare `docker ps -a` with
`GET /api/system/containers`. A container created and removed entirely
between polling and event coverage can still be missed.

## The console shows stale data

Metrics sample every 30s, logs tail every 5s. If the gap is longer
than that, check the server log: Docker socket errors and SQLite
locks announce themselves loudly.

## Security shows partial coverage or stale collectors

Open **Performance → Collector status** and identify the source:

- `security_posture`: Docker list/inspect failed for at least one container.
- `security_process`: `docker top` was unavailable or an evaluation failed.
- `security_diff`: writable-layer metadata was unavailable or evaluation failed.

Partial coverage deliberately cannot resolve earlier findings. Fix Docker
access, wait for one complete pass, and verify last success advances. Safe
error categories are shown instead of raw daemon payloads. If the effective
interval increased, storage or scan-cost backoff is active; do not interpret
the last sample as current until freshness recovers.

## A suspicious binary finding remains after cleanup

Findings are durable episodes. Removing a file does not delete history. A
complete later scan resolves the active episode; an incomplete scan preserves
it. Open the finding for its last-seen time and contributing rules, then check
the container's current process and diff snapshots. A later recurrence opens
a new episode rather than rewriting the original evidence.

Touch Grass does not contain the workload automatically. If compromise is
plausible, isolate traffic and rotate credentials through the service's
incident procedure, preserve host evidence, and rebuild from a trusted image.
Do not rely on `docker diff` as a complete forensic record.

## Security activation or policy controls are unavailable

Only admins can activate monitoring, approve/ignore containers, mutate
policies, or change finding workflow state. Viewers can inspect everything but
remain read-only. Before activation, review every unknown and missing-expected
row; activation itself does not approve or modify containers.
