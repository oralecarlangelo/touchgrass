# Fleet monitoring

Every container on the daemon is observed — managed or not. A sampler
reads `docker stats` for each container plus host CPU, memory, disk,
and load every 30 seconds, keeps 7 days of history (the
`TOUCHGRASS_RETENTION_METRICS` window), and serves it on the console's
**Overview → Fleet** page and two API routes. No agents, no
per-container config: a fresh server populates its fleet table within
a minute of boot.

## Managed vs observed

**Managed** containers belong to a service row — they get deploys,
alert rules, log collection, and a link to their service page from
the fleet table. **Observed** containers are everything else on the
daemon: databases, caches, other teams' stacks, infra daemons. They
get metrics only — CPU, memory, restarts, state — which is usually
exactly what you need when the box is hot and the question is "who
is eating CPU?".

This split is deliberate: promoting a stack to managed stays a
conscious act (strategy, health checks, nginx), while visibility is
free and total from second one.

## The page

Host stat cards up top (CPU, memory, disk, load), CPU/memory/load
charts with 1h–7d ranges below them, then the fleet table: name,
compose project, managed/observed badge, state, CPU, memory,
restarts, sampled age — hottest CPU first.

## Promoting to managed

Observed rows have a Manage button. It drafts the service row from
the container's labels (project, service, compose dir) and probes
its published ports for a health URL, with confidence, reasons, and
warnings — blue-green pairs are detected and prefilled, including
the nginx marker file. Review the draft, create, and the stack is
managed: health tracking, metrics history, and deploys. Blue-green
promotion still needs its compose colors and cutover-script fork
first (the manual bootstrap); recreate promotion is one click.

## API

- `GET /api/system/containers` — latest sample per container.
- `GET /api/system/history?metric=cpu|mem|load&hours=N` — host
  history, bucket-averaged to at most 1500 points (default 24h,
  max 168h).
- `GET /api/onboarding/suggest?container=<name>` — promotion draft.
- `POST /api/services` — create a managed service row.

Shapes are in the [REST API reference](/api/).

## Limits

- Containers living under ~30 seconds may never appear — sampling
  is interval-based, not event-based.
- History starts at deploy time; ranges backfill as samples
  accumulate.
- Alert rules stay per-service: observed containers page nobody.
  If a stack needs alerting, promote it to managed.
