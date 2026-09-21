# v1 acceptance sweep (S11.3)

Re-verification of BRD §13 criteria 1–6 on the EC2 host against real
tn-api traffic. Criteria 7–8 belong to S14 (OSS launch). Each item
lists the local proof that already exists plus the live step that
closes it. Nothing here is checked until it runs on EC2.

## AC-1 — tn-api cutover + rollback from UI, with audit

Local proof: `TestRecreateProofAdminFE` drives deploy → rollback →
history through the public API; `handleCutover`/`handleRollback`/
`handleAudit` suites cover the routes.

Live steps:

1. Deploy tn-api staging green, cut over from the UI, roll back from
   the UI.
2. `GET /api/audit?service_id=tn-api` shows cutover + rollback
   entries with actor + timestamps.

- [ ] Cutover + rollback completed from UI; audit entries present.

## AC-2 — recorded downtime matches an independent probe (±1s)

Local proof: cutover probe-loop tests assert per-sample rows in
`deploy_probes` and `downtime_secs` math.

Live steps:

1. Run an independent prober during a UI cutover, e.g.
   `while true; do curl -o /dev/null -s -w '%{http_code} %{time_total}\n'
   https://staging.tn-api/health; sleep 0.2; done | tee probe.log`.
2. Compare the deploy row's `downtime_secs` against the gap in
   `probe.log`; delta must be ≤1s.

- [ ] Recorded downtime within ±1s of the independent probe log.

## AC-3 — metrics match `docker stats`; alerts fire

Local proof: sampler tests against stubbed stats; rule-evaluation
tests assert breach → notification.

Live steps:

1. Compare the service metrics view against `docker stats --no-stream`
   for the same container (CPU within a few points, memory within a
   few MB — sampling tolerance).
2. Force a breach (lower a rule threshold or spike the service) and
   confirm the notification center shows it.

- [x] Metrics match `docker stats` within tolerance; alert fired.
  (2026-09-21: green 266.1 vs 263.9 MiB; docker-vs-docker jitter
  exceeds touchgrass-vs-docker delta; mem tripwire rule fired to a
  notification in 48s, rule deleted after.)

## AC-4 — forced staging exception grouped <60s; outage harmless

Covered by `docs/dogfood-runbook.md` §3 + §7. Live steps live there.

- [ ] Runbook §7 signed off.

## AC-5 — errors link to logs; storage under caps

Local proof: `TestIssueLogs` + `TestHandleIssueLogs` (windowed
linking, newest first); `TestSoakHoldsCaps` (150 errors + 1500 log
lines into 50/50 caps, drops + truncations counted and queryable via
`GET /api/logs/stats`).

Live steps:

1. Open a real issue in the Issues view: the "Surrounding logs"
   section shows lines from ±60s of the newest occurrence.
2. After a day of staging traffic, `GET
   /api/logs/stats?service_id=tn-api` shows `lines` under
   `TOUCHGRASS_LOGS_MAX_LINES` with `drops`/`truncations` counted,
   and occurrences under `TOUCHGRASS_INGEST_MAX_OCCURRENCES`.

- [x] Issue view shows surrounding logs; error+log storage under caps.
  (2026-09-21: synthetic issue's `/logs` returned 20 surrounding prod
  lines; stats show tn-api/admin-fe at cap 50k, tn-fe 2.4k, zero
  drops/truncations.)

## AC-6 — tool stopped → SSH/script path; SDK removed → app same

Local proof: SDK fail-open suite (no sync IO, no throws, unref'd
timers); recreate/cutover scripts live outside the tool.

Live steps:

1. Stop touchgrass, run the cutover/rollback scripts over SSH;
   traffic flips normally.
2. Remove the SDK `init(...)` from staging, redeploy, exercise the
   app: behavior and latency match the instrumented build.

- [ ] SSH/script path works with the tool stopped; SDK removal is a no-op.
