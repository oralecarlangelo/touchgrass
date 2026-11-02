# Host security-event ingestion

Touchgrass can turn a dedicated privacy-safe JSONL stream from an edge or host
control into durable security findings. This source is optional and isolated
from request serving: an unavailable or malformed stream cannot stop the
monitored service.

## Event contract

Each newline-terminated record must contain only bounded labels:

```json
{"timestamp":"2026-11-02T05:04:53Z","event_type":"security_probe","rule_id":"edge.malformed_server_action","category":"traffic","severity":"medium","confidence":"high","source":"nginx","action":"blocked","method":"POST","status":"404"}
```

Do not include raw paths, query strings, client addresses, headers, cookies,
referrers, or user agents. Touchgrass ignores unknown JSON fields and never
copies them into evidence, but the producer should omit them so the source file
itself remains privacy-safe.

Valid categories are `posture`, `process`, `drift`, and `traffic`. Severities
are `low`, `medium`, `high`, or `critical`; confidence is `low`, `medium`, or
`high`. Identifier fields use lowercase letters, digits, dots, underscores, and
hyphens and are limited to 128 bytes.

## Enable and verify

Set both the absolute source path and the existing owner service:

```bash
TOUCHGRASS_SECURITY_EVENT_LOG=/var/log/nginx/security/events.jsonl
TOUCHGRASS_SECURITY_EVENT_SERVICE_ID=web
TOUCHGRASS_SECURITY_EVENT_INTERVAL=5s
```

The file must be read-only to the Touchgrass process and writable only by its
producer. Restart Touchgrass, generate one controlled blocked request, and
verify one `traffic` finding with the producer rule ID and `blocked` action.
Then confirm an ordinary request and a valid application action create no
finding. Rotation by rename/create is supported; a changed inode or truncated
file increments the source's dropped-record counter.

To roll back, unset both source variables and restart Touchgrass. Existing
findings remain available for their normal retention period.
