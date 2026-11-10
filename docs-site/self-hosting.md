# Self-hosting

One static binary, one SQLite file, no dependencies. Everything below
runs anywhere Linux runs; the [troubleshooting](/troubleshooting) page
covers the common mistakes.

## Run it

```bash
export TOUCHGRASS_DB=/data/touchgrass.db # default ./touchgrass.db
./touchgrass serve                       # listens on 127.0.0.1:8080
cat /data/initial-admin-password         # first boot only: the one-time admin password
```

## First login

There is no default password. On the first boot touchgrass generates a
one-time password for the `admin` account and writes it to
`initial-admin-password` in the same directory as the database, readable
only by the user the server runs as. The server logs where the file is,
never what it contains.

1. Read the file (`docker compose exec touchgrass cat
   /data/initial-admin-password` in the Docker image).
2. Sign in as `admin` with it. The console shows one screen: choose your
   own password (12 characters or more).
3. That is it — the one-time password stops working and the file is
   deleted. Create a second admin in **Users** so one lost password never
   locks everyone out.

Everyone can change their own password later from the account menu;
doing so signs them out everywhere else.

**Adding people.** Create accounts in **Users**. A `viewer` is
read-only, and you can limit one to selected services so a developer
sees only what they work on. The password you set is temporary: the
user replaces it at first login. See [Users, roles & access](/access).

**Lost a password** and no other admin can reset it in **Users**? On the
host, as the user the server runs as and with the same `TOUCHGRASS_DB`:

```bash
touchgrass users reset-password                  # the admin account
touchgrass users reset-password --username dana  # anyone else
```

It prints a new one-time password once; the account must replace it at
the next login. The reset is recorded in the audit trail. The command
changes the database only: sessions that account already holds on the
running server become limited to the change-password screen on their
next request. An admin's reset in **Users** ends them outright.

**Prefer to set the password yourself?** Export
`TOUCHGRASS_ADMIN_PASSWORD` (12 characters or more) before the first
boot: the admin is seeded with it and no change is forced. It is ignored
once an admin has chosen a password.

`migrate up` runs automatically on boot; the binary self-applies
schema versions. Put it behind nginx or Caddy for TLS, set
`TOUCHGRASS_COOKIE_SECURE=true` behind HTTPS, and rate-limit
`/api/auth/login` — the reference nginx config does all three.

## Your first service

A new install manages nothing: the inventory starts empty. Open **Fleet**,
which lists every container on the Docker daemon, and promote the ones
you want to a managed service; touchgrass drafts the definition for you
to review. `POST /api/services` does the same from a script.

**Upgrading from v0.4.0 or earlier?** Those versions inserted three
services from the project's first deployment (`tn-api`, `tn-fe`,
`admin-fe`) into every new database. Upgrading never removes services,
so if your inventory lists those three and they are not yours, delete
them from each service's page.

## Configuration

| Variable                           | Default            | Meaning                                              |
| ---------------------------------- | ------------------ | ---------------------------------------------------- |
| `TOUCHGRASS_ADMIN_PASSWORD`        | (empty)            | Optional seed for `admin`; empty = [generated one-time password](#first-login) |
| `TOUCHGRASS_DB`                    | `./touchgrass.db`  | SQLite path (WAL sidecars live alongside it)         |
| `TOUCHGRASS_ADDR`                  | `127.0.0.1:8080`   | Listen address; keep loopback behind a proxy         |
| `APP_ENV`                          | `prod`             | `dev` enables the Vite proxy; `prod` serves embedded |
| `TOUCHGRASS_COOKIE_SECURE`         | `false`            | Set `true` behind TLS                                |
| `TOUCHGRASS_ENV_KEY_FILE`          | (empty)            | 0600 key file enabling [env management](/env)        |
| `TOUCHGRASS_ENV_KEY`               | (empty)            | Inline hex key — development only                    |
| `TOUCHGRASS_METRICS_INTERVAL`      | `30s`              | Container sample cadence                             |
| `TOUCHGRASS_WATCH_INTERVAL`        | `5s`               | Live-color poll cadence                              |
| `TOUCHGRASS_LOG_POLL_INTERVAL`     | `5s`               | Container log tail cadence                           |
| `TOUCHGRASS_CUTOVER_TIMEOUT`       | `10m`              | Deploy script ceiling                                |
| `TOUCHGRASS_RETENTION_METRICS`     | `168h`             | Metric sample age trim                               |
| `TOUCHGRASS_RETENTION_NOTIFICATIONS` | `720h`           | Notification age trim                                |
| `TOUCHGRASS_RETENTION_DEPLOYS`     | `8760h`            | Deploy + probe-sample age trim                       |
| `TOUCHGRASS_RETENTION_ERRORS`      | `720h`             | Occurrence age trim (issues follow)                  |
| `TOUCHGRASS_INGEST_MAX_OCCURRENCES` | `10000`           | Occurrence count cap per service                     |
| `TOUCHGRASS_RETENTION_LOGS`        | `168h`             | Log line age trim                                    |
| `TOUCHGRASS_LOGS_MAX_LINES`        | `50000`            | Log line count cap per service                       |
| `TOUCHGRASS_RETENTION_SDK_LOGS`    | `7`                | SDK (application) log age trim, in whole days        |
| `TOUCHGRASS_POSTGRES_CONTAINER`    | (empty)            | Postgres container for health + jobs; empty disables |
| `TOUCHGRASS_POSTGRES_USER`         | `postgres`         | Local-trust postgres role for health and jobs        |
| `TOUCHGRASS_POSTGRES_DB`           | `postgres`         | Database health checks and backups target            |
| `TOUCHGRASS_DB_BACKUP_DIR`         | `./backups`        | pg_dump artifacts plus `.sha256` sidecars            |
| `TOUCHGRASS_DB_BACKUP_KEEP`        | `14`               | Newest backups retained after each backup            |
| `TOUCHGRASS_DB_BACKUP_INTERVAL`    | `24h`              | Scheduled-backup cadence; `0` disables               |
| `TOUCHGRASS_REDIS_CONTAINER`       | (empty)            | Redis container for the health chip; empty hides it  |
| `TOUCHGRASS_SCRIPTS_DIR`           | `scripts`          | Deploy helper scripts; relative resolves at app root |

Durations parse Go syntax (`30s`, `10m`, `168h`).
`TOUCHGRASS_RETENTION_SDK_LOGS` is the exception: a whole-day count.

## Data and backups

State is `touchgrass.db` plus `-wal`/`-shm` sidecars — back them up
together, ideally with SQLite's backup API against a quiet moment.
The DB holds prod log lines and error payloads: `chmod 600`, keep it
off shared backups, and treat read access as production access.

## Sizing

The steady-state footprint is small: sampling every 30s, log polls
every 5s with a 2k-line cap, retention sweeping on schedule. The two
knobs that move disk are `TOUCHGRASS_LOGS_MAX_LINES` and
`TOUCHGRASS_INGEST_MAX_OCCURRENCES` — size them from a day of real
volume, then leave them alone.
