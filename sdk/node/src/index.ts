// @touchgrass/node — fail-open error SDK for touchgrass.
//
//   import { init, captureException, addBreadcrumb } from '@touchgrass/node';
//
//   init({ endpoint: 'https://tg.internal', key: 'tg_...', release: 'v1.2.3' });
//   addBreadcrumb({ category: 'nav', message: 'checkout opened' });
//   try {
//     await checkout();
//   } catch (error) {
//     captureException(error);
//   }
//
// Every export is fail-open: invalid options yield a disabled client,
// delivery failures resolve false, and nothing here throws into host
// code, performs sync IO, or holds the event loop.

import { TouchgrassClient } from './client.js';
import type { BreadcrumbInput, InitOptions, Logger, LogLevel } from './types.js';

export { TouchgrassClient } from './client.js';
export { levelToSeverityNumber } from './logger.js';
export type {
  BeforeSendLog,
  Breadcrumb,
  BreadcrumbInput,
  ErrorReport,
  InitOptions,
  LogAttribute,
  LogAttributes,
  LogBatchRequest,
  LogItem,
  Logger,
  LogLevel,
  StackFrame,
} from './types.js';

let defaultClient: TouchgrassClient | null = null;

/**
 * init builds the default client, replacing any previous one.
 * Never throws: invalid options yield a disabled client.
 */
export function init(options: InitOptions): TouchgrassClient {
  try {
    if (defaultClient !== null) {
      void defaultClient.close();
    }
  } catch {
    // Swallow: replacing a broken client must still succeed.
  }

  let client: TouchgrassClient;

  try {
    client = new TouchgrassClient(options ?? ({} as InitOptions));
  } catch {
    client = new TouchgrassClient({ endpoint: '', key: '' });
  }

  defaultClient = client;

  return client;
}

/** captureException queues an uncaught-style error report. Never throws. */
export function captureException(error: unknown): void {
  try {
    defaultClient?.captureException(error);
  } catch {
    // Fail open.
  }
}

/** captureMessage queues a message report. Never throws. */
export function captureMessage(message: unknown): void {
  try {
    defaultClient?.captureMessage(message);
  } catch {
    // Fail open.
  }
}

/** addBreadcrumb appends to the report trail. Never throws. */
export function addBreadcrumb(input: BreadcrumbInput): void {
  try {
    defaultClient?.addBreadcrumb(input);
  } catch {
    // Fail open.
  }
}

/**
 * captureLog queues one structured log at any level. Never throws.
 * Prefer the logger.* shortcuts; this generic form suits multi-client
 * hosts and dynamic levels.
 */
export function captureLog(level: LogLevel, message: unknown, ...args: unknown[]): void {
  try {
    defaultClient?.captureLog(level, message, ...args);
  } catch {
    // Fail open.
  }
}

function logAt(level: LogLevel, message: unknown, args: unknown[]): void {
  try {
    defaultClient?.captureLog(level, message, ...args);
  } catch {
    // Fail open.
  }
}

/**
 * logger ships structured logs with printf formatting, typed
 * attributes, and OTel trace auto-capture. Every method is fail-open:
 * a trailing plain object becomes attributes, Errors capture type and
 * stack, and nothing here throws into host code.
 */
export const logger: Logger = {
  trace(message: unknown, ...args: unknown[]): void {
    logAt('trace', message, args);
  },
  debug(message: unknown, ...args: unknown[]): void {
    logAt('debug', message, args);
  },
  info(message: unknown, ...args: unknown[]): void {
    logAt('info', message, args);
  },
  warn(message: unknown, ...args: unknown[]): void {
    logAt('warn', message, args);
  },
  error(message: unknown, ...args: unknown[]): void {
    logAt('error', message, args);
  },
  fatal(message: unknown, ...args: unknown[]): void {
    logAt('fatal', message, args);
  },
};

/**
 * flush delivers queued reports within timeoutMs. Resolves true when
 * the queue drains, false on any trouble. Never rejects.
 */
export function flush(timeoutMs?: number): Promise<boolean> {
  try {
    if (defaultClient === null) {
      return Promise.resolve(true);
    }

    return defaultClient.flush(timeoutMs);
  } catch {
    return Promise.resolve(false);
  }
}

/**
 * close stops the default client and flushes best-effort.
 * Never rejects.
 */
export function close(): Promise<boolean> {
  try {
    if (defaultClient === null) {
      return Promise.resolve(true);
    }

    const client = defaultClient;
    defaultClient = null;

    return client.close();
  } catch {
    defaultClient = null;

    return Promise.resolve(false);
  }
}
