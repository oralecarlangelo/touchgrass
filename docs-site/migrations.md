# Schema migrations & seeds

touchgrass can tell you whether a service's database matches the build
that is running: which migrations are applied, which are pending, which
failed, and which were edited after they ran. An admin can apply
pending migrations, resolve a failed one, and run the seed from the
console, each audited.

Two tools are supported: **Prisma Migrate on PostgreSQL** (the usual
NestJS + Prisma setup) and **migrate-mongo on MongoDB**. They share one
view and differ in what their history can say; see
[What is supported](#what-is-supported) and
[MongoDB with migrate-mongo](#mongodb-with-migrate-mongo).

## How it works

Two things are compared on every read:

| Side | Where it is read from | How |
| --- | --- | --- |
| **History** | The application's database (`_prisma_migrations` for Prisma, the `changelog` collection for migrate-mongo) | One short read-only connection per read, with the database URL you store |
| **Running build** | The service's running container (`prisma/migrations/` for Prisma, `migrations/` for migrate-mongo) | The container engine's file API. No shell or tool is needed in the image |

Commands (deploy, resolve, seed) run **inside the application's
container**, so they use the application's own environment, network and
tool version. touchgrass itself only ever reads the database.

## Set it up

You need the [env key](./env.md#key-custody) configured: the database
URL is stored encrypted with it. Without a key the setup answers 503
`env_key_missing`.

**1. Create a read-only database role.** touchgrass reads one table, so
do not give it the application's credentials. For PostgreSQL (MongoDB is
[below](#mongodb-with-migrate-mongo)):

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
| Migration tool | `prisma` or `migrate-mongo` |
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

| Button | Prisma | migrate-mongo | Notes |
| --- | --- | --- | --- |
| Apply pending migrations | `prisma migrate deploy` | `migrate-mongo up` | Disabled while a migration is failed |
| Mark rolled back | `prisma migrate resolve --rolled-back <name>` | not offered | Only on a failed migration. Undo its partial changes by hand first |
| Mark applied | `prisma migrate resolve --applied <name>` | not offered | Only on a failed migration. Complete its SQL by hand first |
| Run seed | `prisma db seed` | not offered | Only when the running build declares a seed |

One command runs per service at a time, for at most 20 minutes. If
touchgrass restarts while one is running, the run is marked failed with
"outcome unknown": check the status before running it again.

::: warning Seeds write to the database you point them at
A seed that is not safe to repeat will duplicate or overwrite data.
Write seeds so that running them twice changes nothing; the
[coding standard](./migration-standard.md) says how.
:::

The image must contain the migration CLI. Many production images prune
it: if a command fails with `npx` unable to find `prisma` (or
`migrate-mongo`), keep it in `dependencies`, or name a compose service
that has it.

## MongoDB with migrate-mongo

Prisma keeps no migration history for MongoDB (it only has `db push`),
so MongoDB support reads [migrate-mongo](https://github.com/seppevs/migrate-mongo)'s
record instead: one document per applied script in the `changelog`
collection.

**Create a user that can only read the database:**

```js
// mongosh, in the application's database
db.createUser({ user: 'touchgrass_ro', pwd: '<generate one>', roles: [{ role: 'read', db: 'shop' }] });
```

**Set it up** with the tool `migrate-mongo` and a URL the touchgrass
host can reach. The database name in the path is required:

```
mongodb://touchgrass_ro:…@127.0.0.1:27017/shop
mongodb://touchgrass_ro:…@db1:27017,db2:27017/shop?replicaSet=rs0
mongodb+srv://touchgrass_ro:…@cluster0.example.net/shop
```

| URL part | Meaning |
| --- | --- |
| Path (`/shop`) | The database that holds the changelog. It is also where the user is looked up unless `authSource` says otherwise |
| `authSource`, `authMechanism`, `replicaSet`, `directConnection`, `readPreference`, `tls`, `tlsCAFile`, `tlsCertificateKeyFile` | Passed to the driver. Every other option is dropped |
| `changelogCollection=<name>` | Only if `changelogCollectionName` in `migrate-mongo-config.js` is not `changelog`. Read by touchgrass, never sent to MongoDB |

The migrations directory defaults to `migrations` and the file
extension is read from `migrate-mongo-config.js` (`.js` when it is not
a plain string there). If `migrationsDir` in the config is something
else, set the **Migrations directory** field to match.

**What is different from Prisma**, because of what migrate-mongo
records:

- **No failed state, nothing to resolve.** migrate-mongo writes a
  document only after a script succeeds. A script that throws leaves
  the migration **Pending**; the failure is in the run list, with the
  tool's output, not in the history.
- **No rolled-back state.** `migrate-mongo down` deletes the document,
  so the migration reads as Pending again. touchgrass does not run
  `down`.
- **No seed.** migrate-mongo has none, so the seed card stays empty.
- **Edits are visible only with `useFileHash: true`** in
  `migrate-mongo-config.js`. Without it the changelog holds no hash and
  an edited script still reads as Applied. With it, migrate-mongo runs
  an edited script **again** on the next `up`: *Edited after apply*
  here means "the next apply will re-run this file".
- **Turning `useFileHash` on later:** migrate-mongo then treats every
  earlier document (which has no hash) as not applied and re-runs those
  scripts on the next `up`. touchgrass shows them as Applied until that
  `up` has written hashed documents. Make scripts safe to run twice
  before switching it on.
- **No duration.** The changelog stores when a script was applied, not
  how long it took.

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
| migrate-mongo | MongoDB (standalone, replica set, `mongodb+srv`) | Supported: status and apply. No resolve, no seed |
| Prisma | MongoDB | Not possible: Prisma records no migration history for MongoDB |

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| Database: `connecting to postgres: … connection refused` or a hostname error | The URL is not reachable from the touchgrass host. Use an address the host can reach, not the container-network name |
| Database: `permission denied for table _prisma_migrations` | The read-only role lacks `SELECT` on the table |
| Database: `server selection error` or `server selection timeout` (MongoDB) | The host cannot reach any server in the URL within 5 seconds, or `replicaSet` names a set whose members advertise addresses the host cannot resolve. Try `directConnection=true` with one reachable member |
| Database: `Authentication failed` (MongoDB) | Wrong password, or the user lives in another database: add `authSource=<that database>` |
| Every migrate-mongo migration shows **Pending** though they were applied | The changelog is in another collection or database: check `changelogCollectionName` and `databaseName` in `migrate-mongo-config.js`, and the file extension |
| Apply fails with `Could not migrate up, a lock is in place` | An earlier `migrate-mongo up` was killed and left its lock. Check nothing is running, then delete the document in `changelog_lock` |
| The status takes several seconds, or fails once and works on retry | The container engine is busy (touchgrass limits itself to two engine calls at a time, and its own periodic collectors share that limit). Reload |
| Running build: `no running container for this service` | The service's container is not running, or the compose project or service name in the service definition does not match. Name the compose service in the setup |
| Running build: `path not found in container` | The migrations are somewhere else in the image. Set the working directory or the migrations directory |
| Every migration shows **Edited after apply** | The image changes the files (for example line endings) between build and run |
| 503 `engine_unavailable` | The container engine stopped answering; touchgrass pauses engine calls for a minute after repeated failures |
