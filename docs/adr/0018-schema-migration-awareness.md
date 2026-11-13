# ADR-0018: Schema migration awareness — read the history, compare with the build, run the tool in its own container

- Status: Accepted (2026-11-13)
- Source: S35, the request to make touchgrass aware of an application's
  migration history and seeds, starting with Prisma on PostgreSQL
- Builds on: ADR-0004 (agentless), ADR-0006 (generic engine), ADR-0012
  (encrypted store), ADR-0014 (capabilities with service scope)

## Context

A deploy can succeed while the database it runs against is in the wrong
shape: a migration that was never applied, one that failed half way, or
one that was edited after it ran. Until now touchgrass could not see
any of this. The operator found out from an error in the logs, then
opened a shell in the container to ask the migration tool.

Three facts shaped the design:

- **The history lives in the application's database.** Prisma keeps one
  row per attempt in `_prisma_migrations`: name, checksum, start, finish,
  rolled-back time, and the failure text.
- **What is pending lives in the running build.** A migration is pending
  when the image ships it and the database does not record it. Neither
  side alone can say so.
- **A seed leaves no record anywhere.** `prisma db seed` runs a script
  and writes nothing about having run.

The feature also has to stay inside the project-agnostic boundary
(AGENTS.md): Prisma is the first tool, not the shape of the feature.
MongoDB is next.

## Decision

**A new package, `internal/dbmigrate`, owns the comparison; a `Tool`
owns everything tool-specific.** A tool says where its files live by
default, validates and redacts a database URL, reads its history,
finds migrations and a seed in a build's files, and names the arguments
for each action. The comparison (`Compare`), the counts and the verdict
are shared and know no tool. `Prisma` is the first `Tool`; tools are
registered in one place (`cmd/touchgrass/migrations.go`).

**History is read over a direct, read-only database connection.** The
setup stores a database URL. Each status read opens one connection,
starts a read-only transaction, runs one fixed `SELECT`, and closes.
Nothing is pooled or cached. A database the tool has never migrated
(no history table) reads as an empty history, not an error.

**The URL is sealed like an env value, in its own table.** It is
encrypted with the env key (ADR-0012) with the service id as associated
data, so the envelope cannot be moved to another service. It is
write-only: no route returns it. The API reports a redacted target
(host, port, database, schema). It is deliberately *not* an entry in
the service's env store:

- env entries are rendered into the application's env file, and this
  value is for touchgrass, not for the application;
- the application's own URL usually names a host on the container
  network that the touchgrass process cannot resolve;
- reading history needs `SELECT` on one table, so the setup can and
  should use a read-only role instead of the application's credentials.

Without an env key the feature reports every service as not configured
and refuses to save a setup.

**Shipped migrations are read from the running container through the
engine's archive API.** No shell, no tools in the image, and no
dependency on the migration CLI. The container is the service's own
(for blue-green, the live color first) unless the setup names a compose
service. For Prisma the checksum is the SHA-256 of `migration.sql`,
which is what Prisma stores, so an applied migration whose shipped file
differs is detected exactly.

**Each migration is in one of six states**: `applied`, `pending`,
`failed`, `modified` (applied, shipped file differs), `missing`
(applied, not in this build) and `rolled_back`. The service verdict is
the worst present: `failed`, then `drift` (modified or missing), then
`pending`, then `in_sync`; `unknown` when the history cannot be read.
A side that cannot be read is part of the answer: the status says which
side and why, and reports what the other side still shows. Without the
build's files nothing is called pending, modified or missing.

**Commands run inside the application's container.** Deploy, resolve
and seed are `docker exec` of an argument vector: the stored command
prefix (default: the tool's) plus the tool's arguments for the action.
No shell is involved, and the only request-supplied token is a
migration name held to a strict pattern. Running in the container means
the application's own environment, tool version and network are what
run, and touchgrass never needs a credential that can write.

Guards on a command:

- admin only (`migrations.manage`), with the service name typed as
  confirmation, the same gate as an env apply;
- one command per service at a time;
- a resolve is refused unless the history records that migration as
  failed, and refused outright when the history cannot be read;
- a seed is refused unless the running build declares one;
- 20 minutes at most; a restart fails the run as "outcome unknown".

**touchgrass keeps the run history, and that is the seed history.**
Every command is a row in `migration_runs` (who, when, command line,
container, image, exit code, the last 256 KiB of output) and an audit
row (`migration_deploy`, `migration_resolve`, `seed_run`, plus
`migration_config` for setup changes). A seed run outside touchgrass is
invisible, and the UI says so.

**Access.** `migrations.read` (status and the run list) goes to
developers and admins, service-scoped. `migrations.manage` (setup,
commands, run output) is admin-only. Viewers gain nothing: the frozen
v0.6.0 viewer matrix lists all nine routes as refused. Run output is
admin-only because a seed script can print anything.

## Alternatives considered

- **`docker exec psql` into a database container**, as the Databases
  page does. No credential to store, but it only works when the
  database is a local container, and it ties the feature to one engine's
  client. Rejected for the direct connection, which also reaches managed
  databases.
- **Parse `prisma migrate status`.** It needs the CLI in the production
  image, gives no timings, checksums or failure text, and its output is
  prose that changes between versions.
- **Reference a key in the env store** for the URL. See the three
  reasons above.
- **An application-side seed table** that touchgrass reads. It would see
  every seed run, but every application would have to adopt a schema
  touchgrass defines, which the project-agnostic boundary rules out as a
  requirement. It remains possible later as an optional reader.
- **Run the migration tool from the touchgrass host.** That needs the
  tool, the application's schema files and a writing credential on the
  host. Rejected.

## Consequences

- touchgrass now holds a database credential for each configured
  service. A read-only role limits what a leak of the key and the
  SQLite file exposes to the migration history.
- touchgrass gains a PostgreSQL driver (`jackc/pgx`, pure Go).
- An admin can change an application's database from the console. This
  is new operator power and is listed in the threat model.
- Pending detection depends on a running container. With none, the
  status still reports the history and says the build could not be read.
- Prisma datasources other than PostgreSQL are refused with a clear
  error until they have a reader.

## Adding the next tool (MongoDB)

A tool is one type implementing `dbmigrate.Tool`, registered in
`migrationTools()`. The store, the service layer, the routes, the access
policy and the console need no change: the setup form reads each tool's
defaults from the API. `docs/MIGRATION_TOOLS.md` is the standard a new
tool must meet. For MongoDB the open choice is which history to read
(Prisma keeps none for MongoDB; `migrate-mongo` keeps a `changelog`
collection), and that choice belongs in its own ADR.
