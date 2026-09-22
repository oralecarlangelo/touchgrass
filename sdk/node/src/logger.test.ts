import { afterEach, describe, expect, it, vi } from 'vitest';
import { context, trace } from '@opentelemetry/api';
import { AsyncLocalStorageContextManager } from '@opentelemetry/context-async-hooks';
import { TouchgrassClient } from './client.js';
import { __setOtelLoaderForTests, buildLogItem, levelToSeverityNumber } from './logger.js';
import type { LogBatchRequest, LogItem, LogLevel } from './types.js';

interface SentBatch {
  url: string;
  key: string | undefined;
  auth: string | undefined;
  batch: LogBatchRequest;
}

function parseSent(url: unknown, init: unknown): SentBatch {
  const request = init as { headers: Record<string, string>; body: string };

  return {
    url: String(url),
    key: request.headers['X-Touchgrass-Key'],
    auth: request.headers['Authorization'],
    batch: JSON.parse(request.body) as LogBatchRequest,
  };
}

function mockFetchOk(sent: SentBatch[]): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: unknown, init: unknown) => {
      sent.push(parseSent(url, init));
      return { status: 202 };
    }),
  );
}

const clients: TouchgrassClient[] = [];

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
  __setOtelLoaderForTests(null);

  while (clients.length > 0) {
    const client = clients.pop();
    await client?.close();
  }
});

function track(client: TouchgrassClient): TouchgrassClient {
  clients.push(client);
  return client;
}

function makeClient(options?: Record<string, unknown>): TouchgrassClient {
  return track(
    new TouchgrassClient({
      endpoint: 'https://tg.internal',
      key: 'tg_test',
      release: 'v2.0.0',
      captureUncaught: false,
      ...(options ?? {}),
    }),
  );
}

async function sentItems(client: TouchgrassClient, sent: SentBatch[]): Promise<LogItem[]> {
  expect(await client.flush()).toBe(true);
  expect(sent).toHaveLength(1);

  return sent[0]?.batch.items ?? [];
}

describe('severity mapping', () => {
  it.each([
    ['trace', 1],
    ['debug', 5],
    ['info', 9],
    ['warn', 13],
    ['error', 17],
    ['fatal', 21],
  ] as Array<[LogLevel, number]>)('maps %s to severity %i', async (level, severity) => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog(level, 'hello');

    const items = await sentItems(client, sent);

    expect(items).toHaveLength(1);
    expect(items[0]?.level).toBe(level);
    expect(items[0]?.severity_number).toBe(severity);
    expect(levelToSeverityNumber(level)).toBe(severity);
  });
});

describe('formatting and attributes', () => {
  it('formats printf args and stores template plus parameters', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('info', 'User %s has %d messages', 'ann', 3);

    const items = await sentItems(client, sent);
    const attrs = items[0]?.attributes ?? {};

    expect(items[0]?.body).toBe('User ann has 3 messages');
    expect(attrs['sentry.message.template']).toEqual({
      value: 'User %s has %d messages',
      type: 'string',
    });
    expect(attrs['sentry.message.parameter.0']).toEqual({ value: 'ann', type: 'string' });
    expect(attrs['sentry.message.parameter.1']).toEqual({ value: 3, type: 'integer' });
  });

  it('treats a trailing plain object as attributes', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('info', 'checkout done', { order: 'o-1', total: 42.5, paid: true });

    const items = await sentItems(client, sent);

    expect(items[0]?.body).toBe('checkout done');
    expect(items[0]?.attributes).toEqual({
      order: { value: 'o-1', type: 'string' },
      total: { value: 42.5, type: 'double' },
      paid: { value: true, type: 'boolean' },
    });
  });

  it('combines format args with trailing attributes', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('info', 'order %s', 'o-9', { region: 'eu' });

    const items = await sentItems(client, sent);
    const attrs = items[0]?.attributes ?? {};

    expect(items[0]?.body).toBe('order o-9');
    expect(attrs['sentry.message.template']?.value).toBe('order %s');
    expect(attrs['sentry.message.parameter.0']).toEqual({ value: 'o-9', type: 'string' });
    expect(attrs['region']).toEqual({ value: 'eu', type: 'string' });
  });

  it('omits template attrs when there are no parameters', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('info', 'plain message');

    const items = await sentItems(client, sent);

    expect(items[0]?.body).toBe('plain message');
    expect(items[0]?.attributes).toBeUndefined();
  });

  it('keeps arrays as format args, not attributes', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('info', 'vals', [1, 2]);

    const items = await sentItems(client, sent);

    expect(items[0]?.body).toBe('vals [ 1, 2 ]');
    expect(items[0]?.attributes?.['sentry.message.template']?.value).toBe('vals');
    expect(items[0]?.attributes?.['sentry.message.parameter.0']).toEqual({
      value: '[1,2]',
      type: 'string',
    });
  });

  it('stringifies non-string messages', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('info', { a: 1 });

    const items = await sentItems(client, sent);

    expect(items[0]?.body).toBe('{ a: 1 }');
  });

  it('coerces attribute value types', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('info', 'types', {
      int: 7,
      float: 1.5,
      bool: false,
      str: 'x',
      nested: { deep: true },
      list: [1],
      nil: null,
      missing: undefined,
      notFinite: Number.NaN,
    });

    const attrs = (await sentItems(client, sent))[0]?.attributes ?? {};

    expect(attrs['int']).toEqual({ value: 7, type: 'integer' });
    expect(attrs['float']).toEqual({ value: 1.5, type: 'double' });
    expect(attrs['bool']).toEqual({ value: false, type: 'boolean' });
    expect(attrs['str']).toEqual({ value: 'x', type: 'string' });
    expect(attrs['nested']).toEqual({ value: '{"deep":true}', type: 'string' });
    expect(attrs['list']).toEqual({ value: '[1]', type: 'string' });
    expect(attrs['nil']).toBeUndefined();
    expect(attrs['missing']).toBeUndefined();
    expect(attrs['notFinite']).toEqual({ value: 'NaN', type: 'string' });
  });

  it('caps attributes at 64 keys with template data kept', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const attrs: Record<string, number> = {};

    for (let i = 0; i < 70; i += 1) {
      attrs[`k${i}`] = i;
    }

    const client = makeClient();
    client.captureLog('info', 'many %s', 'x', attrs);

    const items = await sentItems(client, sent);
    const keys = Object.keys(items[0]?.attributes ?? {});

    expect(keys).toHaveLength(64);
    expect(items[0]?.attributes?.['sentry.message.template']?.value).toBe('many %s');
  });

  it('truncates oversized bodies and string values', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('info', 'x'.repeat(9000), { big: 'y'.repeat(5000) });

    const items = await sentItems(client, sent);

    expect(items[0]?.body).toHaveLength(8192);
    expect(items[0]?.attributes?.['big']?.value).toHaveLength(4096);
  });

  it('replaces empty bodies so the wire stays valid', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('info', '');

    const items = await sentItems(client, sent);

    expect(items[0]?.body).toBe('(empty)');
  });
});

describe('Error handling', () => {
  it('captures type and stack attributes from an Error message', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('error', new Error('boom'));

    const items = await sentItems(client, sent);
    const attrs = items[0]?.attributes ?? {};

    expect(items[0]?.body).toBe('Error: boom');
    expect(attrs['error.type']).toEqual({ value: 'Error', type: 'string' });
    expect(attrs['error.value']).toEqual({ value: 'boom', type: 'string' });
    expect(typeof attrs['error.stacktrace']?.value).toBe('string');
    expect(String(attrs['error.stacktrace']?.value)).toContain('boom');
  });

  it('merges trailing attributes with error attributes', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('error', new TypeError('bad'), { route: '/pay' });

    const items = await sentItems(client, sent);

    expect(items[0]?.body).toBe('TypeError: bad');
    expect(items[0]?.attributes?.['error.type']?.value).toBe('TypeError');
    expect(items[0]?.attributes?.['route']?.value).toBe('/pay');
  });

  it('falls back to the error name when the message is empty', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('error', new Error(''));

    const items = await sentItems(client, sent);

    expect(items[0]?.body).toBe('Error');
    expect(items[0]?.attributes?.['error.type']?.value).toBe('Error');
  });

  it('appends extra args after an Error message', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();
    client.captureLog('error', new Error('boom'), 'while saving');

    const items = await sentItems(client, sent);

    expect(items[0]?.body).toBe('Error: boom while saving');
    expect(items[0]?.attributes?.['error.value']?.value).toBe('boom');
  });
});

describe('beforeSendLog', () => {
  it('drops logs returning null', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient({ beforeSendLog: () => null });
    client.captureLog('info', 'dropped');

    expect(await client.flush()).toBe(true);
    expect(sent).toHaveLength(0);
  });

  it('sends modified items', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient({
      beforeSendLog: (item: LogItem) => ({ ...item, body: 'rewritten' }),
    });
    client.captureLog('info', 'original');

    const items = await sentItems(client, sent);

    expect(items[0]?.body).toBe('rewritten');
  });

  it('keeps in-place mutations returning undefined', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient({
      beforeSendLog: (item: LogItem) => {
        item.body = 'mutated';
      },
    });
    client.captureLog('info', 'original');

    const items = await sentItems(client, sent);

    expect(items[0]?.body).toBe('mutated');
  });

  it('keeps the original when the hook throws', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient({
      beforeSendLog: () => {
        throw new Error('hook bug');
      },
    });
    client.captureLog('info', 'original');

    const items = await sentItems(client, sent);

    expect(items[0]?.body).toBe('original');
  });

  it('keeps the original on garbage hook results', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    for (const result of [42, 'x', { level: 'nope', body: 'bad' }]) {
      const client = makeClient({ beforeSendLog: () => result as unknown as LogItem });
      client.captureLog('info', 'original');

      expect(await client.flush()).toBe(true);
    }

    expect(sent).toHaveLength(3);
    expect(sent.every((entry) => entry.batch.items[0]?.body === 'original')).toBe(true);
  });

  it('sanitizes hook output so one item cannot poison the batch', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient({
      beforeSendLog: (item: LogItem) => ({
        ...item,
        severity_number: 99,
        trace_id: 'not-hex',
        attributes: { evil: { value: 'x'.repeat(9000), type: 'string' } },
      }),
    });
    client.captureLog('warn', 'hooked');

    const items = await sentItems(client, sent);

    expect(items[0]?.severity_number).toBe(13);
    expect(items[0]?.trace_id).toBeUndefined();
    expect(items[0]?.attributes?.['evil']?.value).toHaveLength(4096);
  });
});

describe('trace auto-capture', () => {
  const traceId = '5b8efff798038103d269b633813fc60c';
  const spanId = 'b0e6f15b45c36b12';

  function fakeApi(ids: { traceId: string; spanId: string } | null): () => unknown {
    return () => ({
      trace: {
        getSpan: () =>
          ids === null ? undefined : { spanContext: () => ({ ...ids, traceFlags: 1 }) },
      },
      context: { active: () => ({}) },
    });
  }

  it('captures trace and span from the active span', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);
    __setOtelLoaderForTests(fakeApi({ traceId, spanId }));

    const client = makeClient();
    client.captureLog('info', 'traced');

    const items = await sentItems(client, sent);

    expect(items[0]?.trace_id).toBe(traceId);
    expect(items[0]?.span_id).toBe(spanId);
  });

  it('omits trace fields with no active span', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);
    __setOtelLoaderForTests(fakeApi(null));

    const client = makeClient();
    client.captureLog('info', 'untraced');

    const items = await sentItems(client, sent);

    expect(items[0]?.trace_id).toBeUndefined();
    expect(items[0]?.span_id).toBeUndefined();
  });

  it('stays silent when @opentelemetry/api is absent', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);
    __setOtelLoaderForTests(() => {
      throw new Error("Cannot find module '@opentelemetry/api'");
    });

    const client = makeClient();
    client.captureLog('info', 'no-otel');

    const items = await sentItems(client, sent);

    expect(items[0]?.trace_id).toBeUndefined();
    expect(items[0]?.body).toBe('no-otel');
  });

  it('ignores garbage loaders and invalid span contexts', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const loaders: Array<() => unknown> = [
      () => null,
      () => 42,
      () => ({ trace: {} }),
      fakeApi({ traceId: 'not-hex', spanId: 'zzz' }),
      fakeApi({ traceId: '0'.repeat(32), spanId: '0'.repeat(16) }),
      () => ({
        trace: {
          getSpan: () => {
            throw new Error('span bug');
          },
        },
        context: { active: () => ({}) },
      }),
    ];

    for (const loader of loaders) {
      __setOtelLoaderForTests(loader);

      const built = buildLogItem('info', 'x', []);
      expect(built.trace_id).toBeUndefined();
      expect(built.span_id).toBeUndefined();
    }

    expect(sent).toHaveLength(0);
  });

  it('reads the real @opentelemetry/api without throwing', async () => {
    // Default loader, real installed module, no provider: no active span.
    const built = buildLogItem('info', 'real-api', []);

    expect(built.body).toBe('real-api');
    expect(built.trace_id).toBeUndefined();
  });

  it('captures a span through the real api context manager', async () => {
    // Noop manager (default) cannot propagate; enable the real ALS one.
    const manager = new AsyncLocalStorageContextManager();
    manager.enable();
    context.setGlobalContextManager(manager);

    try {
      const fakeSpan = {
        spanContext: () => ({ traceId, spanId, traceFlags: 1 }),
      };
      const ctx = trace.setSpan(context.active(), fakeSpan as never);

      await context.with(ctx, async () => {
        const built = buildLogItem('info', 'ctx-span', []);

        expect(built.trace_id).toBe(traceId);
        expect(built.span_id).toBe(spanId);
      });
    } finally {
      context.disable();
      manager.disable();
    }
  });
});

describe('delivery', () => {
  it('posts one batch with release and key headers', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const before = Date.now() / 1000;
    const client = makeClient();
    client.captureLog('info', 'first');
    client.captureLog('warn', 'second');
    const after = Date.now() / 1000;

    expect(await client.flush()).toBe(true);
    expect(sent).toHaveLength(1);
    expect(sent[0]?.url).toBe('https://tg.internal/api/ingest/logs');
    expect(sent[0]?.key).toBe('tg_test');
    expect(sent[0]?.auth).toBe('Bearer tg_test');
    expect(sent[0]?.batch.release).toBe('v2.0.0');
    expect(sent[0]?.batch.items.map((item) => item.body)).toEqual(['first', 'second']);

    for (const item of sent[0]?.batch.items ?? []) {
      expect(item.timestamp).toBeGreaterThanOrEqual(before);
      expect(item.timestamp).toBeLessThanOrEqual(after);
    }
  });

  it('omits empty releases', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);
    vi.stubEnv('TOUCHGRASS_RELEASE', '');

    const client = track(
      new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test', captureUncaught: false }),
    );
    client.captureLog('info', 'x');

    expect(await client.flush()).toBe(true);
    expect(sent[0]?.batch).not.toHaveProperty('release');
  });

  it('scrubs bodies and string attributes', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient({ scrub: ['hunter2'] });
    client.captureLog('info', 'leak hunter2 %s', 'hunter2', { secret: 'hunter2' });

    const items = await sentItems(client, sent);
    const attrs = items[0]?.attributes ?? {};

    expect(items[0]?.body).toBe('leak [redacted] [redacted]');
    expect(attrs['sentry.message.parameter.0']).toEqual({ value: '[redacted]', type: 'string' });
    expect(attrs['secret']).toEqual({ value: '[redacted]', type: 'string' });
  });

  it('drops oldest logs past the queue cap', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient({ maxQueue: 2 });
    client.captureLog('info', 'first');
    client.captureLog('info', 'second');
    client.captureLog('info', 'third');

    const items = await sentItems(client, sent);

    expect(items.map((item) => item.body)).toEqual(['second', 'third']);
  });

  it('resolves false on non-202 without throwing', async () => {
    const sent: SentBatch[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: unknown, init: unknown) => {
        sent.push(parseSent(url, init));
        return { status: 400 };
      }),
    );

    const client = makeClient();
    client.captureLog('info', 'lost');

    await expect(client.flush()).resolves.toBe(false);
    expect(sent).toHaveLength(1);
  });

  it('never rejects with ingestion down', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new Error('connect ECONNREFUSED');
      }),
    );

    const client = makeClient();
    client.captureLog('info', 'lost');

    await expect(client.flush(200)).resolves.toBe(false);

    const closing = makeClient();
    closing.captureLog('info', 'lost');

    await expect(closing.close()).resolves.toBe(false);
  });
});

describe('fail-open', () => {
  it('no-ops on invalid levels without throwing', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = makeClient();

    expect(() => client.captureLog('nope' as LogLevel, 'x')).not.toThrow();
    expect(() => client.captureLog(null as unknown as LogLevel, 'x')).not.toThrow();
    expect(await client.flush()).toBe(true);
    expect(sent).toHaveLength(0);
  });

  it('survives garbage inputs', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = track(
      new TouchgrassClient({
        endpoint: 'https://tg.internal',
        key: 'tg_test',
        beforeSendLog: 42 as unknown as never,
      }),
    );

    expect(() => client.captureLog('info', Symbol('sym'))).not.toThrow();
    expect(() => client.captureLog('info', Object.create(null))).not.toThrow();
    expect(() => client.captureLog('info', 'x', undefined, null, 0)).not.toThrow();

    const circular: Record<string, unknown> = {};
    circular['self'] = circular;
    expect(() => client.captureLog('info', 'x', circular)).not.toThrow();
    expect(() => client.captureLog('info', 'x', { bad: circular })).not.toThrow();

    expect(await client.flush()).toBe(true);
    expect(sent).toHaveLength(1);
    expect(sent[0]?.batch.items.length).toBeGreaterThan(0);

    for (const item of sent[0]?.batch.items ?? []) {
      expect(typeof item.body).toBe('string');
      expect(item.body.length).toBeGreaterThan(0);
    }
  });

  it('disables cleanly on invalid options', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const client = track(new TouchgrassClient({ endpoint: '', key: '' }));

    expect(() => client.captureLog('info', 'x')).not.toThrow();
    expect(await client.flush()).toBe(true);
    expect(sent).toHaveLength(0);
  });

  it('capture returns fast while ingestion hangs', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: unknown, init: unknown) => {
        const signal = (init as { signal: AbortSignal }).signal;

        await new Promise((_resolve, reject) => {
          signal.addEventListener('abort', () => reject(new Error('aborted')), { once: true });
        });

        return { status: 202 };
      }),
    );

    const client = makeClient();
    const start = Date.now();
    client.captureLog('info', 'slow');
    expect(Date.now() - start).toBeLessThan(1000);

    await expect(client.flush(150)).resolves.toBe(false);
  });
});
