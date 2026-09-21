# ADR-0006: v1 pillar scope — generic engine day one

- Status: Accepted (2026-09-21)
- Source: `16-custom-deploy-manager-scope.md`, key decision 6; open
  question Q1 (generic strategy engine from day one)

## Context

Hardcoding tn-api's blue-green flow would ship the first service faster
but bake single-service assumptions into the engine, making the second
service a rewrite.

## Decision

The deploy engine is strategy-based and generic from day one — no
per-service hardcoding. tn-api (blue-green) is the first live service and
the SDK dogfood target; a second strategy (recreate) is proven on another
service in the MVP. Resources stays metrics + alerts with no control
actions (view-only); logging ships error tracking and log search together,
linked.

## Consequences

- Sprint 6 must prove a second strategy end-to-end (deploy + rollback +
  history) with zero tn-api-specific code paths.
- Per-service differences live in data (strategy config), never in
  branches on service identity.
