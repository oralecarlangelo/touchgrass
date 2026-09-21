# ADR-0004: Agentless control plane v1, SDK ingestion, agents later

- Status: Accepted (2026-09-21)
- Source: `16-custom-deploy-manager-scope.md`, key decision 4

## Context

v1 targets a single EC2 host. Installing per-host agents would add ops
burden with no multi-host requirement to justify it, while error reports
still need a path from apps into the tool.

## Decision

v1 runs on (or next to) the target host and drives the Docker socket plus
the existing cutover scripts as its deploy engine; apps report errors to
an ingestion endpoint via the SDK using per-project API keys. No agent to
install. Retention caps and sampling live in the ingestion path, not as an
afterthought. Multi-host agents become Phase 8; service/host config stays
data so agents later don't require rewrites.

## Consequences

- Simplest viable topology: one binary + one SQLite file + Docker socket.
- The `docker/` package is the seam a future agent transport remotes
  behind; `service`/`store` interfaces stay host-agnostic.
