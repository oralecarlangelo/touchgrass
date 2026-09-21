# ADR-0007: Name — touchgrass

- Status: Accepted (2026-09-21)
- Source: `16-custom-deploy-manager-scope.md`, key decision 7

## Context

The shortlist was vetted live against the npm registry and product
collisions. Runners-up were eliminated on registry squats or real product
collisions.

## Decision

touchgrass. Free on npm with no dev-infra collision (only small wellbeing
apps in an unrelated category), and it fits the platform story ("go touch
grass, we got prod") better than any deploy-only name.

## Consequences

- Module `github.com/oralecarlangelo/touchgrass`, npm scope `@touchgrass`,
  binary `touchgrass`.
