# Threat model

What touchgrass trusts, what it doesn't, and where the sharp edges
are. v1 scope: single-host, single admin credential, in-app
notifications only.

## Trust boundaries

| # | Boundary | Trusts | Does not trust |
| - | -------- | ------ | -------------- |
| 1 | Admin browser ↔ API | Session cookie (login-gated) | The network (no in-app TLS; terminate TLS in front) |
| 2 | SDK → ingest API | Bearer API key per service | Payload contents (validated, size-capped, scrubbed client-side) |
| 3 | touchgrass → Docker socket | Local daemon answers truthfully | Socket file permissions (see below) |
| 4 | touchgrass → service health URLs | HTTP status codes | Latency/body (timeouts on every probe) |
| 5 | Operator → host | Env-provided secrets | Disk (no secrets at rest by design) |

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
  team-reviewed per the dogfood runbook), log lines, hashed keys, and
  the audit trail. Filesystem permissions are the control:
  `chmod 600` the DB and keep it out of backups that leave the trust
  boundary.
- Logs and occurrences are retention-trimmed (`TOUCHGRASS_RETENTION_*`)
  and per-service capped; trims are logged, FTS indexes follow deletes.

## What v1 deliberately lacks

- No TLS listener (terminate in nginx/Caddy/ALB in front).
- No RBAC, no second operator role — one admin credential.
- No webhook/email egress — alerts stay in-app, so a compromised box
  can't spam from here.
- No secrets manager — env vars only, documented in README/CONTRIBUTING.

Out of scope for this doc: host hardening (SSH, firewall, updates)
and the security of the watched services themselves.
