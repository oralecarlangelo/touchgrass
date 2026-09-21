# ADR-0002: License Apache-2.0

- Status: Accepted (2026-09-21)
- Source: `16-custom-deploy-manager-scope.md`, key decision 2

## Context

touchgrass is us-first with OSS hygiene (open question Q6): it must be
free-tier open-source and adoptable by others. MIT, Apache-2.0, and
AGPL-3.0 were compared.

## Decision

Apache-2.0. It is as permissive as MIT and adds an express patent grant
plus a state-changes condition — strictly better protection for an
infrastructure tool at no adoption cost. AGPL-3.0 was rejected: its
network-use-is-distribution and same-license conditions would deter
external adoption.

## Consequences

- `LICENSE` carries the Apache-2.0 text; the OSS launch (Sprint 14)
  adds headers and contribution terms on the same license.
