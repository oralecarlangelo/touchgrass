## Goal

Scope (and approve the build of) touchgrass: a custom, open-source,
self-hosted server-management platform with a Gen Z voice that manages
TicketNation's whole infrastructure from one place across three pillars —
(1) Deploys: blue-green deploys, one-click rollback, deploy history/audit,
per-deploy downtime statistics; (2) Resources: container metrics + alerts,
view-only; (3) Logs: Sentry-style error tracking via a lightweight SDK plus
log aggregation, unified so errors link to surrounding logs. This plan
covers the scope, key decisions, phased work plan, validation, risks, and
open questions. No code is written until this plan is approved.

## Success Criteria

- tn-api blue-green is fully operable from the tool's UI: see live color and
  running versions, cut over, roll back, with an audit trail for every action.
- Every cutover records a downtime statistic (seconds of failed health probes
  during the switch) plus full deploy history.
- Resources dashboard shows per-container CPU/RAM/disk, restarts, and uptime
  for managed services, and alerts fire on breached thresholds.
- The touchgrass SDK captures grouped errors with stack traces from tn-api,
  container logs are searchable, and errors link to surrounding log lines.
- The tool is additive: with the tool stopped, the existing SSH/script path
  (`deploy/bluegreen-*.sh`, `deploy.yml`) still works unchanged, and with
  the SDK disabled the app behaves exactly as before.
- Fresh-host install from the README completes in under 30 minutes.
- The project is public open-source with a license file, docs, and green CI.

## Context And Current Facts

- Repos in this workspace (verified by listing): `ticketnation-main-api/`,
  `tn-fe-2025/`, `ticketnation-mobile/`, `ticketnation-admin-fe/`,
  plus `ticketpass/`, `corbin-job-portal/`, `learning-app/`.
- Stack evidence from `package.json` files: tn-api is NestJS 10 + Prisma 6;
  tn-fe-2025 is Next 16; ticketnation-mobile is Expo 54; admin-fe is React.
  The team's entire codebase and muscle memory is TypeScript.
- Production today: single EC2 host, system-level nginx reverse proxy,
  multi-tenant (tn-api, ims-be on 4001, monitoring). tn-api runs blue-green
  on ports 4101/4102 with an nginx upstream flip (prior session context).
- Proven execution engine already exists in `ticketnation-main-api/deploy/`:
  `bluegreen-cutover.sh`, `bluegreen-lib.sh`,
  `bluegreen-restore-legacy.sh`, `local-test/`, `nginx-upstream-block.md`;
  CI entry point is `ticketnation-main-api/.github/workflows/deploy.yml`;
  runbook is `docs/rollback-runbook.md`. Local test showed 0.0s cutover
  downtime vs ~6s for the old flow (prior session context).
- Already installed on the box (prior session context): Portainer (container
  state and control), Dozzle (logs). Deploy logs/history live in GitHub
  Actions runs.
- User decisions this session: project name touchgrass (vetted live via
  registry lookups and product-collision searches — free on npm, only
  collisions are small wellbeing apps in an unrelated category; runners-up
  eliminated on registry squats or real product collisions); resources =
  metrics + alerts, view-only, Portainer stays for control; logging =
  errors + logs unified; product voice = Gen Z tone.
- Prior art evaluated this run: Coolify and Dokploy (full self-hosted PaaS,
  own the whole deploy path), Semaphore UI (generic task runner, no deploy
  model), Teal (exact deploy-tool concept but 0 stars, 0 forks, 49 commits,
  single author; too immature). Uptime Kuma was discussed earlier for
  downtime proof; this tool absorbs that need, so Kuma is superseded by
  this plan, not installed alongside it.

## Constraints And Non-goals

- Must be free-tier open-source and self-hostable with a small footprint on
  the existing EC2 host. No paid SaaS, no license keys, no phone-home.
- Must be additive: existing scripts, CI workflow, and SSH procedures keep
  working with the tool stopped or uninstalled; the SDK must be fail-open
  (an SDK failure can never break or slow the host app).
- Must stay operable by a small team: boring technology, SQLite, minimal
  moving parts. No Kubernetes, no Redis, no external DB for v1.
- Retention caps are mandatory from day one: error/log volume is unbounded
  by nature, and prior session context flagged disk pressure.
- Non-goals for v1: one-click container actions (restart/stop — parked by
  the view-only resources decision), multi-host management (design for it,
  don't build it), Kubernetes, distributed tracing/APM, browser and mobile
  SDKs (Node SDK only), mobile/EAS release pipelines, a secrets manager,
  full RBAC, PR preview environments, replacing Portainer/Dozzle, Windows
  support.

## Key Decisions

1. **Build narrow vs adopt — BUILD narrow, three pillars.**
   Inspected sources show Coolify is an open-source self-hostable PaaS that
   deploys to any server over SSH and owns monitoring, notifications, SSL,
   and server automation end to end; Dokploy is a self-hosted open-source
   platform with native compose support, multi-server, monitoring, and its
   own proxy management. Adopting either means migrating tn-api's proven
   nginx-flip + compose + Actions flow onto their stack — a migration, not
   an add-on. Semaphore UI is one self-hosted place to run, share, schedule,
   and audit operational workflows (Ansible/Terraform/Bash task templates),
   but it has no deployment model (no colors, versions, cutovers). Teal is
   the closest deploy-tool concept and validates the idea, but is too
   immature to adopt. The expanded scope neighbors error-tracking and
   container-management tools, but the build case rests on one unified,
   free, self-hosted control plane instead of three separate tools. Honest
   counter: if the team ever wants to stop owning this, Coolify's free
   self-hosted tier covers the deploy pillar via migration.
2. **License — Apache-2.0.**
   Inspected all three licenses. MIT is the simplest permissive license
   (use/modify/distribute/sell, keep the notice). Apache-2.0 is equally
   permissive and adds an express patent grant plus a state-changes
   condition — strictly better protection for an infrastructure tool with
   no adoption cost. AGPL-3.0 rejected: its network-use-is-distribution and
   same-license conditions would deter external adoption.
3. **Stack — Go API + embedded UI + TS SDK, SQLite. (Supersedes the
   earlier TypeScript-API recommendation per stakeholder decision; see
   `docs/BRD-touchgrass.md`.)**
   Control plane in Go for single-binary distribution and small footprint;
   React + Vite + Tailwind SPA embedded into the binary (single artifact,
   Teal-validated model); SQLite via a pure-Go driver (no CGO) with
   versioned SQL migrations; the SDK stays TypeScript (`@touchgrass/node`)
   because the host apps are Node. Consequence: Go is a new competency for
   a TypeScript team — mitigated by a narrow API surface and stdlib-first
   code, recorded as a risk below.
4. **Architecture — agentless control plane v1, SDK ingestion, agents later.**
   v1 runs on (or next to) the target host and drives the Docker socket plus
   the existing cutover scripts as its deploy engine; apps report errors to
   an ingestion endpoint via the SDK using per-project API keys. No agent to
   install; matches the single-EC2 reality. Multi-host agents become
   Phase 8. Retention caps and sampling live in the ingestion path, not as
   an afterthought.
5. **Engine reuse — wrap, don't rewrite.**
   `bluegreen-*.sh` remain the source of truth for cutover semantics; the
   tool shells out, parses results, and records history. This preserves the
   SSH fallback and the already-proven 0.0s cutover behavior.
6. **v1 pillar scope — generic engine day one; deploy full, resources
   view-only, logs unified.**
   The deploy engine is strategy-based and generic from day one — no
   per-service hardcoding. tn-api (blue-green) is the first live service
   and the SDK dogfood target; a second strategy (recreate) is proven on
   another service in the MVP. Resources stays metrics + alerts with no
   control actions; logging ships error tracking and log search together,
   linked.
7. **Name — touchgrass.** Picked by the user from a live-vetted shortlist:
   free on npm with no dev-infra collision (vetting detail in session
   chat). Fits the platform story ("go touch grass, we got prod") better
   than any deploy-only name.
8. **Product voice — Gen Z tone, credible core.** Status and empty-state
   copy leans playful (vibe checks, cooking deploys); error messages,
   audit entries, and docs stay precise. Voice never obscures meaning.

## Recommended Approach

Build in eight phases. MVP is Phases 0–3 (deploy pillar usable against
tn-api); v1 adds Phases 4–5 (logging pillar). Each phase ends in a usable
increment on the real EC2 host. Assumptions (reversible): solo or
small-team effort with AI assistance; GitHub Actions remains the CI that
builds images while the tool orchestrates and records; the tool itself
ships as a single Go binary (UI embedded, SQLite file) run via compose on
the same EC2 host, localhost-bound behind auth.

- **Phase 0 — Scaffold (2–3 person-days).**
  New repo, Apache-2.0 LICENSE, skeleton (Go module + SPA + sdk folder
  reserved), CI (Go toolchain: vet, lint, test, build; plus SPA checks),
  docs skeleton, ADRs for decisions 1–8.
- **Phase 1 — Read-only observability (6–9 person-days).**
  Service inventory read from compose/Docker (containers, images, SHAs,
  ports), live-color detection for tn-api (nginx upstream parse + probe),
  per-container metrics (CPU/RAM/disk, restarts, uptime), health dashboard,
  deploy history (record cutovers going forward; backfill notes manually).
  No execution: cannot change anything yet.
- **Phase 2 — Execution + audit (5–8 person-days).**
  One-click cutover and rollback that shell out to the existing scripts,
  confirmation gate, auth (single admin credential v1), append-only audit
  log of every action with actor + timestamp + result.
- **Phase 3 — Downtime stats + notifications (3–5 person-days).**
  Probe loop during every cutover (reuse the local-test probe approach),
  per-deploy downtime seconds stored with the deploy record, trend view,
  outbound webhooks + email on deploy start/finish/failure.
- **Phase 4 — Error tracking (8–12 person-days).**
  `@touchgrass/node` SDK (fail-open, async, sampled), ingestion API with
  per-project keys, grouping/fingerprinting, issue view, alert rules,
  retention caps; dogfood by wiring tn-api staging, then production.
- **Phase 5 — Log aggregation + unification (5–8 person-days).**
  Container log collection and search, errors linked to surrounding log
  lines, shared retention enforcement.
- **Phase 6 — Full onboarding: tn-fe-2025 + admin-fe (4–8 person-days).**
  Graduate both services (admin-fe's recreate strategy already proven in
  the MVP): per-service health endpoints, metrics, history, and runbooks.
- **Phase 7 — OSS launch hardening (3–5 person-days).**
  README quickstart (fresh host < 30 min), demo compose stack, contributing
  guide, security notes (privileged-access warnings), public repo hygiene.
- **Phase 8 — Parked (not in v1).**
  One-click container actions, multi-host agents, RBAC, secrets management,
  tracing/APM, browser/mobile SDKs, PR previews.

Rough totals: MVP (Phases 0–3 + generic-engine proof) 17–26 person-days (~4–6 calendar weeks
part-time); full v1 (through Phase 5) 30–46 person-days (~7–11 calendar
weeks part-time). Estimates are assumptions, not commitments.

## Validation Plan

- **Phase 0:** `verify` (type-check + lint) and `test` pass in CI on the new
  repo; reviewer confirms LICENSE + README skeleton. Highest-risk step in
  the whole plan is Phase 2's first real cutover (below), not scaffolding.
- **Phase 1:** dashboard shows tn-api blue/green container IDs, image SHAs,
  and live color matching `docker ps` + nginx upstream + a manual
  `/health` probe; metrics match `docker stats` within sampling tolerance;
  history page records a manually-run script cutover.
- **Phase 2 (highest risk):** trigger a same-SHA cutover from the UI on EC2
  at low traffic; confirm traffic flips, audit entry appears, then trigger
  rollback from the UI and confirm restore. Abort check: stop the tool
  mid-phase and confirm the SSH/script path still works (additivity).
- **Phase 3:** run a UI cutover and confirm the recorded downtime seconds
  match an independent probe log within ±1s; confirm webhook + email fire
  on success and on a forced-failure cutover (unhealthy image).
- **Phase 4:** forced exception in tn-api staging appears grouped in the UI
  within 60s and fires its alert; SDK-failure chaos check (ingestion down)
  proves the app is unaffected (fail-open); retention cap trims old issues
  on schedule.
- **Phase 5:** log search returns surrounding lines for a linked error;
  retention enforcement holds total log/error storage under the configured
  cap after a volume soak.
- **Phase 6:** each onboarded service shows health + metrics + history; its
  UI deploy matches the documented manual procedure output.
- **Phase 7:** fresh EC2-class VM follows README only: installed, connected,
  showing inventory in < 30 min (timed manual check); license checker
  confirms Apache-2.0 headers.

## Risks / Rollback

- **You own it forever, times three pillars (maintenance burden).**
  Mitigation: narrow per-pillar scope, stdlib-first Go with a small API
  surface, scripts stay the deploy engine, each pillar is usable alone so
  any one can rot without breaking the others.
- **Go is new to a TypeScript team.** Mitigation: idiomatic stdlib-first
  code, SPA and SDK stay in team-familiar TypeScript, ADRs record every
  non-obvious Go choice; revisit if hiring/velocity suffers.
- **SDK in the production request path.** A buggy SDK could slow or crash
  tn-api. Mitigation: fail-open by construction (async, no sync IO, guarded
  init), staging dogfood before production, chaos check in validation.
- **Unbounded error/log volume vs disk.** Mitigation: retention caps and
  sampling in the ingestion path from day one, enforced and validated, not
  documented-and-forgotten.
- **PII in errors/logs.** Mitigation: scrub hooks in the SDK, self-hosted
  storage (no third-party exfil), auth from day one, documented access
  notes in Phase 7.
- **Privileged access.** The tool holds the Docker socket and runs deploy
  scripts: root-equivalent. Mitigation: bind localhost only, require auth
  from day one, no secrets in logs, append-only audit, documented threat
  notes in Phase 7.
- **Scope creep into a mini-Coolify.** Mitigation: Non-goals list is binding;
  Phase 8 items need a new plan to start.
- **Single-host assumptions baked in.** Mitigation: service/host config is
  data (Phase 1 model), so agents later don't require rewrites.
- **OSS support burden after launch.** Mitigation: launch-oriented docs are
  Phase 7, after the tool proves itself internally; issues template sets
  expectations.
- **Rollback:** uninstall is `docker compose down` + delete one volume;
  remove the SDK import to de-instrument apps. All deploy capability
  reverts to today's scripts/SSH/CI with zero migration.

## Open Questions (resolved 2026-09-21)

| # | Answer |
| - | ------ |
| Q1 scope | Generic strategy engine from day one (no per-service hardcoding); tn-api first live service, second strategy proven in MVP |
| Q2 hosting | Same EC2 box |
| Q3 org | `oralecarlangelo` → module `github.com/oralecarlangelo/touchgrass` |
| Q4 notifications | In-app web notifications first; webhooks/email post-v1 |
| Q5 auth | Single admin credential |
| Q6 ambition | Us-first with OSS hygiene |

## Sources

- https://choosealicense.com/licenses/mit/
- https://choosealicense.com/licenses/apache-2.0/
- https://choosealicense.com/licenses/agpl-3.0/
- https://coolify.io/
- https://dokploy.com/
- https://semaphoreui.com/
- https://github.com/sariakos/teal
