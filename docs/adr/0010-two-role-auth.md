# ADR-0010: Two-role auth — admin and viewer

- Status: Accepted (2026-10-14)
- Source: S28 multi-user auth; overrides the "single admin
  credential" answers in the BRD (FR-P1, Q5) and the "no RBAC"
  lines in `docs/ARCHITECTURE.md` and `docs/threat-model.md`

## Context

Day-one auth was one admin password shared by every operator.
The team outgrew it: other devs need to read server logs and
metrics without holding deploy power, and a shared credential
can't tell actors apart in the audit trail.

## Decision

Two global roles, managed in SQLite, enforced server-side:

- `admin` holds every power the old single credential held,
  plus user management.
- `viewer` is strictly read-only: every `GET` plus
  login/logout; every mutating route answers 403 `forbidden`.
- `TOUCHGRASS_ADMIN_PASSWORD` seeds the bootstrap admin on
  first boot only. Later rotations go through user
  management; the env is ignored once an enabled admin
  exists. Zero enabled admins (unreachable through the API)
  reseeds admin from the env at boot with a warning.
- A last-enabled-admin guard rejects demotes, disables, and
  deletes that would orphan management.
- Sessions bind tokens to usernames and re-resolve role and
  disabled state per request, so revocations bite immediately.

No per-service scoping, no operator/deployer role, no SSO.
Those stay out per the narrowed Phase 8 list.

## Consequences

- Login takes `{username?, password}`; empty username still
  means admin, so old clients keep working.
- Audit `actor` carries the real username; user management
  writes `user_create`, `user_update`, `user_delete`.
- The console hides mutating controls for viewers and lands
  them on Logs; the API is the enforcement point, not the UI.
- Operators rotate the admin password in the Users screen
  now, not by restarting with a new env value.
