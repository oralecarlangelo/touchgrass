# ADR-0015: Developer role — run what an admin configured, in scope, under your own name

- Status: Accepted (2026-11-12)
- Source: S32, the second sprint of the roles-and-access plan
- Amends: ADR-0014 (its "reserved" third role is now defined; "a scoped
  account is read-only" no longer holds for a developer)
- Supersedes in part: ADR-0010 ("no operator/deployer role")
- Amended by: ADR-0016, which adds `env.stage` to the role as a right to
  propose non-secret env changes. Where this record says the role holds
  no env write, ADR-0016 is current.

## Context

ADR-0014 gave every account a role and a service scope, and shipped two
roles: `viewer` (read) and `admin` (everything, always every service). A
third value, `developer`, was admitted by the schema but held nothing and
could not be assigned.

That left one gap. The people who write a service's code are the people
who release it, watch its alerts and dismiss them. To let them deploy,
an operator had to make them an admin, which also hands over env and
secrets, SDK keys, user management, every other service and the host.

Three things had to be true before a non-admin could be allowed to
operate a service:

1. Deploying must not be a way to run arbitrary commands.
2. Every action a developer takes must name the developer.
3. Nobody who already has an account may gain a power as a side effect.

## Decision

**A developer is a viewer who may also operate the services in scope.**

- The role holds the viewer's capabilities plus four: `deploy.trigger`
  (deploy, cutover, rollback), `deploy.record` (record a manual deploy),
  `alerts.manage` (create and delete alert rules and issue rules) and
  `notifications.ack` (mark notifications read). All four are `service`
  capabilities. The role therefore adds no fleet-level mutation, even
  for a developer with all services.
- **A developer runs what an admin configured and nothing else.** A
  deploy request carries a service id and, for a cutover, one of
  `auto`, `blue`, `green`. The script, its arguments, `validate_cmd`,
  `restart_cmd` and the service definition are stored configuration
  that only `services.manage` (admin) can create or delete; there is no
  route that edits them. Granting the role on a service is a decision
  to let that person run that service's deploy script on the host.
- **Actions are attributed to the session user.** Deploy, cutover and
  rollback already recorded the caller. A manual deploy record took
  `actor` from the request body; for anyone but an admin it is now the
  session user and the body field is ignored. An admin may still
  attribute a record to someone else, because existing clients do, and
  the audit row then names the admin and says who it was attributed to.
- **The mutations a developer gains are audited.** Rule create and
  delete and manual deploy records wrote no audit row. They now write
  `alert_rule_create`, `alert_rule_delete`, `issue_rule_create`,
  `issue_rule_delete` and `deploy_record`, on the service concerned.
- **Marking read is audited too.** The read flag of a notification is
  one flag shared by every user, so a developer who marks a
  notification read hides it from the admin as well. Rather than build
  per-user read state, the shared flag stays and each change is
  recorded as `notification_read` on the notification's service: one
  row for a single notification, one row per service for a mark-all. A
  call that changes nothing records nothing.
- **Scope is enforced by the guards ADR-0014 built.** No guard changed.
  A service out of scope answers 403; a rule or notification that
  belongs to another service answers the same 404 as a missing id; a
  mark-all with no `service_id` touches only the caller's services.
- **The role is opt-in.** No migration changes a role. Migration `0024`
  only rebuilds the audit table to admit the new actions. The viewer's
  capabilities are frozen by a checked-in matrix produced against
  v0.6.0, and a second checked-in matrix states what a developer gets
  from every route; a test holds the two to differing in exactly the
  ten routes above.
- **`env.stage` is not part of the role.** Staging has an open question
  of its own (a staged value that equals the applied one is reported as
  unchanged, which lets anyone who can stage test guesses against a
  secret). It stays admin-only until that is closed.

## Consequences

- An operator can let a developer release and tend a service without
  making them root-equivalent on the host through the env pillar, user
  management or other services.
- A developer on a service can still do whatever that service's deploy
  script does, with the privileges of the touchgrass process, and can
  take the service down by deploying a broken release or rolling back.
  That is the grant. One run per service at a time is still enforced.
- Deploy script output is broadcast on the event stream to everyone who
  may read that service's deploys. Scripts should not echo secrets.
- A developer can delete an alert rule and so silence an alert. The
  audit row is the control; there is no approval step.
- The audit trail now grows with rule changes and mark-read calls.
  These are low-volume, human-driven actions.
- An all-services developer still sees the host screens a viewer with
  all services sees, and still cannot change anything on them.
- Not decided here: env proposals by developers, SDK key management for
  a service's own developers, API tokens for CI-triggered deploys, a
  per-user notification read state.
