# Threat model

What touchgrass trusts, what it doesn't, and where the sharp edges
are. v1 scope: single-host, single admin credential, in-app
notifications only. Covers everything through S27 (fleet, SDK
logs/OTel, databases, onboarding, service delete).

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

- One admin password from `TOUCHGRASS_ADMIN_PASSWORD` (required,
  env-only). Stored in memory as bcrypt (`DefaultCost`); compared with
  `CompareHashAndPassword`.
- Session cookie: `HttpOnly`, `SameSite=Lax`, `Secure` behind
  `TOUCHGRASS_COOKIE_SECURE=true` (set it whenever TLS terminates in
  front). Sessions are server-side random tokens; logout destroys.
- Every `/api/*` route except `/api/health` and login requires the
  session. The SPA is useless without it.

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
  including other teams' stacks. Same admin boundary as services —
  no separate reader role exists to leak across.

## What v1 deliberately lacks

- No TLS listener (terminate in nginx/Caddy/ALB in front).
- No RBAC, no second operator role — one admin credential.
- No webhook/email egress — alerts stay in-app, so a compromised box
  can't spam from here.
- No secrets manager — env vars only, documented in README/CONTRIBUTING.

Out of scope for this doc: host hardening (SSH, firewall, updates)
and the security of the watched services themselves.
