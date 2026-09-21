# ADR-0001: Build narrow, three pillars

- Status: Accepted (2026-09-21)
- Source: `16-custom-deploy-manager-scope.md`, key decision 1

## Context

TicketNation needs deploy orchestration, container metrics, and error/log
visibility on a single EC2 host. Adopting an existing platform (Coolify,
Dokploy) would mean migrating tn-api's proven nginx-flip + compose +
Actions flow onto their stack — a migration, not an add-on. Semaphore UI
runs generic workflows but has no deployment model (no colors, versions,
cutovers). Teal validates the deploy-tool concept but is too immature to
adopt (0 stars, single author). Error tracking and container management
neighbors exist, but nothing unifies all three needs in one free,
self-hosted control plane.

## Decision

Build a narrow, three-pillared tool (deploys, resources, logs) instead of
adopting. If the team ever wants to stop owning it, Coolify's free
self-hosted tier covers the deploy pillar via migration — recorded here as
the honest exit.

## Consequences

- We own maintenance for three pillars; each must stay usable alone so any
  one can rot without breaking the others (see ADR-0006).
- No migration risk to the proven deploy flow; the tool wraps it (see
  ADR-0005).
