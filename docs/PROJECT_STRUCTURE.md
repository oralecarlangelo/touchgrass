# touchgrass Project Structure Standard

The authoritative folder layout. Follows `golang-project-layout` (service
type: `cmd/` + `internal/` + `web/`). Create exactly this in Phase 0;
extend only by the placement rules below.

Module path: `github.com/oralecarlangelo/touchgrass`.

## Annotated tree

```
touchgrass/
├── AGENTS.md                  # agent working agreement (this repo's manual)
├── README.md                  # what/why, quickstart, links (Phase 0)
├── LICENSE                    # Apache-2.0 (Phase 0)
├── go.mod / go.sum            # single Go module (Phase 0)
├── Makefile                   # build/test/lint/run targets (Phase 0)
├── .golangci.yml              # lint source of truth (Phase 0)
├── .gitignore                 # binaries, web/dist, node_modules, *.db
├── Dockerfile                 # multi-stage: build SPA+Go → slim runtime
├── docker-compose.yml         # binary + SQLite volume (self-host run)
│
├── cmd/
│   └── touchgrass/
│       └── main.go            # ONLY main: flags, wire deps, call Run().
│                              # Subcommands: serve (default), migrate, version.
│
├── internal/                  # all private Go code (never importable externally)
│   ├── http/                  # transport: routes, handlers, middleware, SSE.
│   │                          # Parses input, calls services, writes JSON. No SQL, no domain logic.
│   ├── service/               # domain logic, one file set per area:
│   │                          # deploy.go, resources.go, issues.go, logs.go, notify.go, audit.go.
│   │                          # No net/http, no SQL here.
│   ├── store/                 # SQLite persistence: one repo per area + migrate runner.
│   │                          # Only package allowed to import the SQL driver.
│   ├── model/                 # shared domain types (Service, Deploy, Issue, …). No logic.
│   ├── docker/                # Docker socket client wrapper (ps, stats, logs tail).
│   ├── probe/                 # health probing + downtime-stat computation.
│   ├── notify/                # webhook + email senders.
│   └── config/                # env parsing + validation. Single Config struct.
│
├── web/                       # React + Vite + Tailwind SPA source
│   ├── src/                   # components/, routes/, hooks/, lib/, styles/
│   ├── public/                # static assets
│   ├── package.json           # build → web/dist (gitignored, embedded by Go)
│   └── vite.config.ts
│
├── sdk/
│   └── node/                  # @touchgrass/node: src/, package.json, vitest config.
│                              # Zero deps. Future SDKs sit beside it (sdk/browser/…).
│
├── migrations/                # versioned SQL: 0001_init.sql, 0002_….sql. Forward-only.
│                              # (+ embed.go: go:embed holder, the only .go file here.)
├── scripts/                   # dev helpers (seed, smoke). Bash/Go utilities, not app code.
├── docs/
│   ├── ARCHITECTURE.md        # system design
│   ├── PROJECT_STRUCTURE.md   # this file
│   └── adr/                   # architecture decision records (NNNN-title.md)
└── .github/
    └── workflows/
        ├── ci.yml             # vet + lint + test -race + build (+ govulncheck)
        └── release.yml        # GoReleaser on tag (Phase 7)
```

(`BRD-touchgrass.md`, `16-custom-deploy-manager-scope.md`,
`deployment-overview.md` currently sit at root as reference; `docs/adr`
starts in Phase 0.)

## Package contracts

| Package | Owns | Must never import/own |
| ------- | ---- | --------------------- |
| `cmd/touchgrass` | flags, wiring, `Run()` | business logic, SQL, HTTP handlers |
| `internal/http` | routes, handlers, middleware, SSE | `store`, `docker` directly (go via `service`) |
| `internal/service` | domain rules per pillar area | `net/http`, SQL driver |
| `internal/store` | repos, queries, migrate runner | HTTP, services |
| `internal/model` | shared types only | anything with logic |
| `internal/docker` | socket client wrapper | services, store |
| `internal/probe` | probing + downtime math | store (returns results; service persists) |
| `internal/notify` | webhook/email senders | services (service decides when) |
| `internal/config` | env parsing into `Config` | everything else |

## Placement rules (with examples)

1. New HTTP endpoint → handler in `internal/http/<area>.go` + route
   registration; logic goes in `internal/service/<area>.go`.
2. New DB query → method on the area repo in `internal/store/<area>.go`;
   new table → new `migrations/NNNN_*.sql` (never edit applied ones).
3. New background loop → `internal/service` (or `probe`) with `Run(ctx)`
   + `goleak` coverage; wired in `cmd`, stopped on SIGTERM.
4. Shared type used by 2+ packages → `internal/model/`. Used by one →
   keep it in that package.
5. New external integration (e.g. future agent transport) → new
   `internal/<name>/` package with a narrow interface, not code dumped
   into `service/`.
6. UI work → `web/src/`; shared API client → `web/src/lib/api.ts`.
7. SDK work → `sdk/node/src/`; SDK must stay dependency-free.
8. Dev-only helper → `scripts/`; never in `cmd/` (that's shipped code).

## Naming rules

- Packages: lowercase, singular, match dir name; no `util`/`helper`/
  `common` packages — name the abstraction.
- Files: lowercase with underscores (`deploy_service.go`); tests
  co-located (`foo.go` → `foo_test.go`, same function order).
- Test fixtures in per-package `testdata/`. Integration tests carry
  `//go:build integration`.

## What NOT to create

- `pkg/` — only when code is genuinely useful to external consumers
  (nothing qualifies in v1).
- `api/` OpenAPI specs — REST is hand-rolled in v1; add specs only if
  consumers demand them.
- `vendor/` — rely on the module cache; never commit it.
- `web/dist/`, `*.db`, binaries — all gitignored build artifacts.
- Second `main` packages — one binary, subcommands only.
