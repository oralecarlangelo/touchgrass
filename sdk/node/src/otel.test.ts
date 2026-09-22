import { afterEach, describe, expect, it, vi } from 'vitest';
import { context, trace } from '@opentelemetry/api';
import type { ExportResult } from '@opentelemetry/core';
import { SeverityNumber } from '@opentelemetry/api-logs';
import { LoggerProvider, SimpleLogRecordProcessor } from '@opentelemetry/sdk-logs';
import type { ReadableLogRecord } from '@opentelemetry/sdk-logs';
import { TouchgrassLogRecordExporter, mapReadableLogRecord } from './otel.js';
import type { LogBatchRequest } from './types.js';

interface SentBatch {
  url: string;
  key: string | undefined;
  batch: LogBatchRequest;
}

function parseSent(url: unknown, init: unknown): SentBatch {
  const request = init as { headers: Record<string, string>; body: string };

  return {
    url: String(url),
    key: request.headers['X-Touchgrass-Key'],
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

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
});

function exportAsync(
  exporter: TouchgrassLogRecordExporter,
  logs: ReadableLogRecord[],
): Promise<ExportResult> {
  return new Promise((resolve) => {
    exporter.export(logs, (result) => resolve(result));
  });
}

function fakeRecord(overrides: Record<string, unknown> = {}): ReadableLogRecord {
  return {
    hrTime: [1700000000, 123000000],
    hrTimeObserved: [1700000000, 123000000],
    attributes: {},
    droppedAttributesCount: 0,
    resource: { attributes: {}, merge: () => ({}) } as never,
    instrumentationScope: { name: 'test' },
    ...overrides,
  } as unknown as ReadableLogRecord;
}

describe('mapReadableLogRecord', () => {
  it.each([
    [1, 'trace'],
    [4, 'trace'],
    [5, 'debug'],
    [8, 'debug'],
    [9, 'info'],
    [12, 'info'],
    [13, 'warn'],
    [16, 'warn'],
    [17, 'error'],
    [20, 'error'],
    [21, 'fatal'],
    [24, 'fatal'],
  ] as Array<[number, string]>)('maps severity number %i to %s', (number, level) => {
    const mapped = mapReadableLogRecord(fakeRecord({ severityNumber: number, body: 'x' }));

    expect(mapped?.level).toBe(level);
    expect(mapped?.severity_number).toBe(number);
  });

  it('falls back to severity text, then info', () => {
    expect(mapReadableLogRecord(fakeRecord({ severityText: 'WARN', body: 'x' }))?.level).toBe('warn');
    expect(mapReadableLogRecord(fakeRecord({ severityText: 'warning', body: 'x' }))?.level).toBe('warn');
    expect(mapReadableLogRecord(fakeRecord({ severityText: 'Error', body: 'x' }))?.level).toBe('error');
    expect(mapReadableLogRecord(fakeRecord({ severityText: 'TRACE', body: 'x' }))?.level).toBe('trace');
    expect(mapReadableLogRecord(fakeRecord({ severityText: 'bogus', body: 'x' }))?.level).toBe('info');
    expect(mapReadableLogRecord(fakeRecord({ body: 'x' }))?.level).toBe('info');
  });

  it('prefers severity number ranges over text', () => {
    const mapped = mapReadableLogRecord(
      fakeRecord({ severityNumber: 17, severityText: 'INFO', body: 'x' }),
    );

    expect(mapped?.level).toBe('error');
    expect(mapped?.severity_number).toBe(17);
  });

  it('converts hrTime to unix seconds and keeps trace context', () => {
    const mapped = mapReadableLogRecord(
      fakeRecord({
        body: 'traced',
        spanContext: {
          traceId: '5b8efff798038103d269b633813fc60c',
          spanId: 'b0e6f15b45c36b12',
          traceFlags: 1,
        },
      }),
    );

    expect(mapped?.timestamp).toBeCloseTo(1700000000.123, 6);
    expect(mapped?.trace_id).toBe('5b8efff798038103d269b633813fc60c');
    expect(mapped?.span_id).toBe('b0e6f15b45c36b12');
  });

  it('drops invalid span contexts and timestamps', () => {
    const zeros = mapReadableLogRecord(
      fakeRecord({
        body: 'x',
        hrTime: [0, 0],
        spanContext: { traceId: '0'.repeat(32), spanId: '0'.repeat(16), traceFlags: 0 },
      }),
    );

    expect(zeros?.trace_id).toBeUndefined();
    expect(zeros?.timestamp).toBeGreaterThan(1700000000);

    const bad = mapReadableLogRecord(fakeRecord({ body: 'x', hrTime: 'nope' }));
    expect(bad?.timestamp).toBeGreaterThan(1700000000);
  });

  it('coerces bodies and attributes to wire values', () => {
    const mapped = mapReadableLogRecord(
      fakeRecord({
        body: { action: 'pay' },
        attributes: { n: 2, ok: true, tags: ['a', 'b'], skip: undefined },
      }),
    );

    expect(mapped?.body).toBe('{"action":"pay"}');
    expect(mapped?.attributes).toEqual({
      n: { value: 2, type: 'integer' },
      ok: { value: true, type: 'boolean' },
      tags: { value: '["a","b"]', type: 'string' },
    });
  });

  it('falls back for missing bodies and returns null on garbage', () => {
    expect(mapReadableLogRecord(fakeRecord({}))?.body).toBe('(empty)');
    expect(mapReadableLogRecord(fakeRecord({ body: 42 }))?.body).toBe('42');
    expect(mapReadableLogRecord(null as unknown as ReadableLogRecord)).toBeNull();
    expect(mapReadableLogRecord(42 as unknown as ReadableLogRecord)).toBeNull();
  });

  it('caps runaway attributes', () => {
    const attributes: Record<string, string> = {};

    for (let i = 0; i < 80; i += 1) {
      attributes[`k${i}`] = 'v';
    }

    const mapped = mapReadableLogRecord(fakeRecord({ body: 'x', attributes }));

    expect(Object.keys(mapped?.attributes ?? {})).toHaveLength(64);
  });
});

describe('LoggerProvider end-to-end', () => {
  it('ships OTel logger output through a real provider', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const exporter = new TouchgrassLogRecordExporter({
      endpoint: 'https://tg.internal/',
      key: 'tg_test',
      release: 'v2.0.0',
    });
    const provider = new LoggerProvider({
      processors: [new SimpleLogRecordProcessor({ exporter })],
    });

    try {
      const logger = provider.getLogger('shop');
      logger.emit({
        severityNumber: SeverityNumber.WARN,
        severityText: 'WARN',
        body: 'stock low for sku-7',
        attributes: { sku: 'sku-7', left: 3 },
      });

      await provider.forceFlush();

      expect(sent).toHaveLength(1);
      expect(sent[0]?.url).toBe('https://tg.internal/api/ingest/logs');
      expect(sent[0]?.key).toBe('tg_test');
      expect(sent[0]?.batch.release).toBe('v2.0.0');

      const items = sent[0]?.batch.items ?? [];
      expect(items).toHaveLength(1);
      expect(items[0]?.level).toBe('warn');
      expect(items[0]?.severity_number).toBe(13);
      expect(items[0]?.body).toBe('stock low for sku-7');
      expect(items[0]?.attributes).toEqual({
        sku: { value: 'sku-7', type: 'string' },
        left: { value: 3, type: 'integer' },
      });
    } finally {
      await provider.shutdown();
      await exporter.shutdown();
    }
  });

  it('carries explicit span context to trace fields', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const traceId = '5b8efff798038103d269b633813fc60c';
    const spanId = 'b0e6f15b45c36b12';
    const fakeSpan = { spanContext: () => ({ traceId, spanId, traceFlags: 1 }) };
    const spanCtx = trace.setSpan(context.active(), fakeSpan as never);

    const exporter = new TouchgrassLogRecordExporter({
      endpoint: 'https://tg.internal',
      key: 'tg_test',
    });
    const provider = new LoggerProvider({
      processors: [new SimpleLogRecordProcessor({ exporter })],
    });

    try {
      provider.getLogger('shop').emit({ body: 'in-span', context: spanCtx });
      await provider.forceFlush();

      expect(sent).toHaveLength(1);
      expect(sent[0]?.batch.items[0]?.trace_id).toBe(traceId);
      expect(sent[0]?.batch.items[0]?.span_id).toBe(spanId);
    } finally {
      await provider.shutdown();
      await exporter.shutdown();
    }
  });
});

describe('exporter behavior', () => {
  it('chunks past the 1000-item wire cap', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const exporter = new TouchgrassLogRecordExporter({
      endpoint: 'https://tg.internal',
      key: 'tg_test',
    });

    const logs = Array.from({ length: 1001 }, (_, i) => fakeRecord({ body: `m${i}` }));
    const result = await exportAsync(exporter, logs);

    expect(result.code).toBe(0);
    expect(sent).toHaveLength(2);
    expect(sent[0]?.batch.items).toHaveLength(1000);
    expect(sent[1]?.batch.items).toHaveLength(1);
  });

  it('applies beforeSendLog and scrub before send', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const exporter = new TouchgrassLogRecordExporter({
      endpoint: 'https://tg.internal',
      key: 'tg_test',
      scrub: ['hunter2'],
      beforeSendLog: (item) => (item.body === 'drop' ? null : item),
    });

    const result = await exportAsync(exporter, [
      fakeRecord({ body: 'drop' }),
      fakeRecord({ body: 'leak hunter2', attributes: { s: 'hunter2' } }),
    ]);

    expect(result.code).toBe(0);
    expect(sent).toHaveLength(1);

    const items = sent[0]?.batch.items ?? [];
    expect(items).toHaveLength(1);
    expect(items[0]?.body).toBe('leak [redacted]');
    expect(items[0]?.attributes?.['s']?.value).toBe('[redacted]');
  });

  it('reports success with nothing to send', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const exporter = new TouchgrassLogRecordExporter({
      endpoint: 'https://tg.internal',
      key: 'tg_test',
    });

    await expect(exportAsync(exporter, [])).resolves.toEqual({ code: 0 });
    await expect(exportAsync(exporter, null as unknown as ReadableLogRecord[])).resolves.toEqual({
      code: 0,
    });
    expect(sent).toHaveLength(0);
    await expect(exporter.forceFlush()).resolves.toBeUndefined();
  });

  it('drops quietly on invalid options', async () => {
    const sent: SentBatch[] = [];
    mockFetchOk(sent);

    const exporter = new TouchgrassLogRecordExporter({ endpoint: '', key: '' });
    await expect(exportAsync(exporter, [fakeRecord({ body: 'x' })])).resolves.toEqual({ code: 0 });
    expect(sent).toHaveLength(0);
  });

  it('fails closed on shutdown and delivery trouble, never throwing', async () => {
    const exporter = new TouchgrassLogRecordExporter({
      endpoint: 'https://tg.internal',
      key: 'tg_test',
    });
    await exporter.shutdown();

    await expect(exportAsync(exporter, [fakeRecord({ body: 'x' })])).resolves.toEqual({ code: 1 });

    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new Error('down');
      }),
    );

    const live = new TouchgrassLogRecordExporter({
      endpoint: 'https://tg.internal',
      key: 'tg_test',
    });
    await expect(exportAsync(live, [fakeRecord({ body: 'x' })])).resolves.toEqual({ code: 1 });

    vi.stubGlobal('fetch', vi.fn(async () => ({ status: 500 })));
    await expect(exportAsync(live, [fakeRecord({ body: 'x' })])).resolves.toEqual({ code: 1 });

    expect(() =>
      live.export([fakeRecord({ body: 'x' })], () => {
        throw new Error('callback bug');
      }),
    ).not.toThrow();

    // Constructor and export survive garbage without throwing.
    expect(() => new TouchgrassLogRecordExporter(null as never)).not.toThrow();
    expect(() =>
      live.export(null as unknown as ReadableLogRecord[], () => undefined),
    ).not.toThrow();
  });
});
