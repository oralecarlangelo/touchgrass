# @touchgrass/node

Fail-open observability SDK for touchgrass. Captures uncaught
exceptions, unhandled rejections, and manual reports with stack traces,
breadcrumbs, and release tags (async to `POST /api/ingest`), plus
structured logs with levels, attributes, and trace context (batched to
`POST /api/ingest/logs`).

Zero runtime dependencies. Node 20+.

## Install

```bash
npm install @touchgrass/node
```

## Use

```ts
import { init, addBreadcrumb, captureException } from '@touchgrass/node';

init({
  endpoint: 'https://tg.internal', // touchgrass base URL
  key: 'tg_...', // per-service key from POST /api/keys
  release: 'v1.2.3', // or TOUCHGRASS_RELEASE
  scrub: [/token=[^&]+/g, 'hunter2'], // PII redacted before send
});

addBreadcrumb({ category: 'nav', message: 'checkout opened' });

try {
  await checkout();
} catch (error) {
  captureException(error);
}
```

Uncaught exceptions and rejections are captured automatically. Reports
flush in the background every second; call `flush()` before short-lived
exits and `close()` on shutdown.

CommonJS hosts use `require` (a CJS build ships alongside the ESM one):

```js
const { init, captureException } = require('@touchgrass/node');

init({ endpoint: 'https://tg.internal', key: 'tg_...' });
```

## Logging

```ts
import { logger } from '@touchgrass/node';

logger.info('order %s', order.id, { total: order.total });
logger.error(new Error('charge failed'), { order: order.id });
```

`logger.{trace,debug,info,warn,error,fatal}(msg, ...args)` formats with
`util.format`, stores a trailing plain object as typed attributes, and
auto-captures the OTel active span when `@opentelemetry/api` is
present. `beforeSendLog` in `init` filters or rewrites logs (return
`null` to drop). OTel-native apps can use
`TouchgrassLogRecordExporter` from `@touchgrass/node/otel` instead —
see the [structured logging guide](https://github.com/oralecarlangelo/touchgrass/blob/develop/docs-site/sdk-logging.md).

## Fail-open contract (FR-L2)

- Every export is guarded: invalid options yield a disabled client,
  delivery trouble resolves `false`, and nothing here throws into host
  code.
- No sync IO and no event-loop holds: timers are `unref`'d, sends carry
  a 5s abort timeout, queues are bounded (oldest drops past the cap).
- Crash behavior is preserved: with a host `uncaughtException`
  listener, the host owns the outcome; as the sole listener the SDK
  prints the error, flushes bounded (2s), and exits 1 — the same exit
  node would take without it.
- PII scrubbing runs client-side before send (FR-L6).

## Develop

```bash
npm install
npm run verify # tsc + eslint + vitest
npm run build # emits dist/
```
