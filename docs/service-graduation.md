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

- [ ] `curl <health_url>` returns 2xx from the box.
- [ ] Dashboard shows the service green; stop the container and it
      goes red within one watch interval.

## 2. Metrics + history

- [ ] `GET /api/services/{id}/metrics` returns samples within one
      metrics interval of startup.
- [ ] Spot-check one sample against `docker stats --no-stream`
      (AC-3 tolerance: CPU within a few points, memory within a few MB).
- [ ] History survives a touchgrass restart (SQLite-backed).

## 3. Deploy + rollback live run

Drive the generic engine end to end (admin-fe already proved the
recreate path in `TestRecreateProofAdminFE`; this is the live twin):

- [ ] `POST /api/services/{id}/deploy` (recreate) succeeds; the new
      container serves and the dashboard records the deploy.
- [ ] Roll back from the UI; traffic returns to the prior container.
- [ ] `GET /api/audit?service_id={id}` holds both entries.

## 4. Logs + errors (if the service opts in)

- [ ] Logs view shows the service's container lines within one poll.
- [ ] If the SDK is installed: forced staging error groups in Issues
      with surrounding logs (dogfood runbook §3 pattern).

## 5. Runbook + sign-off

- [ ] Service-specific notes exist (on-call, deploy cadence, known
      quirks) — link them from this file.
- [ ] Caps + retention confirmed sane for the service's volume
      (`/api/logs/stats`, occurrence counts after a day).

## Sign-off

- [ ] S12: `tn-fe` graduated (all boxes above ticked on EC2).
- [ ] S13: `admin-fe` graduated (all boxes above ticked on EC2).

Service notes:

- `tn-fe`: _pending_
- `admin-fe`: _pending_
