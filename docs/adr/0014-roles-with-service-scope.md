# ADR-0014: Roles with service scope — a role says what, a scope says where

- Status: Accepted (2026-11-10)
- Source: S31, the first sprint of the roles-and-access plan
- Supersedes in part: ADR-0010 ("No per-service scoping, no
  operator/deployer role"). The two roles and everything else in
  ADR-0010 stand.
- Amends: ADR-0013 (a new account and an admin's reset now also leave a
  temporary password)
- Amended by: ADR-0015, which defines the `developer` role this record
  reserved. Where the text below says the role holds nothing or that a
  scoped account is read-only, ADR-0015 is current.

## Context

ADR-0010 gave touchgrass two global roles. A `viewer` is read-only, but
read-only across the whole host: every service's logs, env key names and
non-secret values, deploy output, the whole audit trail including login
addresses, and every container on the Docker daemon. That is too much to
hand to a developer who works on one or two services, so in practice
those developers were either given nothing or given everything.

Reading the code showed how wide the gap was. Authorization was one
wrapper, `requireAdmin`, applied by hand to 40 of 92 routes. No handler
compared the caller to the service it was asked about. The live event
stream sent every event, deploy script output included, to every
subscriber. And one route, the onboarding suggestion, was open to any
session although the threat model said it was admin-only: with
hand-applied guards, the document and the code had drifted apart
unnoticed.

Four models were considered: more fixed roles (does not limit which
services), fixed roles plus a per-user service scope, admin-composed
custom roles, and a per-user per-service permission matrix. The last two
are flexible, but every combination has to be reviewed for leaks and
somebody has to maintain them; that is a poor trade for a tool run by a
small team with a handful of services.

## Decision

**A role decides what a user may do. A scope decides on which services.**

- Roles stay fixed and few: `viewer` and `admin`. A third, `developer`,
  is reserved: the schema admits the value so the table need not be
  rebuilt again, but no account can be given it and it holds nothing
  until its rules (deploys attributed to the caller, env staging limited
  to non-secret keys) are built and reviewed.
- A scope is either **all services**, which also covers services created
  later and the host-level screens, or **a list of services**. An admin
  always has all services; the schema enforces it with a CHECK.
- Every power is a named **capability** with a scope class: `self` (the
  caller's own account), `service` (needs the service in scope) or
  `fleet` (needs all services). One table in `internal/authz` maps roles
  to capabilities. Enforcement, the session payload the UI reads, and
  the published matrix all come from that table.
- Host-level screens (system, container inventory, images, security,
  databases, fleet-wide performance, the global audit rows) are `fleet`.
  They show every container on the daemon and cannot be cut by service,
  so a user limited to a list never sees them, whatever their role.
- Routes are **data**. Each `/api/` route is one row: method, pattern,
  capability, and how the service it concerns is found (none, the path,
  the `service_id` query parameter, a body field, or the owner of a
  resource id). The server registers routes only from that table, and a
  test fails on any route registered elsewhere, so a new route cannot
  ship unguarded.
- A service outside the caller's scope answers 403 `forbidden` before
  any lookup, whether or not it exists. A route keyed by a resource id
  answers the same 404 for a missing id and for one that belongs to a
  service out of scope. A scoped user therefore cannot learn which ids
  exist.
- Lists are bounded in the query, not filtered afterwards, so limits and
  unread counts stay correct: services, notifications, and audit rows
  are the caller's services only, and rows that belong to no service
  (logins, user management, host actions) go to all-services users only.
- The event stream is filtered per event by the subscriber's role and
  scope, and a user's streams are closed whenever their role, scope,
  password or disabled state changes, so a stream never outlives the
  access that opened it. An event type with no declared capability is
  delivered to nobody.
- Existing accounts migrate to all services and keep their role. They
  behave exactly as before, with one deliberate exception:
  `GET /api/onboarding/suggest` becomes admin-only, as the threat model
  already said it was.
- A new account and an admin's password reset both leave a **temporary
  password** that must be replaced at the next login, and a reset ends
  the user's sessions. touchgrass sends no email, so the admin passes
  the temporary password on directly; it stops being a shared secret at
  first use.

## Consequences

- An admin can hand a developer a read-only account for exactly the
  services they work on. The API refuses everything else; the UI hides
  it, but the UI is not the enforcement point.
- Granting a service grants what its logs and non-secret env values
  contain. touchgrass does not redact log content, and the import that
  guesses which env keys are secret can guess wrong. Review a service's
  secret flags before granting it.
- New services are invisible to scoped users until granted. "All
  services" is a separate grant; listing every service today is not the
  same thing.
- Deleting a service deletes its grants, so a service re-created under
  the same id starts with none.
- The schema carries a trap for later migrations: `user_services`
  cascades from `users`, and SQLite's `DROP TABLE` fires that cascade
  even inside a migration. Any future rebuild of `users` must copy the
  grants aside first. Migration `0023` records this where the next
  author will read it.
- `POST /api/users/{id}/role` stays for old clients. Promoting a scoped
  user to admin through it widens their scope, since an admin is never
  scoped.
- Denied requests are written to the structured log with user,
  capability and service. They are not audit rows: the audit trail
  records what happened, and a refused request changed nothing.
- Not decided here: what the `developer` role may do, API tokens, groups,
  SSO. The capability table and the route table are where they would
  attach.
