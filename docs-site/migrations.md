# Schema migrations & seeds

touchgrass can tell you whether a service's database matches the build
that is running: which migrations are applied, which are pending, which
failed, and which were edited after they ran. An admin can apply
pending migrations, resolve a failed one, and run the seed from the
console, each audited.

The first supported tool is **Prisma Migrate on PostgreSQL** (the usual
NestJS + Prisma setup). Other tools plug into the same view; see
[What is supported](#what-is-supported).

## How it works

Two things are compared on every read:

| Side | Where it is read from | How |
| --- | --- | --- |
| **History** | The application's database (`_prisma_migrations` for Prisma) | One short read-only connection per read, with the database URL you store |
| **Running build** | The service's running container (`prisma/migrations/`) | The container engine's file API. No shell or tool is needed in the image |

Commands (deploy, resolve, seed) run **inside the application's
container**, so they use the application's own environment, network and
tool version. touchgrass itself only ever reads the database.

## Set it up

You need the [env key](./env.md#key-custody) configured: the database
URL is stored encrypted with it. Without a key the setup answers 503
`env_key_missing`.

**1. Create a read-only database role.** touchgrass reads one table, so
do not give it the application's credentials:

```sql
CREATE ROLE touchgrass_ro LOGIN PASSWORD '<generate one>';
GRANT CONNECT ON DATABASE shop TO touchgrass_ro;
GRANT USAGE ON SCHEMA public TO touchgrass_ro;
GRANT SELECT ON public._prisma_migrations TO touchgrass_ro;
```

If Prisma has not created the table yet, grant it once it exists, or
set a default so new tables are readable:
`ALTER DEFAULT PRIVILEGES FOR ROLE <app role> IN SCHEMA public GRANT SELECT ON TABLES TO touchgrass_ro;`

**2. Open the service, then Migrations → Set up migrations** (admin):

| Field | Meaning |
| --- | --- |
| Migration tool | `prisma` |
| Database URL | A URL the **touchgrass host** can reach, for example `postgresql://touchgrass_ro:…@127.0.0.1:5432/shop?schema=public`. The application's own URL usually names a host on the container network that touchgrass cannot resolve |
| Compose service | Optional. The compose service whose container ships the migrations and runs the commands. Blank uses the service's own container; for blue-green, the live color |
| Working directory | Optional. Blank uses the container's working directory |
| Migrations directory | Optional, relative to the working directory. Blank uses the tool's default |
| Command | Optional. How the tool is started, split on spaces with no shell. Blank uses the tool's default (`npx --no-install prisma` for Prisma) |

The URL is never shown again. The setup shows where it points (host,
port, database, schema). Leave the field blank when editing to keep it.

The same setup over the API:

```bash
curl -X PUT https://touchgrass.example.com/api/services/shop-api/migrations/config \
  -b cookies.txt -H 'Content-Type: application/json' \
  -d '{"tool":"prisma","database_url":"postgresql://touchgrass_ro:…@127.0.0.1:5432/shop?schema=public"}'
```

## Read the status

Each migration is in one state:

| State | Meaning | What to do |
| --- | --- | --- |
| **Applied** | Recorded as applied, and the running build ships the same file | Nothing |
| **Pending** | The running build ships it; the database has not applied it | Apply pending migrations |
| **Failed** | It started and never finished. The failure text is shown | Fix the cause, then resolve it. Nothing later applies until you do |
| **Edited after apply** | Applied, but the file in the running build differs from the one that ran | Restore the original file and make the change in a new migration |
| **Not in this build** | Applied, but the running build does not ship it | Expected right after rolling back to an older image; otherwise find out which build applied it |
| **Rolled back** | Marked rolled back and no longer shipped | Nothing |

The service verdict is the worst state present: **Failed**, then
**Drift** (edited or not in this build), then **Pending**, then
**In sync**. It is **Unknown** when the database cannot be read.

If one side cannot be read, the status says which and why, and still
reports the other. With no running container, the history is shown but
nothing can be called pending or edited.

## Run a command

Admins only. Each asks you to type the service name, runs in the
service's container, and is recorded in the run list and the audit log
under your name.

| Button | Runs (Prisma) | Notes |
| --- | --- | --- |
| Apply pending migrations | `prisma migrate deploy` | Disabled while a migration is failed |
| Mark rolled back | `prisma migrate resolve --rolled-back <name>` | Only offered on a failed migration. Undo its partial changes by hand first |
| Mark applied | `prisma migrate resolve --applied <name>` | Only offered on a failed migration. Complete its SQL by hand first |
| Run seed | `prisma db seed` | Only when the running build declares a seed |

One command runs per service at a time, for at most 20 minutes. If
touchgrass restarts while one is running, the run is marked failed with
"outcome unknown": check the status before running it again.

::: warning Seeds write to the database you point them at
A seed that is not safe to repeat will duplicate or overwrite data.
Write seeds so that running them twice changes nothing; the
[coding standard](./migration-standard.md) says how.
:::

The image must contain the migration CLI. Many production images prune
it: if a command fails with `npx` unable to find `prisma`, keep `prisma`
in `dependencies`, or name a compose service that has it.

## Seed history

A seed leaves no record in the application's database, so the only
history is the seeds run **from touchgrass**: who, when, the result.
A seed run from a terminal, a CI job or a deploy script does not
appear.

## Who can do what

| | Viewer | Developer (service in scope) | Admin |
| --- | --- | --- | --- |
| See status and the run list | no | yes | yes |
| See a run's output | no | no | yes |
| Set up, apply, resolve, seed | no | no | yes |

Run output is admin-only because a seed script can print anything.

## What is supported

| Tool | Database | Status |
| --- | --- | --- |
| Prisma Migrate | PostgreSQL | Supported |
| Prisma Migrate | MySQL, SQL Server, SQLite, CockroachDB | Not yet: the setup refuses the URL |
| MongoDB migration tools | MongoDB | Next |

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| Database: `connecting to postgres: … connection refused` or a hostname error | The URL is not reachable from the touchgrass host. Use an address the host can reach, not the container-network name |
| Database: `permission denied for table _prisma_migrations` | The read-only role lacks `SELECT` on the table |
| Running build: `no running container for this service` | The service's container is not running, or the compose project or service name in the service definition does not match. Name the compose service in the setup |
| Running build: `path not found in container` | The migrations are somewhere else in the image. Set the working directory or the migrations directory |
| Every migration shows **Edited after apply** | The image changes the files (for example line endings) between build and run |
| 503 `engine_unavailable` | The container engine stopped answering; touchgrass pauses engine calls for a minute after repeated failures |
