# AGENTS.md — touchgrass

touchgrass is an open-source, self-hosted server-management platform:
blue-green deploys, container metrics + alerts (view-only), and
Sentry-style error tracking unified with log search. Single Go binary
(UI embedded) + single SQLite file. Full context: `BRD-touchgrass.md`.

Before any Go coding, review, debugging, troubleshooting, or setup task, load the `samber/cc-skills-golang@golang-how-to` skill first — it routes to whichever other Go skills the task needs.

## Required Go skills

Beyond routing, these always apply to Go work here: `golang-project-layout`,
`golang-code-style`, `golang-naming`, `golang-error-handling`,
`golang-testing`, `golang-lint`. Load task secondaries per the
`golang-how-to` table (DB → `golang-database`, goroutines →
`golang-concurrency` + `golang-context`, audit → `golang-security`).

## Stack

- Go control plane, module `github.com/oralecarlangelo/touchgrass`
- SQLite via pure-Go driver (no CGO), versioned SQL in `migrations/`
- HTTP: stdlib `net/http` + Go 1.22 ServeMux; SSE for live streams
- UI: React + Vite + Tailwind SPA in `web/`, embedded from `web/dist`
- SDK: TypeScript `@touchgrass/node` in `sdk/node/` (zero-dep, fail-open)
- Logging: `log/slog` JSON to stdout; `samber/oops` for production errors
- Lint: `golangci-lint`; `.golangci.yml` is the source of truth

## Layout (summary)

`cmd/touchgrass/` (thin mains) · `internal/{http,service,store,docker,
probe,notify,config,model}` · `web/` · `sdk/node/` · `migrations/` ·
`docs/` · `scripts/`. Full standard: `docs/PROJECT_STRUCTURE.md`.
System design: `docs/ARCHITECTURE.md`.

Rules: mains parse flags, wire deps, call `Run()` — no business logic.
Business logic in `internal/`. No `pkg/` until external consumers exist.

## Commands

`Makefile` is the source of truth (created in Phase 0). Expected targets:

```bash
make build      # go build ./...
make test       # go test ./...
make test-race  # go test -race ./...
make test-int   # go test -tags=integration ./...
make lint       # golangci-lint run ./...
make lint-fix   # golangci-lint run --fix ./...
make fmt        # golangci-lint fmt ./... (gofmt/gofumpt/goimports)
make vet        # go vet ./...
make run        # go run ./cmd/touchgrass serve
make migrate    # go run ./cmd/touchgrass migrate up
```

CI runs vet + lint + test (`-race`) + build. Keep every one green.

## Go conventions (musts)

Style (`golang-code-style`, `golang-naming`):

- `gofmt`/`gofumpt` clean; break >120 cols at semantic boundaries; 4+
  call args go one per line; ≤4 params or use an options struct
- `:=` for non-zero, `var` for zero values; init slices/maps non-nil
  (`[]T{}`, `map[K]V{}`); field names in all composite literals
- Early return, no `else` after return, `switch` over if-else chains,
  named booleans for 3+ operand conditions, `ctx` first param
- MixedCaps always; packages lowercase singular, no stutter
  (`http.Client`, `user.New()`); `is`/`has`/`can` bool fields;
  `Err*` vars / `*Error` types; acronyms all-caps (`URL`, `HTTPServer`);
  iota enums start with an `Unknown` sentinel at 0
- Unexport aggressively; no dot imports; blank imports only in
  `main`/tests; one primary type per file

Errors (`golang-error-handling`):

- Check every returned error — never discard with `_`
- Wrap with context: `fmt.Errorf("doing thing: %w", err)`; lowercase,
  no punctuation (acronyms lowercase too)
- Log OR return, never both; `%w` internally, `%v` at system boundaries
- `errors.Is`/`As` for inspection, `errors.Join` for independent errors
- No `panic` for expected conditions; never expose internals to users

Lint (`golang-lint`):

- Run `golangci-lint run ./...` after every significant change
- `//nolint:<linter> // reason` — name + justification mandatory;
  never suppress security linters without a strong reason

## Testing (`golang-testing`)

- Table-driven with named (lowercase) subtests; `foo.go` → `foo_test.go`
  in the same order as the source; assert behavior, not internals
- testify as helpers only; build `assert.New(t)` inside each subtest
- `t.Parallel()` where independent; `//go:build integration` for DB /
  Docker tests; `goleak` in goroutine packages; `httptest` for handlers
- Fast unit tests (<1ms); coverage is a gap finder, not a target

## TypeScript side (`web/`, `sdk/node/`)

- `npm run verify` (type-check + lint) clean; Vitest for SDK and complex UI logic
- SDK is zero-dependency and fail-open: async, no sync IO, guarded init —
  an SDK failure must never break or slow the host app

## Git

- `develop` is the default branch; PR `develop` → `main` for releases
- Conventional Commits; small focused PRs
- Never merge to `main` without explicit user confirmation

## Voice

Professional

## Docs index

- `BRD-touchgrass.md` — requirements, pillars, acceptance criteria
- `16-custom-deploy-manager-scope.md` — phased build plan
- `deployment-overview.md` — TicketNation as-is reference
- `docs/ARCHITECTURE.md` — system design
- `docs/PROJECT_STRUCTURE.md` — folder structure standard
- `docs/adr/` — architecture decision records (from Phase 0)
