# touchgrass

Self-hosted server-management platform: blue-green deploys, bounded
agentless performance diagnostics, all-state container inventory and
read-only security findings, container metrics + alerts, and Sentry-style
error tracking unified with log search. One Go binary
(UI embedded) + one SQLite file.

Touch Grass is project-agnostic. Services and providers are configured as
data or connected through adapters; the core does not depend on Ticketnation
or another monitored application's API, schema, credentials, or release
process. Project names in dogfood runbooks and validation evidence are examples
only.

> Status: v1 code-complete (Sprints 0–11). The login-gated dashboard
> manages services end to end: live color, health, metrics + history,
> confirmation-gated cutover/rollback/recreate with streamed progress
> and per-run downtime proof, append-only audit, in-app notifications,
> Sentry-style error issues with alert rules, and FTS log search with
> a live tail — errors linked to surrounding log lines. Live EC2
> validation (dogfood, acceptance sweep, service graduations) is
> tracked in `SPRINTS.md`.

## Quickstart (< 30 minutes on a fresh host)

Prerequisites: Go 1.26+, Node 20+, Docker, and `golangci-lint` v2
(`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`).

```bash
make build && make test && make lint
make run &
curl -s http://127.0.0.1:8080/api/health
cat initial-admin-password          # the one-time admin password
open http://127.0.0.1:8080
```

Expected: `{"status":"ok","version":"..."}` (`status` is always `ok`
on a healthy process; `version` comes from `git describe` at build
time, `dev` when unset — same value `touchgrass version` prints).
The dashboard at `/` asks for a login. On a first boot with no
`TOUCHGRASS_ADMIN_PASSWORD`, touchgrass generates a one-time password
for `admin`, writes it to `initial-admin-password` beside the database
(mode 0600) and makes you choose your own at first login; the file is
deleted once you have. Lost a password? `touchgrass users
reset-password` on the host issues a new one-time password. The
dashboard then lists services from `GET /api/services`. Hand other
devs read-only `viewer` accounts from the Users screen, limited to the
services they work on if you like (see `docs-site/access.md`).

Or run with Docker Compose (binds `127.0.0.1:8080` on the host):

```bash
docker compose up --build -d
curl -s http://127.0.0.1:8080/api/health
docker compose exec touchgrass cat /data/initial-admin-password
```

The compose file refuses to start without the password (fail-closed
interpolation) and mounts the Docker socket read-only for inventory,
metrics, and log tails — read `docs/threat-model.md` before exposing
the port beyond loopback.

New here? `demo/` runs touchgrass plus a toy service to watch, with
its own ten-minute README — the fastest way to see every pillar move.

For UI work with hot reload: `make dev` runs the API (dev proxy mode)
plus Vite side by side (`/api/*` served locally, `/` proxied to
`:5173`).

## Configuration

Environment only (no config file):

| Variable                             | Default            | Purpose                                                |
| ------------------------------------ | ------------------ | ------------------------------------------------------ |
| `TOUCHGRASS_ADDR`                    | `127.0.0.1:8080`   | Listen address (localhost-first)                       |
| `TOUCHGRASS_DB`                      | `./touchgrass.db`  | SQLite file path                                       |
| `APP_ENV`                            | `prod`             | `dev` proxies `/` to Vite, else embedded UI             |
| `DOCKER_HOST`                        | unix socket        | Docker daemon (standard client env)                    |
| `TOUCHGRASS_ADMIN_PASSWORD`          | (empty)            | Optional: seeds `admin` on first boot (12+ chars). Empty = a one-time password is generated in `initial-admin-password` beside the DB |
| `TOUCHGRASS_COOKIE_SECURE`           | `false`            | Set `true` when serving behind TLS                     |
| `TOUCHGRASS_ENV_KEY_FILE`            | (empty)            | 0600 file with the AES-256 key for the env pillar; empty = pillar off |
| `TOUCHGRASS_ENV_KEY`                 | (empty)            | Hex key inline (development only)                      |
| `TOUCHGRASS_METRICS_INTERVAL`        | `30s`              | Metric sample cadence                                  |
| `TOUCHGRASS_INVENTORY_INTERVAL`      | `2m`               | All-state Docker reconciliation cadence                |
| `TOUCHGRASS_INVENTORY_WORKERS`       | `2`                | Maximum concurrent safe container inspections          |
| `TOUCHGRASS_SECURITY_PROCESS_INTERVAL` | `2m`             | Running-container process scan cadence                 |
| `TOUCHGRASS_SECURITY_DIFF_INTERVAL`  | `0`                | Writable-layer metadata scan cadence; `0` disables this expensive opt-in scan |
| `TOUCHGRASS_SECURITY_WORKERS`        | `2`                | Maximum concurrent process/diff scans                  |
| `TOUCHGRASS_RETENTION_METRICS`       | `168h`             | Metric max age                                         |
| `TOUCHGRASS_RETENTION_NOTIFICATIONS` | `720h`             | Notification max age                                   |
| `TOUCHGRASS_RETENTION_DEPLOYS`       | `8760h`            | Deploy history max age                                 |
| `TOUCHGRASS_WATCH_INTERVAL`          | `5s`               | Live-target poll cadence                               |
| `TOUCHGRASS_CUTOVER_TIMEOUT`         | `10m`              | Maximum duration for one cutover/rollback run          |
| `TOUCHGRASS_RETENTION_ERRORS`        | `720h`             | Occurrence/issue max age                               |
| `TOUCHGRASS_INGEST_MAX_OCCURRENCES`  | `10000`            | Stored occurrences per service (newest kept)           |
| `TOUCHGRASS_RETENTION_LOGS`          | `720h`             | Generic log max age (30 days)                          |
| `TOUCHGRASS_LOGS_MAX_LINES`          | `3000000`          | Stored log lines per service (newest kept)             |
| `TOUCHGRASS_RETENTION_SDK_LOGS`      | `30`               | SDK log max age, in whole days                         |
| `TOUCHGRASS_LOG_POLL_INTERVAL`       | `5s`               | Container log poll cadence                             |
| `TOUCHGRASS_HOST_ROOT`               | `/`                | Host filesystem root (`/hostfs` in Compose)            |
| `TOUCHGRASS_MAINTENANCE_INTERVAL`    | `5m`               | Bounded SQLite retention/checkpoint cadence            |
| `TOUCHGRASS_SQLITE_MAX_BYTES`        | `4294967296`       | SQLite storage budget                                  |
| `TOUCHGRASS_SQLITE_BACKUP_DIR`       | `./backups/sqlite` | Online SQLite snapshot directory                       |
| `TOUCHGRASS_SQLITE_BACKUP_KEEP`      | `14`                | Newest online SQLite snapshots retained                |
| `TOUCHGRASS_RETENTION_PERFORMANCE`   | `168h`             | Minute performance aggregate max age                   |
| `TOUCHGRASS_RETENTION_PERFORMANCE_HOURLY` | `720h`        | Hourly performance aggregate max age                   |
| `TOUCHGRASS_RETENTION_SECURITY_OBSERVATIONS` | `24h`     | Raw security transition max age                        |
| `TOUCHGRASS_RETENTION_CONTAINER_EVENTS` | `2160h`         | Topology event max age; routine exec events use 24h    |
| `TOUCHGRASS_RETENTION_SECURITY_FINDINGS` | `8760h`        | Resolved finding max age; unresolved preserved         |
| `TOUCHGRASS_PERFORMANCE_SERVICE_ID`  | empty              | Existing service that owns enabled native-source data  |
| `TOUCHGRASS_NGINX_TIMING_LOG`        | empty              | Absolute dedicated JSONL path; empty disables this source |
| `TOUCHGRASS_SECURITY_EVENT_LOG`      | empty              | Absolute privacy-safe JSONL security stream; empty disables it |
| `TOUCHGRASS_SECURITY_EVENT_SERVICE_ID` | empty            | Existing service that owns security events; required with the stream |
| `TOUCHGRASS_SECURITY_EVENT_INTERVAL` | `5s`               | Security-event polling interval                          |
| `TOUCHGRASS_PERFORMANCE_INTERVAL`    | `30s`              | Native-source collection cadence                       |
| `TOUCHGRASS_POSTGRES_CONTAINER`      | empty              | PostgreSQL container; empty disables its health/collector |
| `TOUCHGRASS_POSTGRES_USER`           | `postgres`         | Local-trust role used through `docker exec`            |
| `TOUCHGRASS_POSTGRES_DB`             | `postgres`         | PostgreSQL database inspected by native collection     |
| `TOUCHGRASS_REDIS_CONTAINER`         | empty              | Redis container; empty disables its health/collector   |

New knobs land in this table in the same PR that introduces them.

## Commands

```bash
make build      # web UI + bin/touchgrass
make build-web  # SPA only (npm ci + vite build)
make test       # unit tests
make test-race  # tests with the race detector
make test-int   # integration tests (Docker; build tag)
make test-security-soak # 100-container latency/resource/storage gate
make lint       # golangci-lint (config: .golangci.yml)
make lint-fix   # lint with auto-fix
make fmt        # format via golangci-lint
make vet        # go vet
make run        # run the server locally
make dev        # API + Vite with hot reload
make migrate    # schema migrations (up|status)
./bin/touchgrass env import|set|apply --service <id>   # env pillar CLI (see docs/env)
make help       # list targets
```

Web UI checks: `npm run test --prefix web` (pure view-model tests) and
`npm run verify --prefix web` (type-check + lint).

The Performance workspace is intentionally agentless: it correlates bounded
Nginx, Docker, PostgreSQL, Redis, and existing-log aggregates, but it is not a
distributed trace and does not prove causation. Read
[`docs/performance-operations.md`](docs/performance-operations.md) before
enabling a source. The Nginx fixtures in `deploy/nginx/` exclude raw paths,
queries, headers, cookies, and client identity.

The **Fleet** workspace inventories running and stopped containers and exposes
unknown, approved-observed, ignored, and missing-expected states. The database
migration remains inert for safe backup/rollback rehearsal; the native service
activates read-only monitoring on its first successful startup. Posture,
process, and writable-layer collectors then create durable finding
episodes with bounded secret-safe evidence. Collector failures stay visible as
partial/stale coverage and cannot silently resolve earlier findings.

Touch Grass is monitoring, not containment: it never stops a container, kills
a process, removes a file, blocks traffic, or rotates credentials in response
to a finding. Docker polling/top/diff can miss short-lived or evasive activity,
and the socket remains root-equivalent even when mounted `:ro`. Read
[`docs-site/fleet.md`](docs-site/fleet.md) and
[`docs/threat-model.md`](docs/threat-model.md) before deployment. Release
validation includes the generic replay/privacy gate, migration rehearsal, and
`make test-security-soak`.

Binary subcommands: `touchgrass serve` (default), `touchgrass migrate`,
`touchgrass version`.

## Layout

Go module: `github.com/oralecarlangelo/touchgrass` (single module, no
`pkg/` until external consumers exist). `cmd/touchgrass` holds the only
`main`; business logic lives in `internal/`; the SPA lives in `web/`
(built to gitignored `web/dist/`, embedded by the binary) and the Node
SDK in `sdk/node/` (`@touchgrass/node`: zero-dep, fail-open error
capture with breadcrumbs and client-side PII scrub).
The authoritative map is `docs/PROJECT_STRUCTURE.md`.

## Docs

- `BRD-touchgrass.md` — requirements, pillars, acceptance criteria
- `SPRINTS.md` — sprint plan and Definition of Done
- `16-custom-deploy-manager-scope.md` — phased build plan
- `demo/` — ten-minute demo stack (touchgrass + toy service)
- `CONTRIBUTING.md` — setup, gates, and workflow for contributors
- `docs/ARCHITECTURE.md` — system design
- `docs/PROJECT_STRUCTURE.md` — folder structure standard
- `docs/threat-model.md` — trust boundaries and sharp edges
- `docs/dogfood-runbook.md` — S9 staging-first SDK rollout + S10 log validation
- `docs/performance-operations.md` — collector privacy, rollout, recovery, and rollback
- `docs/v1-acceptance.md` — BRD AC-1..6 live sweep checklist
- `docs/service-graduation.md` — S12–S13 per-service onboarding checklist
- `docs/adr/` — architecture decision records
- `AGENTS.md` — working agreement for humans and agents

## License

Apache-2.0 — see `LICENSE`.
