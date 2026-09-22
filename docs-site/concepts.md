# Concepts

The five ideas the whole system hangs on.

## Services are data

Everything touchgrass manages is a row in the `services` table: an id,
a strategy (`bluegreen` or `recreate`), a compose project, and a JSON
config blob. There is no per-service code — onboarding a new service
is an insert plus verification, which is why the console and the API
treat every service identically.

## Reports become occurrences, occurrences become issues

The SDK sends **reports**. Each accepted report is stored as an
**occurrence**, then grouped into an **issue** by fingerprint:
service + error type + templated message + top three stack frames.
IDs, UUIDs, emails, hex blobs, and quoted strings are normalized out
of the message so one bug is one issue even when the details vary.
Releases are tracked per issue, so you can see exactly which deploy
introduced (or fixed) a crash.

## Sampling happens at the key

Every ingestion key carries a `sample_rate` between 0 and 1, evaluated
with crypto randomness at write time. Start at 1; when a noisy service
drowns the signal, mint a lower-rate key and revoke the old one —
counts fall proportionally, and the `sampled` flag in the ingest
response tells the SDK what happened.

## Retention is a design constraint, not a cleanup job

Every store has a cap and every cap has a counter: occurrences per
service, log lines per service, metrics/notifications/deploys by age.
Oldest data trims on schedule; drops and truncations are counted and
queryable (`GET /api/logs/stats`) instead of failing silently. The
database cannot grow without bound no matter how loud your services
get.

## Not everything needs managing

Managed services get deploys, alerts, and log collection — but the
sampler observes every container on the daemon regardless, plus host
CPU/memory/load history. Visibility is free and total from second
one; management stays a conscious promotion. See [Fleet
monitoring](/fleet).
