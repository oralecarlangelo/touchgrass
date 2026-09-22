# Database ops runbook (S22)

PostgreSQL health, backups, and restores from the console's Databases
page (plus Redis/SQLite health chips). All Postgres access runs
`docker exec` against `TOUCHGRASS_POSTGRES_CONTAINER` as
`TOUCHGRASS_POSTGRES_USER` — Unix-socket `trust` auth inside the
container, so touchgrass holds no database credentials.

## What the page shows

- **PostgreSQL**: reachability, version, database size, connections
  used/max, postmaster uptime, newest backup age. Unconfigured when
  `TOUCHGRASS_POSTGRES_CONTAINER` is empty.
- **Redis**: ping + used memory + version (hidden when
  `TOUCHGRASS_REDIS_CONTAINER` is empty).
- **SQLite**: touchgrass's own database file size + integrity verdict.

## Backups

`POST /api/databases/backups` (Backup button) streams
`pg_dump -Fc` to `TOUCHGRASS_DB_BACKUP_DIR/<db>-<UTC>.dump` (0600)
with a `.sha256` sidecar, then trims to `TOUCHGRASS_DB_BACKUP_KEEP`
newest (default 14). One job at a time — a second trigger gets 409
`busy` while one runs. Jobs survive nothing: a restart orphans the
row, and boot reconciles it to failed ("interrupted by restart").

Pre-existing manual dumps in the same dir (e.g.
`ticketnation-pre-*.dump`) list and verify like any other backup,
and count toward the keep trim — rename or move anything the trim
must never eat.

At 2.3GB the dump takes a few minutes and compresses to ~400MB.
Disk headroom matters more than CPU: each backup is another ~400MB
until the trim. Watch `/opt/backups` volume usage, not the job.

## Restores

`POST /api/databases/restore {"name": "<file>.dump"}` (Restore
button + typed-filename confirm). The job, in order:

1. Safety backup `<db>-pre-restore-<UTC>.dump`. If this fails, the
   restore aborts before touching anything.
2. Terminate every other backend on the database
   (`pg_terminate_backend`, self excluded). The app starts erroring
   here — this is expected, not a failure signal.
3. `pg_restore --clean --if-exists` into the live database. The
   database itself is never dropped — only its contents are
   replaced. Names are validated (`<name>.dump`, no separators) and
   must already exist in the backup dir.
4. Verify: `SELECT 1` plus a public-schema table count, recorded in
   the job detail.

Plan restores off-peak: the app errors from step 2 until step 4
completes (minutes at this size). Every backup/restore writes an
audit entry (`db_backup`/`db_restore`) with the actor.

## First-restore drill (not yet run)

Restore is proven by tests + review only — no live restore has run
against prod data. Before the first real one: pick an off-peak
window, confirm the newest backup verifies, restore, then confirm
the app recovers (health 200, a smoke login/checkout). Keep the
auto safety backup until the drill is signed off.

## Configuration (prod)

```bash
TOUCHGRASS_POSTGRES_CONTAINER=ticketnation-db-1
TOUCHGRASS_POSTGRES_USER=ticketnation
TOUCHGRASS_POSTGRES_DB=ticketnation
TOUCHGRASS_DB_BACKUP_DIR=/opt/backups/ticketnation
TOUCHGRASS_DB_BACKUP_KEEP=14
TOUCHGRASS_REDIS_CONTAINER=ticketnation-redis-1
```
