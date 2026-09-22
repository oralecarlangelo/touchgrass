// @touchgrass/node/otel — OpenTelemetry bridge for touchgrass logs.
//
//   import { LoggerProvider, BatchLogRecordProcessor } from '@opentelemetry/sdk-logs';
//   import { TouchgrassLogRecordExporter } from '@touchgrass/node/otel';
//
//   const provider = new LoggerProvider({
//     processors: [
//       new BatchLogRecordProcessor({
//         exporter: new TouchgrassLogRecordExporter({
//           endpoint: 'https://tg.internal',
//           key: 'tg_...',
//         }),
//       }),
//     ],
//   });
//
// Type-only OTel imports: this module loads and runs with zero OTel
// packages installed (it just never receives records then). Real OTel
// hosts install @opentelemetry/sdk-logs as usual.

import type { ExportResult, ExportResultCode } from '@opentelemetry/core';
import type { LogRecordExporter, ReadableLogRecord } from '@opentelemetry/sdk-logs';
import {
  applyBeforeSendLog,
  levelToSeverityNumber,
  maxLogBatchSize,
  sanitizeLogItem,
  toTypedAttribute,
} from './logger.js';
import { compileScrub, scrubLogItem } from './scrub.js';
import { postLogs } from './transport.js';
import type { BeforeSendLog, LogAttributes, LogItem, LogLevel, ScrubPattern } from './types.js';

// Numeric mirror of ExportResultCode (SUCCESS 0, FAILED 1) so the
// exporter needs no OTel runtime import. Verified against
// @opentelemetry/core's ExportResultCode enum.
const exportSuccess = 0 as ExportResultCode;
const exportFailed = 1 as ExportResultCode;

const maxReleaseLen = 128;
const defaultTimeoutMs = 5000;

/** Options for TouchgrassLogRecordExporter. */
export interface TouchgrassLogExporterOptions {
  /** Base URL of the touchgrass server, e.g. https://tg.internal. */
  endpoint: string;
  /** Per-service ingestion key (tg_...). */
  key: string;
  /** Release tag on every batch. Defaults to TOUCHGRASS_RELEASE. */
  release?: string;
  /** PII patterns redacted client-side before send. */
  scrub?: ScrubPattern[];
  /** Per-request abort timeout in ms. Defaults to 5000. */
  timeoutMs?: number;
  /** Hook run on each mapped log; return null to drop. */
  beforeSendLog?: BeforeSendLog;
}

function resolveBase(endpoint: unknown): string | null {
  try {
    if (typeof endpoint !== 'string' || endpoint.trim() === '') {
      return null;
    }

    return `${endpoint.replace(/\/+$/, '')}/api/ingest/logs`;
  } catch {
    return null;
  }
}

function resolveKey(key: unknown): string | null {
  try {
    if (typeof key !== 'string' || key === '') {
      return null;
    }

    return key;
  } catch {
    return null;
  }
}

function resolveRelease(release: unknown): string {
  try {
    const value = typeof release === 'string' ? release : process.env['TOUCHGRASS_RELEASE'] ?? '';

    return value.length <= maxReleaseLen ? value : value.slice(0, maxReleaseLen);
  } catch {
    return '';
  }
}

function severityNumberToLevel(value: unknown): LogLevel | null {
  if (typeof value !== 'number' || !Number.isInteger(value)) {
    return null;
  }

  if (value >= 1 && value <= 4) {
    return 'trace';
  }

  if (value >= 5 && value <= 8) {
    return 'debug';
  }

  if (value >= 9 && value <= 12) {
    return 'info';
  }

  if (value >= 13 && value <= 16) {
    return 'warn';
  }

  if (value >= 17 && value <= 20) {
    return 'error';
  }

  if (value >= 21 && value <= 24) {
    return 'fatal';
  }

  return null;
}

function severityTextToLevel(value: unknown): LogLevel | null {
  if (typeof value !== 'string') {
    return null;
  }

  switch (value.trim().toLowerCase()) {
    case 'trace':
      return 'trace';
    case 'debug':
      return 'debug';
    case 'info':
      return 'info';
    case 'warn':
    case 'warning':
      return 'warn';
    case 'error':
      return 'error';
    case 'fatal':
      return 'fatal';
    default:
      return null;
  }
}

function coerceBody(body: unknown): string {
  try {
    if (typeof body === 'string') {
      return body;
    }

    if (typeof body === 'number' || typeof body === 'boolean' || typeof body === 'bigint') {
      return String(body);
    }

    if (body === null || body === undefined) {
      return '';
    }

    const json = JSON.stringify(body);

    if (typeof json === 'string') {
      return json;
    }
  } catch {
    // Fall through to String().
  }

  try {
    return String(body);
  } catch {
    return '';
  }
}

function hrTimeToSeconds(value: unknown): number | null {
  try {
    if (!Array.isArray(value) || value.length < 2) {
      return null;
    }

    const [seconds, nanos] = value as [unknown, unknown];

    if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds <= 0) {
      return null;
    }

    const nanosPart = typeof nanos === 'number' && Number.isFinite(nanos) ? nanos / 1e9 : 0;

    return seconds + nanosPart;
  } catch {
    return null;
  }
}

/**
 * mapReadableLogRecord converts one OTel record to a touchgrass item:
 * severity number ranges win, then severity text, else info; hrTime to
 * unix seconds; span context to trace/span; OTel attributes to typed
 * attributes. Never throws; never returns an invalid item.
 */
export function mapReadableLogRecord(record: ReadableLogRecord): LogItem | null {
  try {
    if (record === null || typeof record !== 'object') {
      return null;
    }

    const level =
      severityNumberToLevel(record.severityNumber) ??
      severityTextToLevel(record.severityText) ??
      'info';

    const incomingSeverity =
      typeof record.severityNumber === 'number' &&
      Number.isInteger(record.severityNumber) &&
      record.severityNumber >= 1 &&
      record.severityNumber <= 24
        ? record.severityNumber
        : levelToSeverityNumber(level);

    const attributes: LogAttributes = {};

    try {
      const raw = record.attributes;

      if (typeof raw === 'object' && raw !== null) {
        for (const [key, value] of Object.entries(raw)) {
          if (key === '' || Object.keys(attributes).length >= 64) {
            continue;
          }

          const typed = toTypedAttribute(value);

          if (typed !== null && !Object.hasOwn(attributes, key)) {
            attributes[key] = typed;
          }
        }
      }
    } catch {
      // Fail open: keep the attributes collected so far.
    }

    const item: LogItem = {
      timestamp: hrTimeToSeconds(record.hrTime) ?? Date.now() / 1000,
      level,
      body: coerceBody(record.body),
      severity_number: incomingSeverity,
    };

    const spanContext = record.spanContext;

    if (typeof spanContext?.traceId === 'string' && typeof spanContext?.spanId === 'string') {
      item.trace_id = spanContext.traceId;
      item.span_id = spanContext.spanId;
    }

    if (Object.keys(attributes).length > 0) {
      item.attributes = attributes;
    }

    return sanitizeLogItem(item, level);
  } catch {
    return null;
  }
}

/**
 * TouchgrassLogRecordExporter ships OTel log records to
 * POST /api/ingest/logs in batches. Fail-open: mapping and delivery
 * trouble report FAILED to the processor without throwing, and invalid
 * options quietly drop (SUCCESS) like a disabled client.
 */
export class TouchgrassLogRecordExporter implements LogRecordExporter {
  private readonly endpoint: string | null;
  private readonly key: string | null;
  private readonly release: string;
  private readonly scrubPatterns: RegExp[];
  private readonly beforeSendLog: BeforeSendLog | undefined;
  private readonly timeoutMs: number;
  private shutDown = false;

  constructor(options: TouchgrassLogExporterOptions) {
    let endpoint: string | null = null;
    let key: string | null = null;
    let release = '';
    let patterns: RegExp[] = [];
    let beforeSendLog: BeforeSendLog | undefined;
    let timeoutMs = defaultTimeoutMs;

    try {
      const opts = (options ?? {}) as TouchgrassLogExporterOptions;
      endpoint = resolveBase(opts.endpoint);
      key = resolveKey(opts.key);
      release = resolveRelease(opts.release);
      patterns = compileScrub(opts.scrub);

      if (typeof opts.beforeSendLog === 'function') {
        beforeSendLog = opts.beforeSendLog;
      }

      if (typeof opts.timeoutMs === 'number' && Number.isFinite(opts.timeoutMs) && opts.timeoutMs > 0) {
        timeoutMs = opts.timeoutMs;
      }
    } catch {
      endpoint = null;
      key = null;
    }

    this.endpoint = endpoint;
    this.key = key;
    this.release = release;
    this.scrubPatterns = patterns;
    this.beforeSendLog = beforeSendLog;
    this.timeoutMs = timeoutMs;
  }

  export(logs: ReadableLogRecord[], resultCallback: (result: ExportResult) => void): void {
    const done = (code: ExportResultCode): void => {
      try {
        resultCallback({ code });
      } catch {
        // Fail open: a throwing callback must not break the processor.
      }
    };

    try {
      if (this.shutDown) {
        done(exportFailed);

        return;
      }

      if (this.endpoint === null || this.key === null) {
        done(exportSuccess);

        return;
      }

      if (!Array.isArray(logs) || logs.length === 0) {
        done(exportSuccess);

        return;
      }

      const items: LogItem[] = [];

      for (const record of logs) {
        const mapped = mapReadableLogRecord(record);

        if (mapped === null) {
          continue;
        }

        const kept = applyBeforeSendLog(this.beforeSendLog, mapped);

        if (kept === null) {
          continue;
        }

        items.push(scrubLogItem(sanitizeLogItem(kept, mapped.level), this.scrubPatterns));
      }

      if (items.length === 0) {
        done(exportSuccess);

        return;
      }

      void this.sendBatches(items).then(done, () => done(exportFailed));
    } catch {
      done(exportFailed);
    }
  }

  async shutdown(): Promise<void> {
    try {
      this.shutDown = true;
    } catch {
      this.shutDown = true;
    }
  }

  forceFlush(): Promise<void> {
    // No internal buffer: every export() sends immediately.
    return Promise.resolve();
  }

  private async sendBatches(items: LogItem[]): Promise<ExportResultCode> {
    try {
      if (this.endpoint === null || this.key === null) {
        return exportSuccess;
      }

      let ok = true;

      for (let offset = 0; offset < items.length; offset += maxLogBatchSize()) {
        const batch = items.slice(offset, offset + maxLogBatchSize());

        if (batch.length === 0) {
          continue;
        }

        const sent = await postLogs(this.endpoint, this.key, this.release, batch, this.timeoutMs);
        ok = ok && sent;
      }

      return ok ? exportSuccess : exportFailed;
    } catch {
      return exportFailed;
    }
  }
}
