# Databases

The console's **Databases** page watches the data layer next to the
services: PostgreSQL health plus one-click backups and restores,
with Redis and touchgrass's own SQLite as health chips alongside.
Everything Postgres runs `docker exec` against the database
container — Unix-socket trust auth inside the container, so
touchgrass holds no database credentials.

## Health at a glance

- **PostgreSQL**: reachability, version, database size, connections
  used/max, postmaster uptime, and newest backup age. The card
  hides itself when `TOUCHGRASS_POSTGRES_CONTAINER` is empty.
- **Redis**: ping, used memory, version. Hidden when
  `TOUCHGRASS_REDIS_CONTAINER` is empty.
- **SQLite**: touchgrass's own database file size plus an integrity
  verdict.

## Backups

The Backup button streams `pg_dump -Fc` to
`TOUCHGRASS_DB_BACKUP_DIR/<db>-<UTC>.dump` (mode 0600) with a
`.sha256` sidecar, then trims to the `TOUCHGRASS_DB_BACKUP_KEEP`
newest (default 14). One job at a time — a second trigger gets 409
`busy` while one runs.

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

- PostgreSQL and Redis are configured by container name
  (`TOUCHGRASS_POSTGRES_CONTAINER`, `TOUCHGRASS_REDIS_CONTAINER`)
  — one of each per host.
- No scheduled backups in v1: trigger from the console (or the
  API) on whatever cadence fits, or call the API from cron.
- No point-in-time recovery: restores replace contents from a
  full dump. Keep the safety backups until each restore is
  signed off.
