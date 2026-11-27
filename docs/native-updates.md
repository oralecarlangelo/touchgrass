# Native console updates

Administration → Updates compares the installed build with the latest published
GitHub release. A direct development deployment is not a published release.
Custom builds such as `0.9.0-fleet.1` can move to a newer base release, such as
`v0.10.0`, but are never offered their same-base or an older stable release.
“Check again” explicitly refreshes the upstream release instead of reusing the
two-minute cache. Release information remains visible when installation is not
yet enabled.

## One-time setup for an existing native service

Use the `scripts/updater` directory from a checksum-verified Touch Grass release
archive. On the Linux host where the console runs:

```sh
sudo python3 -I scripts/updater/install.py
```

The installer discovers the running `touchgrass.service`, service user, local
port, database and working directory. It backs up SQLite, installs the generic
updater, and moves the running executable to a root-owned location via a systemd
drop-in. The console briefly restarts with the **same version**. Existing data,
environment files and application services are preserved. No environment editing
or GitHub token is required. Include this command in a new native installation's
setup after its initial service is healthy.

If the console has a different unit name, pass `--service example.service`. If
other deployment tooling already uses an exclusive cutover lock, pass its exact
path with `--cutover-lock /var/tmp/example-deploy.lock` on first setup. Re-running
setup preserves that lock. This is deployment metadata, not customer-specific
behavior in Touch Grass.

The bootstrap requires Python 3, systemd, sudo/visudo, a running non-root native
service using the `serve` command, a local IPv4 listener and an on-disk SQLite
database. Containers and non-systemd installations keep their existing deployment
workflow. A service that sets `NoNewPrivileges=yes` cannot invoke the narrow sudo
helper; the installer reports this instead of silently weakening its policy.

## What happens when Install update is selected

1. The fleet administrator confirms the exact published release. The API checks
   authorization, browser origin, version and matching architecture assets.
2. A root-owned request helper starts a separate, time-limited systemd job.
   Concurrent updates and configured cutovers cannot overlap.
3. The job downloads the fixed upstream release, checks the exact archive against
   `checksums.txt`, and extracts only its regular `touchgrass` executable.
4. A consistent SQLite backup passes `quick_check`. The binary is replaced
   atomically and the service starts. Success requires the exact target version
   in the health response.
5. If startup fails, the previous protected binary is restored. The live database
   is never rewound automatically. The verified database backup and prior executable remain available for
   reviewed recovery, including when automatic recovery fails. Successful updates retain three updater database backups,
   separately from scheduled backups.

The console reconnects after restart; a new login may be needed. Installation
uses no GitHub write credentials. Application credentials never enter updater
configuration or status files.

## Host files and recovery

- `/usr/local/lib/touchgrass/touchgrass`: protected executable.
- `/usr/local/libexec/touchgrass-updater/`: request helper, runner and root-only
  installation metadata.
- `/var/lib/touchgrass-updater/status.json`: durable job status, readable by the
  console.
- `/var/lib/touchgrass-updater/bootstrap-*`: original binary, SQLite snapshot and
  previous updater drop-in, if any.
- `/etc/systemd/system/<unit>.d/50-touchgrass-updater.conf`: executable and helper
  wiring; original unit and environment files remain unchanged.
- `/etc/sudoers.d/touchgrass-updater`: exact request helper grant.

Inspect the corresponding `touchgrass-update-*.service` journal for failed jobs.
The API checks generated job units against systemd so a reboot or terminated job
does not permanently disable retries. Each job has a ten-minute execution limit
and a five-minute termination allowance for bounded binary recovery.
Before reverting a bootstrap, stop the console, restore the saved drop-in (or
remove the new one when none existed), reload systemd and restart. The original
executable path is preserved. Do not restore SQLite over a live database.

### Update request could not be recorded

The console writes an audit entry before it dispatches an update. Check the
console's systemd journal for the audit failure. If an older installation
rejects the `self_update` action, use the existing deployment recovery workflow
to install v0.10.8 or later. On a native host with the root-owned updater already
configured, the host administrator can use its request helper with the exact
published tag. That helper verifies the archive and backs up the database before
restarting the console. The new binary applies the audit migration on startup.
Verify the exact installed version and the durable job status before retrying
from the console.

Explicit `TOUCHGRASS_UPDATE_HELPER`, `TOUCHGRASS_UPDATE_STATUS_FILE` and
`TOUCHGRASS_PUBLIC_ORIGIN` settings remain supported. Standard paths are discovered
without those settings. Without an explicit origin, requests must carry a valid
same-host browser Origin and pass Go's cross-origin protection; forwarded host
headers are never trusted for this decision.
