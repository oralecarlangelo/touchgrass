# Touch Grass Self-Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a fleet admin discover and install a published Touch Grass release from the console without SSH.

**Architecture:** The Go server checks the fixed upstream release and authorizes one exact tag. A root-owned host helper starts a detached systemd job, shares the production cutover lock, verifies both release archives, atomically replaces only the binary, and writes durable status. A React screen reads status and reconnects after restart.

**Tech Stack:** Go stdlib HTTP and exec, React/Vite, bash/systemd, existing GitHub release installer.

**Spec:** `docs/superpowers/specs/2026-11-27-touchgrass-self-update-design.md`

## Global Constraints

- No arbitrary download URL, command, file path, or release tag from the browser.
- Fleet admin only; auditable, same-origin mutation; no GitHub write credential on host.
- Host helper and every ancestor must be root-owned and non-writable by `touchgrass`.
- Reuse the existing verified installer and production cutover lock.
- Bootstrap the first feature release with the existing upgrade workflow; production merge and rollout are separate gates.

## Review Focus

- GitHub unavailable or missing architecture asset: show retry; reject install.
- Concurrent install requests: exactly one job starts.
- Console restarts mid-request: durable status survives and UI reconnects.
- Helper/installer fails: report failure, restore the prior binary when replaced, and preserve the database backup without discarding newer writes.
- A scoped or developer session, or cross-origin POST: return 403 without dispatch.

---

### Task 1: Touch Grass update API

**Files:** `internal/service/self_update.go`, `internal/http/self_update.go`, `internal/http/routes.go`, `internal/http/server.go`, `cmd/touchgrass/main.go`, `internal/authz/capability.go`, `internal/model/audit.go`, `internal/service/audit.go`, API and service tests.

**Interfaces:** `SelfUpdate.Check(ctx) (UpdateStatus, error)` reads a bounded upstream release and durable job status. `SelfUpdate.Start(ctx, tag) error` validates exact current release and calls a configured helper with an argument vector. `GET/POST /api/self-update` use a new fleet `updates.manage` capability; the POST requires configured public origin and records audit.

- [ ] Add failing tests for tag/asset validation, authorization, origin, duplicate dispatch, and status parsing.
- [ ] Implement the smallest service and HTTP route contract with an optional helper configuration.
- [ ] Run affected Go tests and lint, then commit the API slice.

### Task 2: Console Updates screen

**Files:** `web/src/components/UpdatesScreen.tsx`, `web/src/lib/api.ts`, `web/src/lib/access.ts`, `web/src/lib/views.ts`, `web/src/lib/hash.ts`, `web/src/App.tsx`, UI tests.

**Interfaces:** `fetchSelfUpdate()` returns installed/latest/job state; `startSelfUpdate(tag)` submits explicit confirmation. Screen uses the existing dialog and status primitives and only appears for fleet admins.

- [ ] Add UI behavior tests for unavailable release, confirmation, restart/reconnect, and failure.
- [ ] Implement Administration → Updates and a bounded reconnect state.
- [ ] Run `npm run verify` and affected UI tests, then commit the UI slice.

### Task 3: Root-owned production updater and rollout

**Files:** Stellar OS `ops/touchgrass/` helper and runner scripts, `install-touchgrass.sh`, `touchgrass.service`, updater sudoers, `.github/workflows/upgrade-touchgrass.yml`, `docs/operations/bluegreen-touchgrass-runbook.md`.

**Interfaces:** `/usr/local/libexec/touchgrass-updater/request vX.Y.Z` writes `pending` status then starts a detached job. The runner takes `/var/tmp/bluegreen-cutover-stellar-web.lock`, verifies prior and new archives, replaces the binary, verifies the exact version, and writes `succeeded`/`failed` to `/var/lib/touchgrass-updater/status.json`.

- [ ] Check shell behavior with stubbed systemd and installer commands, including refusal of malformed tags and concurrent requests.
- [ ] Install the kit through the existing GitHub workflow and retain it as recovery path.
- [ ] Rehearse upgrade and failed-health recovery in a disposable instance.
- [ ] Complete repo verification, prepare PRs, and release only after the existing production gates.
