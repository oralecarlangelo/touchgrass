# ADR-0016: Env proposals — a developer stages plain keys, an admin applies

- Status: Accepted (2026-11-12)
- Source: S33, the third sprint of the roles-and-access plan
- Amends: ADR-0015 (`env.stage` is now part of the developer role) and
  ADR-0012 (who may stage; what a reader learns about the rendered file)

## Context

ADR-0015 kept every env write route admin-only and named the reason:
staging a value equal to the applied one answered `applied` with no
audit row, and any other value answered `changed`. Anyone allowed to
stage could therefore test guesses against a secret, one request per
guess, with no rate limit and no trace for a correct guess.

Two more facts shaped the design:

- `GET …/env/diff` returned the SHA-256 of the file an apply would
  write to every reader. The same digest was in each row of the revision
  list, in the render step of every run, and in the `env.apply` event of
  that step. A reader who knows every other value (the non-secret ones
  are shown) can confirm a guess at a short secret offline.
- An env value can decide what a service runs or where it connects
  (interpreter options, preload paths, connection URLs). Whoever applies
  a change can make a service disclose its own secrets.

Developers still need a way to change configuration without becoming
admins, which hands over every secret and every service.

## Decision

**A developer proposes; an admin applies.** The `developer` role gains
`env.stage`, service-scoped like the rest of the role. `env.apply`,
`env.discard`, `env.import`, `env.reveal` and revision rollback stay
admin-only.

**The capability opens the route; the service layer holds the rules.**
A caller of a stage route who also holds `env.apply` for that service is
a *reviewer* and stages as before. Everyone else who reaches the route
is a *proposer*. `service.EnvActor` carries the distinction and its zero
value is the proposer, so the restricted path is the default.

A proposer may:

- add a key that does not exist;
- change or remove a key whose applied entry and pending entry are both
  not flagged secret;
- replace or withdraw their own pending change.

A proposer may not:

- stage, remove or re-flag a key that is flagged secret in the applied
  revision or in the working set (so a key an admin is in the middle of
  flagging or un-flagging is covered either way);
- set the secret flag on anything, including a new key;
- touch a key that carries someone else's pending change. The plan was
  silent on this; the safe reading is that a proposal belongs to its
  author. Otherwise a second developer could alter a value between the
  admin reading it and applying it, under the first developer's name in
  the admin's memory. An admin may replace anyone's.

**One refusal for everything that touches a secret.** The check reads
only flags, runs before the value is validated and never decrypts. It
answers 403 `env_secret_restricted` with a fixed body and writes one
`env.stage` failure audit row, `refused secret <KEY>`, whatever value or
flag was sent. A test stages the true value, wrong values and invalid
values, and compares status, body bytes, audit rows and the stored
entry. Recording these refusals in the audit table is a deliberate
exception to "denied requests go to the log" (ADR-0014): the caller is
authorized for the route, and an attempt on a secret is something the
service's readers should be able to see.

**The rendered-file digest goes only to callers who hold `env.apply`.**
It is absent from the diff and from the revision list for everyone else.
It is no longer written into a run's render step or its event at all,
and it is stripped from the render step of runs recorded by earlier
versions when a non-applier reads them.

**Applying is the review, and it is bound to what was reviewed.** The
env view already records who staged each entry. The apply request may
carry the digest the admin's diff showed; if the pending set has changed
since, the apply answers 409 `env_pending_changed` and nothing runs. The
console always sends it.

## Rejected alternatives

- **Let developers stage secrets but hide the equality result.** The
  oracle is only one path. Being able to overwrite a secret is a
  denial-of-service on the service at the next apply and makes review
  harder (the admin cannot see what a masked value was changed to
  without revealing it). Refusing the key outright is simpler to prove.
- **A separate proposals table with approve and reject.** A workflow
  beyond "an admin applies" was out of scope, and the pending state plus
  `updated_by` already is the proposal.
- **Rate-limiting stage attempts on secrets.** A limit slows an oracle;
  it does not close it.
- **Keeping the digest for readers but salting it.** The digest exists
  so an applier can tie a review to a file. Readers have no use for it.

## Consequences

- A developer can prepare a config change for a service in scope, and
  an admin sees who proposed each key before applying.
- What a non-admin can learn about a secret value through touchgrass is
  narrower than in v0.6.0: the digest is gone for viewers and
  developers. Nothing about a secret was added to any read.
- Viewers lose the first eight characters of the digest in the
  Revisions drawer. Scripts that read `rendered_sha256` from the diff or
  the revision list with a non-admin session no longer find the field.
- New render steps say `rendered N keys` without the digest, for admins
  too. The digest of every applied revision stays in the revision list
  for admins.
- A developer who proposes a plain value that a service treats as code
  or as a destination can still attack that service **if an admin
  applies it unread**. The author column and the review warning are the
  control; there is no technical one.
- Command output is unchanged: `validate_cmd`, `write_cmd` and
  `restart_cmd` output is stored and shown to everyone who can read the
  service's env, as before. A validator that echoes values leaks them to
  readers. ADR-0012's rule stands: print key names and reasons only.
- A developer can fill the audit trail with refusals, as they already
  can with stage and unstage rows. These are attributed and low-value.
- Import still guesses which keys are secret. A wrongly unflagged key is
  readable, and now also changeable, by a developer in scope. Review a
  service's flags before granting it.
