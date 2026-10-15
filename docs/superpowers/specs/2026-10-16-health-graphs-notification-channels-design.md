# Health Graphs and Provider-Neutral Emergency Notifications

**Date:** 2026-10-16  
**Status:** Provider-neutral direction approved; awaiting written-spec review  
**Owners:** Touch Grass maintainers

## Summary

Touch Grass will make infrastructure health easier to understand and make urgent alerts useful outside the browser. The Performance workspace will replace long, repetitive PostgreSQL and Redis metric lists with a small set of accessible trend charts and directly labeled health indicators. The alert system will gain durable outbound channels that any self-hosted deployment can configure without depending on Ticketnation.

The first outbound providers will be generic SMTP and signed webhooks. SMTP gives immediate compatibility with Amazon SES, Mailgun, SendGrid, Postmark, and self-hosted mail servers without vendor-specific SDKs. A provider registry will keep the delivery pipeline open to later native adapters. Operators may configure channels in the dashboard with encrypted-at-rest credentials or supply read-only configuration through environment variables and mounted secret files.

Urgent delivery will reuse Touch Grass's existing alert rules and notifications. Alert evaluation will never wait for email or webhook delivery: a durable outbox will enqueue one delivery per matching channel, and a background worker will retry transient failures with bounded backoff. Breaches, recoveries, unexpected traffic flips, issue spikes, and deployment failures can be routed by severity and event type.

## Goals

- Show health trends, thresholds, and change over time instead of presenting only current counters.
- Make PostgreSQL and Redis panels understandable without knowing their raw metric names.
- Correctly distinguish Redis command families instead of rendering repeated “command calls” rows.
- Let open-source operators configure emergency email using any standards-compatible SMTP provider.
- Support signed webhook delivery for chat, incident-management, and custom automation systems.
- Keep provider credentials out of logs, API responses, audit details, and browser storage.
- Deliver asynchronously with deduplication, retries, recovery notices, and visible delivery state.
- Preserve Touch Grass's existing single-binary, SQLite, agentless architecture.

## Non-goals

- Building a general marketing-email or campaign system.
- Sending customer or organizer email through the Ticketnation API.
- Requiring Amazon, Mailgun, or another vendor SDK in the core release.
- Replacing PagerDuty, Opsgenie, or specialist incident-management products.
- Sending raw logs, SQL text, request paths, secrets, user data, or stack traces in outbound alerts.
- Automatically remediating incidents from an email or webhook.
- Adding OpenTelemetry or an application agent to monitored services.
- Supporting arbitrary executable notification plugins in the first release.

## Considered approaches

### A. Generic SMTP and signed webhooks behind a provider registry — selected

One notification pipeline owns routing, outbox persistence, retries, and delivery history. Provider implementations only validate configuration, send a bounded message, and classify failures as retryable or permanent. SMTP covers SES, Mailgun, SendGrid, Postmark, and conventional mail servers. Signed webhooks provide a universal integration point.

This approach gives open-source users useful compatibility immediately while retaining a stable extension boundary for future native adapters.

### B. Native SES and Mailgun integrations first — deferred

Native APIs can expose provider-specific features and error detail, but they increase dependencies, credential formats, documentation, and test burden. They would also privilege two vendors while still leaving most self-hosted users unsupported. Native adapters may be added later when they provide a concrete advantage over SMTP.

### C. Ticketnation API sends Touch Grass email — rejected

Routing operational alerts through the monitored application creates circular dependency: an API outage could prevent its own emergency alert. It also makes the open-source project depend on Ticketnation-specific credentials and endpoints.

### D. Fire-and-forget delivery during alert evaluation — rejected

Synchronous sending would slow the sampler, lose notifications during provider outages, and create duplicate delivery after uncertain failures. Outbound delivery must be queued and observable.

## Health-graph experience

### Performance workspace structure

The existing service and time-range controls remain at the top. The page presents information in this order:

1. **Health summary** — current request rate, error rate, p95 latency, slow routes, and collector freshness.
2. **Request health** — traffic, latency, and failure trends aligned to the selected time range.
3. **Runtime pressure** — CPU, memory, PostgreSQL connection pressure, and Redis evictions.
4. **Route families** — the existing sortable route table.
5. **Dependency health** — curated PostgreSQL and Redis trend cards.
6. **Investigation feed** — correlated changes and links to logs, issues, and deployments.

The page will keep progressive disclosure: the first view answers whether the system is healthy; details remain available without making every raw metric equally prominent.

### Request and runtime charts

The current correlation chart will be split into two coordinated charts sharing the same time axis:

- **Request health:** request rate as restrained bars, p95 latency as a line, and error rate as a separate directly labeled line or band. Latency and percentage retain distinct axes.
- **Runtime pressure:** CPU, memory, and PostgreSQL connection utilization as percentage lines, with Redis eviction events as bars. Deployments and unexpected runtime events remain in the marker strip below.

Charts use no more than three continuous series per panel. Thresholds from active alert rules appear as labeled reference lines. Tooltips include value, timestamp, and unit. The visual design never relies on color alone: series use direct labels, line styles, or markers, and each chart has a compact textual summary.

### PostgreSQL health card

The PostgreSQL card will derive and show:

- Connection utilization: `connections_total / max_connections`.
- Transactions per interval: commit plus rollback deltas.
- Cache-hit ratio: hits divided by hits plus reads.
- Deadlocks per interval.
- Checkpoint pressure as a secondary detail.

The card shows a concise current status row, then small trend charts for connection utilization, transaction rate, cache-hit ratio, and deadlocks. Percentage charts have a fixed 0–100 domain; count/rate charts begin at zero. Missing denominators display “Unavailable” rather than a misleading zero.

Statement fingerprints remain a separate empty state until `pg_stat_statements` is enabled. Their absence does not degrade the aggregate health card.

### Redis health card

The Redis card will derive and show:

- Memory utilization when `maxmemory` is configured; otherwise used memory with an explicit byte unit.
- Connected and blocked clients.
- Cache-hit ratio from keyspace hits and misses.
- Evictions per interval.
- Command rate grouped by the existing bounded command family.

Command-family rows and tooltips include the family label, such as “Read commands” or “Write commands,” so repeated generic labels cannot occur. Blocked clients and evictions receive visible event markers when non-zero.

### Responsive and accessible behavior

- Desktop uses two-column dependency cards; narrow screens stack them.
- Charts reduce tick density at small widths instead of shrinking labels below legibility.
- Each chart exposes an accessible name and concise text summary.
- Keyboard users can reach tooltips or switch to the compact data table.
- A table toggle exposes the exact plotted values without requiring hover.
- Loading, stale, unavailable, and partial-source states remain independent per panel.

## Notification architecture

### Core interfaces

Outbound providers implement a small internal interface:

```go
type Provider interface {
    Kind() string
    Validate(context.Context) error
    Send(context.Context, Delivery) error
}
```

Providers are constructed through a registry keyed by a stable kind such as `smtp` or `webhook`. Alert generation and routing depend only on the outbox, not on provider-specific code. Provider errors are classified as retryable, permanent, or configuration failures.

The first release includes:

- **SMTP:** STARTTLS, implicit TLS, or plaintext only when explicitly allowed for a private network. Supports authenticated and intentionally unauthenticated relays.
- **Webhook:** HTTPS JSON POST with timestamp, delivery ID, and HMAC-SHA256 signature headers.

Native SES and Mailgun API providers can later register against the same interface. Their addition must not require schema or alert-pipeline changes.

### Channel configuration

Each notification channel contains:

- Display name and provider kind.
- Enabled state.
- Provider-specific encrypted configuration.
- Sender identity and bounded recipient list for SMTP.
- Minimum severity and selected event types.
- Creation/update timestamps and last test result.

There are two supported configuration modes:

1. **Dashboard-managed channels.** An administrator uses a provider wizard, enters credentials, chooses routes, and sends a test. Secrets are encrypted before SQLite persistence using an authenticated-encryption key supplied as `TOUCHGRASS_SECRET_KEY`. API reads return only masked metadata. If the key is absent, the dashboard explains that persisted credential setup is unavailable.
2. **Deployment-managed channels.** Operators mount a permission-restricted JSON configuration file referenced by `TOUCHGRASS_NOTIFICATION_CONFIG`. Secret values may reference environment variables rather than appearing directly in the file. These channels are read-only in the dashboard and work without storing provider secrets in SQLite.

The mounted-file path is the canonical multi-channel automation interface. A documented single-SMTP environment-variable shortcut may be provided for small deployments. Environment and mounted-file channels take precedence over a dashboard channel with the same stable identifier.

`TOUCHGRASS_PUBLIC_URL` supplies links in messages. Missing or invalid public URLs omit the link rather than constructing one from request headers.

### Secret protection

- Dashboard-managed provider secrets use a versioned AES-GCM envelope and a deployment-controlled 256-bit master key.
- Plaintext credentials exist only during validation, provider construction, and sending.
- Secrets are never returned after creation, written to logs, placed in audit details, or included in backups outside the encrypted envelope.
- Updating a channel without a replacement secret preserves the existing encrypted value.
- A future key-rotation command can re-encrypt stored envelopes; automated rotation is not required for the first release.
- Losing the deployment key makes persisted provider credentials unusable but does not block Touch Grass startup or monitoring.

### Events and severity

Outbound routing operates on a bounded event envelope containing event ID, service ID, event type, severity, title, body, occurrence time, and optional safe dashboard URL. Initial event classes are:

| Event | Default severity | Email eligible |
|---|---:|---:|
| Sustained metric or performance breach | Critical | Yes |
| Recovery from a fired breach | Resolved | Yes |
| Public health check unavailable/recovered | Critical / Resolved | Yes |
| New issue | Warning | Configurable |
| Issue spike | Critical | Yes |
| Unexpected blue-green traffic flip | Critical | Yes |
| Deployment failure | Critical | Yes |
| Successful deployment | Info | No by default |
| Collector stale or repeatedly failing | Warning | Configurable |

Existing alert-rule duration remains the anti-flap mechanism. Recovery sends only when the corresponding breach previously fired. Repeated samples in the same incident do not enqueue new email.

The existing health probe loop will gain a small transition detector for public-health failures and recoveries. It uses the same sustained-duration and episode rules as metric alerts, so one failed probe does not create an incident. This detector records a bounded event and in-app notification before the outbox routes it; it does not change traffic or restart a service.

### Durable outbox

Creating an in-app notification and its outbound deliveries occurs in one SQLite transaction. The schema adds:

- `notification_channels` — provider metadata and encrypted configuration.
- `notification_routes` — selected event types and minimum severity.
- `notification_deliveries` — notification/event ID, channel ID, status, attempt count, next attempt, last error class, timestamps, and provider message identifier.

A unique constraint on event ID and channel ID prevents duplicate enqueue. Delivery bodies are rendered from the safe event envelope at send time; credentials and raw logs are never copied into the outbox.

The worker claims a bounded batch, sends with a timeout, and records the result. Retryable failures use jittered exponential backoff at approximately 1, 5, 15, and 60 minutes, then every six hours up to a configurable maximum attempt count. Permanent authentication or validation failures stop retrying and mark the channel degraded. Process restart resumes pending deliveries. Shutdown waits only for an in-flight bounded send, not the full queue.

Email or webhook failure cannot recursively generate another outbound delivery. Touch Grass records one in-app channel-health notification per degraded episode and exposes the failure in the settings screen.

### Message contents

Emergency email is intentionally concise:

- Severity and service name in the subject.
- What crossed a threshold or changed.
- Current value, configured threshold, duration, and UTC/local timestamp where available.
- Recovery duration for resolved incidents.
- Safe link to the scoped Touch Grass investigation view.
- Delivery ID for support and deduplication.

Email supports plain text and a minimal escaped HTML alternative. Subjects and headers reject CR/LF injection. Webhook JSON follows a versioned schema and excludes secrets, raw logs, SQL text, request paths, and customer identifiers.

### Webhook security

- HTTPS is required by default.
- Loopback, link-local, cloud metadata, and private destinations are blocked after DNS resolution unless an explicit deployment allowlist permits them.
- Redirects are disabled or revalidated against the same policy.
- Each request includes a delivery ID, timestamp, and HMAC signature.
- Timeout and response-body limits prevent a receiver from exhausting the worker.

## Dashboard experience

Add **Notification channels** under the existing Notifications area.

The page contains:

1. **Delivery health:** enabled channels, pending deliveries, failures requiring attention, and most recent success.
2. **Channel cards:** provider, masked sender/endpoint, routing summary, last delivery, and health state.
3. **Add channel wizard:** SMTP or webhook, connection details, recipients, event routing, review, and test.
4. **Delivery history:** event, channel, status, attempts, last error summary, and retry time.

Only administrators can create, update, test, enable, disable, or delete channels. Other signed-in users may see channel health and masked metadata but never credential fields. Destructive actions require confirmation. Test messages are clearly labeled and enter the same outbox as production messages.

The service Alerts tab keeps rule creation focused on thresholds. It adds a short delivery summary and link to global channel settings instead of duplicating provider configuration per service.

## APIs

Administrator-only endpoints:

- `GET /api/notification-channels`
- `POST /api/notification-channels`
- `PATCH /api/notification-channels/{id}`
- `DELETE /api/notification-channels/{id}`
- `POST /api/notification-channels/{id}/test`
- `GET /api/notification-deliveries`
- `POST /api/notification-deliveries/{id}/retry`

Responses expose masked configuration and operational status only. Create and update requests accept write-only secret fields. Requests use the existing authentication, authorization, JSON size limits, audit logging, and structured error format.

Performance APIs remain backward compatible. Derived chart series should be computed in the web client from existing bounded PostgreSQL and Redis rows unless a calculation requires unavailable source data or would duplicate substantial logic. Any new server-derived fields must be additive.

## Failure behavior

- Invalid channel configuration is rejected before enablement.
- A provider outage does not block sampling, alert evaluation, HTTP serving, or deployment operations.
- An unavailable master key marks dashboard-managed channels unusable and leaves them disabled; it does not expose or erase ciphertext.
- A stale collector preserves historical graphs and identifies the affected source.
- Counter resets break rate calculations at that point; charts show a gap or reset marker rather than a negative spike.
- A missing metric produces “Unavailable,” not zero.
- Queue and history retention are bounded by age and row/byte budget.

## Testing strategy

### Go

- Provider registry and configuration validation.
- SMTP TLS/auth behavior with a local fake server; no external provider calls in tests.
- Webhook signatures, destination policy, redirects, timeout, and response limits.
- Encryption round-trip, tamper detection, missing/wrong key, masked API projection, and secret-preserving updates.
- Transactional event plus outbox insertion and uniqueness under retry.
- Worker success, retryable/permanent failure, restart recovery, backoff bounds, and shutdown.
- Breach-to-recovery episode behavior and suppression before a breach fires.
- Role enforcement and audit coverage for every channel mutation and manual retry.
- Migration upgrade from the current production schema and rollback-safe backup procedure.

### Web

- Derived PostgreSQL and Redis time-series calculations, including reset and missing-data cases.
- Correct Redis command-family labels and units.
- Accessible chart names, text summaries, keyboard data-table toggle, loading, empty, stale, and partial states.
- Channel wizard validation, masked-secret editing, route selection, test delivery, and degraded status.
- Responsive rendering at narrow and wide viewports.

### Operational validation

- Use a local capture SMTP server and signed webhook receiver before production credentials.
- Confirm one breach creates one delivery and one recovery creates one delivery.
- Stop the receiver, verify retries survive a Touch Grass restart, then restore it and confirm delivery.
- Confirm alert evaluation and dashboards remain healthy while every provider is unavailable.
- Scan logs, API responses, SQLite exports, and audit records for plaintext test credentials.
- Verify SES and Mailgun using their SMTP endpoints in documented integration tests or staging, not unit tests.

## Rollout

1. Ship health-graph changes with existing APIs and no notification behavior change.
2. Ship migrations, provider registry, encrypted configuration, and outbox with channels disabled by default.
3. Enable local SMTP/webhook capture and exercise breach, recovery, retry, restart, and test-message paths.
4. Configure one production SMTP channel through a mounted secret file or encrypted dashboard setup.
5. Send and verify a labeled test alert before enabling critical routes.
6. Enable critical breach, recovery, unexpected flip, issue-spike, and deployment-failure routes.
7. Monitor queue depth, failures, duplicate rate, SQLite growth, and provider latency.

Rollback disables the delivery worker and channels while preserving the outbox and in-app notifications. Schema additions remain backward compatible with the previous binary after a database backup; the previous binary ignores the new tables.

## Documentation

Open-source documentation will include:

- Generic SMTP setup.
- Amazon SES SMTP credentials and endpoint example.
- Mailgun SMTP credentials and endpoint example.
- SendGrid/Postmark-compatible SMTP notes.
- Signed webhook schema and verification example.
- Dashboard-managed versus deployment-managed secrets.
- Docker Compose and systemd examples using mounted secret files.
- Test-email and troubleshooting runbooks.
- Backup, master-key loss, channel degradation, and credential-rotation guidance.

## Acceptance criteria

- PostgreSQL and Redis panels show meaningful trends, units, status, and labels rather than repeated raw counters.
- Every graph has a text summary and exact-value table alternative.
- Counter resets and missing denominators cannot produce misleading negative or zero health values.
- A self-hosted operator can configure SES or Mailgun using SMTP without modifying Touch Grass code.
- Dashboard-managed credentials are encrypted at rest and never returned or logged.
- Deployment-managed channels can run with no provider secret persisted in SQLite.
- Alert evaluation never blocks on provider network calls.
- A sustained breach and its recovery each send once to matching channels.
- Retryable failures survive restart and eventually deliver without duplicates.
- Permanent failures become visible without causing recursive alerts.
- Non-admin users cannot mutate or reveal channel configuration.
- Existing in-app notifications, alert rules, dashboards, and agentless collectors continue working when no outbound channel is configured.

