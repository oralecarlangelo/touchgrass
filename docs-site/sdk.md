# Node SDK

`@touchgrass/node` — fail-open error capture for Node 20+. Zero runtime
dependencies, ESM + CommonJS builds, typed from the same shapes the
ingest API stores.

```bash
npm install @touchgrass/node
```

## init

Call once, earliest in the entrypoint, before routes boot:

```ts
import { init } from '@touchgrass/node';

init({
  endpoint: 'https://tg.internal',
  key: process.env.TOUCHGRASS_KEY!,
  release: process.env.APP_VERSION ?? 'dev',
  scrub: [/token=[^&\s]+/g],
});
```

| Option            | Required | Default              | Meaning                                                        |
| ----------------- | -------- | -------------------- | -------------------------------------------------------------- |
| `endpoint`        | yes      | —                    | touchgrass base URL (**no** `/api/ingest` — the SDK appends it) |
| `key`             | yes      | —                    | Per-service `tg_…` key; keep it in env, never in code           |
| `release`         | no       | `TOUCHGRASS_RELEASE` | Release tag on every report; empty when unset                   |
| `scrub`           | no       | `[]`                 | `RegExp` or string patterns redacted client-side before send    |
| `maxQueue`        | no       | `100`                | Max queued reports; oldest drop past the cap                    |
| `captureUncaught` | no       | `true`               | Hook `uncaughtException` + `unhandledRejection`                 |

Invalid options yield a disabled client — `init` never throws.

CommonJS hosts use `require`; the API is identical:

```js
const { init, captureException } = require('@touchgrass/node');
```

## Capturing

```ts
import { addBreadcrumb, captureException, captureMessage } from '@touchgrass/node';

// Cheap, high-signal trail: call at route and job boundaries.
addBreadcrumb({ category: 'http', message: 'POST /checkout' });

// Manual reports. Unknown inputs are stringified, never thrown on.
try {
  await checkout(order);
} catch (error) {
  captureException(error);
  throw error;
}

// Messages group like errors but carry no stack.
captureMessage(`backfill finished: ${count} orders`);
```

Uncaught exceptions and rejections are captured automatically when
`captureUncaught` is on. Crash semantics are preserved: if your app
has its own `uncaughtException` listener, it owns the outcome; as the
sole listener the SDK prints the error, flushes bounded (2s), and
exits 1 — exactly what node would do without it.

## Lifecycle

Reports flush in the background every second over HTTPS with a 5s
abort timeout. The queue is bounded and the timers are `unref`'d, so
the SDK never holds the event loop open.

```ts
import { close, flush } from '@touchgrass/node';

// Short-lived scripts: await delivery before exit.
const delivered = await flush(2000);

// Servers: drain on shutdown.
process.on('SIGTERM', async () => {
  await close();
  server.close();
});
```

Both resolve `false` on delivery trouble instead of throwing.

## Scrubbing PII

`scrub` patterns run client-side before anything leaves the process —
the server never sees the raw values and never logs payloads:

```ts
init({
  endpoint,
  key,
  scrub: [
    /token=[^&\s]+/g, // query-string tokens
    /sk-[a-z0-9]+/gi, // api keys
    'hunter2', // literal strings work too
  ],
});
```

Review captured payloads with your team after the first week and
extend the patterns for anything that slipped through.

## Fail-open contract

- Every export is guarded: bad input disables, delivery trouble
  resolves `false`, nothing throws into host code.
- No sync IO, no event-loop holds, bounded memory.
- An SDK failure must never break or slow the host app — if you can
  measure the SDK, that is a bug: file it.
