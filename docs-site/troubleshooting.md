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

Sampling runs every 30 seconds, so containers living under one
interval may never appear. Anything running longer than a minute
shows up — if it doesn't, check the sampler errors in the server
log (`metric collection failed` lines name the cause).

## The console shows stale data

Metrics sample every 30s, logs tail every 5s. If the gap is longer
than that, check the server log: Docker socket errors and SQLite
locks announce themselves loudly.
