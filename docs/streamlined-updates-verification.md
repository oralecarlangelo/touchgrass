# Streamlined updates: verification and rollout

Status: implementation verified locally and on an isolated Linux service; production rollout pending publication of a new release.

## Causes

- Stellar reports v0.9.0 because v0.9.0 is the latest published release. Ticketnation's 0.9.0-fleet.1 contains unpublished direct-deployment changes.
- Release discovery returned early when the updater helper was absent, hiding latest-release information.
- Version comparison rejected custom build identifiers entirely.
- The privileged updater lived in Stellar's deployment repository and required explicit host paths/origin configuration.

## Changes

Release discovery is independent of updater readiness. Explicit refresh bypasses the two-minute release cache. Custom builds can install strictly newer base releases, without same-base downgrades. Standard host paths and same-host browser origin validation remove manual environment settings while preserving explicit overrides and fleet-admin authorization.

The release includes a generic native bootstrap and detached updater. Bootstrap discovers the running systemd service and preserves existing database/environment paths while protecting the executable and updater under root-owned ancestors. Updates verify checksums, take consistent SQLite backups, retain prior executables, recover from failed cutovers, and never automatically rewind a live database. Failed pre-stop attempts remove incomplete/orphan recovery files. Successful updates retain three database/executable recovery pairs. Systemd state reconciles interrupted jobs; browser version transitions reload the document once.

Stellar's companion installer/workflow patch follows the canonical executable, preserves the generic helper, and bootstraps only the kit matching the selected release. Bootstrap acquires the existing cutover lock after the workflow's outer lock is released.

## Evidence

- Regression tests failed before fixes for custom versions, hidden release discovery, default paths, interrupted jobs, directory permissions under umask077, incomplete backups, orphan executables, and cancellation handling.
- 17 Python updater tests pass: exact checksum matching, regular-file extraction, version ordering, generic discovery, locking, successful update, failed health, stop failure, failed recovery, backup cleanup, and bounded cancellation/health behavior.
- Frontend type/lint verification, 153 tests across27files, and production build pass. The existing Vite large-chunk warning remains.
- Go affected packages and full suite pass. New-code lint reports zero issues. Final race checks passed for service (179.266s) and HTTP (120.701s) packages.
- Real ARM64 Linux rehearsal used a disposable native service on loopback18190 with its own SQLite database. A checksum-verified published v0.8.0 was bootstrapped without environment edits; a request through the non-root service user's narrow sudo grant installed published v0.9.0. Exact-version health and both recovery artifacts were verified. The fixture unit, paths and sudoers grant were removed; production remained active.
- Rendered actual UpdatesScreen in an isolated Chrome fixture: confirmation → pending → simulated connection loss → succeeded0.10.0. The document loaded exactly twice (one reload), navigation type was reload, and the pending marker was cleared. Temporary fixture files and browser tab were removed.
- Stellar shell syntax and actionlint pass with its existing self-hosted runner labels. No Stellar automated suites were run, in accordance with its temporary validation policy.

## Release gates

Publish a new reviewed Touch Grass release containing the fleet fixes and this updater; direct deployment does not update GitHub's latest-release result. Verify archive contents/checksums, install the selected release on Stellar and Ticketnation, verify installed health/version and Updates status, and record exact release/deployment evidence. Production success is not claimed by this document until those checks finish.
