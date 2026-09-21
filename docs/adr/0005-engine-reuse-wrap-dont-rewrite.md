# ADR-0005: Engine reuse — wrap, don't rewrite

- Status: Accepted (2026-09-21)
- Source: `16-custom-deploy-manager-scope.md`, key decision 5

## Context

`bluegreen-*.sh` in `ticketnation-main-api/deploy/` already implements
cutover semantics with a locally proven 0.0s cutover, plus an SSH fallback
procedure the team trusts. Reimplementing that logic in Go would fork the
semantics and void the proof.

## Decision

The scripts remain the source of truth for cutover semantics. The tool
shells out, parses results, and records history — never reimplements the
flip.

## Consequences

- Traffic behavior stays identical with or without the tool (additivity:
  stopping the tool leaves the SSH/script path working).
- Cutover work in Sprints 4–5 is integration + recording, not engine design.
- A script bug is fixed in the scripts, not papered over in Go.
