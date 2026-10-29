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
| 5 | Operator → host | Env-provided secrets | Disk (no secrets at rest by design) |
| 6 | touchgrass → Postgres/Redis | `docker exec` + in-container trust auth | No DB credentials held anywhere (nothing to leak) |

## Authentication and sessions

- Managed users in SQLite (`users` table): username, bcrypt hash
  (`DefaultCost`), role (`admin` or `viewer`), disabled flag.
  `TOUCHGRASS_ADMIN_PASSWORD` (required, env-only) seeds the
  bootstrap admin on first boot only — later rotations happen
  through user management, and the env is ignored once an enabled
  admin exists. With zero enabled admins (fresh DB or manual
  surgery — unreachable through the API), boot reseeds admin from
  the env and logs a warning.
- Roles: admins hold full power; viewers are read-only (every
  `GET` plus login/logout; every mutating route answers 403
  `forbidden`). UI controls hide for viewers, but enforcement is
  server-side per route — a raw HTTP client gains nothing.
- Sessions are server-side random tokens bound to a username; role
  and disabled state re-resolve from the store on every request,
  so disables and role changes bite immediately. Password resets
  do not kill existing sessions — disable first to lock out.
- Session cookie: `HttpOnly`, `SameSite=Lax`, `Secure` behind
  `TOUCHGRASS_COOKIE_SECURE=true` (set it whenever TLS terminates in
  front). Logout destroys the token. Per-IP login rate limiting
  (5/minute) with 429s; every attempt is audited with the
  attempted username.
- Every `/api/*` route except `/api/health`, login, and ingest
  requires the session. The last-enabled-admin guard rejects
  demotes, disables, and deletes that would orphan management.
- User management (`user_create`, `user_update`, `user_delete`)
  is admin-only and audited with the acting username.

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
  first 2xx wins). Admin-gated; it never probes arbitrary hosts.
- **Fleet/system views** expose every container on the daemon,
  including other teams' stacks. Viewers can read them too, so
  treat fleet visibility as team-wide — that is the point of the
  reader role, but don't be surprised by it.
- **User management** creates admins, who are root-equivalent via
  the deploy engine. Only trust operators with admin; hand devs
  the viewer role.

## What v1 deliberately lacks

- No TLS listener (terminate in nginx/Caddy/ALB in front).
- No per-service scoping, no operator/deployer role, no SSO — two
  global roles (admin/viewer) only.
- No password rotation expiry and no session persistence across
  restarts (restart logs everyone out, same as before).
- No webhook/email egress — alerts stay in-app, so a compromised box
  can't spam from here.
- No secrets manager — env vars only, documented in README/CONTRIBUTING.
- No automatic containment, kernel telemetry, eBPF, host file scanning, or
  binary-content collection.

Out of scope for this doc: host hardening (SSH, firewall, updates)
and the security of the watched services themselves.
