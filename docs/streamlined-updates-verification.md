# Streamlined updates: verification and rollout

Status: deployed and verified on both production hosts on 2026-11-27 using published v0.10.1 (source a748181d762b6df64de46c2f230e984d6b952336).

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
- 18 Python updater tests pass, including concurrent WAL writes during backup: exact checksum matching, regular-file extraction, version ordering, generic discovery, locking, successful update, failed health, stop failure, failed recovery, backup cleanup, and bounded cancellation/health behavior.
- Frontend type/lint verification, 153 tests across27files, and production build pass. The existing Vite large-chunk warning remains.
- Go affected packages and full suite pass. New-code lint reports zero issues. Final race checks passed for service (179.266s) and HTTP (120.701s) packages.
- Real ARM64 Linux rehearsal used a disposable native service on loopback18190 with its own SQLite database. A checksum-verified published v0.8.0 was bootstrapped without environment edits; a request through the non-root service user's narrow sudo grant installed published v0.9.0. Exact-version health and both recovery artifacts were verified. The fixture unit, paths and sudoers grant were removed; production remained active.
- Rendered actual UpdatesScreen in an isolated Chrome fixture: confirmation → pending → simulated connection loss → succeeded0.10.0. The document loaded exactly twice (one reload), navigation type was reload, and the pending marker was cleared. Temporary fixture files and browser tab were removed.
- Stellar shell syntax and actionlint pass with its existing self-hosted runner labels. No Stellar automated suites were run, in accordance with its temporary validation policy.

## Release gates

Publish a new reviewed Touch Grass release containing the fleet fixes and this updater; direct deployment does not update GitHub's latest-release result. Verify archive contents/checksums, install the selected release on Stellar and Ticketnation, verify installed health/version and Updates status, and record exact release/deployment evidence. Production success is not claimed by this document until those checks finish.


## Production rollout evidence — 2026-11-27

- [Touch Grass PR23](https://github.com/oralecarlangelo/touchgrass/pull/23) passed all CI checks and merged as 9a872744d1f85ca2f46c207c320fb1961a8a6571. [Stellar PR303](https://github.com/oralecarlangelo/stellar-os/pull/303) supplied the host workflow integration.
- The initial v0.10.0 rollout exposed starvation in an incremental backup of a busy 2 GB WAL database. Setup was stopped before changing the running service. [PR24](https://github.com/oralecarlangelo/touchgrass/pull/24) pins a read snapshot; its concurrent-write regression failed before and passed after the fix. All CI checks passed. A live copy plus integrity check completed in 211.4 seconds while the console remained available. Abandoned partial files were removed; the verified recovery copy was retained.
- [Published v0.10.1](https://github.com/oralecarlangelo/touchgrass/releases/tag/v0.10.1) built successfully in [release run 45995013563](https://github.com/oralecarlangelo/touchgrass/actions/runs/45995013563). The downloaded ARM64 archive matched checksums.txt, contained the native kit, and its updater source matched the tested patch.
- [Stellar rollout 45995772041](https://github.com/oralecarlangelo/stellar-os/actions/runs/45995772041) succeeded: checksum-verified installation, generic bootstrap, exact 0.10.1 healthy response, and unchanged live web target. The browser endpoint requires separate HTTP authentication, so its UI was not visually rechecked; the workflow's local health/version gates verified the deployment.
- Ticketnation bootstrapped from the verified release without editing environment configuration. The installed request helper dispatched the detached update as the native service user. Durable job status recorded succeeded for v0.10.1 at 12:14:35 UTC; the public health endpoint reported status ok/version 0.10.1. Database and prior-binary recovery artifacts were retained. Helper ownership is root 0755 and private configuration root 0600.
- In the signed-in production browser, Updates showed installed 0.10.1, latest published v0.10.1, current release, and last update succeeded. The setup warning was absent. Check again refreshed successfully. The updated page was left open for review.
- Ticketnation website and Stellar app returned HTTP 200 after rollout. No application release or database restore was performed by this console update.

The repository has no remote main branch; the existing release path targets develop. Both release tags followed that established path. The earlier main-merge gate therefore did not apply. Future standard binary releases can be installed from Updates. Native updater-code changes require refreshing the packaged host kit; Stellar's workflow performs that automatically.
