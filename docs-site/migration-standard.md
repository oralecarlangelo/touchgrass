# Migration & seed coding standard

Rules for writing database migrations and seeds in an application that
touchgrass deploys and watches. They are written for **AI coding agents
and people alike**: each rule is something a reviewer can check, and
the block at the end can be pasted into an application repository's
`AGENTS.md` or `CLAUDE.md` so an agent follows it without being told.

The examples use NestJS with Prisma on PostgreSQL. The rules are about
migrations in general; only the commands are Prisma's.

## Why these rules

touchgrass compares the migration history in the database with the
migrations in the running build ([how](./migrations.md)). Three things
make that comparison, and a zero-downtime deploy, go wrong:

- a migration **edited after it ran** (the database and the code
  disagree about what happened);
- a migration the **old build cannot survive** (during a blue-green
  cutover both builds run against one database);
- a seed that **cannot be run twice**.

## Migrations

**M1. Never edit, rename or delete a migration that has been applied
anywhere.** Change the schema with a new migration. An edited file
shows as *Edited after apply* and Prisma refuses to continue in
development.

**M2. Create migrations with the tool, from the schema.**
`npx prisma migrate dev --name <what_it_does>`, then review the
generated SQL. Do not hand-write the directory name or timestamp. Edit
the generated SQL only before it is first applied, and only to make it
safer (M5–M7).

**M3. One purpose per migration, named for what it does.**
`add_order_status`, not `update` or `fix`. A failure then points at one
change.

**M4. Commit the migration with the code that needs it, in the same
change.** Never commit a schema change without its migration, and never
use `prisma db push` against a shared or production database: it
changes the schema and records nothing.

**M5. Every migration must be safe while the previous build is still
running** (expand, then contract):

| Change | Do | Do not |
| --- | --- | --- |
| Add a column | Add it nullable or with a default | Add it `NOT NULL` with no default |
| Remove a column | Release 1: stop reading and writing it. Release 2: drop it | Drop it in the release that stops using it |
| Rename a column or table | Add the new one, write both, backfill, switch reads, drop the old one later | `RENAME` in one step |
| Make a column required | Backfill first, then add the constraint in a later migration | Add `NOT NULL` and backfill together |
| Change a type | Add a new column, backfill, switch, drop | `ALTER COLUMN … TYPE` on a live table |

**M6. Do not hold long locks on large tables.** In PostgreSQL create
indexes with `CREATE INDEX CONCURRENTLY` (in its own migration: it
cannot run inside a transaction), and add foreign keys and checks as
`NOT VALID`, then `VALIDATE CONSTRAINT` separately.

**M7. Keep data changes out of schema migrations when they are large.**
A backfill of millions of rows belongs in a batched job, not in a
migration that blocks the deploy. Small, bounded data fixes may live in
a migration.

**M8. Never put a secret, a real person's data or an
environment-specific value in a migration.** Migrations are in the
repository and in every image.

**M9. Production applies migrations one way only:**
`prisma migrate deploy`. Never `migrate dev`, `migrate reset` or
`db push` outside a developer's own database.

**M10. A failed migration is resolved, not retried blindly.** Find out
what it changed, fix the cause, then mark it rolled back (to apply it
again) or applied (after finishing its SQL by hand). Say in the change
description which one and why.

## Seeds

**S1. A seed must be safe to run twice.** Use `upsert` keyed on a
natural unique field, or `createMany({ skipDuplicates: true })`. Never
a bare `create` in a loop, and never `deleteMany` followed by inserts.

```ts
// prisma/seed.ts
await prisma.plan.upsert({
  where: { code: 'starter' },
  update: {},
  create: { code: 'starter', name: 'Starter', priceCents: 0 },
});
```

**S2. A seed holds reference data only**: the rows the application
cannot run without (plans, roles, feature flags). Test fixtures and
demo data go in a separate script that is never the declared seed.

**S3. Declare exactly one seed command** where the tool looks for it
(`"prisma": { "seed": "…" }` in `package.json`, or `migrations.seed` in
`prisma.config.ts`). That is the command touchgrass shows and runs.

**S4. The declared seed must run in the production image.** If it is
TypeScript, either compile it and declare `node dist/prisma/seed.js`,
or keep its runner (`tsx`) in `dependencies`.

**S5. No secrets or personal data in a seed, and none printed.** Read
them from the environment. Seed output is stored by touchgrass and
shown to admins.

**S6. Print one line per thing done and exit non-zero on failure.** Do
not swallow errors: an exit code of 0 is recorded as success.

**S7. Disconnect when done** (`prisma.$disconnect()` in a `finally`),
or the command hangs until it times out.

## The image

**I1. Ship the migrations directory and the migration CLI in the
runtime image.** touchgrass reads `prisma/migrations` from the running
container and runs the CLI there. Keep `prisma` in `dependencies`, and
copy `prisma/` into the final stage.

**I2. Do not transform migration files during the build** (line
endings, formatting, minification). The checksum of each file must be
the one that was applied.

**I3. Do not run migrations on container start in a blue-green
service.** Two colors starting together would race. Apply them as a
step: from touchgrass, or from the deploy script before the new color
takes traffic.

## Before opening a pull request

- [ ] No applied migration was edited, renamed or deleted
- [ ] The new migration was generated by the tool and its SQL was read
- [ ] It is safe with the previous build still running (M5)
- [ ] No long lock on a large table (M6)
- [ ] The seed, if touched, runs twice with the same result
- [ ] `npx prisma migrate deploy` succeeds on a database at the
      previous release, and `npx prisma migrate status` is clean after
- [ ] The description says what the migration changes and how to
      undo it

## Paste into an application's AGENTS.md

```md
## Database migrations and seeds (binding)

- Never edit, rename or delete a migration that has been applied
  anywhere. Change the schema with a new migration.
- Create migrations with `npx prisma migrate dev --name <what_it_does>`
  and read the generated SQL. Never use `prisma db push`,
  `migrate reset` or `migrate dev` against a shared database.
- Commit a schema change and its migration together.
- Every migration must work while the previous build is still running:
  add columns nullable or with a default; remove or rename in two
  releases; backfill before adding NOT NULL.
- On large tables use CREATE INDEX CONCURRENTLY (own migration) and
  NOT VALID constraints validated separately.
- Seeds must be idempotent (upsert or skipDuplicates), hold reference
  data only, print no secrets, and exit non-zero on failure.
- Declare one seed command, and make sure it runs in the production
  image.
- Keep `prisma` in dependencies and ship `prisma/` in the runtime
  image, byte for byte.
- If a migration fails, stop. Report what it changed and propose
  whether to mark it rolled back or applied. Do not retry it blindly.
```
