# ADR-0019: MongoDB migrations — read migrate-mongo's changelog, not Prisma

- Status: Accepted (2026-11-13)
- Source: S36, the request to support MongoDB after Prisma on
  PostgreSQL, for services that use migrate-mongo
- Builds on: ADR-0018 (schema migration awareness)

## Context

ADR-0018 made touchgrass compare a migration history in the
application's database with the migrations its running build ships.
The first tool was Prisma on PostgreSQL. MongoDB was next.

"Prisma on MongoDB" is not a second reader for the same tool. Prisma
keeps **no** migration history for MongoDB: it has no `migrate` for
that datasource, only `db push`, which changes the database and records
nothing. There is no history to compare, and inventing one from the
schema would be a guess presented as a fact.

The services this is for keep their MongoDB changes as **migrate-mongo**
scripts. migrate-mongo does keep a record, and it differs from Prisma's
in ways the shared code had quietly assumed away (checked against
migrate-mongo 14.0.7's source and a real run):

| | Prisma on PostgreSQL | migrate-mongo |
| --- | --- | --- |
| Record | One row per attempt, started and finished | One document per successful run: `fileName`, `appliedAt`, `migrationBlock` |
| Failure | The row stays, unfinished, with the error | Nothing is written. The migration stays pending |
| Rollback | The row is marked rolled back | `down` deletes the document |
| Checksum | Always | `fileHash` only when the application sets `useFileHash` |
| Edited file | Refused | With `useFileHash`, run again and recorded again |
| Seed | Declared in the manifest | None |

## Decision

**A second `Tool`, `migrate-mongo`, reads the changelog collection of
a MongoDB database.** It is registered beside Prisma and nothing else
names it.

**It reports only what the changelog can prove.** Every document is an
applied migration. A migration is never *failed* or *rolled back*, so
there is nothing to resolve; the tool has no seed. Those actions return
`ErrUnsupportedAction`, and the console, which offers resolve only on a
failed migration and seed only when one is declared, never shows them.
A failed `up` is visible as a failed run in touchgrass's own run list
with the tool's output, and the migration stays *pending*.

**Only `up` is offered.** `down` undoes one migration per call and its
effect depends on what was applied last. That is a decision for a
person at a terminal who has read the script, not a button.

**The shared comparison stops assuming Prisma's record** (a contract
change, made for every tool, in its own commit):

- A migration is *modified* only when the applied row **and** the
  shipped file both carry a checksum. Without `useFileHash` an edit
  cannot be detected, and the status says nothing rather than calling
  every migration edited.
- A row whose finish equals its start reports no duration. The
  changelog holds one instant, not a run time.

**The changelog collection name travels in the stored URL** as
`changelogCollection`, a parameter private to this tool and stripped
before the driver sees the URL (the same device as Prisma's `schema`).
The default is `changelog`. The alternative, a per-tool options field
in the setup, means a store migration, API fields and a form for one
setting that migrate-mongo itself says to leave alone.

**The file extension is read from `migrate-mongo-config.js`** by a
best-effort pattern for the literal form, defaulting to `.js`, and the
file filter is migrate-mongo's own (that extension, directly in the
directory, not `sample-migration`). The config file is JavaScript, so a
computed value cannot be read and falls back to the default.

**The official Go driver** (`go.mongodb.org/mongo-driver/v2`,
Apache-2.0, pure Go) connects, issues one bounded `find`, and
disconnects. The URL is parsed by hand first, because a MongoDB URL may
list several hosts and `net/url` refuses that; only connection and read
options are passed on. Server selection is capped at 5 seconds (the
driver's default is 30).

## Consequences

- MongoDB has no read-only transaction to hide behind. Read-only access
  rests on two things: the reader's code path issues a single `find`,
  and the setup is documented and tested with a user that holds only
  the `read` role.
- **With `useFileHash` off, an edited migration is invisible.** The
  guide says so, and the coding standard tells applications to turn it
  on.
- **Turning `useFileHash` on later is a one-time disagreement.**
  migrate-mongo then treats every document without a hash as not
  applied and will run those scripts again on the next `up`; touchgrass
  still reads them as applied until that `up` writes hashed documents.
- The second-line password scrub in the service layer read the URL with
  `net/url` and so missed the password of a multi-host URL. It now
  reads the authority by hand. Fixed for every tool.
- The binary grows by the driver and its compression and SCRAM
  dependencies.
- A `mongodb+srv` URL is resolved by the driver when it connects, with
  the system resolver; that lookup is not bounded by the request
  context.

## Alternatives considered

- **A Prisma reader for MongoDB.** There is no history to read.
- **Inferring "applied" from collections or indexes.** A guess, and
  different for every application.
- **Offering `down`.** See above.
- **Reading `useFileHash` from the config to decide whether to compute
  checksums.** The config is code; the per-row rule (compare only when
  both sides have one) needs no parsing and is right row by row,
  including while hashed and unhashed documents coexist.
- **mongosh in the application's container instead of a driver.**
  Most application images do not ship it, and it would put the URL on
  a command line.
