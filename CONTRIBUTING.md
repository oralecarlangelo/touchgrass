# Contributing to touchgrass

Thanks for helping keep prod boring. This guide covers the workflow;
`AGENTS.md` in the repo root holds the full Go/style/testing standards
and wins on any conflict.

## Setup

Prerequisites: Go 1.26+, Node 20+, `golangci-lint` v2.

```bash
git clone https://github.com/oralecarlangelo/touchgrass
cd touchgrass
export TOUCHGRASS_ADMIN_PASSWORD='dev-password'
make build && make test
make run # http://127.0.0.1:8080
```

For UI work with hot reload: `make dev` (API + Vite side by side).

No Docker daemon is needed for unit work — Docker-backed paths are
covered by fakes, with live tests behind the `integration` tag.

## Branches and commits

- `develop` is the default branch; open PRs against it. `main` takes
  releases only (`develop` → `main` PRs), never direct commits.
- Conventional Commits: `feat(scope): ...`, `fix(scope): ...`, etc.
- Small, focused PRs. One behavior change per PR beats a bundle.

## Before you push

Every gate below runs in CI. Keep all of them green:

```bash
make vet
make lint # golangci-lint; .golangci.yml is the source of truth
make test # unit suite
make test-race # same suite under -race
make test-int # integration tag (DB/Docker paths)
make audit # govulncheck gate (known accepted-risk IDs live in scripts/)
npm run verify --prefix web # tsc + eslint
npm run build --prefix web # the SPA that embeds into the binary
```

`make all` runs vet + lint + test + build as one shot.

## Code standards (short version)

- Go: `gofmt`/`gofumpt` clean, ≤120 cols, early returns, `ctx` first,
  wrap errors with context (`%w`), log OR return never both. Business
  logic in `internal/`; `cmd/` mains only parse flags and wire deps.
- Tests: table-driven, `foo.go` → `foo_test.go`, assert behavior not
  internals, `t.Parallel()` where independent. Changed behavior ships
  with its regression test in the same PR.
- `//nolint:<linter> // reason` needs the linter name plus a
  justification; never suppress security linters casually.
- Web/SDK: `npm run verify` clean. The SDK is zero-dependency and
  fail-open — an SDK failure must never break or slow the host app.

## Docs are code

Plan/BRD/architecture drift is a bug: update the doc in the same PR
that introduces the change. New services onboard as data (see
`docs/service-graduation.md`), never as code forks.

## Security

Found a vulnerability? See `docs/threat-model.md` for the trust
boundaries, then open a private security advisory on GitHub instead
of a public issue. Never commit secrets, tokens, or production
passwords — env vars and EAS-style secret stores only.
