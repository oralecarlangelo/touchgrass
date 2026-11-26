# Touch Grass self-update from GitHub Releases

Status: proposed · 27 November 2026

## Outcome

An administrator can open **Administration → Updates**, see the installed version and the latest published compatible Touch Grass release, and start installation from the app without SSH or a GitHub personal access token. The app shows the exact tag, release notes link, running state, and final installed version. Updates are operator-triggered; they never run on a timer. The first release containing this feature is bootstrapped through the existing GitHub Actions upgrade workflow.

## Authority and release selection

The browser never supplies an executable path or URL. The server reads the fixed Touch Grass upstream repository, `oralecarlangelo/touchgrass`, which is also the source used by the host installer. It accepts only published, non-prerelease `vMAJOR.MINOR.PATCH` tags newer than the running version and requires the current Linux architecture's archive plus `checksums.txt`. A bounded HTTPS request fetches the release metadata; a failed or stale check leaves installation disabled and offers retry. The app server provides `GET /api/self-update` for status and `POST /api/self-update` for one exact tag and an explicit confirmation. Both routes require a fleet-wide administrator capability. The mutation also requires an `Origin` matching the operator-configured public origin, an audit entry, and a single active-job guard. Missing or stale release metadata disables installation. Scoped accounts and developers cannot start an update.

## Execution boundary

The Go server does not replace its own binary. It passes only the validated tag as one argument to a root-owned, configured request helper via `exec.CommandContext` (no shell). The helper starts a separate systemd job and returns before Touch Grass stops. A narrow sudoers entry allows the `touchgrass` service user to call only that helper; the helper validates the tag again. The helper, its parent directories, and the request and runner scripts live under `/usr/local/libexec/touchgrass-updater/`, owned by root and not writable by the `touchgrass` user. This is required because `/opt/touchgrass` itself is service-user-owned. The root job takes the existing Stellar OS production cutover lock, so a console update cannot overlap a web cutover or another operation that takes that lock. It downloads the prior and requested release archives into a root-owned work directory, verifies both against published checksums, then atomically replaces only the console binary. Host configuration and migrations requiring a reviewed rollout stay with the existing workflow. No GitHub write credential is stored on the host.

The job records a compact status file under root-owned `/var/lib/touchgrass-updater/`, readable but not writable by Touch Grass: requested tag, started/finished times, and `pending`, `running`, `succeeded`, or `failed`. It never writes credentials, command output, release assets, or raw logs to that file. Before replacement it takes an SQLite online backup and downloads the prior published binary into a root-owned work directory, verifying its checksum. It never reads or executes the current binary from service-user-writable `/opt/touchgrass` as root. If installation fails, it restores the verified prior binary and preserves the database backup, root-readable only, for reviewed recovery. The job never automatically rewinds the database because the replacement process may have accepted writes before failing health. The status file and systemd journal preserve evidence across the process restart.

## User experience

The Updates page shows installed version, available tag, published date, release-notes link, and a single **Install update** action. A confirmation dialog names the exact tag and explains that the console will briefly reconnect and require sign-in again. After acceptance, the page polls the status endpoint with bounded backoff. During the restart it shows a reconnecting state rather than reporting failure immediately; once the new server answers, it reloads and rechecks version and status. If the job fails, it displays a safe error and points the operator to the existing GitHub Actions recovery workflow. The page is absent when the updater is not configured or the platform is unsupported, except for an admin-facing setup explanation.

## Contracts and rollout

- Touch Grass owns release discovery, admin authorization, audit, update status API, and UI. The update module is separate from service deploy/cutover logic.
- The Stellar OS production overlay owns the root-owned systemd helper and directory, restricted sudoers entry, installer-kit copy, public-origin setting, and lock path. Other deployments can supply their own helper through the same narrow command/status interface.
- The existing `Upgrade touchgrass console` workflow remains the bootstrap and recovery path. Its checked-in installer installs the root-owned request and runner scripts so future UI updates use the same verified release assets.
- Rollout: release the app feature, run the existing workflow once to install that release and its host helper, verify the Updates page, then test a later tagged release through the page. Do not self-update production to the same version merely to demonstrate the button.

## Verification

Cover release-tag and asset validation, authorization and same-origin refusal, duplicate requests, helper argument construction, root-owned path checks, status parsing, and browser reconnection with generic fixtures. Check the host helper with stubbed systemd and installer commands, then rehearse one upgrade and failed-health rollback in a disposable instance. The production gate confirms a checksum-verified archive, preserved database and login, unchanged live web target, and a recorded audit/status trail.

## Alternatives considered

A link to GitHub Actions is already available but requires leaving Touch Grass and manually entering the tag. Direct GitHub workflow dispatch from Touch Grass would need a persistent GitHub App credential on the console host. Running the installer inside the HTTP process would terminate the request mid-update and make failures hard to recover. A separate root-owned local job keeps the existing release trust and cutover controls without adding a GitHub write secret.
