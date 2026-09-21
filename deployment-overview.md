# How We Deploy on TicketNation

As-is documentation of the production deployment flow and infrastructure.
Verified 2026-09-21 against the workflows, compose files, and scripts in
the ticketnation workspace (sibling folder `../ticketnation/` — all repo
paths below are relative to it). Live EC2 state (IPs, tenants, tool URLs) is per
`docs/rollback-runbook.md` and prior session notes — re-verify on the box
if anything looks stale.

## Big picture

```
 push to main ──► GitHub Actions (self-hosted EC2 runner, arm64)
                        │  build → push localhost:5000 → migrate (api) →
                        │  recreate / cutover → health check → tag release
                        ▼
 /opt/ticketnation[-fe|-admin] ──► host nginx ──► api|ticketnation|admin .ticketnation.ph

 ticketnation-mobile ──► EAS build/submit (no EC2 path, no CI workflow in repo)
```

All repos are independent Git repos with their own `main`. The release flow
is `develop` → PR → `main`; **pushing to `main` deploys to production**.
Each deploy workflow uses a per-repo concurrency group (`deploy-<repo>`,
no cancel-in-progress), so deploys queue instead of overlapping.

## Shared conventions (all EC2 services)

Every `Deploy to EC2` workflow follows the same skeleton:

1. **Trigger**: push to `main`, plus manual `workflow_dispatch`.
2. **Runner**: `[self-hosted, linux, arm64, ec2]` — builds run ON the
   production box. Builds are serialized with `flock /var/tmp/docker-build.lock`
   and memory-capped (2.5–3.5g) because the box is shared.
3. **Version**: `v<semver>+<run_number>.<sha8>` from `package.json` +
   GitHub run context; also tagged `:latest`, `:<full-sha>`, `:v<semver>`.
4. **Registry**: self-hosted at `localhost:5000` on the EC2
   (`tn-registry` container, compose in `/opt/registry/`, MinIO-backed
   bucket `tn-registry` per the runbook). Every build pushes
   `:latest`, `:<sha>`, `:v<semver>`.
5. **Rollback snapshot**: before restarting, the running container's image
   is retagged `<registry>:rollback` (one-deploy-ago).
6. **Restart + health check**: recreate the container, then poll the public
   health URL (5s interval, 24–36 tries). On failure: dump logs, exit 1.
7. **Auto-rollback**: if restart or health fails, retag `:rollback` back to
   the local name and recreate.
8. **Notify Portainer**: fire the stack webhook (non-fatal if unconfigured).
9. **Release tag**: create the `v<semver>+<run>.<sha>` git tag via API.

## Per-service flows

### ticketnation-api (NestJS) — blue-green, zero-downtime

Workflow: `ticketnation-main-api/.github/workflows/deploy.yml`.
Compose: `/opt/ticketnation/docker-compose.prod.yml`
(`api-blue` :4101, `api-green` :4102), plus Postgres 16 + Redis 7 in the
same project. Host nginx upstream `tn_api_active` points at the live color
(config: `/etc/nginx/sites-available/ticketnation`).

Extra steps the API has over the other services:

- **Strategy resolve**: push-to-main reads the repo variable
  `DEPLOY_STRATEGY` (default `bluegreen`); manual runs can pick
  `bluegreen` / `legacy` per run.
- **Compose sync**: `docker-compose.prod.yml` + `docker-compose.legacy.yml`
  are copied from the checkout to `/opt/ticketnation`.
- **Migrations**: `prisma migrate status` (shown), DB snapshot
  (`pg_dump -Fc` to `/opt/ticketnation/backups/`, verified with
  `pg_restore --list` + `.sha256` sidecar), then `prisma migrate deploy`
  — all BEFORE traffic moves.
- **Cutover** (`deploy/bluegreen-cutover.sh`): detect live color → boot
  idle color with the new image → wait its direct `/health` → flip nginx
  (`nginx -s reload`, hitless) → verify public URL → 45s settle → remove
  old color. If the public check fails post-flip, it flips BACK
  automatically. Proven locally: 4 flip windows, zero failed requests.
- **Legacy fallback**: `strategy: legacy` recreates the single `api`
  container on :4000 from the frozen `docker-compose.legacy.yml` (has the
  old downtime gap; escape hatch only).

Rules that matter: **all migrations must be backward-compatible**
(expand → deploy → backfill → contract) because old code serves the
migrated schema during every deploy. Destructive migrations must be
flagged in the PR and gated behind manual approval. Ports are 4101/4102
because ims-be holds 0.0.0.0:4001 on the shared box.

### tn-fe-2025 (Next.js) — recreate

Workflow: `tn-fe-2025/.github/workflows/deploy.yml`. Standard skeleton,
no migrations: build `--target runner` (3.5g cap) with `NEXT_PUBLIC_*`
build args from secrets → push `localhost:5000/tn-fe-2025` → recreate
`fe` in `/opt/ticketnation-fe` → poll `https://ticketnation.ph`.

Note: the repo also contains `amplify.yml` and there is a
`plans/02-amplify-fe-deployment.md` plan, but the active production path
per the workflow and runbook service map is **EC2**, not Amplify.

### ticketnation-admin-fe (React) — recreate

Workflow: `ticketnation-admin-fe/.github/workflows/deploy.yml`. Standard
skeleton: build with `REACT_APP_*` args (2.5g cap) → push
`localhost:5000/tn-admin-fe` → recreate `app` in
`/opt/ticketnation-admin` → poll `https://admin.ticketnation.ph/health`.

### ticketnation-mobile (Expo) — EAS, no CI

No `.github/workflows/` in the repo. Release config is
`ticketnation-mobile/eas.json`: `development` / `preview` / `production`
build profiles (production: autoIncrement, node 22.11.0) and `submit`
targets (iOS App Store Connect app id `6741431592`, Android `internal`
track). The human release process (who runs `eas build`/`submit`, when)
is not documented in the repo.

## The box (shared EC2)

- Host: `52.74.121.228`, OS user `ubuntu`, SSH key `~/.ssh/ticketnation-api.pem`
  (per runbook). Multi-tenant: tn-api, tn-fe, tn-admin, ims-be (:4001),
  monitoring (Portainer, Dozzle), self-hosted registry, GitHub runner.
- Compose dirs: `/opt/ticketnation`, `/opt/ticketnation-fe`,
  `/opt/ticketnation-admin`, `/opt/registry`.
- Host-level nginx terminates TLS and routes to localhost-bound
  containers; only the live API color is reachable through it.
- Observability today: Portainer (`https://portainer.altev.tech`,
  stacks/env vars/rollback), Dozzle (`https://logs.altev.tech`, live
  logs), GitHub Actions run logs. No centralized metrics, error
  tracking, or deploy notifications beyond the Portainer webhook.
- Resource pressure is real: builds are flock-serialized and
  memory-capped; disk pressure has been flagged before — any new
  on-box tooling must be small and retention-capped.

## Rollback (summary)

Full procedure: `docs/rollback-runbook.md`. Short version:

- **API, code-only**: pull good SHA from `localhost:5000/tn-api`, retag to
  `ticketnation-api:latest`, re-run the workflow (bluegreen) — zero-downtime.
  Or one-command restore via `bluegreen-restore-legacy.sh`.
- **FE/Admin**: Portainer → stack → set `<SVC>_IMAGE_TAG` to a prior SHA →
  update stack. Or SSH: pull prior tag, retag to local name, recreate.
- **DB (API)**: image rollback ≠ schema rollback. Additive migrations are
  safe to roll back over; destructive ones need a forward-fix or a
  snapshot restore with an approved outage. Pre-deploy snapshots live in
  `/opt/ticketnation/backups/` (local only — no confirmed offsite copy).
- Verify: `curl -sf https://api.ticketnation.ph/health`,
  `https://ticketnation.ph` → 200, `https://admin.ticketnation.ph/health`.
  File an incident issue in the affected repo afterwards.

## Gaps this doc does not cover

- Mobile release process (undocumented in repo — ask who runs EAS).
- Whether Amplify still serves anything (EC2 is the active FE path).
- GHCR: the runbook cites GHCR package versions as a SHA source and the
  compose comments mention a GHCR rollback archive, but current workflows
  push only to `localhost:5000` — confirm what actually lands in GHCR.
- Offsite DB backups: not confirmed.
- Other workspace projects (`ticketpass/`, `corbin-job-portal/`,
  `learning-app/`) are separate products with their own setups — out of
  scope here.

## File pointers

- API: `ticketnation-main-api/.github/workflows/deploy.yml`,
  `ticketnation-main-api/docker-compose.prod.yml`,
  `docker-compose.legacy.yml`, `deploy/bluegreen-*.sh`,
  `deploy/nginx-upstream-block.md`, `deploy/local-test/`
- FE: `tn-fe-2025/.github/workflows/deploy.yml`
- Admin: `ticketnation-admin-fe/.github/workflows/deploy.yml`
- Mobile: `ticketnation-mobile/eas.json`
- Runbook: `docs/rollback-runbook.md`
- History: `plans/01-ec2-api-deployment.md`, `plans/02-amplify-fe-deployment.md`
