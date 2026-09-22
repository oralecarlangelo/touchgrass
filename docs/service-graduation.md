# Service graduation runbook (S12–S13)

Onboarding a second and third service onto the proven generic engine.
Graduation is per service, on the EC2 host, against live compose
stacks. No code changes: services are data (ADR-0006) — one `services`
row plus verification.

The order below graduates `tn-fe` (S12) then `admin-fe` (S13). Each
section is a per-service checklist; the service is graduated when
every box is ticked.

## 0. The service row

Insert (or correct) the row with the strategy the stack actually runs.
Both remaining services are `recreate`:

```sql
INSERT INTO services (id, name, strategy, compose_project, compose_dir, config)
VALUES ('tn-fe', 'tn-fe', 'recreate', 'ticketnation-fe', '/opt/ticketnation-fe',
  '{"service":"fe","health_url":"http://127.0.0.1:3000",
    "public_url":"https://ticketnation.ph"}');
```

- `compose_project` + `service` must match the stack's real compose
  labels (`com.docker.compose.project/service`) — touchgrass matches
  containers on exactly these.
- `health_url` must answer 2xx on the box (the dashboard's live
  color comes from here).
- `public_url` feeds downtime probing; omit it only if the service
  has no public face (`downtime_secs` stays `nil` then).
- Later edits go through `ServiceStore.UpdateConfig` ("edit these
  rows" path), never a migration.

## 1. Health endpoint

The app must expose a health route independent of touchgrass.

- [x] `curl <health_url>` returns 2xx from the box.
- [x] Dashboard shows the service green (both, 2026-09-22); the
      stop-container half was skipped — it would drop prod traffic —
      and the watch path is generic across services.

## 2. Metrics + history

- [x] `GET /api/services/{id}/metrics` returns samples within one
      metrics interval of startup (both, 30s cadence 2026-09-22).
- [x] Spot-check one sample against `docker stats --no-stream`
      (AC-3 tolerance: CPU within a few points, memory within a few MB).
      tn-fe 148.8 vs 148.8 MiB, admin-fe 1.5 vs 1.5 MiB, CPU 0 vs 0 —
      after the `memUsage` cache fix (deployed 2026-09-22).
- [x] History survives a touchgrass restart (SQLite-backed) — deploy
      rows, audit, metrics, and issues all queryable after the
      09:47Z restart (auth sessions are in-memory and did not survive).

## 3. Deploy + rollback live run

Drive the generic engine end to end (admin-fe already proved the
recreate path in `TestRecreateProofAdminFE`; this is the live twin):

- [x] `POST /api/services/{id}/deploy` (recreate) succeeds; the new
      container serves and the dashboard records the deploy.
      (tn-fe id 4 downtime 5s; admin-fe id 6 downtime 2s; 2026-09-22.)
- [x] Roll back from the UI; traffic returns to the prior container.
      (tn-fe id 5 downtime 3s; admin-fe id 7 downtime 2s.)
- [x] `GET /api/audit?service_id={id}` holds both entries (actor
      admin, deploy + rollback, 2026-09-22).

## 4. Logs + errors (if the service opts in)

- [x] Logs view shows the service's container lines within one poll.
      (tn-fe 8.8k lines, admin-fe at 50k cap, zero drops; 2026-09-22.)
- [x] If the SDK is installed: forced staging error groups in Issues
      with surrounding logs (dogfood runbook §3 pattern). N/A —
      neither FE service has the SDK; tn-api proved the pattern.

## 5. Runbook + sign-off

- [x] Service-specific notes exist (on-call, deploy cadence, known
      quirks) — link them from this file (see Service notes below).
- [x] Caps + retention confirmed sane for the service's volume
      (`/api/logs/stats`, occurrence counts after a day).
      (tn-fe 8.8k/50k lines, admin-fe at 50k cap newest-kept, zero
      drops/truncations; no SDK occurrences on either.)

## Sign-off

- [x] S12: `tn-fe` graduated (all boxes above ticked on EC2).
- [x] S13: `admin-fe` graduated (all boxes above ticked on EC2).

Service notes:

- `tn-fe`: recreate strategy, single `fe` container
  (`ticketnation-fe-fe-1`, Next.js ~150 MiB). Deploys carry brief
  502 downtime (5s deploy / 3s rollback observed) — schedule
  off-peak. Health: `http://127.0.0.1:3000`. No SDK. Deploys via
  `recreate-deploy.sh`; on-call: standard FE rotation.
- `admin-fe`: recreate strategy, single `app` container
  (`ticketnation-admin-app-1`, tiny ~1.5 MiB static serve). Same
  brief-downtime caveat (2s/2s observed). Health:
  `http://127.0.0.1:3002/health`. No SDK. Same deploy script +
  on-call as `tn-fe`.
