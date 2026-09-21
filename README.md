# touchgrass

Self-hosted server-management platform: blue-green deploys, container
metrics + alerts (view-only), and Sentry-style error tracking unified
with log search. One Go binary (UI embedded) + one SQLite file.

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
export TOUCHGRASS_ADMIN_PASSWORD='choose-a-strong-password'
make build && make test && make lint
make run &
curl -s http://127.0.0.1:8080/api/health
open http://127.0.0.1:8080
```

Expected: `{"status":"ok","version":"..."}` (`status` is always `ok`
on a healthy process; `version` comes from `git describe` at build
time, `dev` when unset — same value `touchgrass version` prints).
The dashboard at `/` asks for the admin password, then lists services
from `GET /api/services`.

Or run with Docker Compose (binds `127.0.0.1:8080` on the host):

```bash
export TOUCHGRASS_ADMIN_PASSWORD='choose-a-strong-password'
docker compose up --build -d
curl -s http://127.0.0.1:8080/api/health
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
| `TOUCHGRASS_ADMIN_PASSWORD`          | required           | Admin password (bcrypt-hashed at boot; never stored)    |
| `TOUCHGRASS_COOKIE_SECURE`           | `false`            | Set `true` when serving behind TLS                     |
| `TOUCHGRASS_METRICS_INTERVAL`        | `30s`              | Metric sample cadence                                  |
| `TOUCHGRASS_RETENTION_METRICS`       | `168h`             | Metric max age                                         |
| `TOUCHGRASS_RETENTION_NOTIFICATIONS` | `720h`             | Notification max age                                   |
| `TOUCHGRASS_RETENTION_DEPLOYS`       | `8760h`            | Deploy history max age                                 |
| `TOUCHGRASS_WATCH_INTERVAL`          | `5s`               | Live-target poll cadence                               |
| `TOUCHGRASS_CUTOVER_TIMEOUT`         | `10m`              | Maximum duration for one cutover/rollback run          |
| `TOUCHGRASS_RETENTION_ERRORS`        | `720h`             | Occurrence/issue max age                               |
| `TOUCHGRASS_INGEST_MAX_OCCURRENCES`  | `10000`            | Stored occurrences per service (newest kept)           |
| `TOUCHGRASS_RETENTION_LOGS`          | `168h`             | Log line max age                                       |
| `TOUCHGRASS_LOGS_MAX_LINES`          | `50000`            | Stored log lines per service (newest kept)             |
| `TOUCHGRASS_LOG_POLL_INTERVAL`       | `5s`               | Container log poll cadence                             |

New knobs land in this table in the same PR that introduces them.

## Commands

```bash
make build      # web UI + bin/touchgrass
make build-web  # SPA only (npm ci + vite build)
make test       # unit tests
make test-race  # tests with the race detector
make test-int   # integration tests (Docker; build tag)
make lint       # golangci-lint (config: .golangci.yml)
make lint-fix   # lint with auto-fix
make fmt        # format via golangci-lint
make vet        # go vet
make run        # run the server locally
make dev        # API + Vite with hot reload
make migrate    # schema migrations (up|status)
make help       # list targets
```

Web UI checks: `npm run verify --prefix web` (type-check + lint).

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
- `docs/v1-acceptance.md` — BRD AC-1..6 live sweep checklist
- `docs/service-graduation.md` — S12–S13 per-service onboarding checklist
- `docs/adr/` — architecture decision records
- `AGENTS.md` — working agreement for humans and agents

## License

Apache-2.0 — see `LICENSE`.
