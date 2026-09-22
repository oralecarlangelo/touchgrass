// Public SDK types. Error wire shapes match POST /api/ingest;
// log wire shapes match POST /api/ingest/logs.

export interface StackFrame {
  function: string;
  file: string;
  line: number;
  column: number;
}

export interface Breadcrumb {
  at: string;
  category: string;
  message: string;
}

export interface BreadcrumbInput {
  category?: string;
  message?: string;
}

export interface ErrorReport {
  type: 'exception' | 'message';
  message: string;
  stack: StackFrame[];
  breadcrumbs: Breadcrumb[];
  release: string;
}

export type ScrubPattern = RegExp | string;

/** Log severity level, matching the OTel severity-text vocabulary. */
export type LogLevel = 'trace' | 'debug' | 'info' | 'warn' | 'error' | 'fatal';

/** Typed attribute value on the log wire format. */
export interface LogAttribute {
  value: string | number | boolean;
  type: 'string' | 'integer' | 'double' | 'boolean';
}

/** Attribute map on the log wire format (at most 64 keys). */
export type LogAttributes = Record<string, LogAttribute>;

/**
 * One structured log on the POST /api/ingest/logs wire format.
 * timestamp is unix seconds (float); severity_number follows the OTel
 * ranges (trace 1, debug 5, info 9, warn 13, error 17, fatal 21).
 */
export interface LogItem {
  timestamp: number;
  level: LogLevel;
  body: string;
  severity_number?: number;
  trace_id?: string;
  span_id?: string;
  attributes?: LogAttributes;
}

/** Batched log ingest body: release plus 1..1000 items. */
export interface LogBatchRequest {
  release?: string;
  items: LogItem[];
}

/**
 * beforeSendLog receives each log before it is queued. Return the
 * (possibly modified) item to keep it, mutate in place and return
 * void to keep the mutation, or return null to drop the log.
 * Throwing or returning a non-object keeps the original item.
 */
export type BeforeSendLog = (item: LogItem) => LogItem | null | undefined | void;

/** Levelled logger: logger.info(msg, ...args), etc. Never throws. */
export interface Logger {
  trace(message: unknown, ...args: unknown[]): void;
  debug(message: unknown, ...args: unknown[]): void;
  info(message: unknown, ...args: unknown[]): void;
  warn(message: unknown, ...args: unknown[]): void;
  error(message: unknown, ...args: unknown[]): void;
  fatal(message: unknown, ...args: unknown[]): void;
}

export interface InitOptions {
  /** Base URL of the touchgrass server, e.g. https://tg.internal. */
  endpoint: string;
  /** Per-service ingestion key (tg_...). */
  key: string;
  /** Release tag attached to every report. Defaults to TOUCHGRASS_RELEASE. */
  release?: string;
  /** PII patterns redacted client-side before send. */
  scrub?: ScrubPattern[];
  /** Max queued reports; oldest drop past the cap. Defaults to 100. */
  maxQueue?: number;
  /** Install uncaughtException/unhandledRejection capture. Defaults to true. */
  captureUncaught?: boolean;
  /** Hook run on each structured log before queueing; return null to drop. */
  beforeSendLog?: BeforeSendLog;
}
