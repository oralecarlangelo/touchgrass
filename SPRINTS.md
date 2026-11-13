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
| Q5 auth | Single admin credential (superseded by S28: admin/viewer roles, ADR-0010) |
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
(`https://infra.ticketnation.ph`), never loopback. Later the same day
CI redeployed tn-api (builds 275/276) and pruned the dogfood image, so
the SDK is no longer in prod — permanent wiring needs a tn-api repo PR
(working diff backed up at `~/tn-api-sdk-dogfood-20260922.patch` on the
box). Remaining: S9.2 tuning after a week of real volume (sampling,
scrub, thresholds).

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

### S16 — Prod verification + flip detection (unplanned hardening, 2026-09-22)

**Status**: DONE (2026-09-22). A prod-version audit found live tn-api
one build behind (green 274 vs idle blue 275) plus an out-of-band nginx
flip (10:14Z) with no touchgrass audit trail; before acting, CI shipped
build 276 to live green and started an FE rebuild, so all prod
mutations stood down. FE/admin versions are indeterminate from the box
(no baked version trail; on-box FE sources stale since May, rebuild
unsafe) — needs a repo-CI comparison. Hardening shipped: the watch
loop now records a notification + audit entry (actor `system`) when
the live target moves with no active touchgrass run (`SetFlipReporter`,
`Cutover.Active`; own runs stay event-only). Gated green (vet/lint/
unit/race), deployed, verified healthy with no false alert on boot;
live-fire verification awaits the next external flip.

### S17 — Logs level color-coding + filters (2026-09-22)

**Status**: DONE (2026-09-22). `GET /api/logs` stamps every line
with a parsed `level` (error/warn/info/debug — substring markers,
stderr defaults to warn) and accepts `stream=` + `level=` (comma set)
filters; level filtering pages newest-first in bounded 1k chunks so
limits stay exact for rare levels. Same stamping on issue-linked logs
and line context. No migration (levels computed at read). UI: shared
`LogLineRow` (level badge colors, ANSI stripped) in Logs view, context
sheet, and issue-linked logs; stream + level selects apply instantly
server-side. Gated green (go vet/lint/tests/race, web verify/build),
deployed, live bundle hash-verified; prod window splits 147 info /
42 warn / 8 debug / 3 error.

### S18 — SDK publish prep + /docs site (2026-09-22)

**Status**: DONE (2026-09-22). `@touchgrass/node` is publish-ready:
registry metadata (repo, homepage, keywords), `prepublishOnly`
(verify + build), CJS usage docs, clean `npm pack` (27 files), and a
tarball smoke test proving ESM `import` + CJS `require` with fail-open
flush. Actual `npm publish` needs the maintainer's login — run it
from `sdk/node/`. Docs: VitePress guides (quickstart, concepts, SDK,
ingest, self-hosting, troubleshooting) + a Redoc API reference from
a 35-operation `openapi.yaml` that mirrors `server.go`, embedded in
the binary and served at `GET /docs/` (ahead of the SPA fallback,
public like the console). Wired through Makefile (`build-docs`),
goreleaser hooks, and CI (build + `redocly lint`). Gated green,
deployed, verified live (guides 200, API ref 200, missing 404).

### S19 — Activity timeline: logs + errors + deploys (2026-09-22)

**Status**: DONE (2026-09-22). Sentry-style unified timeline per
service: new `GET /api/issues/occurrences` (service-wide, newest
first) feeds an Activity tab merging log lines, error occurrences,
and deploy markers chronologically with filter chips (All/Logs/
Errors/Deploys), level-colored rows, and click-through to the issue
sheet. Spec updated (`listRecentOccurrences`), docs rebuilt. Gated
green (go vet/lint/tests, web verify/build, redocly lint), deployed,
live bundle + API verified against prod data.

### S20 — Structured logging SDK + OTel bridge (Sentry Logs-style)

**Status**: DONE (2026-09-22). Shipped end to end: `logger.info/warn/error…` in
`@touchgrass/node` ships structured logs (levels, attributes, trace
context) to a new batched ingest endpoint, stored per service and
explorable in the Logs view; plus an OTel `LogRecordExporter` so
OTel-native apps get integration without code changes. Modeled on
[develop.sentry.dev/sdk/telemetry/logs](https://develop.sentry.dev/sdk/telemetry/logs/)
(Sentry Log protocol v2.2 + OTel severity mapping). FROZEN CONTRACT:

Wire — `POST /api/ingest/logs` (Bearer API key, same keys as
`/api/ingest`): body `{ "release"?: string≤128, "items": [Item×1..1000]
}`. Item: `timestamp` (unix seconds float, required), `level`
(trace|debug|info|warn|error|fatal, required), `body` (1..8192,
required), `severity_number` (1..24; server infers 1/5/9/13/17/21 from
level when absent), `trace_id` (32 lowercase hex), `span_id` (16
lowercase hex), `attributes` (≤64 keys, key 1..128, value
`{value: string|number|boolean, type: string|integer|double|boolean}`,
string values ≤4096). Invalid item → whole batch 400 with
`{error, index}`. Success → 202 `{accepted: n}`.

Store — migration `0010_sdk_logs.sql`: `sdk_logs(id, service, ts`
ms`, level, severity, message, attributes JSON, trace_id, span_id,
release)`, indexes `(service, ts)`, `(trace_id)`. Retention via the
`resources.go` trim framework, new target `sdk_logs`, env
`TOUCHGRASS_RETENTION_SDK_LOGS` (days, default 7).

Query — `GET /api/logs` gains `source=sdk|containers` (default
`containers`, back-compat) and `trace_id` (sdk only; 400 with
`source=containers`). Existing `level=` filter applies to both. OpenAPI
updated, redocly clean.

SDK (`@touchgrass/node` → 0.2.0) — `logger.{trace,debug,info,warn,
error,fatal}(msg, attrs?)` with printf formatting (`util.format`) when
extra args present and trailing-plain-object = attributes; formatting
stored as `sentry.message.template` / `sentry.message.parameter.N`
typed attrs; `Error` first-arg captures type+stack attrs; queued on the
existing client batcher (same flush/close, fail-open, scrub,
`beforeSendLog` hook); trace/span auto-capture from
`@opentelemetry/api` active span via guarded dynamic require (never
throws, no hard dep). `./otel` subpath exports
`TouchgrassLogRecordExporter` (`@opentelemetry/sdk-logs` peerDep,
optional) mapping OTel LogRecords → ingest items; zero-dep core
preserved. Docs page `sdk-logging.md` (API, attrs, trace correlation,
OTel guide).

UI — Logs view gains Containers | Application (SDK) source tabs; SDK
rows reuse level colors with attribute expansion + trace/span chips
(trace chip filters by trace); empty state links `/docs/sdk-logging`.
SCOPE GUARD: Activity timeline untouched in S20 (no SDK merge there).

Validation: per-area gates green, full gate (vet, lint 0 issues, tests,
race, web/SDK verify+build, redocly, govulncheck known-only), deploy,
live dogfood (logger batch → 3 rows with severities 9/13/17 and
`sentry.message.*` template attrs; OTel exporter + real span →
trace-correlated row queried back by trace_id), docs live. Dogfood key
revoked after the run. Docs note: span context flows from the record's
context (NodeSDK async tracking or explicit `emit({…, context})`).

### S21 — Blue-green for tn-fe + admin-fe (2026-09-22)

**Status**: DONE (2026-09-22). Both FE services migrated from
recreate (5s/3s and 2s/2s 502 downtime) to blue-green, following
tn-api's legacy-migration path: compose colors (fe on 4201/4202,
app on 4301/4302), nginx active-upstream bootstrap, per-stack
cutover-script forks + touchgrass adapters, DB row flips, live
migration cutovers + rollback drills, legacy cleanup. All four runs
success with `downtime_secs: 0`; 225/225 public probes 200.
Found + fixed in the forks: `compose rm -f` skips running
containers (stop-before-rm). Full procedure + findings in
`docs/service-graduation.md` §6.

### S22 — Databases page: Postgres health + backup/restore (2026-09-22)

**Status**: DONE (2026-09-22). Shipped end to end: a top-level Databases view showing
PostgreSQL health (version, size, connections, uptime, last backup),
one-click `pg_dump -Fc` backups to the existing
`/opt/backups/ticketnation/<db>-<ts>.dump` + `.sha256` convention,
a backup list with verify, and restore-behind-type-to-confirm with
an automatic pre-restore safety backup; plus read-only health chips
for Redis and touchgrass's own SQLite. All Postgres access via
`docker exec` + `local trust` auth (no new secrets). FROZEN CONTRACT:

Env (`internal/config`): `TOUCHGRASS_POSTGRES_CONTAINER` ("",
empty = postgres card unconfigured), `TOUCHGRASS_POSTGRES_USER`
("postgres"), `TOUCHGRASS_POSTGRES_DB` ("postgres"),
`TOUCHGRASS_DB_BACKUP_DIR` ("./backups"),
`TOUCHGRASS_DB_BACKUP_KEEP` (14),
`TOUCHGRASS_REDIS_CONTAINER` ("" = chip hidden). Prod box sets the
real values in `touchgrass.env` at deploy time.

API (all admin-session): `GET /api/databases` →
`{"databases":[{id,label,configured,reachable,version?,size_bytes?,
connections_used?,connections_max?,uptime_secs?,used_memory_bytes?,
integrity?,last_backup_at?}]}` with ids `postgres|redis|sqlite`;
`GET /api/databases/backups` →
`{"backups":[{name,size_bytes,created_at,sha256:
"present"|"missing"}]}` newest first;
`POST /api/databases/backups` → 202 `{"id","status":"running"}`
(409 `busy` when a job runs, 503 unconfigured);
`GET /api/databases/jobs/{id}` →
`{"id",kind:"backup"|"restore",target,status:"running"|"success"|
"failed",detail,started_at,finished_at?}`;
`GET /api/databases/jobs` → last 20, newest first;
`POST /api/databases/backups/{name}/verify` → 200 `{"name",ok}`;
`POST /api/databases/restore {"name"}` → 202 job (400 bad/missing
name, 409 busy, 503 unconfigured). Names must match
`^[A-Za-z0-9_.-]+\.dump$` and exist in the backup dir (no traversal).

Backend behavior: backup = `docker exec $CTR pg_dump -U $U -d $D
-Fc` streamed to host file `<db>-<UTC>.dump` (0600) + `.sha256`
sidecar, then trim to KEEP newest; restore = safety backup
`<db>-pre-restore-<ts>.dump`, terminate other backends, `docker
exec -i $CTR pg_restore -U $U -d $D --clean --if-exists` with the
dump on stdin (never `DROP DATABASE`), verify `SELECT 1` + public
table count; every job audited (`db_backup`/`db_restore` actions).
Jobs in new `db_jobs` migration (keep last 50). Exec behind an
injectable runner — no real docker in unit tests. OpenAPI +
self-hosting env table updated, redocly clean.

UI (`databases` view in `lib/views.ts` + nav): Postgres health
card (stats grid, last backup, Backup button with 2s-polled job
progress), backups table (name/size/age/sha badge/Verify/Restore),
restore modal (type filename to confirm + off-peak warning + what
happens list), jobs history, Redis/SQLite chips, unconfigured +
error + empty states. SCOPE GUARD: no backup delete, no scheduled
backups, no multi-Postgres inventory in S22.

Validation: per-area gates, full gate (vet, lint incl. GOOS=linux,
tests, race, web/docs verify+build, redocly, govulncheck
known-only), CI green, deploy with box env set, live-verify (all
three health probes, backup list, real 431MB backup in 68s,
verify ok:true, db_backup audit). Plus reviewer-added startup
reconcile for orphaned job rows (store/service/main + tests). NO
live restore test (prod data; restore proven by tests + review —
first real restore is a planned drill, see database-ops.md).

### S23 — Fleet monitoring: all containers + host history (2026-09-22)

**Status**: DONE (2026-09-22). Shipped end to end: every container on the daemon observed
(metrics-only, no service rows) and server-side host history, surfaced
on a reworked fleet page. FROZEN CONTRACT:

Store — migration `0012_fleet.sql`: `host_samples(sampled_at,
cpu_percent, mem_used, mem_total, disk_used, disk_total, load1)` +
`container_samples(sampled_at, container_name, project, managed,
service_id, state, cpu_percent, mem_bytes, mem_limit, restarts)`,
indexes on `(sampled_at)` and `(container_name, sampled_at)`.
Retention reuses `TOUCHGRASS_RETENTION_METRICS` via the existing trim
framework (new trim targets, no new env).

Sampler (`resources.go` collect): after the managed loop, sample
EVERY container into `container_samples` each interval with the
managed flag (and `service_id` when managed) — the 3 managed rows
duplicate the metrics table deliberately so the fleet table is one
query; plus one `host_samples` row reusing the existing
`service/system.go` snapshot helpers (no duplicated /proc parsing).

API (admin-session): `GET /api/system/containers` →
`{"containers":[{name,project,managed,service_id?,state,
cpu_percent,mem_bytes,mem_limit,restarts,sampled_at}]}` latest row
per container, ordered by cpu desc;
`GET /api/system/history?metric=cpu|mem|load&hours=N` (default 24,
max 168) → `{"points":[{ts,value}]}` bucket-averaged in SQL to
≤1500 points (mem = used bytes). OpenAPI updated, redocly clean.

UI: rename the `system` view to `fleet` (view id, `#/fleet` hash,
`FleetScreen.tsx`, label "Fleet", moved to the Overview group after
Services; backend routes stay `/api/system*`). Page: host stat
cards (existing snapshot), CPU/memory/load charts from history with
1h/6h/24h/7d range selector (recharts, existing patterns), fleet
table (name, project, managed/unmanaged badge with service link
when managed, state, cpu, mem, restarts, sampled age) in server
order. SCOPE GUARD: no alert rules on unmanaged containers, no
per-container history drill-down, no client column sorting in S23.

Validation: per-area gates, full gate (vet, lint incl. GOOS=linux,
tests, race, web/docs verify+build, redocly, govulncheck
known-only), CI green, deploy, live-verify (20-container fleet: 3
managed + 17 unmanaged across 15 projects incl. the project's own
db/redis; cpu/mem/load history accumulating; Fleet bundle live).

### S24 — Promote to managed: suggest + create (2026-09-22)

**Status**: DONE (2026-09-22). Shipped end to end: one-click promotion of an observed
fleet container to a managed service: a suggest endpoint that drafts
the row from labels + port probes, and a create endpoint with
validation. FROZEN CONTRACT:

Env: `TOUCHGRASS_SCRIPTS_DIR` (default `"scripts"`, resolved
relative to the app root; prod sets the absolute
`/home/ubuntu/touchgrass/scripts` at deploy).

`GET /api/onboarding/suggest?container=<name>` → 200:
`{container, service_id, strategy:"recreate"|"bluegreen",
compose_project, compose_dir, service, health_url, public_url:"",
deploy_script, rollback_script, blue_service?, green_service?,
blue_target?, green_target?, blue_url?, green_url?, nginx_conf?,
marker?, cutover_script?, confidence:"high"|"medium"|"low",
reasons[], warnings[]}`. 400 unknown container (no such name on the
daemon); 409 already managed (project+service matches an existing
row). Logic: labels give project/service/working_dir (missing
labels → low + warnings, blanks for the operator); service_id tries
project then project-service, else "" + warning when both taken;
health probe tries the lowest published host port's `/` then
`/health` (3s timeouts, first 2xx wins; none → "" + warning; no
published ports → warning); recreate scripts default to
`<scripts-dir>/recreate-deploy.sh` / `recreate-rollback.sh`.
Blue-green detection: project container services contain an
`X-blue`+`X-green` pair → strategy bluegreen with services/targets
(from published ports)/urls (probed per color) prefilled,
nginx_conf from scanning `/etc/nginx/sites-enabled/*` for a
`# BLUEGREEN-ACTIVE` marker line on either port (unreadable →
"" + warning), marker default, cutover_script "" + "fork the team
script (manual)" warning. Probing + nginx scan behind injectable
helpers — no real HTTP/fs in unit tests.

`POST /api/services` → body `{id, name?, strategy,
compose_project, compose_dir, config}` (config = raw strategy JSON);
201 `{id, strategy}`. Validate: id `^[a-z0-9-]+$` + unique (409),
strategy enum, project/dir non-empty, dir exists (400), config
decodes per strategy with required fields (recreate: service,
health_url, both scripts; bluegreen: services, targets, urls,
nginx_conf, marker, cutover_script), scripts executable (400).
Audit `service_create` (new model constant). Add
`ServiceStore.Create` if missing (+ test). OpenAPI both, redocly
clean.

UI (FleetScreen): unmanaged rows get a Manage button → Dialog with
editable draft (service id, strategy select, compose project/dir,
service, health_url + probe-result note, public_url, script paths,
blue-green fields shown when detected, confidence + reasons +
warnings) → Create → sonner toast + navigate `#/services/<id>`;
errors inline. SCOPE GUARD: no auto blue-green bootstrap (compose
colors + nginx edits stay manual per graduation §6), no bulk
promote, no delete-service in S24.

Validation: per-area gates (backend child failed late; parent took
over, fixed the migration-count tests, reviewed the slice
directly), full gate (vet, lint incl. GOOS=linux, tests, race,
web/docs verify+build, redocly, govulncheck known-only), deploy
with scripts-dir env set, live-verify (suggest on job-portal: high
confidence, probed health; 409 on managed tn-api; created
job-portal for real — healthy, fleet row flipped, service_create
audited; no deploy triggered).

### S25 — Delete service + Fleet polish (2026-09-22)

**Status**: DONE (2026-09-22). Two operator asks in one: removing a
service from management, and making the Fleet page (esp. the Manage
button) obvious.

`DELETE /api/services/{id}` → 204; 400 bad id shape, 404 unknown
id. Store deletes in one transaction: all child rows with enforced
FKs to `services(id)` (alert_rules, api_keys, audit,
deploy_probes, deploys, issue_rules, issues, log_lines, metrics,
notifications, occurrences, sdk_logs) then the service row; unknown
ids fail with `ErrServiceNotFound`. Service layer validates id +
actor, then audits global `service_delete` (migration 0014 extends
the audit CHECK; audit model + service allowlist updated). Running
containers are never touched — the workload keeps serving and the
container shows as unmanaged on Fleet again.

UI: Services list rows get a Delete button → confirm Dialog
(spells out what goes + that containers keep running) → sonner
toast + list refresh + selection cleared. Fleet page: header counts
("N managed · M unmanaged") with guidance, filter input in the
header, unmanaged-first/CPU sort, primary Manage button on
unmanaged rows, ghost Open button on managed rows, empty-filter
state. OpenAPI `delete /services/{id}` (redocly clean), graduation
runbook §7.

SCOPE GUARD: no bulk delete, no container stop/remove on delete, no
blue-green auto-bootstrap.

Validation: full gate (vet, lint 0 issues, tests, -race, web/docs
verify+build, redocly, govulncheck known-only), pushed 5882372,
deployed, live-verified: canary create 201 → delete 204 → re-delete
404 `not_found`; global `service_delete` audit id 75 (migration
0014 live); inventory intact; new bundle `index-uJsc4pnq.js` serves
the Fleet counts/filter/sort + delete dialog strings.

### S26 — Quiet idle-color probe spam (2026-09-22)

**Status**: DONE (2026-09-22). The 60s sampler probed both
blue-green colors every tick, so each stopped idle color logged a
WARN (`health probe failed … connection refused`) every minute.
`blueGreenView` now probes the live color plus any color with a
running container (the daemon list covers running containers only),
and reports a stopped idle color unhealthy without dialing it (Debug
log only). Live-color failures still probe + WARN; recreate
services unchanged (their single URL is the live endpoint).

Validation: new recording-prober tests (skip stopped idle, probe
when running) + full Go gate; deployed; journal quiet + inventory
health unchanged.

### S27 — Docs completeness: guides + architecture + threat model (2026-09-22)

**Status**: DONE (2026-09-22). Audit found the API reference at
100% route parity and SDK/fleet guides solid, but no Deploys or
Databases guides, a stale ARCHITECTURE.md, ADRs stopping at 0008,
and a pre-fleet threat model.

Public (`docs-site/`, new nav under Operate): `deploys.md`
(strategies, cutover/rollback/deploy flows, 202+SSE+poll,
downtime proof, history/record, service delete, limits) and
`databases.md` (health cards, backups, restore steps, jobs, API,
limits) — every behavioral claim verified against code
(409-busy/conflict, `pg_restore --list` verify, nil downtime
without a public URL). Quickstart gains next-step links to
fleet/deploys/databases.

Implementation: ARCHITECTURE.md refreshed (service diagram,
accurate resource list pointing at openapi.yaml, full schema
table, SDK logger + OTel, embedded docs site, ADR-0009 pointer);
ADR-0009 records OTel-logs-only (overriding the old
"Tracing/APM out" ban); threat model covers S27 surfaces (SDK log
PII, backup dir perms + name validation, docker-exec trust auth,
operator destruction section incl. the audit append-only exception
for service delete).

Validation: docs-site verify+build, redocly, redeploy (docs are
embedded), live fetch of /docs/deploys.html + /docs/databases.html.

### S28 — Multi-user auth + read-only viewers (2026-10-14)

**Status**: DONE (2026-10-14, code-complete). Two global roles
end to end: admins hold every power the old single credential
held plus user management; viewers read everything (services,
metrics, deploys history, logs, issues, notifications, fleet/
system, audit, keys list) and mutate nothing. FROZEN CONTRACT:

Store — migration `0015_users.sql`: `users(id, username UNIQUE,
password_hash, role CHECK admin|viewer, disabled_at,
created_at)`; audit CHECK rebuilt with `user_create`,
`user_update`, `user_delete`.

Service (`internal/service/users.go`) — `Create` (username
`^[a-z0-9-]+$` 1–64, password 12–72 chars, role enum,
duplicate → 409), `SetRole`/`SetPassword`/`SetDisabled`/
`Delete` (last-enabled-admin guard → 409 on demote/disable/
delete), `Verify`/`Lookup` (unknown/disabled/wrong all map to
`ErrUnauthorized`, indistinguishable), `EnsureAdmin` (seeds or
repairs the bootstrap admin from `TOUCHGRASS_ADMIN_PASSWORD`
only when zero enabled admins exist; otherwise untouched).
Every mutation audits with the acting username.

Auth (`internal/http/auth.go`) — sessions bind random tokens
to usernames; role + disabled re-resolve per request, so
disables bite on the next call. Login body
`{username?, password}` (empty username = admin, v1 clients
keep working); `GET /api/auth/me` returns
`{authenticated, username, role}`. Mutating routes wrap in
`requireAdmin` → 403 `forbidden`; audit `actor` is the real
username. 403s are not audited (request logs capture them).

API (all admin-only): `GET /api/users` → `{users: [...]}`
(hash-free); `POST /api/users` → 201; `POST
/api/users/{id}/password` → 204; `POST /api/users/{id}/role`
→ 200; `POST /api/users/{id}/disable|enable` → 200; `DELETE
/api/users/{id}` → 204. Unknown ids 404 `not_found`.

UI — login gains a username field (prefilled `admin`);
viewers get a read-only badge, land on Logs, see no Users
nav, and lose every mutating control (deploy/cutover/
rollback/record, rule/key/service/user/database/image
actions, notification mark-read, fleet Manage); new Users
screen (table + create/reset/role/disable/delete dialogs).
`openapi.yaml` mirrors the routes (49 → 56 operations,
`Forbidden` response, `User` schema), redocly clean.

Validation: `make vet`/`lint` (0 issues)/`test`/`test-race`/
`test-int` green; web `verify` green; new coverage —
service validation + last-admin guard + bootstrap,
per-request session revalidation, a 25-route viewer 403
matrix plus a viewer reads-stay-open matrix, user CRUD
round-trips with hash-leak assertions. EC2-VALIDATED
(2026-10-14, binary 6dc45e1): migration 0015 applied +
admin seeded on boot; legacy password login 204; viewer
created (201, hash-free), logged in, read live tn-api prod
logs; viewer cutover + user-delete both 403 `forbidden`;
probe viewer deleted after the run; audit shows per-actor
entries; public bundle hash-verified with Users strings.

SCOPE GUARD: no per-service scoping, no operator role, no
SSO, no password-expiry, no session persistence across
restarts — each a one-sprint follow-up, not S28 creep.

**Goal**: other devs get read-only log access without deploy
power; every action still audited, per actor.

- S28.1 Users store + migration + bootstrap (1pd) [FR-P1, NFR-5]
  Acceptance: `users` table + audit actions migrate cleanly;
  fresh boot seeds admin from env; reboot with users present
  changes nothing.
- S28.2 Multi-user sessions + authorization (1.5pd) [FR-P1, NFR-5]
  Acceptance: named logins work, legacy password-only login
  still works; viewers read and get 403 on every mutation;
  disable kills sessions immediately.
- S28.3 User-management API (1pd) [FR-P1, FR-D4]
  Acceptance: full CRUD behind admin-only routes with audit
  entries; last-admin guard proven; hashes never leave the API.
- S28.4 UI + docs (1.5pd) [NFR-6]
  Acceptance: viewer console is read-only end to end with a
  Users screen for admins; ARCHITECTURE/threat-model/OpenAPI/
  ADR-0010 updated in the same sprint.
- Standards in play: `golang-database` (parameterized store,
  sentinel not-found), `golang-security` (bcrypt, session
  handling, per-route authz), `golang-error-handling`
  (user-safe envelopes, no credential leaks in errors),
  `golang-testing` (table-driven matrices).
- Validation: gate suite green; EC2 viewer walkthrough per the
  status note.

### S29 — Environment & secrets pillar, backend (2026-11-10)

**Status**: BUILT (2026-11-10, API + CLI + docs; the React UI ships
separately against the contract below). Each managed service may
own one runtime env file that touchgrass renders, validates,
writes, restarts, probes, and rolls back — values encrypted at
rest, secrets masked for every role, reveals audited, every apply
a revision. FROZEN CONTRACT:

Config — `Service.Config.env`: `target{path,owner,mode}`,
`validate_cmd`, `restart_cmd`, `probe_url`, `sudo`, plus optional
`probe_timeout_secs` (default 120), `read_cmd` (import parses its
stdout) and `write_cmd` (apply pipes the rendered file to its stdin;
the command owns install; failure = write step failed with its output
as `validation_lines`, nothing restored; later restart/probe failure
replays the applied revision through it). `model.EnvConfigOf`.

Store — migration `0021_env.sql`: `env_entries(service_id,key,
ciphertext,nonce,key_id,is_secret,state,updated_by,updated_at)`,
`env_revisions`, `env_revision_entries` (cascade), `env_runs(id,
service_id,revision_id,state,started_at,finished_at,steps_json)`,
`env_validations(service_id PK,ok,lines_json,at)`; audit CHECK
rebuilt with `env.stage|discard|import|reveal|apply|apply_failed|
rollback`; `services.Delete` clears the env tables.

Crypto — `internal/secret`: AES-256-GCM, 12-byte random nonce,
AAD = service id + key, key from `TOUCHGRASS_ENV_KEY_FILE` (32 raw
or 64 hex bytes, mode 0600 enforced) or `TOUCHGRASS_ENV_KEY` (hex,
dev); `key_id` = first 8 hex of SHA-256(key); no key → pillar off,
503 `env_key_missing`, view `configured:false, reason:key_missing`.

API (session auth; viewer GET only; mutations `requireAdmin`):
`GET /api/services/{id}/env` → `{configured, reason?, target,
entries[{key,is_secret,value|null,applied_value?(changed non-secret
only),masked,updated_by,updated_at,state:applied|added|changed|
removed,required}], applied_revision, pending_count, last_validation
{ok,lines,missing_required,at}, apply_in_progress, active_run_id?}`;
`PUT …/env/entries/{key}` `{value,is_secret}` → 200 entry (400
`env_key_invalid` `^[A-Z_][A-Z0-9_]*$`, 400 `env_value_invalid`
UTF-8/no newlines/≤64 KiB); `DELETE …/entries/{key}` → 204;
`POST …/entries/{key}/reveal` → `{value}` (admin, audited,
10/min/user → 429 `env_reveal_rate_limited`); `GET …/env/diff` →
`{added,changed,removed,rendered_sha256}`; `POST …/env/discard` →
204; `POST …/env/import` `{replace?}` → `{imported,skipped,
secret_guessed}` (409 `env_has_entries`); `POST …/env/apply`
`{confirm:<service name>,summary}` → 202 `{run_id}` (409
`env_apply_in_progress`, 409 `env_nothing_to_apply`, 400
`confirmation_mismatch`); `GET …/env/runs/{run_id}` → `{run_id,
status:queued|running|succeeded|failed, steps[{name,status,message?,
at}], error?, validation_lines?, restored?, started_at, finished_at?}`;
`GET …/env/revisions` → newest first with key diffs + `applied`;
`POST …/env/revisions/{rev}/rollback` `{confirm}` → 202 `{run_id}`.
Service without an `env` block: view `configured:false,
reason:not_configured`, mutations 400 `env_not_configured`.

Run — render (header `# managed by touchgrass — do not edit`,
sorted `KEY=VALUE`, LF, raw values) to a temp file beside the
target with mode/owner → `validate_cmd` + candidate path as last
arg and `TOUCHGRASS_ENV_CANDIDATE|TARGET|SERVICE` env (60 s; fail →
delete candidate, store `last_validation`, run failed
`env_validation_failed`) → atomic rename keeping one `.bak` →
`restart_cmd` (10 min, streamed) → probe until 200 (default 120 s)
→ revision + mark applied + audit `env.apply`; restart/probe
failure → restore `.bak`, rerun restart, audit `env.apply_failed`.
SSE named event `event: env.apply` with data `{type,service_id,
run_id,step:render|validate|write|restart|probe|done|failed,message,
at}`. 429 reveal sets `Retry-After`. Error envelope stays the repo's
flat `{error,code}` (the UI accepts both forms). `required`
parses `INVALID <service>:<KEY>:` validator lines. `sudo:true`
escalates file ops and commands with `sudo -n`.

CLI — `touchgrass env import [--replace] | set [--secret]
KEY=VALUE | apply [--summary] --service <id>` (thin; actor
`cli:<user>`).

Docs — ADR-0012, threat-model boundary 7 + env section,
ARCHITECTURE pillar/diagram/data/config/CLI, PROJECT_STRUCTURE +
AGENTS layout (`internal/secret`), docs-site `env.md` + sidebar,
openapi (10 paths, redocly clean), README + self-hosting env vars.

**Goal**: operators manage each service's env file from the
console with staged review, validation before restart, audit per
actor, and one-click rollback — with no plaintext at rest.

- S29.1 Secret envelope + key loading (0.5pd) [NFR-5]
  Acceptance: round trip, tamper/wrong-key/AAD-mismatch → error,
  key file mode check, key id stable per key.
- S29.2 Store + migration (1pd) [NFR-2]
  Acceptance: entries CRUD with pending/applied states, revision
  snapshot + applied pointer, runs with step log, orphan reconcile,
  service delete cascades.
- S29.3 Service: stage/diff/discard/import/reveal (1pd) [FR-P1]
  Acceptance: state machine proven, discard restores from the
  applied snapshot, import heuristic + 409, reveal limit + audit,
  masked view for every role.
- S29.4 Apply/rollback engine + SSE (1.5pd) [FR-D1, FR-D3]
  Acceptance: deterministic render + sha, validate failure keeps
  the target and records lines, restart/probe failure restores the
  `.bak` and restarts, rollback stages the snapshot, in-progress
  lock, events carry run id + step.
- S29.5 API + CLI + docs (1pd) [NFR-6]
  Acceptance: viewer 403 on every mutation, admin flow end to end
  with fake host/prober, contract codes per route, OpenAPI parity
  test, ADR/threat-model/guide updated in the same sprint.
- Standards in play: `golang-security` (AES-GCM, AAD, zeroing,
  no values in logs/errors), `golang-database` (parameterized
  store, sentinel not-found, transactions), `golang-concurrency` +
  `golang-context` (per-service run lock, detached apply context,
  bounded step timeouts), `golang-testing` (fixtures with scripted
  host, table-driven validation).
- Validation: `make vet`/`lint` (0 new issues)/`test`/`test-race`/
  `build` green; redocly lint clean; new suites — secret (8),
  store env (11), service env (18), http env (12) + 7 viewer
  matrix rows. Not yet exercised on a host with real sudo.

SCOPE GUARD: one key, no rotation tooling, no per-service keys,
no external secrets manager, no UI (separate PR) — follow-ups.

### S30 — Generated first-boot admin password (2026-11-10)

**Status**: IMPLEMENTED on `feat/first-boot-admin-password`; PR open.
**Goal**: a fresh install needs no admin secret, never has a default
password, and a lost password has a way back (ADR-0013).

- S30.1 Optional seed + generated one-time password (1pd) [FR-P1, NFR-5]
  Acceptance: no `TOUCHGRASS_ADMIN_PASSWORD` → boot writes
  `initial-admin-password` (0600) beside the DB before the admin row,
  logs the path never the value; a restart keeps the unused password;
  a configured password still seeds unchanged and replaces an unused
  generated one; the CLI no longer demands the variable.
- S30.2 Must-change gate + self-service change (1pd) [FR-P1, NFR-5]
  Acceptance: migration 0022 `users.must_change_password`; middleware
  answers 403 `password_change_required` on everything but me,
  change-password and logout; `POST /api/auth/password` (any role)
  proves the current password, shares the login rate limit, audits,
  ends the caller's other sessions, deletes the initial file.
- S30.3 Offline recovery (0.5pd) [NFR-6]
  Acceptance: `touchgrass users reset-password [--username]` issues
  a one-time password once, audited as `cli:<os user>`; refuses an
  unmigrated database, an unknown or a disabled account.
- S30.4 UI + docs (1pd) [NFR-6]
  Acceptance: forced change screen (does not re-ask the password just
  typed), account-menu "Change password" for every role, first-time
  hint on the login form; ADR-0013, README, self-hosting, threat
  model, ARCHITECTURE, OpenAPI, compose default updated.
- Standards in play: `golang-security` (CSPRNG, 0600 atomic write,
  no secret in logs, rate-limited verify), `golang-database`
  (additive migration), `golang-testing` (table-driven, temp dirs).
- Validation: `make vet`/`lint` (0 new issues)/`test`/`build` green;
  web `verify` + vitest green; exercised end to end against a real
  binary in a browser: generated file → login → forced change →
  dashboard → file gone; account-menu change with a wrong current
  password; CLI reset locks the live session to the change screen.
  Not yet exercised on a host behind a proxy.

SCOPE GUARD: no forced change after an admin's reset in Users, no
password history, no invitation flow, no extra roles — the
roles-and-access plan picks those up (S31+).

### S31 — Service-scoped access: authorization core + scoped read-only users (2026-11-10)

**Status**: SHIPPED in v0.6.0 (PR #6).
**Goal**: an admin can create a read-only account limited to chosen
services; that account sees nothing else, on any route, list or
stream. Existing admin and viewer accounts behave as before
(ADR-0014). First of four sprints in the roles-and-access plan.

- S31.1 Policy table (1pd) [FR-P1, NFR-5]
  Acceptance: `internal/authz` holds 30 capabilities with a scope
  class (self / service / fleet), one role-to-capability table
  (viewer and admin; `developer` reserved and empty) and `Subject`;
  a table-driven test covers every role, capability and scope
  combination against the checked-in `testdata/matrix.tsv`.
- S31.2 Store + migration (1pd) [NFR-2, NFR-5]
  Acceptance: migration 0023 rebuilds `users` (three-role CHECK,
  `all_services`, CHECK that an admin is never scoped) before
  creating `user_services`; a v0.4.0 database keeps every user, gets
  `all_services = 1`, keeps `must_change_password` and never reuses a
  deleted id; service delete clears grants; the last-admin guard
  covers any change away from admin; `EnsureAdmin` resets scope.
- S31.3 Route guards + scope enforcement (2pd) [FR-P1, NFR-5]
  Acceptance: `requireAdmin` is gone; all 93 routes are rows of one
  table (capability + how the service is found); a service out of
  scope → 403 before any lookup, a foreign or missing resource id →
  the same 404; services, notifications and audit are bounded in the
  query; the SSE hub filters per event and closes a user's streams
  on any change to the account; `me` reports scope and capabilities;
  onboarding suggest is admin-only (D-11). Tests fail on a route
  registered outside the table and on one that answers a role
  holding nothing.
- S31.4 Access management API + first login (0.5pd) [FR-P1]
  Acceptance: `PUT /api/users/{id}/access`; scope on create and
  list; new accounts and admin resets leave a temporary password and
  a reset ends the user's sessions (D-13); audit rows carry the role
  and scope; a non-admin gets 403 on every access route.
- S31.5 UI + docs (1.5pd) [NFR-6]
  Acceptance: `can(capability, serviceId?)` built from `me` replaces
  the `isAdmin` boolean at its source; nav entries a user cannot open
  are hidden; one access-denied state and a no-services state; scope
  picker, Access column and narrowing confirmation on Users; real
  role and scope in the header; ADR-0014, threat model,
  ARCHITECTURE, `docs-site/access.md`, OpenAPI with
  `x-touchgrass-capability` / `x-touchgrass-scope` on every
  operation and a parity test against the route table.
- Standards in play: `golang-security` (deny by default, no
  existence oracle, role checked before a body is read),
  `golang-database` (transactional access change, cascade trap
  documented in the migration), `golang-concurrency` (hub closes
  streams under its mutex), `golang-testing` (matrices derived from
  the route table; generic non-dogfood fixtures).
- Validation: `go build`/`vet`/`test` green (also `-race` on
  `internal/http`), lint 0 new issues, web `verify` + vitest green,
  docs build + redocly lint clean. Exercised end to end against a
  real binary on a loopback port with a throwaway database and two
  generic services: 248 raw-HTTP checks as a viewer scoped to one
  service, an all-services viewer and an admin (every fleet route
  403, every route of the other service 403, foreign and missing
  resource ids 404, lists and the live stream carrying only the
  granted service, a stream closed within a second of an access
  change, admin reset ending sessions), then the Users screen and a
  scoped viewer's session in a browser with no refused request.
  Not yet exercised on a host behind a reverse proxy.

SCOPE GUARD: the `developer` role is not enabled, no deploy or env
write rights for non-admins, no custom roles, no groups, no API
tokens — S32–S34 of the plan pick those up.

### S32 — Developer role: deploy and operate in scope (2026-11-12)

**Status**: SHIPPED to `develop` (PR #9); not yet released.
**Goal**: an admin can give an account the `developer` role with a
service scope; that developer can deploy, cut over and roll back the
services in scope, record a manual deploy, manage their alert and
issue rules and mark their notifications read, with every action
audited under their own name (ADR-0015). Outside scope they are
refused exactly as a scoped viewer is. Existing admin and viewer
accounts behave as in v0.6.0. Second of four sprints in the
roles-and-access plan.

- S32.1 Enable the role (0.5pd) [FR-P1, NFR-5]
  Acceptance: the policy table gives `developer` the viewer's
  capabilities plus `deploy.trigger`, `deploy.record`,
  `alerts.manage` and `notifications.ack` (all service-scoped; no
  `env.stage`); `testdata/matrix.tsv` and its exhaustive test are
  updated; create, `PUT …/access` and the older role route accept
  the role; the last-admin guard refuses demotion to it.
- S32.2 Deploy rights (1pd) [FR-P1]
  Acceptance: an in-scope deploy answers 202, runs the configured
  script with only the stored arguments and is audited under the
  developer; out of scope 403; a manual deploy record from anyone
  but an admin carries the session user and ignores a body `actor`;
  an admin may still attribute a record, and the audit row names
  the admin.
- S32.3 Alerts and notifications (1pd) [FR-P1, NFR-2]
  Acceptance: migration 0024 rebuilds the audit table for
  `deploy_record`, `alert_rule_create`, `alert_rule_delete`,
  `issue_rule_create`, `issue_rule_delete` and `notification_read`
  (a v0.6.0 database keeps every row, id and grant); rule and
  mark-read service methods take an actor; a rule delete loads the
  rule first so the row lands on its service; a foreign or missing
  rule or notification id answers the same 404; a scoped mark-all
  touches only in-scope rows and writes one row per service.
- S32.4 UI (1pd) [NFR-6]
  Acceptance: the role picker offers Developer with one line on what
  it may do; deploy, rollback and cutover panels follow
  `can('deploy.trigger', id)`, the record form follows
  `can('deploy.record', id)` and shows "Recorded as <user>" for
  non-admins; rules and mark-read already followed `can()`; the
  header shows `developer · N services`.
- S32.5 Docs (0.5pd) [NFR-6]
  Acceptance: ADR-0015; threat model (operator-powered section: a
  developer can run the configured deploy script for an in-scope
  service and nothing else); `docs-site/access.md` matrix with a
  developer column held to the policy by a test; ARCHITECTURE;
  OpenAPI role enums, audit actions and operation descriptions, with
  the capability parity test still green.
- Standards in play: `golang-security` (opt-in power, attribution
  forced server-side, no existence oracle), `golang-database` (audit
  rebuild inside the migration transaction, `UPDATE … RETURNING` for
  an atomic mark-read), `golang-testing` (frozen and reviewed route
  matrices checked in as data; generic non-dogfood fixtures).
- Validation: `go build`/`vet`/`test` green (also `-race` on
  `internal/http` and `internal/service`), lint 0 new issues, web
  `verify` + vitest green, docs build + redocly lint clean. Safety
  tests: `access_viewer_v060.tsv` (what a viewer got from all 89
  non-public routes, produced against the v0.6.0 tag) still holds for
  an all-services and a scoped viewer; `access_developer.tsv` lists
  every route a developer is allowed and refused, with all services,
  in scope and out of scope, and may differ from the viewer's file in
  exactly ten routes; 32 routes stay admin-only for a developer of
  any scope. Exercised against the real binary on a loopback port
  with a throwaway database, two generic recreate services with
  harmless local scripts and a developer scoped to one of them: 187
  raw-HTTP checks (own-service deploy, rollback, record, rules and
  mark-read audited as the developer; a forged body `actor` and
  injected script fields ignored; every deploy, rule and
  notification route of the other service refused; every env write,
  key, user, service-management and host mutation refused for a
  scoped and an all-services developer; a scoped viewer still
  read-only; a role change applying to the next request), then the
  Users screen (assigning Developer) and a developer's session in a
  browser with no refused request. Not exercised: a blue-green
  cutover by a developer on a real service (the fixtures are
  recreate services), and a host behind a reverse proxy.

SCOPE GUARD: no env write rights for non-admins (`env.stage` is
S33), no SDK key management, no host or security mutations, no API
tokens, no custom roles, no groups.

### S33 — Env proposals by developers (2026-11-12)

**Status**: IMPLEMENTED on `claude/roles-s33-env-proposals-41d100`; PR open.
**Goal**: a developer can propose non-secret env changes for a service
in their scope; only an admin can apply them, and no path lets a
non-admin learn, confirm or influence a secret value (ADR-0016). Admin
behaviour is unchanged. Third of four sprints in the roles-and-access
plan.

- S33.1 Stage rules (1pd) [FR-P1, NFR-5]
  Acceptance: `developer` holds `env.stage`, service-scoped. Past the
  two stage routes a caller without `env.apply` is a proposer
  (`service.EnvActor`, zero value restricted): may add a new key and
  change or remove a key whose applied and pending entries are not
  secret; may not touch a secret key, set the secret flag, or replace
  or withdraw another user's pending change (409
  `env_pending_by_other`). Every attempt on a secret answers 403
  `env_secret_restricted` with the same body and the same `env.stage`
  failure audit row, decided from flags before the value is validated
  or anything is decrypted; tests stage the true value, wrong values
  and invalid values and compare status, body, audit rows and the
  stored entry.
- S33.2 Read hardening (0.5pd) [NFR-5]
  Acceptance: `rendered_sha256` is absent from the diff and the
  revision list for a caller without `env.apply`; the digest is no
  longer written into a run's render step or its `env.apply` event,
  and is stripped from the steps of older runs for non-appliers.
  Checked and left as they are: the view (a secret is `value: null`,
  no length, no `applied_value`), entry states, and command output
  (`validate_cmd`, `write_cmd`, `restart_cmd`), which is the
  operator's contract under ADR-0012.
- S33.3 Review UI (1pd) [NFR-6]
  Acceptance: the Environment tab takes per-capability access instead
  of one admin flag; the pending bar names who staged the changes and
  shows "Awaiting an admin" where an admin sees Review & apply; the
  apply dialog lists the author beside each key and warns when
  someone else proposed; a secret row and a row with someone else's
  pending change show as not editable with the reason; discard,
  import, reveal and rollback stay hidden for non-admins; component
  tests cover each. The dialog sends the digest it reviewed, and the
  server refuses the apply (409 `env_pending_changed`) if anything
  was staged since.
- S33.4 Docs (0.5pd) [NFR-6]
  Acceptance: ADR-0016 with pointers from ADR-0012, 0014 and 0015;
  threat model env section "Env proposals by developers" (applying a
  proposal is a review step); `docs-site/env.md` proposal flow;
  `docs-site/access.md` matrix and API answers, held to the policy by
  the existing test; OpenAPI operations, optional digest fields and
  the new codes, capability parity test green.
- Standards in play: `golang-security` (no oracle: one refusal decided
  before the value is read; safe zero value for authority; digest
  only for appliers), `golang-testing` (checked-in route matrices, an
  env route table that fails on an unnamed route; generic non-dogfood
  fixtures with placeholder values only).
- Validation: `go build`/`vet`/`test` green (also `-race` on
  `internal/http` and `internal/service`), lint 0 new issues, web
  `verify` + vitest green, docs build + OpenAPI lint clean.
  `access_developer.tsv` differs from S32's only in the stage and
  remove-entry routes and from the frozen v0.6.0 viewer file in
  exactly twelve routes; 30 routes stay admin-only for a developer of
  any scope. Exercised against the real binary on a loopback port
  with a throwaway database and key file, two generic services whose
  env target, validator and restart command are local files in a temp
  directory, a scoped developer, a second one, an all-services
  developer and a scoped viewer: 64 raw-HTTP checks, then the
  Environment tab in a browser as the developer and as the admin
  (propose, refused secret, stale review refused, apply). Not
  exercised: the three production deployments, a host behind a
  reverse proxy, a validator that echoes values.

SCOPE GUARD: no apply, reveal, import, discard or revision rollback
for non-admins; no approval workflow beyond "an admin applies"; no
key management; no access overview or login throttle (S34).

### S35 — Schema migration and seed awareness: Prisma on PostgreSQL (2026-11-13)

**Status**: IMPLEMENTED on `feature/schema-migrations`; PR open.
**Goal**: for a service with a migration setup, show whether its
database matches the running build (applied, pending, failed, edited,
missing), and let an admin apply pending migrations, resolve a failed
one and run the seed from the console, each audited (ADR-0018).
Prisma Migrate on PostgreSQL first; the design must take the next tool
(MongoDB) without touching shared code.

- S35.1 Tool-neutral core (1pd) [FR-P1, NFR-2]
  Acceptance: `internal/dbmigrate` with a `Tool` contract, `Compare`,
  `Count`, `Verdict`; six states and five verdicts covered by
  table-driven tests, including a retried migration, a rolled-back one
  and an unreadable build (nothing is then called pending, modified or
  missing).
- S35.2 Prisma on PostgreSQL (1pd) [FR-P1]
  Acceptance: history from `_prisma_migrations` over one read-only
  connection with Prisma's private URL parameters stripped; a database
  Prisma never touched reads as empty; checksum is the SHA-256 of
  `migration.sql`; seed found in `package.json` or `prisma.config.ts`;
  other datasources refused; the password in no label or error.
- S35.3 Engine access (0.5pd) [NFR-5]
  Acceptance: `docker.ReadFiles` through the archive API (bounded, no
  shell) and `docker.Exec` of an argument vector that holds no engine
  permit while running. A missing path or a stopped container is an
  answer and never counts against the request guard (regression tests
  with a fake engine).
- S35.4 Store, service, routes (2pd) [FR-P1, NFR-5]
  Acceptance: migration 0025 (`service_migrations`, `migration_runs`,
  four audit actions; audit rows and ids kept); URL sealed with the env
  key and bound to its service; setup validated field by field; nine
  routes under `/api/services/{id}/migrations`; `migrations.read` for
  developers and admins, `migrations.manage` for admins; one command
  per service; resolve only for a failed migration; seed only when
  declared; restart recovery audits interrupted runs.
- S35.5 Console (1.5pd) [NFR-6]
  Acceptance: a Migrations tab for developers and admins: verdict with
  a one-sentence meaning, both probes, history newest first with
  failure text, seed card, run list; setup and command dialogs with the
  typed service name for admins; the form quotes each tool's defaults
  from the API and names no tool.
- S35.6 Docs and standards (1pd) [NFR-6]
  Acceptance: ADR-0018; threat model section; `docs-site/migrations.md`;
  `docs-site/migration-standard.md` (migration and seed rules for
  application code, with an `AGENTS.md` block); `docs/MIGRATION_TOOLS.md`
  (standard for adding a tool); access matrix, OpenAPI and parity tests
  green.
- Standards in play: `golang-security` (write-only secret, argument
  vectors, read-only database access, scrubbed output),
  `golang-database` (one bounded connection per read, identifier
  quoting), `golang-concurrency` (detached run context, single slot per
  service, clean shutdown), `golang-testing` (a made-up tool in the
  service and HTTP suites so nothing leans on Prisma).
- Validation: recorded in the PR description.

SCOPE GUARD: no MongoDB or other tool yet; no migration authoring,
diffing or `migrate reset`; no automatic migrate on deploy; no backup
before a command; no seed history for seeds run outside touchgrass;
no migration alerts or notifications.

## Working agreements

- Sprint goal over story count: a sprint succeeds if its goal + validation
  are met, even with spillover.
- No pillar may break another: deploys, metrics, and logging stay
  independently usable (BRD risk mitigation).
- Destructive-schema or auth changes get a second pair of eyes before merge.
- Plan/BRD/ARCHITECTURE drift is a bug: update the doc in the same sprint
  that introduces the change.
