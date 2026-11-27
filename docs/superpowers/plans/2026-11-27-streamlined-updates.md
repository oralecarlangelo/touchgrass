# Streamlined Touch Grass Updates Implementation Plan

> **For agentic workers:** Use executing-plans to implement this plan task-by-task.

**Goal:** Make standard native installs update-ready automatically, expose accurate release availability, and bring Stellar and Ticketnation onto the same published release.

**Architecture:** Keep the authenticated console unprivileged. Package a generic root-owned updater with native installation, discover its conventional paths, and keep host-specific service paths and cutover locks in root-owned configuration. Updates use checksum-verified release binaries, SQLite backup, detached execution, and exact-version health verification.

**Tech Stack:** Go, React, Python/shell host tooling, systemd, GitHub releases.

**Spec:** User request and screenshots; ADR-0017 supplies the privilege and recovery constraints. Its Stellar-specific deployment details must become configurable installation metadata rather than core product behavior.

## Global Constraints

- Preserve authorization, same-origin protection, checksum verification, bounded execution, root ownership, backups, and binary-only rollback.
- No customer-specific branches in product code or installer defaults.
- Do not overwrite application configuration or live databases.
- A custom build must not downgrade to its same-base stable release.
- Main-branch merge requires explicit user confirmation after reviewable work is ready.

## Review Focus

- Missing helper must not hide release discovery.
- Nonstandard service directories and ports must be discovered or validated once by installation.
- Existing custom binary requires a trustworthy rollback artifact; never execute a service-user-writable binary as root.
- A failed dispatch or interrupted update must leave actionable durable state.
- Published assets must contain the actual fleet fixes; a direct deployment is not a public release.

### Task 1: Accurate release discovery

**Files:** internal/service/self_update.go and tests; web/src/components/UpdatesScreen.tsx and tests.

- [x] Reproduce missing release without helper and custom-version rejection with failing tests.
- [x] Keep discovery independent of installation readiness; allow strictly newer base releases for custom versions.
- [ ] Add conventional helper/status discovery, preserve explicit overrides and origin security.
- [ ] Explain installed/current/custom/setup states and force an actual check on Check again.
- [ ] Run affected Go and UI tests, lint, and authorization regressions.

### Task 2: Portable updater bootstrap

**Files:** scripts/updater/ (installer, request, runner, tests); release packaging; native installation documentation.

- [ ] Adapt the existing Stellar detached updater into a generic packaged kit with root-owned host configuration.
- [ ] Bootstrap from the existing systemd service without manual environment editing; preserve existing cutover lock via explicit deployment metadata.
- [ ] Test generic paths, malformed tags, concurrency, checksum failure, rollback, custom builds, and success in a disposable fixture.
- [ ] Document one-time bootstrap for legacy installations; new native installations include the updater automatically.

### Task 3: Publish and verify consistent rollout

- [ ] Prepare release PR containing fleet and updater changes; complete repository-required checks.
- [ ] Obtain required main merge approval only after the release is concrete and reviewable.
- [ ] Publish versioned assets through release workflow and verify checksums/content.
- [ ] Bootstrap legacy host integrations and exercise the real update path on Stellar and Ticketnation.
- [ ] Verify installed versions, durable success, application health, and Updates UI; record evidence.
