# Databases

The **Databases** page reads a shared heartbeat for Docker databases and
Touch Grass's own SQLite database. PostgreSQL backups and restores use
the container configured as the backup target.

## Health at a glance

- **Docker databases** appear automatically from inventory on EC2 and other
  hosts where Touch Grass can read the local Docker daemon. Recognized
  images include PostgreSQL, Redis, Valkey, MySQL, MariaDB, and MongoDB.
- **Freshness** shows when inventory was observed. A running container can
  confirm database readiness when it supplies a healthy Docker health check.
- **PostgreSQL and Redis** show configured targets and their observed
  container status. Database metrics from configured collectors are on
  the **Performance** page.
- **SQLite** shows the local file size and recent availability. Integrity
  checks run as part of separate maintenance operations.

**Refresh heartbeat** reads the latest snapshot. The shared collector picks up
Docker startup and lifecycle changes on its own schedule. If inventory is stale
or unavailable, open **Fleet** to check the Docker collector and socket access.
Expand **Backups and job history** to load the backup files and recent jobs.

## Backups

The Backup button streams `pg_dump -Fc` to
`TOUCHGRASS_DB_BACKUP_DIR/<db>-<UTC>.dump` (mode 0600) with a
`.sha256` sidecar, then trims to the `TOUCHGRASS_DB_BACKUP_KEEP`
newest (default 14). One job at a time — a second trigger gets 409
`busy` while one runs.

Backups also run on a schedule: every `TOUCHGRASS_DB_BACKUP_INTERVAL`
(default `24h`, `0` disables), starting one interval after boot. A
scheduled tick never overlaps a running job — it logs and skips — and
scheduled runs audit under the `scheduler` actor so they read apart
from console-triggered ones in the audit log.

Manual dumps dropped in the same directory list and verify like
any other backup — but they count toward the keep trim, so move
anything the trim must never eat. Dumps compress well (gigabytes
in, hundreds of megabytes out), so watch volume headroom, not the
job: each backup is another few hundred megabytes until the trim.

Every backup can be re-verified from the list (checksum, then
`pg_restore --list` over the dump), and every backup writes a
`db_backup` audit entry.

## Restores

The Restore button takes a backup filename (typed to confirm) and
runs, in order:

1. **Safety backup** `<db>-pre-restore-<UTC>.dump`. If this fails,
   the restore aborts before touching anything.
2. **Drain** every other backend on the database. The app starts
   erroring here — expected, not a failure signal.
3. **`pg_restore --clean --if-exists`** into the live database.
   The database itself is never dropped — only its contents are
   replaced. Names must already exist in the backup dir.
4. **Verify**: `SELECT 1` plus a public-schema table count,
   recorded in the job detail.

Plan restores off-peak: the app errors from step 2 until step 4
completes (minutes at scale). Every restore writes a `db_restore`
audit entry. Before the first real restore, run the drill:
confirm the newest backup verifies, restore off-peak, confirm the
app recovers (health 200, a smoke login), and keep the auto safety
backup until sign-off.

## Jobs

Backup and restore run as jobs with streamed progress and a
durable detail row. A restart orphans a running job's row; boot
reconciles it to failed ("interrupted by restart") rather than
leaving it hanging.

## API

- `GET /api/databases` — health cards.
- `GET /api/databases/backups` — backup list with age and size.
- `POST /api/databases/backups` — start a backup (409 `busy` when
  one runs).
- `POST /api/databases/backups/{name}/verify` — re-verify a backup.
- `POST /api/databases/restore` — restore `{"name": "<file>.dump"}`.
- `GET /api/databases/jobs`, `GET /api/databases/jobs/{id}` —
  job list and detail.

Shapes are in the [REST API reference](/api/).

## Limits

- Managed PostgreSQL and Redis targets are configured by container name
  (`TOUCHGRASS_POSTGRES_CONTAINER`, `TOUCHGRASS_REDIS_CONTAINER`)
  — one of each per host. Discovery can show multiple containers.
- Scheduled backups run on a fixed interval from boot, not a
  wall-clock time: the nightly run drifts with restarts. For an
  exact 02:00 window, disable the scheduler (`0`) and call the
  API from cron instead.
- No point-in-time recovery: restores replace contents from a
  full dump. Keep the safety backups until each restore is
  signed off.
