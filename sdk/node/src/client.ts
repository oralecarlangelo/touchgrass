import {
  applyBeforeSendLog,
  buildLogItem,
  isLogLevel,
  maxLogBatchSize,
  sanitizeLogItem,
} from './logger.js';
import { scrubLogItem, scrubReport, compileScrub } from './scrub.js';
import { parseStack } from './stacktrace.js';
import { postLogs, postReport } from './transport.js';
import type {
  BeforeSendLog,
  Breadcrumb,
  BreadcrumbInput,
  ErrorReport,
  InitOptions,
  LogItem,
  LogLevel,
  StackFrame,
} from './types.js';

const maxMessageLen = 4096;
const maxFieldLen = 1024;
const maxReleaseLen = 128;
const maxBreadcrumbs = 100;
const defaultMaxQueue = 100;
const flushIntervalMs = 1000;
const requestTimeoutMs = 5000;
const crashFlushMs = 2000;

function capText(value: string, max: number): string {
  if (value.length <= max) {
    return value;
  }

  return value.slice(0, max);
}

function safeMessage(error: unknown): string {
  try {
    if (error instanceof Error) {
      return error.message === '' ? error.name : error.message;
    }

    if (typeof error === 'string') {
      return error;
    }

    return JSON.stringify(error) ?? 'unknown error';
  } catch {
    return 'unknown error';
  }
}

function safeStack(error: unknown): StackFrame[] {
  try {
    if (error instanceof Error) {
      return parseStack(error.stack);
    }

    return [];
  } catch {
    return [];
  }
}

function resolveRelease(options: InitOptions): string {
  try {
    const release = options.release ?? process.env['TOUCHGRASS_RELEASE'] ?? '';

    return capText(release, maxReleaseLen);
  } catch {
    return '';
  }
}

function resolveBase(options: InitOptions): string | null {
  try {
    if (typeof options.endpoint !== 'string' || options.endpoint.trim() === '') {
      return null;
    }

    return options.endpoint.replace(/\/+$/, '');
  } catch {
    return null;
  }
}

function resolveEndpoint(options: InitOptions): string | null {
  const base = resolveBase(options);

  return base === null ? null : `${base}/api/ingest`;
}

function resolveLogsEndpoint(options: InitOptions): string | null {
  const base = resolveBase(options);

  return base === null ? null : `${base}/api/ingest/logs`;
}

function resolveKey(options: InitOptions): string | null {
  try {
    if (typeof options.key !== 'string' || options.key === '') {
      return null;
    }

    return options.key;
  } catch {
    return null;
  }
}

/**
 * TouchgrassClient captures errors and delivers them async. Every public
 * method is fail-open: SDK trouble never throws into host code, performs
 * no sync IO, and never holds the event loop (timers are unref'd).
 */
export class TouchgrassClient {
  private readonly endpoint: string | null;
  private readonly logsEndpoint: string | null;
  private readonly key: string | null;
  private readonly release: string;
  private readonly scrubPatterns: RegExp[];
  private readonly beforeSendLog: BeforeSendLog | undefined;
  private readonly maxQueue: number;
  private readonly queue: ErrorReport[] = [];
  private readonly logQueue: LogItem[] = [];
  private readonly breadcrumbs: Breadcrumb[] = [];
  private timer: NodeJS.Timeout | null = null;
  private handlersInstalled = false;
  private closed = false;

  constructor(options: InitOptions) {
    let endpoint: string | null = null;
    let logsEndpoint: string | null = null;
    let key: string | null = null;
    let release = '';
    let patterns: RegExp[] = [];
    let beforeSendLog: BeforeSendLog | undefined;
    let maxQueue = defaultMaxQueue;

    try {
      endpoint = resolveEndpoint(options);
      logsEndpoint = resolveLogsEndpoint(options);
      key = resolveKey(options);
      release = resolveRelease(options);
      patterns = compileScrub(options.scrub);

      if (typeof options.beforeSendLog === 'function') {
        beforeSendLog = options.beforeSendLog;
      }

      if (
        typeof options.maxQueue === 'number' &&
        Number.isInteger(options.maxQueue) &&
        options.maxQueue > 0
      ) {
        maxQueue = options.maxQueue;
      }
    } catch {
      endpoint = null;
      logsEndpoint = null;
      key = null;
    }

    this.endpoint = endpoint;
    this.logsEndpoint = logsEndpoint;
    this.key = key;
    this.release = release;
    this.scrubPatterns = patterns;
    this.beforeSendLog = beforeSendLog;
    this.maxQueue = maxQueue;

    if (this.enabled()) {
      this.startTimer();

      if (options.captureUncaught !== false) {
        this.installHandlers();
      }
    }
  }

  /** enabled reports whether the client can deliver (valid options). */
  enabled(): boolean {
    return this.endpoint !== null && this.key !== null && !this.closed;
  }

  captureException(error: unknown): void {
    try {
      if (!this.enabled()) {
        return;
      }

      this.enqueue({
        type: 'exception',
        message: capText(safeMessage(error), maxMessageLen),
        stack: safeStack(error),
        breadcrumbs: [...this.breadcrumbs],
        release: this.release,
      });
    } catch {
      // Fail open: capture must never throw into host code.
    }
  }

  captureMessage(message: unknown): void {
    try {
      if (!this.enabled()) {
        return;
      }

      const text = typeof message === 'string' ? message : safeMessage(message);

      this.enqueue({
        type: 'message',
        message: capText(text, maxMessageLen),
        stack: [],
        breadcrumbs: [...this.breadcrumbs],
        release: this.release,
      });
    } catch {
      // Fail open: capture must never throw into host code.
    }
  }

  addBreadcrumb(input: BreadcrumbInput): void {
    try {
      if (!this.enabled() || input === null || typeof input !== 'object') {
        return;
      }

      this.breadcrumbs.push({
        at: new Date().toISOString(),
        category: capText(input.category ?? '', maxFieldLen),
        message: capText(input.message ?? '', maxFieldLen),
      });

      while (this.breadcrumbs.length > maxBreadcrumbs) {
        this.breadcrumbs.shift();
      }
    } catch {
      // Fail open: breadcrumb trouble stays inside the SDK.
    }
  }

  /**
   * captureLog queues one structured log for batched delivery to
   * POST /api/ingest/logs. Pipeline: printf-format, beforeSendLog,
   * sanitize, scrub, enqueue. Invalid levels no-op. Never throws.
   */
  captureLog(level: LogLevel, message: unknown, ...args: unknown[]): void {
    try {
      if (!this.enabled() || !isLogLevel(level)) {
        return;
      }

      const built = buildLogItem(level, message, args);
      const kept = applyBeforeSendLog(this.beforeSendLog, built);

      if (kept === null) {
        return;
      }

      this.enqueueLog(sanitizeLogItem(kept, level));
    } catch {
      // Fail open: logging must never throw into host code.
    }
  }

  /**
   * flush delivers queued reports and log batches within timeoutMs,
   * resolving true when every queue drains. Best-effort: failures
   * drop rather than wedge the queues. Never rejects.
   */
  async flush(timeoutMs = requestTimeoutMs): Promise<boolean> {
    try {
      if (!this.enabled() || this.endpoint === null || this.key === null) {
        return true;
      }

      const budget = Number.isFinite(timeoutMs) && timeoutMs > 0 ? timeoutMs : requestTimeoutMs;
      const deadline = Date.now() + budget;
      let delivered = true;

      while (this.queue.length > 0 && Date.now() < deadline) {
        const report = this.queue.shift();

        if (report === undefined) {
          break;
        }

        const remaining = Math.max(1, deadline - Date.now());
        const ok = await postReport(
          this.endpoint,
          this.key,
          report,
          Math.min(remaining, requestTimeoutMs),
        );

        delivered = delivered && ok;
      }

      if (this.logsEndpoint !== null) {
        while (this.logQueue.length > 0 && Date.now() < deadline) {
          const batch = this.logQueue.splice(0, maxLogBatchSize());

          if (batch.length === 0) {
            break;
          }

          const remaining = Math.max(1, deadline - Date.now());
          const ok = await postLogs(
            this.logsEndpoint,
            this.key,
            this.release,
            batch,
            Math.min(remaining, requestTimeoutMs),
          );

          delivered = delivered && ok;
        }
      }

      return delivered && this.queue.length === 0 && this.logQueue.length === 0;
    } catch {
      return false;
    }
  }

  /**
   * close stops timers and handlers, then flushes best-effort.
   * Never rejects.
   */
  async close(): Promise<boolean> {
    try {
      this.removeHandlers();

      if (this.timer !== null) {
        clearInterval(this.timer);
        this.timer = null;
      }

      const drained = await this.flush();
      this.closed = true;

      return drained;
    } catch {
      this.closed = true;

      return false;
    }
  }

  private enqueue(report: ErrorReport): void {
    const scrubbed = scrubReport(report, report.breadcrumbs, report.stack, this.scrubPatterns);

    this.queue.push({ ...report, ...scrubbed });

    while (this.queue.length > this.maxQueue) {
      this.queue.shift();
    }
  }

  private enqueueLog(item: LogItem): void {
    this.logQueue.push(scrubLogItem(item, this.scrubPatterns));

    while (this.logQueue.length > this.maxQueue) {
      this.logQueue.shift();
    }
  }

  private startTimer(): void {
    try {
      this.timer = setInterval(() => {
        void this.flush();
      }, flushIntervalMs);

      if (typeof this.timer.unref === 'function') {
        this.timer.unref();
      }
    } catch {
      this.timer = null;
    }
  }

  private installHandlers(): void {
    try {
      process.on('uncaughtException', this.handleUncaught);
      process.on('unhandledRejection', this.handleUnhandled);
      this.handlersInstalled = true;
    } catch {
      this.removeHandlers();
    }
  }

  private removeHandlers(): void {
    try {
      if (this.handlersInstalled) {
        process.removeListener('uncaughtException', this.handleUncaught);
        process.removeListener('unhandledRejection', this.handleUnhandled);
        this.handlersInstalled = false;
      }
    } catch {
      this.handlersInstalled = false;
    }
  }

  // Arrow fields so removeListener matches the installed reference.
  private readonly handleUncaught = (error: unknown): void => {
    try {
      this.captureException(error);
    } catch {
      // Swallow: the process is already crashing.
    }

    // A host listener owns the outcome; without one, node would exit(1),
    // so flush bounded and preserve the exit.
    if (process.listenerCount('uncaughtException') > 1) {
      return;
    }

    try {
      console.error(error);
    } catch {
      // Stderr trouble must not change the exit path.
    }

    void this.flush(crashFlushMs).finally(() => {
      process.exit(1);
    });
  };

  private readonly handleUnhandled = (reason: unknown): void => {
    try {
      this.captureException(reason);
    } catch {
      // Swallow: the process is already crashing.
    }

    if (process.listenerCount('unhandledRejection') > 1) {
      return;
    }

    try {
      console.error(reason);
    } catch {
      // Stderr trouble must not change the exit path.
    }

    void this.flush(crashFlushMs).finally(() => {
      process.exit(1);
    });
  };
}
