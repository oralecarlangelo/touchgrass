# ADR-0008: Product voice — Gen Z tone, credible core

- Status: Accepted (2026-09-21)
- Source: `16-custom-deploy-manager-scope.md`, key decision 8;
  `BRD-touchgrass.md` FR-P4

## Context

The tool is team-internal first and OSS second. A playful voice makes the
dashboard feel like ours, but cutover confirmations, audit entries, and
errors must never be ambiguous.

## Decision

Status and empty-state copy leans playful (vibe checks, cooking deploys);
error messages, audit entries, and docs stay precise. Voice never obscures
meaning.

## Consequences

- UI copy reviews check tone; API errors, audit records, and ADRs/docs
  stay plain and exact.
- `touchgrass version` and `/api/health` output are machine-facing and
  stay out of the playful voice.
