# Quickstart

Five minutes from zero to your first grouped issue. You need a running
touchgrass server (see [Self-hosting](/self-hosting)) and a Node 20+
app.

## 1. Mint a key

Keys are per service and carry a sampling rate. In the console, open
**Keys** for your service and mint one — or with curl (admin session
cookie required):

```bash
curl -b jar -X POST https://tg.internal/api/keys \
  -H 'Content-Type: application/json' \
  -d '{"service_id":"my-api","sample_rate":1}'
# -> {"id":1,"key":"tg_...","key_prefix":"tg_ab12...","sample_rate":1}
```

Record the plaintext `key` once — the API never shows it again.

## 2. Install the SDK

```bash
npm install @touchgrass/node
```

## 3. Initialize first

Earliest in your entrypoint, before routes boot:

```ts
import { init, addBreadcrumb } from '@touchgrass/node';

init({
  endpoint: 'https://tg.internal', // base URL only; /api/ingest is appended
  key: process.env.TOUCHGRASS_KEY!,
  release: process.env.APP_VERSION ?? 'dev',
});
```

Containerized apps must use a reachable base URL — each container has
its own `127.0.0.1`, so loopback never works from inside Docker.

## 4. Force one error

```ts
import { captureException } from '@touchgrass/node';

try {
  await checkout(order);
} catch (error) {
  captureException(error);
  throw error;
}
```

Uncaught exceptions and unhandled rejections are captured automatically —
no `try/catch` needed for those.

## 5. See it grouped

Within a minute, the **Issues** view shows one issue with the stack
trace, breadcrumbs, and release tag — plus ±60s of surrounding
container logs. Create a `new_issue` rule to get notified on the next
one.

Next: [Node SDK reference](/sdk) for every option, [Structured
logging](/sdk-logging) to ship application logs with trace correlation,
or [Concepts](/concepts) for how grouping, sampling, and retention
work. Beyond errors: [Fleet monitoring](/fleet) for every container
on the box, [Deploys](/deploys) for cutover/rollback flows, and
[Databases](/databases) for Postgres health, backups, and restores.
