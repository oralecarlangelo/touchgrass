# Structured logging

`logger` in `@touchgrass/node` (0.2.0+) ships structured application logs —
levels, printf templates, typed attributes, and trace context — to
`POST /api/ingest/logs` in batches. It rides the same client, queue,
scrub, and fail-open contract as error capture, and needs no new
dependencies: the core stays zero-dep.

```ts
import { init, logger } from '@touchgrass/node';

init({ endpoint: 'https://tg.internal', key: process.env.TOUCHGRASS_KEY! });

logger.info('checkout finished for order %s', order.id, { total: order.total });
logger.warn('retrying payment', { attempt, order: order.id });
logger.error(new Error('charge failed'), { order: order.id });
```

In the Logs view, these rows live under the **Application (SDK)** tab;
container output stays under **Containers**.

## API

One method per level — `trace`, `debug`, `info`, `warn`, `error`,
`fatal` — plus a generic `captureLog(level, msg, ...args)` for dynamic
levels. All share one signature:

```ts
logger.info(message, ...args);
```

- **Printf formatting.** Extra args format through `util.format`, so
  `%s`/`%d`/`%j` behave exactly like `console.log`:

  ```ts
  logger.info('user %s has %d messages', 'ann', 3);
  // body: "user ann has 3 messages"
  ```

- **Trailing object = attributes.** When the last argument is a plain
  object, it is stripped from the format args and stored as typed
  attributes (`string`, `integer`, `double`, `boolean`; objects and
  arrays become JSON strings):

  ```ts
  logger.info('order %s', 'o-9', { region: 'eu', total: 42.5 });
  // body: "order o-9", attrs: region="eu", total=42.5
  ```

  Arrays stay format args; only plain objects count. The message itself
  is always the body — `logger.info({ a: 1 })` logs the string
  `"{ a: 1 }"`.

- **Errors.** An `Error` first argument sets the body to
  `"Name: message"` and captures `error.type`, `error.value`, and
  `error.stacktrace` attributes. Trailing attribute objects still merge
  in:

  ```ts
  logger.error(new TypeError('bad sku'), { route: '/pay' });
  // body: "TypeError: bad sku" + error.* attrs + route="/pay"
  ```

- **Levels and severity.** Levels map to OTel severity numbers —
  trace 1, debug 5, info 9, warn 13, error 17, fatal 21 — sent as
  `severity_number` on every item.

Every method is fail-open: garbage input, a throwing hook, or a down
ingest endpoint never throws into host code. Logs queue in memory
(bounded by `maxQueue`, shared cap with errors) and flush every second
alongside error reports — `flush()` and `close()` drain both.

## Attributes

Formatting is stored twice: the interpolated `body` for reading, and
typed attributes for grouping (Sentry Log protocol style):

| Attribute                  | Meaning                                    |
| -------------------------- | ------------------------------------------ |
| `sentry.message.template`  | The original template, e.g. `"user %s"`    |
| `sentry.message.parameter.N` | Positional format args (`0`, `1`, …)     |
| `error.type` / `error.value` / `error.stacktrace` | Error first-argument details |
| your keys                  | Trailing-object attributes, typed          |

Template attributes appear only when format args are present — a plain
`logger.info('up')` carries no `sentry.*` keys. Limits follow the wire:
at most 64 keys (SDK-generated grouping keys win ties), keys capped at
128 chars, string values at 4096, bodies at 8192.

`scrub` patterns from `init` redact the body and every string attribute
before send, same as error reports. To filter or rewrite logs in code,
pass `beforeSendLog`:

```ts
init({
  endpoint,
  key,
  beforeSendLog: (item) => {
    if (item.level === 'trace') return null; // drop
    item.attributes = { ...item.attributes }; // or mutate + return
    return item;
  },
});
```

Return `null` to drop, return the item (possibly modified) to keep, or
mutate in place and return nothing. A throwing hook — or a non-object
return — keeps the original item rather than losing the log.

## Trace correlation

When `@opentelemetry/api` is installed, each log auto-captures the
active span's `trace_id`/`span_id` at capture time — no configuration.
The lookup is fully guarded: no OTel package, no active span, or any
error silently yields a log without trace fields. It never throws and
adds no dependency (`@opentelemetry/api` is an optional peer).

SDK rows render trace/span chips in the Logs view; the trace chip
filters to that trace. Filter programmatically with
`GET /api/logs?source=sdk&trace_id=…`.

## OTel exporter setup

OTel-native apps can skip `logger` entirely: point a logs SDK at
touchgrass with `TouchgrassLogRecordExporter` from the
`@touchgrass/node/otel` subpath (ESM and CJS builds included):

```bash
npm install @touchgrass/node @opentelemetry/sdk-logs
```

```ts
import { LoggerProvider, BatchLogRecordProcessor } from '@opentelemetry/sdk-logs';
import { TouchgrassLogRecordExporter } from '@touchgrass/node/otel';

const provider = new LoggerProvider({
  processors: [
    new BatchLogRecordProcessor({
      exporter: new TouchgrassLogRecordExporter({
        endpoint: 'https://tg.internal',
        key: process.env.TOUCHGRASS_KEY!,
        release: process.env.APP_VERSION,
        scrub: [/token=[^&\s]+/g],
      }),
    }),
  ],
});

provider.getLogger('shop').emit({ body: 'otel-native log' });
```

Mapping: OTel severity numbers win for the level (text falls back, then
`info`); `hrTime` becomes the unix-seconds timestamp; span context
becomes `trace_id`/`span_id`; OTel attributes become typed attributes.
Span context comes from each record's context: with async tracking
enabled (the NodeSDK sets this up) the active span attaches
automatically, otherwise pass it explicitly —
`emit({ body, context: trace.setSpan(context.active(), span) })`.
Records flow through the same `beforeSendLog`, sanitize, and scrub
pipeline as `logger`, in ≤1000-item batches. Delivery trouble reports
`FAILED` to the processor without throwing, and invalid options drop
quietly like a disabled client.

The subpath imports OTel types only — `@opentelemetry/sdk-logs` is an
optional peer, and installing it is required solely to use the
exporter.
