# Threat model

What touchgrass trusts, what it doesn't, and where the sharp edges
are. v1 scope: single-host, managed users (admin/viewer),
in-app notifications only, all-state container inventory, and read-only
container security monitoring.

## Trust boundaries

| # | Boundary | Trusts | Does not trust |
| - | -------- | ------ | -------------- |
| 1 | Admin browser ↔ API | Session cookie (login-gated) | The network (no in-app TLS; terminate TLS in front) |
| 2 | SDK → ingest API | Bearer API key per service | Payload contents (validated, size-capped, scrubbed client-side) |
| 3 | touchgrass → Docker socket | Local daemon answers truthfully | Socket file permissions (see below) |
| 4 | touchgrass → service health URLs | HTTP status codes | Latency/body (timeouts on every probe) |
| 5 | Operator → host | Env-provided secrets for touchgrass itself | Disk for touchgrass's own credentials (none at rest) |
| 7 | Env pillar key file ↔ SQLite | `TOUCHGRASS_ENV_KEY_FILE` (0600, outside the DB) | The SQLite file and its backups alone (ciphertext only; ADR-0012) |
| 6 | touchgrass → Postgres/Redis | `docker exec` + in-container trust auth | No DB credentials held anywhere (nothing to leak) |

## Authentication and sessions

- Managed users in SQLite (`users` table): username, bcrypt hash
  (`DefaultCost`), role (`admin` or `viewer`), scope (all services,
  or the rows of `user_services`), disabled flag.
  The bootstrap admin is seeded on first boot only. With
  `TOUCHGRASS_ADMIN_PASSWORD` set (optional, env-only) it is seeded
  from the env; later rotations happen through user management, and
  the env is ignored once an admin has chosen a password. With it
  unset, boot generates a one-time password (130 bits), writes it
  to `initial-admin-password` beside the database (0600, never
  logged) and flags the account `must_change_password`: until it is
  replaced the session can only read `/api/auth/me`, change the
  password, or log out, and the file is deleted on the change
  (ADR-0013). There is no default password at any point. With zero
  enabled admins (fresh DB or manual surgery — unreachable through
  the API), boot reseeds admin the same way and logs a warning.
- Self-service `POST /api/auth/password` (any role) needs the
  current password, shares the login rate limit, is audited, and
  ends the caller's other sessions. `touchgrass users
  reset-password` on the host issues a one-time password for a
  lost one; host access already implies control of the database.
- Roles and scope (ADR-0014): a role decides what a user may do,
  a scope decides on which services. Admins hold full power on
  every service. Viewers are read-only (every mutating route
  answers 403 `forbidden`), on every service or only on the ones an
  admin granted. See "Access control" below.
- Sessions are server-side random tokens bound to a username; role,
  scope, and disabled state re-resolve from the store on every
  request, so disables and access changes bite immediately. An
  admin's password reset in Users ends that user's sessions and
  live streams and leaves a temporary password they must replace at
  the next login; a user's own password change ends their other
  sessions. A new account also starts on a temporary password.
- Session cookie: `HttpOnly`, `SameSite=Lax`, `Secure` behind
  `TOUCHGRASS_COOKIE_SECURE=true` (set it whenever TLS terminates in
  front). Logout destroys the token. Per-IP login rate limiting
  (5/minute) with 429s; every attempt is audited with the
  attempted username.
- Every `/api/*` route except `/api/health`, login, and ingest
  requires the session. The last-enabled-admin guard rejects
  demotes, disables, and deletes that would orphan management.
- User management (`user_create`, `user_update`, `user_delete`)
  is admin-only and audited with the acting username. Access
  changes are `user_update` rows that record the new role and
  scope. The self-service password route takes the two passwords
  and nothing else; no field in it can change a role or a scope.

## Access control

- **One policy table.** `internal/authz` maps each role to named
  capabilities, each with a scope class: `self`, `service` (the
  service must be in the user's scope) or `fleet` (the scope must be
  all services). A checked-in matrix and a table-driven test cover
  every role, capability and scope combination.
- **Deny by default.** Every `/api/` route is declared in one route
  table with its capability and how its service is found. The server
  registers routes only from that table; tests fail on a route
  registered elsewhere, on a route that answers a role holding
  nothing, and on a table that disagrees with the OpenAPI document.
- **A scoped user sees their services and nothing else.** A service
  out of scope answers 403 before any lookup, with the same body
  whether or not the id exists. A route keyed by a resource id (an
  issue, a log line) answers the same 404 for a missing id and for
  one owned by a service out of scope. Lists (services,
  notifications, audit) are bounded in the SQL query.
- **Host-level data is fleet-only.** System and container inventory,
  images, security findings and policies, database health and
  backups, fleet-wide performance, and audit rows that belong to no
  service (logins with client addresses, user management) are refused
  to anyone limited to a list of services.
- **The event stream is filtered per event** by the subscriber's
  role and scope: deploy output for a service reaches only users who
  may read that service's deploys. A user's streams are closed when
  their role, scope, password or disabled state changes. An event
  type with no declared capability reaches nobody.
- **Escalation paths closed in S31.** Only admins reach any user or
  access route. The last enabled admin cannot lose the admin role by
  any route or to any role, and the schema refuses a scoped admin.
  The reserved `developer` role cannot be assigned and holds nothing.
- **What a grant gives away.** Granting a service grants its logs,
  occurrences and non-secret env values as they are: touchgrass does
  not redact log content, and the env import guesses which keys are
  secret. Review a service's secret flags before granting it. SDK key
  prefixes of a granted service are readable too.
- **Not covered.** A developer who ships code to a service can make
  that service print its own environment. touchgrass cannot hide a
  service's secrets from the people who write its code; the goal is
  that touchgrass itself does not hand them over.
- **Denied requests** are written to the structured log (`access
  denied`, with user, role, capability, service and route), not to
  the audit table.

## SDK keys

- Keys are `tg_`-prefixed random tokens, shown once at mint time.
  Only the SHA-256 hash is stored (`api_keys.key_hash` + prefix for
  lookup); plaintext never hits disk or logs.
- Keys authenticate one service's ingest calls only — no admin power.
  Compromise response: `POST /api/keys/{id}/revoke`, mint a
  replacement. Revoked keys get 401s; SDKs drop best-effort.
- Ingest payloads are size-capped and grouped by server-computed
  fingerprint; the server never executes stack frames or breadcrumbs.
- Structured logs (`POST /api/ingest/logs`, incl. OTel-shaped
  batches) ride the same key model and caps. Log attributes can
  carry app PII — scrubbing is client-side (SDK hooks), so treat
  `sdk_logs` like occurrences at rest. OTel trace/span ids are
  opaque strings to the server: validated for shape, never
  dereferenced.

## The Docker socket (sharpest edge)

touchgrass mounts `/var/run/docker.sock` for inventory, metrics, and
log tails. **That socket is root-equivalent on the host**: anyone who
can call it can start a privileged container. Consequences:

- Never publish the touchgrass API port beyond loopback while the
  socket is mounted (compose files bind `127.0.0.1` only).
- The `:ro` mount flag is hygiene, not enforcement — the daemon API
  does not distinguish read/write callers by mount mode.
- If the box is shared, prefer running touchgrass on a dedicated
  host that only the operator reaches (SSH + loopback).

Touch Grass currently needs list, inspect, stats, logs, events, top, and diff
operations. The application does not call create, start, stop, kill, exec,
copy, update, or remove APIs for monitored containers. This is an application
boundary, not a daemon-enforced permission boundary: Docker's Unix socket has
no read-only API role.

## Container security evidence and limits

- The inventory covers all lifecycle states and uses immutable container IDs.
  Names and Compose labels are descriptive only. Absence is inferred only
  from a complete reconciliation; partial scans preserve the last known state.
- Inspect normalization excludes environment values, arbitrary labels, raw
  entrypoint/command arguments, and host mount sources. Entrypoint/command
  values are hashed; mounts retain destination/type/writability only.
- Process normalization retains PID, parent PID, user, and the executable
  token only. Arguments are discarded. Writable-layer scans retain bounded
  operation/path metadata and never read file contents.
- Evidence values are allowlisted, bounded per key, and capped at 32 KiB per
  finding episode. Raw observations and lifecycle events expire before durable
  findings. Resolved findings expire on their configured window; unresolved
  episodes are not age-trimmed.
- Collector failures open deduplicated incidents and mark coverage partial.
  A failed or incomplete scan cannot close an earlier finding. Freshness,
  safe error category, and effective interval remain visible.
- Polling and Docker snapshots can miss short-lived, in-memory-only, kernel,
  or deliberately evasive activity. A finding is a triage signal, not proof
  of attribution. Touch Grass is not an EDR, IDS, sandbox, or forensic image.

There is no automatic containment. Touch Grass never stops containers,
kills processes, deletes binaries, blocks traffic, rolls credentials, or
changes firewall rules in response to findings. This avoids turning a
monitoring false positive or a compromised dashboard into a host-wide outage.
Operators contain through an independently reviewed incident runbook.

## Data at rest

- Single SQLite file (`TOUCHGRASS_DB`). It holds occurrences (may
  contain app PII — scrub runs in the SDK *before* send, patterns are
  team-reviewed per the dogfood runbook), container log lines, SDK
  logs, fleet samples, hashed keys, and the audit trail.
  Filesystem permissions are the control: `chmod 600` the DB and
  keep it out of backups that leave the trust boundary.
- Postgres dumps live beside the DB in `TOUCHGRASS_DB_BACKUP_DIR`
  (mode 0600, full prod data). Same discipline: tight perms, and
  the keep-trim deletes old dumps — anything the trim must never
  eat belongs elsewhere. Restore/re-verify paths validate backup
  names (`<name>.dump`, no separators, must already exist) so the
  API can't walk the backup dir.
- Logs and occurrences are retention-trimmed (`TOUCHGRASS_RETENTION_*`)
  and per-service capped; trims are logged, FTS indexes follow deletes.
- Security observations default to 168h, container events to 720h, and
  resolved findings to 2160h. All remain inside the global SQLite byte budget.

## Env pillar: managed env files and secrets at rest

ADR-0012 supersedes the original "no secrets at rest" stance for one
scoped case: values of each managed service's runtime env file.

- Values are AES-256-GCM sealed under one operator key loaded from
  `TOUCHGRASS_ENV_KEY_FILE` (32 raw / 64 hex bytes; the binary refuses
  any mode but `0600`) or, for development only, `TOUCHGRASS_ENV_KEY`.
  Associated data binds each ciphertext to its service id and key name;
  `key_id` tags rows for future rotation. Without a key the pillar is
  off and every env route answers 503 `env_key_missing`.
- The SQLite file therefore holds ciphertext, not plaintext. Someone
  who copies `touchgrass.db` without the key file learns key *names*
  and which ones are flagged secret — nothing else. Keep the key out of
  DB backups and off shared hosts; losing it loses the stored values
  (the file on disk and `import` recover the working set).
- Plaintext exists only in request scope: the view never returns a
  secret's value for any role, `String()`/JSON on store models omit
  ciphertext, logs carry key names only, and buffers are zeroed after
  use. Non-secret values are visible to every authenticated role.
- `POST …/reveal` is admin-only, rate-limited (10/min per user), and
  writes an `env.reveal` audit row with the key name. Stage, discard,
  import, apply, apply-failed, and rollback are audited the same way
  with key names and summaries — never values.
- Applies run the service-configured `validate_cmd` and `restart_cmd`
  with the process's privileges, or through `sudo -n` when the service
  says `sudo: true`. That is the same trust as deploy scripts: blast
  radius is one service, scripts live outside touchgrass, and the
  operator reviews the sudoers rule. The written file gets the
  configured owner and mode (default `0600`), the previous file stays
  as `.bak`, and a failed restart or probe restores it.
- Validator output is stored as `last_validation` lines (bounded).
  A validator that echoes values would put them in SQLite in clear —
  write validators that print key names and reasons only.

## Operator-powered destruction (all admin-gated + audited)

These exist so the operator can act fast; the control is the admin
session plus confirm-gating in the UI, with an audit entry per run:

- **Deploys** run service scripts with the process's privileges
  (cutover/rollback/deploy). Blast radius is one service by
  construction — scripts live outside touchgrass and stay
  team-reviewed.
- **Database restore** drains app backends and replaces table
  contents; the app errors until verify completes. Typed-filename
  confirm, auto safety backup first, `db_restore` audit entry.
- **Service delete** wipes the service row plus its touchgrass-side
  history *including its audit rows* — the one sanctioned exception
  to audit append-only. The deletion itself is audited as a global
  `service_delete` entry, so the trail shows who removed what.
- **Image prune** deletes unused Docker images host-wide to reclaim
  disk. Nothing running is touched (daemon-side guarantee), but
  pulled images may need re-pulling.
- **Onboarding suggest** probes the candidate container's own
  published localhost ports for a health URL (short timeouts,
  first 2xx wins). Admin-gated (enforced since S31; before that
  the route was open to any session); it never probes arbitrary
  hosts.
- **Fleet/system views** expose every container on the daemon,
  including other teams' stacks. All-services viewers can read
  them, so treat that scope as team-wide. A viewer limited to
  selected services cannot open them at all.
- **User management** creates admins, who are root-equivalent via
  the deploy engine. Only trust operators with admin; hand devs
  the viewer role, limited to the services they work on.

## What v1 deliberately lacks

- No TLS listener (terminate in nginx/Caddy/ALB in front).
- No operator/deployer role, no custom roles, no groups, no API
  tokens, no SSO: two roles (admin/viewer), each with a scope of all
  services or a list (ADR-0014). A scoped account is read-only.
- No password rotation expiry and no session persistence across
  restarts (restart logs everyone out, same as before).
- No webhook/email egress — alerts stay in-app, so a compromised box
  can't spam from here.
- No general secrets manager — touchgrass holds only the managed
  services' env files (ADR-0012); its own credentials stay env-only.
- No automatic containment, kernel telemetry, eBPF, host file scanning, or
  binary-content collection.

Out of scope for this doc: host hardening (SSH, firewall, updates)
and the security of the watched services themselves.
