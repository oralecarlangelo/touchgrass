# touchgrass Sprints

Derived from `BRD-touchgrass.md` (requirements), the scope plan
(`16-custom-deploy-manager-scope.md`, phases), and the repo standards
(`AGENTS.md`, `docs/ARCHITECTURE.md`, `docs/PROJECT_STRUCTURE.md`).

## How sprints run here

- **Cadence**: 1 week, ~4 person-days capacity (part-time + AI). Adjustable;
  spillover rolls to the next sprint rather than expanding scope.
- **Story format**: `S<n>.<i> Title (est. person-days) [BRD refs]` +
  acceptance bullets. Every story must satisfy the global Definition of Done.
- **Each sprint ends in a usable increment on the real EC2 host** (from
  Sprint 2 on) or a provable local artifact (Sprint 1).
- **Standards in play**: every sprint lists the conventions it exercises.
  `AGENTS.md` musts (style, naming, errors, tests, lint) apply to all code,
  all sprints, no exceptions.
- **Traceability**: `FR-*` = BRD functional requirements, `NFR-*` =
  non-functional, `AC-*` = BRD acceptance criteria.

## Definition of Done (global)

1. `make vet`, `make lint`, `make test`, `make test-race` green (Go);
   `npm run verify` green (TS side).
2. New behavior covered per testing standard (table-driven, named subtests;
   integration tag for DB/Docker tests).
3. Docs touched if behavior changed (`docs/ARCHITECTURE.md`,
   `docs/PROJECT_STRUCTURE.md`, README); ADR written for any decision
   that overrides these standards.
4. Demo-able: sprint goal provable on EC2 (or locally for S1) per the
   sprint's validation steps.

## Open questions → sprint impact

| Question | Answer (resolved 2026-09-21) |
| -------- | ------------------------------ |
| Q1 day-one scope | Generic strategy engine from day one; tn-api first, second strategy proven in S6 |
| Q2 hosting | Same EC2 box |
| Q3 GitHub org | `oralecarlangelo` → `github.com/oralecarlangelo/touchgrass` |
| Q4 notifications | In-app web notifications first; webhooks/email post-v1 |
| Q5 auth | Single admin credential |
| Q6 ambition | Us-first with OSS hygiene |

## MVP track (S1–S6) — deploy pillar usable

### Sprint 1 — Scaffold (Phase 0, 2–3pd)

**Status**: DONE (2026-09-21). All stories + validation green locally;
`version` prints version + SHA; `/api/health` live-proven (200/404/405).

**Goal**: empty-but-real repo: builds, lints, tests, CIs, and runs.

- S1.1 Repo skeleton + `go.mod` placeholder (0.5pd) [NFR-3]
  Acceptance: tree matches `docs/PROJECT_STRUCTURE.md`; `go build ./...`
  passes; module path placeholder documented in README.
- S1.2 Root files: LICENSE, README, Makefile, `.golangci.yml`,
  `.gitignore`, Dockerfile, compose (1pd) [NFR-3, NFR-6]
  Acceptance: `make build/test/lint/fmt/vet` all run; lint config from
  the `golang-lint` recommended set; README quickstart drafted.
- S1.3 CI + release workflows (0.5pd)
  Acceptance: `ci.yml` (vet+lint+test `-race`+build) green on push;
  `release.yml` skeleton present (wired fully in S14).
- S1.4 ADRs for decisions 1–8 + `touchgrass version` (0.5pd)
  Acceptance: `docs/adr/` seeded; binary prints version + SHA;
  `touchgrass serve` boots an empty server with `/api/health` 200.
- Standards in play: `golang-project-layout`, `golang-lint`,
  `golang-continuous-integration`.
- Validation: fresh clone → `make build && make test && make lint`
  green; binary runs and answers `/api/health`.

### Sprint 2 — Inventory + live color + health (Phase 1a, ~4pd)

**Status**: DONE (2026-09-21, code-complete). All stories implemented;
`vet`/`lint`/`test`/`test-race`/`test-int`/`audit`/web `verify` green;
`migrate up`/`status` proven on a real file DB; `/api/services`,
request logging, SPA embed + dev proxy covered by tests. EC2 validation
pending (no Docker/nginx on this box): on EC2, `make build &&
./bin/touchgrass serve`, then compare the dashboard with `docker ps`,
`grep BLUEGREEN-ACTIVE /etc/nginx/sites-available/ticketnation`, and
direct `/health` curls in both live colors.

**Goal**: dashboard shows what's actually running and which color is live.

- S2.1 Docker inventory: containers, images, SHAs, ports (1.5pd) [FR-D1]
  Acceptance: `GET /api/services` lists tn-api blue/green with correct
  SHAs matching `docker ps`; store persists services.
- S2.2 Live-color detection: nginx upstream parse + direct probe (1pd) [FR-D1]
  Acceptance: reported color matches the nginx `tn_api_active` target
  and a manual `/health` probe in both blue-live and green-live states.
- S2.3 Health dashboard UI (1pd) [FR-D1]
  Acceptance: SPA shows services, versions, live color, health; embedded
  and served by the binary; dev-proxy mode works (`make dev`).
- S2.4 Request logging middleware + slog baseline (0.5pd) [NFR-5]
  Acceptance: every request logs method/path/status/duration as JSON.
- Standards in play: layering (`http`→`service`→`store`), `golang-database`,
  `golang-error-handling`, `httptest` handler tests.
- Validation: dashboard matches `docker ps` + nginx + manual probe.

### Sprint 3 — Metrics + history (Phase 1b, ~4pd)

**Status**: DONE (2026-09-21, code-complete). Sampler (docker stats +
retention trim), threshold alerts with edge-triggered in-app
notifications (Q4 applied: no webhook stub), deploy history + manual
record, metrics/alerts/history APIs + dashboard detail view — all gated
green (`lint`/`vet`/`test-race`/`test-int`/`audit`/web `verify`,
goleak-clean). EC2 validation pending: real-daemon stats mapping (run
`make test-int` on the box) and sampler output vs `docker stats`.

**Goal**: resources visible; deploy history recording.

- S3.1 Container metrics collection (1.5pd) [FR-R1]
  Acceptance: CPU/RAM/disk, restarts, uptime sampled on a schedule and
  stored as rollups; values match `docker stats` within tolerance.
- S3.2 Metrics UI + threshold alerts (1.5pd) [FR-R1, FR-R2]
  Acceptance: per-service metrics view; breached rule fires a
  notification (webhook sink stubbed until S6 wiring).
- S3.3 Deploy history recording (1pd) [FR-D5]
  Acceptance: manually-run script cutovers can be recorded; history API
  + UI list exist (auto-record lands with S4).
- Standards in play: `golang-concurrency` + `golang-context` (sampler
  loops), `goleak`, read-only guarantee (no control actions anywhere).
- Validation: metrics match `docker stats`; history records a manual cutover.

### Sprint 4 — Cutover + auth (Phase 2a, ~4pd)

**Status**: DONE (2026-09-21, code-complete). Bcrypt-hashed admin sessions
gate every API route except health/login; the UI requires login and handles
session expiry. Confirmation-gated cutover POSTs, streams script progress
over SSE, records history/notifications, and resolves `auto` to idle; env
config is validated at boot and SIGTERM drains HTTP plus schedulers. Gated
green (`lint`/`vet`/`test`/`test-race`/`test-int`/`audit`/web `verify`).
EC2 validation pending: same-SHA UI cutover at low traffic, traffic flip,
and abort/SSH-path check.

**Goal**: authorized one-click cutover to the idle color.

- S4.1 Auth: single admin credential + sessions (1pd) [FR-P1, NFR-5]
  Acceptance: all mutating routes + UI require login; localhost bind by
  default; bcrypt-hashed credential.
- S4.2 Cutover execution wrapping `bluegreen-cutover.sh` (2pd) [FR-D2]
  Acceptance: UI cutover (with confirmation gate) shells out, streams
  progress, and flips traffic exactly like the script; pre-flip failure
  leaves traffic untouched.
- S4.3 Config + graceful shutdown hardening (1pd) [NFR-4]
  Acceptance: env-only config validated at boot; SIGTERM drains HTTP
  and stops schedulers cleanly.
- Standards in play: engine-reuse rule (wrap, don't rewrite),
  `golang-security` (auth review), user-safe error translation at the
  HTTP boundary.
- Validation (staging-first): same-SHA cutover on EC2 at low traffic;
  traffic flips; abort check (tool stopped → SSH path works).

### Sprint 5 — Rollback + audit (Phase 2b, ~3pd)

**Status**: DONE (2026-09-21, code-complete). `POST
/api/services/{id}/rollback` reuses the cutover engine to flip to the
opposite live color, verifies the target with a post-run health probe,
and records history + notifications + audit; every login (success,
failure, rate-limited) is audited too. `GET /api/audit` and the Audit
nav view expose the append-only log (insert/list only, no update,
delete, or retention path). Failure paths covered: script failure
(skips health check), unhealthy target (marks failure), legacy/unknown
live color rejects, shared cutover/rollback conflict guard. Gated green
(`lint`/`vet`/`test`/`test-race`/`test-int`/`audit`/web `verify`).
EC2 validation pending: full cutover → rollback loop, audit shows both,
flip-back via forced unhealthy-image cutover.

**Goal**: one-click rollback; every action audited. Highest-risk prove-out.

- S5.1 Rollback execution (1.5pd) [FR-D3]
  Acceptance: UI rollback restores the previous color via existing
  scripts; verified healthy post-rollback.
- S5.2 Append-only audit log (1pd) [FR-D4]
  Acceptance: every cutover/rollback/login writes actor + timestamp +
  action + result; audit API + UI; entries immutable.
- S5.3 Auto history recording (0.5pd) [FR-D5]
  Acceptance: UI-driven cutovers auto-record SHA/actor/duration/outcome.
- Standards in play: audit-immutability review, failure-path tests
  (flip-back behavior), `golang-samber-oops` for rich error context.
- Validation: full loop on EC2 — cutover → rollback → audit shows both;
  flip-back path exercised by a forced unhealthy-image cutover.

### Sprint 6 — Downtime stats + notifications + generic proof (Phase 3, 4–6pd) → MVP DONE

**Status**: DONE (2026-09-21, code-complete). The cutover engine is now
generic: one async run/verify/record path serves blue-green cutover,
rollback (both strategies), and recreate deploy via `POST
/api/services/{id}/deploy`; strategy differences end at resolution
(`launchRecreate` shares the execute path, zero tn-api-specific branches).
Every run probes the public URL on a 1s loop, stores per-sample rows
(`deploy_probes`, retention-trimmed with deploys), and records
`downtime_secs` (`nil` without a public URL). Web: notification center
(feed, unread state, per-service filter, mark read/all), recreate Deploy
panel, strategy-aware Rollback panel, per-service downtime trend + per-row
downtime. `ServiceStore.UpdateConfig` is the ADR-0006 "edit these rows"
path. S6.4 proof: `TestRecreateProofAdminFE` drives seeded admin-fe
deploy → rollback → history through the public API. Gated green
(`lint`/`vet`/`test`/`test-race`/`test-int`/`audit`/`build`/web `verify`).
EC2-VALIDATED (2026-09-22, S6.3). AC-1: deploy rows 1–3
(cutover/cutover/rollback, all success, downtime 0) plus clean-image
cutover id 8 (09:40:22–09:42:25Z, green→blue, downtime 0), every run
with a matching audit entry (actor admin). AC-2: recorded 0s vs an
independent 0.2s public-URL prober — 798 samples, 0 non-200 across
the cutover window. AC-6 script path: with touchgrass stopped, the
cutover script flipped blue↔green over SSH (public 200 throughout,
120 probe samples, 0 non-200); the SDK-removal half is still pending.
**MVP milestone validated on EC2.**

**Goal**: every cutover proves its own downtime; team gets notified.

- S6.1 Cutover probe loop + downtime stat (1.5pd) [FR-D6]
  Acceptance: probe during UI cutover; recorded seconds match an
  independent probe log within ±1s; trend view per service.
- S6.2 In-app notification center (1.5pd) [FR-D7]
  Acceptance: deploy start/finish/failure and alert breaches appear in
  a web notification feed (unread state, per-service filter); external
  webhooks/email deferred post-v1.
- S6.3 MVP hardening pass (1pd) [AC-1, AC-2, AC-6]
  Acceptance: BRD acceptance criteria 1, 2, 6 verified end-to-end on EC2.
- S6.4 Generic-engine proof: second strategy live (1–2pd) [FR-D2, FR-D3]
  Acceptance: admin-fe driven end-to-end through the generic engine on
  the recreate strategy (deploy + rollback + history); zero tn-api-
  specific code paths added to do it.
- Standards in play: `golang-observability`, retention caps on probe data.
- Validation: AC-1/2/6 checklist green. **MVP milestone.**

## v1 track (S7–S11) — logging pillar

### Sprint 7 — SDK + ingestion (Phase 4a, ~4pd)

**Status**: DONE (2026-09-21, code-complete). `@touchgrass/node`
zero-dep SDK: uncaught exceptions/rejections + manual reports with V8
traces, breadcrumb ring, release tags, client-side PII scrub; async
bounded queue, unref'd flush timer, 5s abort timeouts, crash semantics
preserved (host listener owns outcome, else print + bounded flush +
exit 1). Ingestion: `POST /api/ingest` (public, `X-Touchgrass-Key`,
1MB cap, 202 `{sampled}`), per-service `tg_` keys (SHA-256 hashed,
prefix-only logs, revocable) via admin `POST/GET /api/keys` +
`POST /api/keys/{id}/revoke`; crypto sampling + per-service count cap
at write time, age trim on schedule (`TOUCHGRASS_INGEST_MAX_OCCURRENCES`,
`TOUCHGRASS_RETENTION_ERRORS`); bad keys 401 without logging payloads
(pinned by test). Fail-open proven by Vitest chaos suite (down/hanging
ingestion, garbage inputs, exit paths). Validation: forced exception
from the real built SDK replayed byte-for-byte through the real handler
→ 202 → stored row with trace + crumb (live-socket run blocked by
sandbox; socket path is stdlib both ends and rides S9 dogfood). Gated
green (`lint`/`vet`/`test`/`test-race`/`test-int`/`audit`/`build`/web
`verify`/sdk `verify`, 28 SDK tests).

**Goal**: errors flow from app to touchgrass.

- S7.1 `@touchgrass/node` SDK core (2pd) [FR-L1, FR-L2]
  Acceptance: captures uncaught exceptions + manual reports (traces,
  breadcrumbs, release tags); zero dependencies; Vitest suite green.
- S7.2 Ingestion API + project keys (1.5pd) [FR-L3]
  Acceptance: keyed HTTPS ingest; sampling + retention caps applied at
  write time; bad keys rejected without logging payloads.
- S7.3 SDK fail-open proof (0.5pd) [FR-L2, NFR-4]
  Acceptance: chaos check — ingestion down/slow ⇒ host app unaffected
  (latency + behavior identical, SDK errors swallowed safely).
- Standards in play: SDK fail-open rule, PII-scrub hooks (FR-L6),
  `golang-security` review of ingest surface.
- Validation: forced exception in a scratch app appears in store <60s.

### Sprint 8 — Issues + alerts (Phase 4b, ~4pd)

**Status**: DONE (2026-09-21, code-complete). Ingest groups every report
by fingerprint (sha256 of service + type + templated message + top-3
frames; ids/uuids/emails/hex/quoted strings normalized, releases and
deep frames excluded) via transactional upsert; per-issue live counts
(computed, never drifted) and release tracking. Alerts: `new_issue`
(one-shot latch) and `spike` (edge-triggered window count) rules fire
into notifications, evaluated on the sample cycle. Retention verified:
occurrences age-trim then issues trim on schedule (wiring test with
nanosecond retention, not just config). Fingerprint fuzzed (837k execs,
no crashers). Web Issues view: per-service list, detail with trace +
14-day frequency strip + releases + occurrence list, rule management.
Validation: 300-report synthetic storm → 3 issues × 100 with both
releases; under a 50-row cap rows stay capped while groups survive.
Gated green (`lint`/`vet`/`test`/`test-race`/`test-int`/`audit`/`build`/web
`verify`).

**Goal**: grouped, actionable, alerting issues.

- S8.1 Fingerprinting + grouping (1.5pd) [FR-L4]
  Acceptance: same-bug occurrences group; distinct bugs don't; release
  tags tracked per issue.
- S8.2 Issue view UI (1.5pd) [FR-L4]
  Acceptance: trace, frequency, affected releases, occurrence list.
- S8.3 Issue alerts + retention enforcement (1pd) [FR-D7, NFR-2]
  Acceptance: new-issue / spike rules fire; retention job trims old
  issues on schedule (verified, not just configured).
- Standards in play: low-cardinality grouping templates, `golang-testing`
  (fuzz the fingerprint function).
- Validation: synthetic error storm groups correctly and stays under caps.

### Sprint 9 — Dogfood tn-api (Phase 4c, 2–4pd)

**Status**: S9.1 LIVE (2026-09-22); S9.2 pending a week of volume.
No staging stack exists on the box, so idle blue stood in for staging:
dual ESM/CJS SDK build (CommonJS `require` fix) instrumented on idle
blue, forced manual + uncaught probes grouped into issues with
`new_issue` notifications (release v1.0.0+274.42309b86), then a clean
image without probe routes rebuilt and cut to prod (deploy id 8,
downtime 0, no new issues after cutover). Runbook §2 corrected along
the way: containerized apps must use the public base URL
(`https://infra.ticketnation.ph`), never loopback. Remaining: S9.2
tuning after a week of real volume (sampling, scrub, thresholds).

**Goal**: touchgrass watches real production traffic.

- S9.1 Wire tn-api staging, then production (1.5pd)
  Acceptance: staging errors visible within a day of traffic; prod
  wired only after staging is quiet and stable.
- S9.2 Tune: sampling, scrub patterns, alert thresholds (1pd)
  Acceptance: noise reviewed with the team; caps holding after a week
  of real volume.
- Standards in play: production caution (staging-first), PII review of
  captured payloads.
- Validation: BRD AC-4 green.

### Sprint 10 — Log aggregation (Phase 5a, ~4pd)

**Status**: DONE (2026-09-21) + EC2-VALIDATED (2026-09-21).
`LogCollector` tails every managed container on
`TOUCHGRASS_LOG_POLL_INTERVAL` (5s), parsing the multiplexed Docker
stream with max-stamp cursors, skip-already-seen filtering (correct
even if the daemon ignores `since`), inclusive-boundary dedupe, and
cursor pruning for dead containers. Backpressure is three-deep:
2k-line poll cap (drops counted + logged), 8KB line truncation,
`TOUCHGRASS_LOGS_MAX_LINES` per-service cap (newest kept), plus
`TOUCHGRASS_RETENTION_LOGS` in the sampler sweep. Storage is FTS5
(`log_lines_fts` + triggers, multiword AND, special chars as terms)
with the MATCH in a planner-friendly subquery (57s → 0.27s at 100k
rows); `GET /api/logs` searches with service/text/time filters and
`GET /api/logs/{id}` returns ±20 surrounding lines. Web Logs view:
service selector, text search, 5s live-tail (pauses while searching),
stderr highlighting, click-to-context panel. Gated green
(`lint`/`vet`/`test`/`test-race`/`test-int`/`audit`/`build`/web
`verify`). Live proof (no staging exists on the box, so adapted):
404-marker emitted into prod tn-api logs found via search +5s after
emit, 3/3 lines (`docs/dogfood-runbook.md` §8). EC2 also caught two
real bugs, both fixed + regression-tested: non-chronological
stdout/stderr batch drove the cursor backward (re-ingestion churn) and
the JOIN-form FTS query scanned per row.

**Goal**: container logs collected and searchable.

- S10.1 Log collection + storage (2pd) [FR-L5]
  Acceptance: managed containers' logs tail into touchgrass under the
  retention cap; backpressure drops (never OOMs).
- S10.2 Log search UI (2pd) [FR-L5]
  Acceptance: full-text search + service/time filters; surrounding-lines
  view per line.
- Standards in play: bounded buffers, retention-first design.
- Validation: find a known staging log line via search <30s after emit.

### Sprint 11 — Unification + soak (Phase 5b, ~3pd) → V1 DONE

**Status**: DONE (2026-09-21, code-complete). `Ingestor.IssueLogs`
returns ±60s of log lines (default, ≤1h) around an issue's newest
occurrence, newest first, served by `GET /api/issues/{id}/logs` and
rendered as "Surrounding logs" in the issue detail view. Soak proof:
`TestSoakHoldsCaps` floods 150 errors + 1500 log lines into 50/50
caps — stored totals hold exactly at cap while poll-cap drops (500)
and line truncations (5) are counted per service, logged
(`log backpressure drop`, `log lines truncated`, `retention trimmed
logs`), and queryable via `GET /api/logs/stats`. `docs/v1-acceptance.md`
maps BRD AC-1..6 to local proof + EC2 live steps. Gated green
(`lint`/`vet`/`test`/`test-race`/`test-int`/`audit`/`build`/web
`verify`). EC2 sweep (2026-09-22): AC-1 PASS (rows 1–3 + 8, all
success, audit complete), AC-2 PASS (recorded 0s vs 798-sample
independent probe, 0 non-200), AC-3 PASS (tn-api within source noise;
tn-fe/admin-fe exact after the S12 mem-accounting fix), AC-4 grouping
PASS (forced manual + uncaught from the in-app SDK grouped with
`new_issue` notes; outage-harmless half still needs a dedicated run),
AC-5 PASS (real issue shows 20 surrounding prod log lines; caps hold
at/under max), AC-6 script-path PASS (tool-stopped SSH flip, 120
samples 0 non-200; SDK-removal half pending).
**V1 milestone code-complete.**

**Goal**: errors ↔ logs linked; v1 acceptance green.

- S11.1 Error↔log linking (1.5pd) [FR-L4]
  Acceptance: issue view shows surrounding log lines for occurrences.
- S11.2 Volume soak + caps proof (1pd) [NFR-2, AC-5]
  Acceptance: synthetic soak holds total error+log storage under caps;
  truncations logged, queryable.
- S11.3 v1 acceptance sweep (0.5pd) [AC-1..6]
  Acceptance: all six applicable AC items re-verified on EC2.
- Validation: **v1 milestone.**

## Post-v1

### S12–S13 — Graduate tn-fe + admin-fe (Phase 6, 4–8pd)

**Status**: GRADUATED (2026-09-22, both services). Live runs on the
EC2 compose stacks: tn-fe deploy id 4 (downtime 5s) + rollback id 5
(downtime 3s), admin-fe deploy id 6 (downtime 2s) + rollback id 7
(downtime 2s) — all success with audit entries; recreate downtime is
expected (single container, 502s during the swap). Metrics flow for
both, history survived the 09:47Z touchgrass restart, logs collected
(tn-fe 8.8k lines, admin-fe at 50k cap, zero drops), health green.
Graduation caught one real bug: touchgrass reported raw cgroup memory
`usage` while `docker stats` subtracts inactive file cache (tn-fe
169.8 vs 144.7 MiB) — fixed in `internal/docker/stats.go` (`memUsage`,
v1+v2 keys, clamped) with unit tests, deployed, re-verified exact
(148.8 vs 148.8 MiB). Checklist + sign-off: `docs/service-graduation.md`.
Gated green (`vet`/`lint`/`test` incl. `-race` on the touched package).

**Goal**: full onboarding (health endpoints, metrics, history,
runbooks) on the proven generic engine. One service per sprint.

### S14 — OSS launch hardening (Phase 7, 3–5pd)

**Status**: DONE (2026-09-21) except fresh-VM timing + repo-public
flip. Shipped: README rewritten (v1 status, <30-min quickstart with
fail-closed compose, full env table, docs index), `demo/` compose
stack + ten-minute README (validated with `docker compose config`),
`CONTRIBUTING.md` (setup, all gates, standards), `docs/threat-model.md`
(5 trust boundaries, socket/auth/key/PII notes, deliberate v1 gaps),
`.goreleaser.yml` + `release.yml` wired (`contents: write`, UI
prebuilt in `before.hooks`; YAML-validated), compose fixed
(`TOUCHGRASS_DB` on the data volume, password fail-closed, socket
mount with documented warning), Dockerfile `/data` writable.
`LICENSE` is Apache-2.0, CI runs vet + lint + race tests + web verify
+ govulncheck. Remaining: time the quickstart on a fresh VM (BRD AC-7)
and flip the repo public (BRD AC-8) — both need a human + a VM, not
code.

**Goal**: README <30-min quickstart (timed on a fresh VM), demo
compose stack, contributing guide, threat-model notes, `release.yml`
wired, repo public.

### S15 — UI/UX refresh on shadcn (Phase 8, ~5–6pd)

**Status**: DONE (2026-09-22). No Go GUI toolkit applies (the UI
is a React SPA; Go only serves the bundle) — the pick is
**shadcn/ui** (Radix primitives + Tailwind v4, copy-paste, no
vendor lock-in) plus **recharts** for metric/downtime trends.
Shipped: app shell (service switcher, tab nav + More menu,
notification bell w/ preview, theme toggle, user menu, hash
deep-links), Dashboard (fleet, host strip, 24h notifications chart),
Services table + 7-tab workspace (Overview/Deploys/Metrics/Logs/
Issues/Alerts/Audit), deploy detail Sheet, Issues (release filter,
paging, occurrence paging, stack/breadcrumbs, linked logs), Logs
(toolbar, load-older `before` paging, context Sheet, stats), Rules +
Keys screens w/ confirms, Images (reclaimable size, prune confirm),
System (CPU/mem/disk gauges + load sparkline). Backend: Images +
System services with docker/system/http unit tests; `/api/system`
also serves host CPU (delta), mem (`/proc/meminfo`), disk
(`Statfs`), all null-safe. Deltas from the S15.0 spec: Services is a
filterable table (not card grid) per the tables/pagination ask;
deploy/cutover/rollback stay inline op cards with confirm gates
(not Dialog wizards); occurrence/log lists use cards, not tables;
filters are per-screen (no shared FilterBar); forms use inline
manual validation (RHF/zod installed, not wired). Validation: vet +
lint (0 issues) + unit + race + govulncheck (accepted-risk only) +
web verify/build green; EC2 click-through 2026-09-22 on
binary 53fc7b9: login 204, all 15 screen APIs 200
(services/system/images/notifications/audit + per-service
metrics/deploys/rules/issues/logs/stats/keys), live values
(CPU/mem/disk, 100 images/2 dangling), served bundle hash matches
local build, journal warning-free. Probe kept at `/tmp` (not in
repo).

**Goal**: a polished app shell with real navigation, dialogs, tables,
and feedback states — same flows, plus three new backend endpoints
for the screens the API can't serve yet (images, system, keys UI).

- S15.0 Screen & feature inventory (this spec; done when S15 ships)
  Shell (all screens): top bar with service-switcher dropdown
  (status dots), tab nav (Dashboard, Services, Issues, Logs,
  Notifications, Images, System, Audit), notification bell with
  unread badge + dropdown preview, user menu (logout). Active-route
  highlight, responsive collapse, usable at 390px.
  1. Dashboard (new home): fleet status cards per service (health,
     live color, containers), host snapshot strip (CPU/mem/disk
     live), recent notifications list, 24h events mini-chart.
  2. Services + service detail: grid of cards (badges, skeletons);
     detail tabs — Overview (health, containers, config), Metrics
     (recharts CPU/mem/disk history per container + downtime trend),
     Deploys (history table + pagination), Audit (service-scoped).
  3. Deploy flows: deploy / cutover / rollback as Dialog wizards
     with the existing confirm gates, SSE streamed progress, result
     state (downtime secs, probe summary); deploy detail Sheet.
  4. Issues: filterable table (service select, release filter, text
     search) + client pagination; detail drawer with occurrences
     table + pagination, stack-trace view, breadcrumbs timeline,
     linked-logs section (existing API).
  5. Logs: toolbar (service select, stream filter, time window,
     search), capped virtual-feel list, line-context drawer, stats
     footer (lines/drops/truncations); "load older" paging via
     `before` cursor (no offset API — time-window paging instead).
  6. Notifications: bell dropdown + full page parity, kind filter,
     mark one/all read, unread counts.
  7. Rules: alert-rules and issue-rules tables + create dialogs
     (validated forms) + delete confirms.
  8. API keys (new screen, existing API): table (prefix, service,
     sample rate, created, revoked) + mint dialog with one-time
     secret reveal + copy button + revoke confirm.
  9. Docker images (new screen, NEW API): images table (repo, tag,
     size, created, dangling badge) + prune-dangling button with
     confirm + reclaimed-space result + daemon disk summary.
  10. System (new screen, NEW API): host cards (hostname, OS/arch,
      uptime, load, docker version), live gauges (host CPU/mem/disk
      auto-refresh + session sparkline), touchgrass self-stats (DB
      bytes incl. WAL sidecars, version, process uptime).
  11. Audit: table with service/actor/action filters + pagination.
  New backend endpoints (with store/service/http tests per area):
  `GET /api/docker/images` (list w/ dangling flag),
  `POST /api/docker/images/prune` (dangling-only, returns reclaimed
  bytes), `GET /api/system` (host snapshot: stdlib + `docker info`,
  null-safe per field, no new deps — `/proc` guarded by GOOS).
  Stretch (needs migration): `POST /api/issues/{id}/resolve`.

- S15.1 Foundation (1pd)
  Scaffold shadcn (`components.json`, `@/lib/utils` with
  clsx + tailwind-merge + class-variance-authority, CSS theme tokens
  in Tailwind v4 `@theme`, lucide-react icons, react-hook-form + zod
  resolvers for dialogs). Install primitives: Button, Input, Select,
  DropdownMenu, Card, Badge, Dialog, Sheet, Tabs, Table, Pagination,
  Skeleton, Separator, Label, Tooltip, Sonner (toasts), Chart
  (recharts). Shared FilterBar + EmptyState + ErrorState components.
  Acceptance: `npm run verify` + `build` green; a `/kitchen-sink`
  dev-only route renders every primitive (removed before merge).
- S15.2 App shell + navigation (1pd)
  Replace the button-row nav with a top bar: service switcher
  dropdown (all services + status dots), tabs for
  Services/Issues/Logs/Notifications/Audit, notification bell with
  unread badge + dropdown preview, user menu (logout). Active-route
  highlighting, consistent page container, responsive collapse.
  Acceptance: every existing route reachable from the shell;
  deep-linking unchanged; usable at 390px wide.
- S15.3 Screens (2–3pd)
  Build S15.0 screens 1–11 in order: Dashboard, Services + detail
  tabs, deploy Dialogs, Issues table + drawer, Logs toolbar + drawer,
  Notifications page, Rules tables + form dialogs, API keys screen,
  Images screen, System screen, Audit table. Backend first for the
  two new areas (docker client ImageList/Prune + system snapshot
  service, each with unit + http tests, then the screens that read
  them). Confirm gates preserved on every destructive action.
  Acceptance: every current user flow still completes (login →
  deploy → cutover → rollback → audit; issue → linked logs;
  log search → context); new screens read live data on EC2.
- S15.4 Feedback + polish (0.5–1pd)
  Toasts on all mutations, skeleton/empty/error-with-retry states
  per view, form validation messages, focus management in dialogs
  (Radix default), keyboard-navigable menus, `prefers-reduced-motion`
  respected. Stretch: dark-mode toggle (shadcn `class` theme).
  Acceptance: no silent failures; no layout shift on load; axe-ish
  manual pass (labels, roles, contrast) on the five main views.
- Standards in play: web `verify` (tsc + eslint) clean; no new
  backend endpoints without a store test; screenshots or a
  click-through note in the PR per reskinned view.
- Validation: full gate suite green; click-through of every flow
  against the EC2 box via https://infra.ticketnation.ph.
- **Backlog (Phase 8, needs a new plan to start)**: one-click container
  actions, multi-host agents, RBAC, secrets management, tracing/APM,
  browser/mobile SDKs, PR previews.

## Working agreements

- Sprint goal over story count: a sprint succeeds if its goal + validation
  are met, even with spillover.
- No pillar may break another: deploys, metrics, and logging stay
  independently usable (BRD risk mitigation).
- Destructive-schema or auth changes get a second pair of eyes before merge.
- Plan/BRD/ARCHITECTURE drift is a bug: update the doc in the same sprint
  that introduces the change.
