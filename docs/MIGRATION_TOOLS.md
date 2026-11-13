# Adding a migration tool

The standard for adding a schema-migration tool to touchgrass (the
feature described in ADR-0018). It is written for contributors and for
AI coding agents: follow it top to bottom, and treat every "must" as a
review blocker. There are two reference implementations: Prisma on
PostgreSQL (`internal/dbmigrate/prisma*.go`), a tool whose history
records failures, checksums and durations, and migrate-mongo on MongoDB
(`internal/dbmigrate/migratemongo*.go`, ADR-0019), a tool whose history
records none of them. Read the one closer to the tool you are adding.

## What a tool is

One Go type that implements `dbmigrate.Tool`, in `internal/dbmigrate`,
registered in `migrationTools()` in `cmd/touchgrass/migrations.go`.
That is the whole change surface:

| You write | You do not touch |
| --- | --- |
| `internal/dbmigrate/<tool>.go` and, per database, `<tool>_<engine>.go` | `Compare`, `Count`, `Verdict` (`status.go`) |
| Their tests | `internal/service/migrations*.go` |
| One line in `migrationTools()` | Routes, access policy, store, migrations |
| The supported-tools table in `docs-site/migrations.md` | The console: it reads each tool's defaults from the API |

If a tool seems to need a change on the right-hand side, the `Tool`
contract is missing something. Change the contract for every tool, in
its own commit, and say why in an ADR. Do not add a branch on the
tool's name anywhere outside its own file.

## The contract

| Method | Must | Must not |
| --- | --- | --- |
| `Name()` | Return a short lowercase identifier. It is stored and shown | Change once released |
| `Layout()` | Return the default migrations directory and any single files to read, relative to the application's working directory | Assume an application |
| `DefaultCommand()` | Return an argument vector that starts the tool's CLI without touching the network (for npm tools, `npx --no-install …`) | Use a shell, or a string that needs splitting |
| `Describe(url)` | Validate the URL and return a label with host, port, database and namespace | Connect. Put the password, the user or the raw URL in the label or the error |
| `History(ctx, url)` | Read every history row in start order over one short-lived connection that it closes. Return an empty history when the tool has never run against the database | Write anything. Keep a pool. Outlive `ctx`. Return the URL in an error |
| `Source(migrations, manifests)` | Return every shipped migration with the checksum the tool itself would record, sorted by name, and the declared seed command or `""` | Fail because an optional file is absent |
| `Command(action, name)` | Return only the arguments for the action. Validate `name` with `ValidName` | Accept an action it does not support (return `ErrUnsupportedAction`), or pass through anything else from the request |

### History rows

Map the tool's record onto `dbmigrate.Record`:

- `FinishedAt == nil` and `RolledBackAt == nil` means the attempt
  **failed** and is unresolved.
- `RolledBackAt != nil` means the attempt was abandoned; a later row
  with the same name supersedes it.
- `Checksum` must be comparable with what `Source` computes for the
  same file. Leave it empty on a row the tool recorded no checksum for:
  a migration reads as modified only when the row and the file both
  have one, which is the honest answer. `Source` may always compute
  its side.
- If the tool records one time per migration, set `FinishedAt` equal
  to `StartedAt`. No duration is then reported.
- If the tool keeps no notion of failure (many do not), every row is
  applied. Do not invent a failed state from timing. Return
  `ErrUnsupportedAction` for the resolve actions, and for the seed if
  the tool has none.
- If the tool can record a name more than once (a re-run), return
  every row in start order. The last one that is not rolled back wins.

### A tool with no history

If the tool records nothing in the database (Prisma with MongoDB uses
`db push` and keeps no history), it is **not** a migration tool for this
feature. Do not fake a history from the schema. Support a tool that
does keep one, and say so in the docs.

## Reading a database

- **Read-only, always.** Open a read-only transaction or the engine's
  equivalent. Where the engine has none (MongoDB), the reader issues
  read commands only and the integration test runs it as a user that
  cannot write. A setup should work with a role that can only read the
  history.
- **One connection per call**, closed before returning, bounded by
  `ctx`. No package-level clients.
- **Fixed queries.** The only value that may be interpolated is an
  identifier taken from the URL (a schema or database name), and it
  must go through the driver's identifier quoting.
- **Bound the read** (`LIMIT`, or the driver's equivalent).
- **Drivers must be pure Go** (the binary builds with CGO off) and
  justified in the pull request: maintenance, license, size.
- **Strip the tool's private URL parameters** before handing the URL to
  a driver; pass on an allowlist, not a denylist.
- **Do not assume `net/url` can parse the URL.** It refuses a URL that
  lists several hosts with ports. Parse such a URL by hand, and test a
  password that contains `@`.
- **Cap the driver's own timeouts.** A status read should give up in
  seconds; some drivers wait 30 by default.
- **A setting the reader needs and the URL does not carry** (a schema,
  a collection name) is a URL parameter private to the tool, validated
  and stripped. Do not add per-tool fields to the setup.

## Secrets

The database URL holds a password. A tool must never log it, wrap it
into an error, or return it. `Describe` errors must not quote the URL.
The service layer scrubs known passwords from messages as a second
line, not as the first.

## Tests a tool must ship

Table-driven, parallel, same file order as the source (AGENTS.md):

1. `Source`: finds migrations, ignores other files, computes the
   checksum the tool records (assert against a value produced by the
   real tool, written into the test as a constant), finds the seed in
   every place the tool allows, tolerates absent manifests.
2. `Command`: every supported action; an invalid name; an unsupported
   action.
3. `Describe`: a URL with the tool's private parameters; defaults;
   every unsupported scheme; malformed input. Assert the password is in
   neither the label nor the error.
4. `History` URL handling through an injected reader (see
   `Prisma.history`), so the unit tests need no database.
5. An integration test behind `//go:build integration` against a real
   database (address from an environment variable, skipped without it,
   working in its own schema or database and cleaning up): an untouched
   database reads as empty, every kind of row maps correctly, and the
   reader's connection cannot write. See
   `prisma_postgres_integration_test.go`.

And before the pull request, by hand or scripted, against the real
binary with a generic fixture (not a Ticketnation one): every state
(pending, applied, failed, modified, missing, rolled back), each
command, the database down, the container down, and a search of the
SQLite file, the server log and every response for the password.

## Checklist for the pull request

- [ ] One new `Tool`, registered in one place; no tool name appears
      outside its own files, the registry and the docs
- [ ] No change to `Compare`, the service layer, routes or the console,
      or a separate commit and ADR explaining the contract change
- [ ] Read-only access proven with a role that cannot write
- [ ] Checksums match the real tool's
- [ ] Password absent from labels, errors, logs, audit rows and storage
- [ ] Unit tests, an integration test, and the real-binary run above
- [ ] `docs-site/migrations.md` supported-tools table and
      troubleshooting updated; `docs-site/migration-standard.md` gains
      the tool's commands if its rules differ
- [ ] All gates green: build, vet, tests, `-race` on `internal/http`
      and `internal/service`, lint with 0 new issues, web verify and
      tests, docs build
