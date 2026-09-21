# ADR-0003: Stack — Go control plane, SQLite, TS SPA + SDK

- Status: Accepted (2026-09-21)
- Source: `16-custom-deploy-manager-scope.md`, key decision 3;
  `BRD-touchgrass.md` section 7 (supersedes the earlier TypeScript-API
  recommendation per stakeholder decision)

## Context

The tool must ship as a single artifact with a tiny footprint on the
existing EC2 host, while the team's muscle memory is entirely TypeScript
(NestJS, Next, Expo). The host apps needing instrumentation are Node, so
only a TypeScript SDK can cover them.

## Decision

- Control plane in Go: single static binary, small footprint, strong stdlib.
- UI as a React + Vite + Tailwind SPA embedded into the binary (single
  artifact, Teal-validated model).
- SQLite via a pure-Go driver (no CGO) with versioned SQL migrations:
  zero ops, single-file backup, preserves cross-compilation.
- SDK stays TypeScript (`@touchgrass/node`): the hosts are Node.

## Consequences

- Two languages in the repo, each where the team and ecosystem are
  strongest (accepted in the BRD).
- Go is a new competency for a TypeScript team: mitigated by a narrow API
  surface, stdlib-first code, and ADRs for non-obvious Go choices.
