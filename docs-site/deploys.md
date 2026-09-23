# Deploys

Every managed service ships one of two strategies, picked at
creation and stored on the service row. Either way the console runs
the operation, streams its progress, measures its downtime, and
keeps the receipt.

## The two strategies

**Recreate** is one container: stop, replace, start. One health URL,
one deploy script, one rollback script. Use it for internal tools,
workers, and anything where a few seconds of downtime is fine.

**Blue-green** is two identical colors behind nginx: only the live
color takes traffic. A cutover boots the idle color, flips the
nginx upstream, and verifies — downtime should read `0`. Use it for
anything user-facing.

The service page shows the live color, per-color health, and the
matched containers. An idle color with no running container reports
unhealthy — that is its resting state between deploys, not an
alarm.

## Running a deploy

- **Recreate** — the Deploy button runs the service's deploy
  script; Rollback runs its rollback script.
- **Blue-green** — Cutover flips traffic to the idle color
  (`auto`, or pick blue/green explicitly); Rollback flips back.

All four are async: the API answers 202 immediately, script output
streams over SSE (`GET /api/events`) into the console's progress
panel, and the run lands in deploy history when it finishes. One
run per service at a time — a second trigger gets 409 while one is
in flight. Every button is confirm-gated, and every run writes an
audit entry with the actor.

While the script runs, touchgrass samples the service's public URL
and records failed-sample seconds as `downtime_secs` on the deploy
row. After the script exits, the new target gets a post-run health
check — a failed check fails the run even though the script passed.

## History and receipts

`GET /api/services/{id}/deploys` lists every run newest first: SHA,
actor, type, outcome, timing, downtime, notes. Deploys run outside
the console (CI, a terminal) can be recorded into the same history
with `POST /api/services/{id}/deploys` so the timeline stays
complete.

## Removing a service

Services → Delete, confirm-gated. The service row plus its
touchgrass-side history (deploy records, alert rules, API keys,
metrics, logs, issues) goes away in one transaction; running
containers are never touched. The workload keeps serving and shows
up as observed on the [Fleet](/fleet) page, where Manage can bring
it back under management at any time.

## API

- `POST /api/services/{id}/deploy` — start a recreate deploy.
- `POST /api/services/{id}/cutover` — flip blue-green traffic
  (`target: auto|blue|green`, default `auto`).
- `POST /api/services/{id}/rollback` — flip back to the other color.
- `GET /api/services/{id}/deploys` — run history, newest first.
- `POST /api/services/{id}/deploys` — record an externally-run deploy.
- `DELETE /api/services/{id}` — remove a service from management.

Shapes are in the [REST API reference](/api/).

## Limits

- Blue-green needs its manual bootstrap first: compose colors,
  nginx upstream block with the `# BLUEGREEN-ACTIVE` marker, and a
  cutover-script fork per stack. Promotion drafts the config, but
  the infrastructure stays hand-built and reviewable.
- Downtime sampling needs a public URL on the service row — without
  one, `downtime_secs` stays null.
- Deploy scripts run with the touchgrass process's privileges and a
  ceiling (`TOUCHGRASS_CUTOVER_TIMEOUT`, default 10m). Keep them
  small, idempotent, and boring.
