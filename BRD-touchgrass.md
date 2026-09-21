# BRD — touchgrass: Self-Hosted Server-Management Platform

- Version: 0.1 (Draft)
- Date: 2026-09-21
- Status: Approved 2026-09-21
- Related: `plans/16-custom-deploy-manager-scope.md` (technical scope plan)
- Decisions locked: name = touchgrass; stack = Go + SQLite; license = Apache-2.0; org = oralecarlangelo

## 1. Background & Purpose

TicketNation runs production on a single EC2 host (system nginx, Docker
Compose, multi-tenant) with deployments via GitHub Actions. tn-api recently
gained script-driven blue-green deploys (0.0s cutover proven locally), but
there is no unified place to see services, cut over with one click, track
resource health, or capture app errors and logs. touchgrass is a custom,
open-source, self-hosted control plane that unifies all three so the team
can "go touch grass" while prod runs itself.

## 2. Objectives

1. One dashboard for every managed service: version, health, deploy history.
2. One-click blue-green cutover and rollback with a full audit trail.
3. Container metrics + alerts (view-only in v1).
4. Sentry-style error tracking (lightweight SDK) unified with log search.
5. Zero new paid SaaS; runs on the existing EC2 host with a tiny footprint.
6. Shippable open-source: install from README in under 30 minutes.

## 3. Scope

### 3.1 In scope (v1)

- Pillar 1 — Deploys: service inventory, blue-green cutover/rollback,
  deploy history, per-deploy downtime stats, notifications.
- Deploy engine is strategy-based and generic from day one: no
  per-service hardcoding. tn-api (blue-green) is the first live service;
  a second strategy (recreate) is proven on another service in the MVP.
- Pillar 2 — Resources: per-container CPU/RAM/disk, restarts, uptime,
  threshold alerts. View-only (no restart/stop actions).
- Pillar 3 — Logs: `@touchgrass/node` SDK error tracking (grouped issues,
  stack traces, alerts) + container log search, errors linked to
  surrounding log lines.
- Platform: single-credential auth, append-only audit log, outbound
  webhooks + email, retention caps enforced from day one.

### 3.2 Out of scope (v1, parked)

One-click container actions, multi-host agents, Kubernetes, distributed
tracing/APM, browser/mobile SDKs, secrets management, full RBAC, PR
preview environments, replacing Portainer/Dozzle, Windows support.

## 4. Users & Stakeholders

| User | Needs |
| ---- | ----- |
| On-call dev | See what's live, cut over / roll back fast, get paged on failure |
| Backend dev | Grouped errors with traces linked to logs; deploy history |
| Team lead | Audit trail, downtime stats per deploy, OSS asset |
| OSS adopter | 30-min install, single binary, no paid deps |

## 5. Functional Requirements

### Pillar 1 — Deploys

- FR-D1: Dashboard lists managed services with running version (image SHA),
  live color (blue/green), and health status.
- FR-D2: One-click cutover wraps the existing `bluegreen-*.sh` engine
  (wrap, don't rewrite) behind a confirmation gate.
- FR-D3: One-click rollback restores the previous color via existing scripts.
- FR-D4: Every action writes an append-only audit entry (actor, timestamp,
  action, result).
- FR-D5: Deploy history per service with SHA, actor, duration, outcome.
- FR-D6: Every cutover records downtime seconds (failed health probes
  during the switch) with a trend view.
- FR-D7: In-app web notifications on deploy start/finish/failure in v1;
  webhook + email sinks are post-v1 enhancements.

### Pillar 2 — Resources

- FR-R1: Per-container CPU/RAM/disk usage, restart counts, uptime.
- FR-R2: Threshold alert rules (e.g. RAM > 85% for 5m) firing to
  notifications.
- FR-R3: Read-only: the UI exposes no restart/stop/exec actions in v1.

### Pillar 3 — Logs

- FR-L1: `@touchgrass/node` SDK captures uncaught exceptions and manual
  reports with stack traces, breadcrumbs, release/SHA tags.
- FR-L2: SDK is fail-open: async, no sync IO, guarded init; SDK or
  ingestion failure must never break or slow the host app.
- FR-L3: Ingestion API authenticates per-project API keys; applies
  sampling and retention caps at write time.
- FR-L4: Issues are fingerprinted and grouped; issue view shows trace,
  frequency, affected releases, linked log lines.
- FR-L5: Container logs are collected, searchable, and retained under a
  configured cap.
- FR-L6: Scrub hooks strip configured PII patterns before storage.

### Platform

- FR-P1: Day-one auth: single admin credential (bcrypt + sessions).
- FR-P2: All state in one SQLite file; backup = copy the file.
- FR-P3: Binds localhost by default; never listens publicly without
  explicit config.
- FR-P4: Product voice: Gen Z tone in status/empty-state copy; audit
  entries, errors, and docs stay precise.

## 6. Non-Functional Requirements

- NFR-1 Self-hosted: runs on the existing single EC2 host; no paid SaaS,
  no license keys, no phone-home.
- NFR-2 Footprint: idle RAM well under 512MB; disk bounded by retention
  caps at all times.
- NFR-3 Distribution: single Go binary (UI embedded) + compose file;
  cross-platform releases via GoReleaser.
- NFR-4 Reliability: SDK fail-open (FR-L2); control-plane outage must not
  affect serving traffic or the SSH/script fallback path.
- NFR-5 Security: auth from day one, no secrets in logs, per-project
  ingestion keys, documented threat model before OSS launch.
- NFR-6 Usability: fresh-host install from README in < 30 minutes;
  voice never obscures meaning.
- NFR-7 Maintainability: stdlib-first Go, narrow API surface, ADRs for
  non-obvious choices; SPA and SDK stay in team-familiar TypeScript.

## 7. Tech Stack & Architecture Direction

| Layer | Choice | Rationale |
| ----- | ------ | --------- |
| Control plane | Go | Single static binary, tiny footprint, strong stdlib; stakeholder decision |
| Database | SQLite (pure-Go driver, no CGO) | Zero ops, single-file backup, preserves cross-compilation |
| Migrations | Versioned SQL via a Go migration tool | Reviewable, reproducible schema changes |
| UI | React + Vite + Tailwind SPA, embedded in the binary | Team-familiar; Teal-validated single-artifact model |
| SDK | TypeScript `@touchgrass/node`, zero-dep | Host apps are Node — a Go SDK cannot instrument them |
| Live streams | SSE | One-way metric/log tails without extra deps |
| Container metrics | Docker socket via the official Docker Go client | No agent to install |
| Ingestion | HTTPS JSON + per-project API keys | Minimal, debuggable |
| Releases | GoReleaser + GHCR/Docker Hub image + compose file | One-liner install |

Consequence: two languages in the repo (Go control plane, TypeScript
SPA + SDK). Accepted: each is used where the team and ecosystem are
strongest.

## 8. Data & Retention

- All state (services, deploys, metrics rollups, issues, logs, audit) in
  one SQLite file with a documented schema.
- Retention caps (max age + max bytes per data class) are enforced by the
  ingestion path on a schedule — mandatory, not advisory.
- PII scrubbing happens in the SDK before network send (FR-L6).

## 9. Assumptions & Dependencies

1. GitHub Actions remains the CI that builds app images; touchgrass
   orchestrates and records (does not replace builds).
2. Existing `bluegreen-*.sh` scripts remain the cutover engine (v1).
3. Single EC2 host is the v1 target; service/host config is data so
   multi-host agents don't require rewrites later.
4. tn-api (staging, then production) is the dogfood target for deploys,
   metrics, and the SDK.
5. Solo/small-team effort with AI assistance; part-time estimates.

## 10. Constraints

- Free-tier open-source only; Apache-2.0 license.
- Additive: every capability the tool provides must still work via
  today's scripts/SSH/CI with the tool stopped.
- No Kubernetes, Redis, or external DB in v1.
- Go is new to a TypeScript team: stdlib-first, narrow surface.

## 11. Risks

| Risk | Mitigation |
| ---- | ---------- |
| Go learning curve slows delivery | Narrow API surface, stdlib-first, TS for SPA/SDK, ADRs |
| Buggy SDK harms tn-api in prod | Fail-open construction, staging dogfood first, chaos check in validation |
| Unbounded error/log volume vs disk | Retention caps + sampling in ingestion path from day one |
| PII in errors/logs | SDK scrub hooks, self-hosted storage, auth from day one |
| Privileged access (Docker socket) | Localhost-only bind, auth, audit log, threat notes pre-launch |
| Three-pillar maintenance burden | Each pillar usable alone; non-goals list is binding |

## 12. Milestones

| Phase | Content | Rough effort |
| ----- | ------- | ------------ |
| 0 | Scaffold: repo, LICENSE, Go + SPA + SDK skeleton, CI, docs, ADRs | 2–3 pd |
| 1 | Read-only: inventory, live color, metrics, health, history | 6–9 pd |
| 2 | Execution: UI cutover/rollback, auth, audit (highest risk) | 5–8 pd |
| 3 | Downtime stats + notifications | 3–5 pd |
| 4 | Error-tracking SDK + ingestion + issues + dogfood | 8–12 pd |
| 5 | Log aggregation + error↔log unification | 5–8 pd |
| 6 | Onboard tn-fe-2025 + admin-fe | 5–10 pd |
| 7 | OSS launch hardening | 3–5 pd |
| 8 | Parked: actions, multi-host, RBAC, tracing, more SDKs | — |

MVP (0–3): ~16–25 person-days (~3–6 weeks part-time).
Full v1 (through 5): ~29–45 person-days (~6–11 weeks part-time).

## 13. Acceptance Criteria (v1)

1. tn-api cutover + rollback fully operable from UI with audit entries.
2. Downtime seconds recorded per cutover, matching an independent probe
   within ±1s.
3. Metrics match `docker stats` within sampling tolerance; alerts fire.
4. Forced staging exception appears grouped in UI within 60s; ingestion
   outage leaves the app unaffected.
5. Errors link to surrounding log lines; storage stays under caps.
6. Tool stopped → SSH/script path works; SDK removed → app unchanged.
7. Fresh-host README install < 30 minutes, timed.
8. Repo public with Apache-2.0, docs, green CI.

## 14. Open Questions (resolved 2026-09-21)

| # | Answer |
| - | ------ |
| Q1 scope | Generic strategy engine from day one (no per-service hardcoding); tn-api first live service, second strategy proven in MVP |
| Q2 hosting | Same EC2 box |
| Q3 org | `oralecarlangelo` → module `github.com/oralecarlangelo/touchgrass` |
| Q4 notifications | In-app web notifications first; webhooks/email post-v1 |
| Q5 auth | Single admin credential |
| Q6 ambition | Us-first with OSS hygiene |

## 15. Glossary

- **Cutover**: the moment traffic flips from one color to the other.
- **Blue/green**: two identical stacks; one serves traffic (live), one
  stages the next version.
- **Dogfood**: running touchgrass against our own production first.
- **Fail-open**: SDK/ingestion failure degrades to "no data", never
  "broken app".
- **Vibe check**: touchgrass slang for a service health check.
