import { afterEach, describe, expect, it, vi } from 'vitest';
import { TouchgrassClient } from './client.js';
import type { ErrorReport } from './types.js';

interface SentRequest {
  url: string;
  key: string | undefined;
  report: ErrorReport;
}

function parseSent(url: unknown, init: unknown): SentRequest {
  const request = init as { headers: Record<string, string>; body: string };

  return {
    url: String(url),
    key: request.headers['X-Touchgrass-Key'],
    report: JSON.parse(request.body) as ErrorReport,
  };
}

function mockFetchOk(sent: SentRequest[]): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: unknown, init: unknown) => {
      sent.push(parseSent(url, init));
      return { status: 202 };
    }),
  );
}

function mockFetchStatus(sent: SentRequest[], status: number): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: unknown, init: unknown) => {
      sent.push(parseSent(url, init));
      return { status };
    }),
  );
}

function mockFetchDown(): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => {
      throw new Error('connect ECONNREFUSED');
    }),
  );
}

function mockFetchHanging(): void {
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
}

const clients: TouchgrassClient[] = [];

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();

  while (clients.length > 0) {
    const client = clients.pop();
    await client?.close();
  }
});

function track(client: TouchgrassClient): TouchgrassClient {
  clients.push(client);
  return client;
}

function silenceConsole(): void {
  vi.spyOn(console, 'error').mockImplementation(() => undefined);
}

describe('delivery', () => {
  it('posts exceptions with trace, crumbs, and release', async () => {
    const sent: SentRequest[] = [];
    mockFetchOk(sent);

    const client = track(
      new TouchgrassClient({ endpoint: 'https://tg.internal/', key: 'tg_test', release: 'v9.9.9' }),
    );
    client.addBreadcrumb({ category: 'nav', message: 'route' });

    const error = new Error('boom');
    client.captureException(error);

    expect(await client.flush()).toBe(true);
    expect(sent).toHaveLength(1);
    expect(sent[0]?.url).toBe('https://tg.internal/api/ingest');
    expect(sent[0]?.key).toBe('tg_test');

    const report = sent[0]?.report;
    expect(report?.type).toBe('exception');
    expect(report?.message).toBe('boom');
    expect(report?.release).toBe('v9.9.9');
    expect(report?.breadcrumbs).toHaveLength(1);
    expect(report?.stack.length).toBeGreaterThan(0);
    expect(report?.stack[0]?.file).toContain('client.test.ts');
  });

  it('posts manual messages without a stack', async () => {
    const sent: SentRequest[] = [];
    mockFetchOk(sent);

    const client = track(new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test' }));
    client.captureMessage('hello');

    expect(await client.flush()).toBe(true);
    expect(sent[0]?.report.type).toBe('message');
    expect(sent[0]?.report.stack).toEqual([]);
  });

  it('resolves false on non-202 without throwing', async () => {
    const sent: SentRequest[] = [];
    mockFetchStatus(sent, 401);

    const client = track(new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test' }));
    client.captureException(new Error('lost'));

    await expect(client.flush()).resolves.toBe(false);
    expect(sent).toHaveLength(1);
  });

  it('applies scrub patterns before send', async () => {
    const sent: SentRequest[] = [];
    mockFetchOk(sent);

    const client = track(
      new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test', scrub: ['hunter2'] }),
    );
    client.captureMessage('password hunter2 leaked');

    expect(await client.flush()).toBe(true);
    expect(sent[0]?.report.message).toBe('password [redacted] leaked');
  });
});

describe('bounds', () => {
  it('rings breadcrumbs at one hundred', async () => {
    const sent: SentRequest[] = [];
    mockFetchOk(sent);

    const client = track(new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test' }));

    for (let i = 0; i < 120; i += 1) {
      client.addBreadcrumb({ category: 'spam', message: `crumb-${i}` });
    }

    client.captureMessage('capped');
    await client.flush();

    expect(sent[0]?.report.breadcrumbs).toHaveLength(100);
    expect(sent[0]?.report.breadcrumbs[0]?.message).toBe('crumb-20');
  });

  it('drops oldest reports past the queue cap', async () => {
    const sent: SentRequest[] = [];
    mockFetchOk(sent);

    const client = track(
      new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test', maxQueue: 2 }),
    );
    client.captureMessage('first');
    client.captureMessage('second');
    client.captureMessage('third');
    await client.flush();

    expect(sent.map((entry) => entry.report.message)).toEqual(['second', 'third']);
  });

  it('truncates oversized messages', async () => {
    const sent: SentRequest[] = [];
    mockFetchOk(sent);

    const client = track(new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test' }));
    client.captureMessage('x'.repeat(5000));
    await client.flush();

    expect(sent[0]?.report.message).toHaveLength(4096);
  });
});

describe('fail-open', () => {
  it('disables on invalid options without throwing', () => {
    const bad = track(new TouchgrassClient({ endpoint: '', key: '' }));

    expect(bad.enabled()).toBe(false);
    expect(() => bad.captureException(new Error('x'))).not.toThrow();
    expect(() => bad.captureMessage('x')).not.toThrow();
    expect(() => bad.addBreadcrumb({})).not.toThrow();
  });

  it('survives garbage inputs', () => {
    const client = track(
      new TouchgrassClient({
        endpoint: 'https://tg.internal',
        key: 'tg_test',
        scrub: [null, 42] as unknown as [],
        maxQueue: -3,
      }),
    );

    expect(() => client.captureException(Object.create(null))).not.toThrow();
    expect(() => client.captureException(Symbol('sym'))).not.toThrow();
    expect(() => client.captureMessage(null)).not.toThrow();
    expect(() => client.addBreadcrumb(null as unknown as { category: string })).not.toThrow();
    expect(() => client.addBreadcrumb(42 as unknown as { category: string })).not.toThrow();
  });

  it('capture returns fast while ingestion hangs', async () => {
    mockFetchHanging();

    const client = track(new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test' }));

    const start = Date.now();
    client.captureException(new Error('slow'));
    const elapsed = Date.now() - start;

    expect(elapsed).toBeLessThan(1000);
    // Bounded flush against the hanging server resolves instead of hanging.
    await expect(client.flush(150)).resolves.toBe(false);
  });

  it('flush and close never reject with ingestion down', async () => {
    mockFetchDown();

    const client = track(new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test' }));
    client.captureMessage('lost');

    await expect(client.flush(200)).resolves.toBe(false);

    const closing = track(new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test' }));
    closing.captureMessage('lost');

    await expect(closing.close()).resolves.toBe(false);
  });

  it('does not hold the event loop', () => {
    const client = track(new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test' }));

    const internals = client as unknown as { timer: { hasRef?: () => boolean } | null };

    expect(internals.timer?.hasRef?.()).toBe(false);
  });
});

describe('uncaught capture', () => {
  it('captures with a host listener and preserves the outcome', async () => {
    silenceConsole();
    const sent: SentRequest[] = [];
    mockFetchOk(sent);

    const hostErrors: unknown[] = [];
    const hostListener = (error: unknown): void => {
      hostErrors.push(error);
    };
    process.on('uncaughtException', hostListener);

    try {
      const client = track(new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test' }));
      const error = new Error('crash-boom');
      process.emit('uncaughtException', error);

      expect(hostErrors).toEqual([error]);
      expect(await client.flush()).toBe(true);
      expect(sent).toHaveLength(1);
      expect(sent[0]?.report.message).toBe('crash-boom');
    } finally {
      process.removeListener('uncaughtException', hostListener);
    }
  });

  it('exits 1 when it is the only uncaught listener', async () => {
    silenceConsole();
    const sent: SentRequest[] = [];
    mockFetchOk(sent);

    const exit = vi.spyOn(process, 'exit').mockImplementation((() => undefined) as never);

    const client = track(new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test' }));

    // The runner holds its own uncaught listeners; park them so the SDK
    // sees itself as the sole owner, then restore.
    const internals = client as unknown as { handleUncaught: (error: unknown) => void };
    const others = process
      .listeners('uncaughtException')
      .filter((listener) => listener !== internals.handleUncaught);

    for (const listener of others) {
      process.removeListener('uncaughtException', listener as (...args: unknown[]) => void);
    }

    try {
      process.emit('uncaughtException', new Error('solo-crash'));

      await vi.waitFor(() => expect(exit).toHaveBeenCalledWith(1));
      expect(sent).toHaveLength(1);
    } finally {
      for (const listener of others) {
        process.on('uncaughtException', listener as (...args: unknown[]) => void);
      }
    }
  });

  it('captures unhandled rejections alongside a host listener', async () => {
    silenceConsole();
    const sent: SentRequest[] = [];
    mockFetchOk(sent);

    const hostReasons: unknown[] = [];
    const hostListener = (reason: unknown): void => {
      hostReasons.push(reason);
    };
    process.on('unhandledRejection', hostListener);

    try {
      const client = track(new TouchgrassClient({ endpoint: 'https://tg.internal', key: 'tg_test' }));
      const reason = new Error('rejected-promise');
      process.emit('unhandledRejection', reason, Promise.resolve());

      expect(hostReasons).toEqual([reason]);
      expect(await client.flush()).toBe(true);
      expect(sent).toHaveLength(1);
    } finally {
      process.removeListener('unhandledRejection', hostListener);
    }
  });
});
