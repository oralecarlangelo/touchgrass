import { createRequire } from 'node:module';
import { format } from 'node:util';
import type { BeforeSendLog, LogAttributes, LogAttribute, LogItem, LogLevel } from './types.js';

/** Lowest OTel severity number per level (Sentry Log protocol v2.2). */
const severityNumbers: Record<LogLevel, number> = {
  trace: 1,
  debug: 5,
  info: 9,
  warn: 13,
  error: 17,
  fatal: 21,
};

const levels: readonly LogLevel[] = ['trace', 'debug', 'info', 'warn', 'error', 'fatal'];

const maxBodyLen = 8192;
const maxAttrStringLen = 4096;
const maxAttrKeyLen = 128;
const maxAttributes = 64;
const maxBatchItems = 1000;

const traceIdPattern = /^[0-9a-f]{32}$/;
const spanIdPattern = /^[0-9a-f]{16}$/;
const zeroTraceId = '00000000000000000000000000000000';
const zeroSpanId = '0000000000000000';

/** levelToSeverityNumber maps a level to its OTel severity number. */
export function levelToSeverityNumber(level: LogLevel): number {
  return severityNumbers[level] ?? 9;
}

/** isLogLevel reports whether value is a valid log level. */
export function isLogLevel(value: unknown): value is LogLevel {
  return typeof value === 'string' && (levels as readonly string[]).includes(value);
}

/** maxLogBatchSize is the wire cap on items per ingest request. */
export function maxLogBatchSize(): number {
  return maxBatchItems;
}

function capText(value: string, max: number): string {
  return value.length <= max ? value : value.slice(0, max);
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (typeof value !== 'object' || value === null) {
    return false;
  }

  const proto: unknown = Object.getPrototypeOf(value);

  return proto === Object.prototype || proto === null;
}

function stringifyUnknown(value: unknown): string {
  try {
    const json = JSON.stringify(value);

    if (typeof json === 'string') {
      return json;
    }
  } catch {
    // Fall through to String().
  }

  try {
    return String(value);
  } catch {
    return '[unprintable]';
  }
}

/**
 * toTypedAttribute coerces any value to a wire attribute. Returns null
 * for null/undefined (callers skip those); everything else becomes a
 * JSON-safe typed value. Never throws.
 */
export function toTypedAttribute(value: unknown): LogAttribute | null {
  try {
    if (value === null || value === undefined) {
      return null;
    }

    if (typeof value === 'boolean') {
      return { value, type: 'boolean' };
    }

    if (typeof value === 'number') {
      if (!Number.isFinite(value)) {
        return { value: String(value), type: 'string' };
      }

      return Number.isInteger(value)
        ? { value, type: 'integer' }
        : { value, type: 'double' };
    }

    if (typeof value === 'string') {
      return { value: capText(value, maxAttrStringLen), type: 'string' };
    }

    if (value instanceof Error) {
      const text = value.message === '' ? value.name : `${value.name}: ${value.message}`;

      return { value: capText(text, maxAttrStringLen), type: 'string' };
    }

    if (typeof value === 'bigint') {
      return { value: capText(value.toString(), maxAttrStringLen), type: 'string' };
    }

    return { value: capText(stringifyUnknown(value), maxAttrStringLen), type: 'string' };
  } catch {
    return { value: '[unprintable]', type: 'string' };
  }
}

/** toTypedParam coerces a template parameter, keeping positionals. */
function toTypedParam(value: unknown): LogAttribute {
  if (value === null) {
    return { value: 'null', type: 'string' };
  }

  if (value === undefined) {
    return { value: 'undefined', type: 'string' };
  }

  return toTypedAttribute(value) ?? { value: 'null', type: 'string' };
}

function safeFormat(template: unknown, args: unknown[]): string {
  try {
    return format(template, ...args);
  } catch {
    try {
      return typeof template === 'string' ? template : stringifyUnknown(template);
    } catch {
      return '[unprintable]';
    }
  }
}

interface ParsedLogCall {
  body: string;
  template: string | null;
  params: unknown[];
  error: Error | null;
  attrs: Record<string, unknown>;
}

/**
 * parseLogCall splits (message, ...args) into body parts. A trailing
 * plain object in args is attributes; remaining args feed util.format;
 * an Error message captures type/stack separately. Never throws.
 */
function parseLogCall(message: unknown, args: unknown[]): ParsedLogCall {
  const parsed: ParsedLogCall = { body: '', template: null, params: [], error: null, attrs: {} };

  try {
    const rest = Array.isArray(args) ? [...args] : [];

    if (rest.length > 0 && isPlainObject(rest[rest.length - 1])) {
      parsed.attrs = rest.pop() as Record<string, unknown>;
    }

    if (message instanceof Error) {
      parsed.error = message;
      const head = message.message === '' ? message.name : `${message.name}: ${message.message}`;
      parsed.body = rest.length > 0 ? head + safeFormat('', rest) : head;

      return parsed;
    }

    if (typeof message === 'string') {
      parsed.template = message;
      parsed.params = rest;
      parsed.body = rest.length > 0 ? safeFormat(message, rest) : message;

      return parsed;
    }

    parsed.params = rest;
    parsed.body = safeFormat(message, rest);

    return parsed;
  } catch {
    parsed.body = '[unprintable]';

    return parsed;
  }
}

// Structural OTel API shape: unknown-safe, no OTel imports (zero-dep core).
interface OTelSpanContext {
  traceId: string;
  spanId: string;
}

interface OTelApiShape {
  trace: {
    getSpan: (context: unknown) => { spanContext: () => OTelSpanContext } | undefined | null;
  };
  context: {
    active: () => unknown;
  };
}

function asOtelApi(value: unknown): OTelApiShape | null {
  try {
    if (typeof value !== 'object' || value === null) {
      return null;
    }

    const api = value as Record<string, Record<string, unknown>>;

    if (
      typeof api['trace']?.['getSpan'] === 'function' &&
      typeof api['context']?.['active'] === 'function'
    ) {
      return value as OTelApiShape;
    }

    return null;
  } catch {
    return null;
  }
}

type OTelLoader = () => unknown;

let cachedApi: OTelApiShape | null | undefined;
let loaderOverride: OTelLoader | null = null;

/** __setOtelLoaderForTests overrides module loading (null restores). */
export function __setOtelLoaderForTests(loader: OTelLoader | null): void {
  loaderOverride = loader;
  cachedApi = undefined;
}

function directRequire(id: string): unknown {
  try {
    if (typeof require === 'function') {
      return (require as unknown as (mid: string) => unknown)(id);
    }
  } catch {
    // CJS require missing or module absent; fall through.
  }

  return null;
}

function defaultLoadOtelApi(): unknown {
  const direct = directRequire('@opentelemetry/api');

  if (direct !== null) {
    return direct;
  }

  try {
    return createRequire(`${process.cwd()}/package.json`)('@opentelemetry/api');
  } catch {
    // Fall through to the entry-point probe.
  }

  try {
    const entry = process.argv[1];

    if (typeof entry === 'string' && entry !== '') {
      return createRequire(entry)('@opentelemetry/api');
    }
  } catch {
    // Module absent; the caller treats this as no trace context.
  }

  return null;
}

function loadOtelApi(): OTelApiShape | null {
  if (cachedApi !== undefined) {
    return cachedApi;
  }

  try {
    const loader = loaderOverride ?? defaultLoadOtelApi;
    cachedApi = asOtelApi(loader());
  } catch {
    cachedApi = null;
  }

  return cachedApi;
}

function validTraceId(value: unknown): string | null {
  if (typeof value !== 'string' || value === zeroTraceId || !traceIdPattern.test(value)) {
    return null;
  }

  return value;
}

function validSpanId(value: unknown): string | null {
  if (typeof value !== 'string' || value === zeroSpanId || !spanIdPattern.test(value)) {
    return null;
  }

  return value;
}

/**
 * captureTraceContext reads the OTel active span, if any. Guarded end
 * to end: a missing @opentelemetry/api, no active span, or any error
 * yields no context. Never throws.
 */
export function captureTraceContext(): { trace_id?: string; span_id?: string } {
  try {
    const api = loadOtelApi();

    if (api === null) {
      return {};
    }

    const span = api.trace.getSpan(api.context.active());

    if (span === undefined || span === null) {
      return {};
    }

    const context = span.spanContext();
    const traceId = validTraceId(context?.traceId);
    const spanId = validSpanId(context?.spanId);

    if (traceId === null || spanId === null) {
      return {};
    }

    return { trace_id: traceId, span_id: spanId };
  } catch {
    return {};
  }
}

function insertAttribute(
  attributes: LogAttributes,
  order: string[],
  key: string,
  value: LogAttribute | null,
): void {
  try {
    if (value === null || key === '' || order.length >= maxAttributes) {
      return;
    }

    const capped = capText(key, maxAttrKeyLen);

    if (capped === '' || Object.hasOwn(attributes, capped)) {
      return;
    }

    attributes[capped] = value;
    order.push(capped);
  } catch {
    // Fail open: skip the attribute.
  }
}

/**
 * buildLogItem renders (message, ...args) into a wire-ready item:
 * printf body, template/parameter attrs, Error type/stack attrs, typed
 * user attrs, severity number, timestamp, and OTel trace context.
 * Never throws; never returns an invalid item.
 */
export function buildLogItem(level: LogLevel, message: unknown, args: unknown[]): LogItem {
  const safeLevel = isLogLevel(level) ? level : 'info';

  try {
    const parsed = parseLogCall(message, args);
    const attributes: LogAttributes = {};
    const order: string[] = [];

    // SDK-generated attrs first so grouping/template data survives the
    // 64-key cap; user attrs fill the remainder.
    if (parsed.error !== null) {
      insertAttribute(attributes, order, 'error.type', toTypedAttribute(parsed.error.name));
      insertAttribute(attributes, order, 'error.value', toTypedAttribute(parsed.error.message));

      if (typeof parsed.error.stack === 'string' && parsed.error.stack !== '') {
        insertAttribute(attributes, order, 'error.stacktrace', toTypedAttribute(parsed.error.stack));
      }
    }

    if (parsed.template !== null && parsed.params.length > 0) {
      insertAttribute(attributes, order, 'sentry.message.template', toTypedAttribute(parsed.template));

      for (let index = 0; index < parsed.params.length; index += 1) {
        insertAttribute(
          attributes,
          order,
          `sentry.message.parameter.${index}`,
          toTypedParam(parsed.params[index]),
        );
      }
    }

    try {
      for (const [key, value] of Object.entries(parsed.attrs)) {
        insertAttribute(attributes, order, key, toTypedAttribute(value));
      }
    } catch {
      // Fail open: keep the attrs collected so far.
    }

    const body = capText(parsed.body, maxBodyLen);
    const item: LogItem = {
      timestamp: Date.now() / 1000,
      level: safeLevel,
      body: body === '' ? '(empty)' : body,
      severity_number: levelToSeverityNumber(safeLevel),
    };

    const trace = captureTraceContext();

    if (trace.trace_id !== undefined && trace.span_id !== undefined) {
      item.trace_id = trace.trace_id;
      item.span_id = trace.span_id;
    }

    if (order.length > 0) {
      item.attributes = attributes;
    }

    return item;
  } catch {
    return {
      timestamp: Date.now() / 1000,
      level: safeLevel,
      body: '[unprintable]',
      severity_number: levelToSeverityNumber(safeLevel),
    };
  }
}

/**
 * applyBeforeSendLog runs the hook over an item. Null drops (returns
 * null); void/undefined keeps the (possibly mutated) item; a returned
 * object with a valid level and string body replaces it; anything else
 * — throw, non-object, invalid shape — keeps the original. Never throws.
 */
export function applyBeforeSendLog(
  hook: BeforeSendLog | undefined,
  item: LogItem,
): LogItem | null {
  try {
    if (hook === undefined || hook === null) {
      return item;
    }

    if (typeof hook !== 'function') {
      return item;
    }

    let result: LogItem | null | undefined | void;

    try {
      result = hook(item);
    } catch {
      return item;
    }

    if (result === null) {
      return null;
    }

    if (result === undefined) {
      return item;
    }

    if (typeof result !== 'object' || result === null) {
      return item;
    }

    if (!isLogLevel(result.level) || typeof result.body !== 'string') {
      return item;
    }

    return result;
  } catch {
    return item;
  }
}

/**
 * sanitizeLogItem re-validates an item after beforeSendLog so one hook
 * cannot poison a whole batch (the server rejects batches with any
 * invalid item). Never throws; never returns an invalid item.
 */
export function sanitizeLogItem(item: LogItem, fallbackLevel: LogLevel): LogItem {
  const level = isLogLevel(item?.level) ? item.level : fallbackLevel;

  try {
    const rawBody = typeof item?.body === 'string' ? item.body : stringifyUnknown(item?.body);
    const body = capText(rawBody, maxBodyLen);

    const rawTimestamp = (item as LogItem)?.timestamp;
    const timestamp =
      typeof rawTimestamp === 'number' && Number.isFinite(rawTimestamp) && rawTimestamp > 0
        ? rawTimestamp
        : Date.now() / 1000;

    const rawSeverity = (item as LogItem)?.severity_number;
    const severityNumber =
      typeof rawSeverity === 'number' &&
      Number.isInteger(rawSeverity) &&
      rawSeverity >= 1 &&
      rawSeverity <= 24
        ? rawSeverity
        : levelToSeverityNumber(level);

    const clean: LogItem = {
      timestamp,
      level,
      body: body === '' ? '(empty)' : body,
      severity_number: severityNumber,
    };

    const traceId = validTraceId((item as LogItem)?.trace_id);
    const spanId = validSpanId((item as LogItem)?.span_id);

    if (traceId !== null && spanId !== null) {
      clean.trace_id = traceId;
      clean.span_id = spanId;
    }

    const rawAttrs = (item as LogItem)?.attributes;

    if (typeof rawAttrs === 'object' && rawAttrs !== null) {
      const attributes: LogAttributes = {};
      const order: string[] = [];

      try {
        for (const [key, entry] of Object.entries(rawAttrs)) {
          if (key === '') {
            continue;
          }

          if (typeof entry === 'object' && entry !== null && 'value' in entry && 'type' in entry) {
            const typed = entry as { value: unknown; type: unknown };

            if (
              (typeof typed.value === 'string' ||
                typeof typed.value === 'number' ||
                typeof typed.value === 'boolean') &&
              (typed.type === 'string' ||
                typed.type === 'integer' ||
                typed.type === 'double' ||
                typed.type === 'boolean')
            ) {
              const value =
                typed.type === 'string' && typeof typed.value === 'string'
                  ? capText(typed.value, maxAttrStringLen)
                  : typed.value;

              insertAttribute(attributes, order, key, {
                value: value as string | number | boolean,
                type: typed.type,
              });
              continue;
            }
          }

          insertAttribute(attributes, order, key, toTypedAttribute(entry));
        }
      } catch {
        // Fail open: keep the attrs collected so far.
      }

      if (order.length > 0) {
        clean.attributes = attributes;
      }
    }

    return clean;
  } catch {
    return {
      timestamp: Date.now() / 1000,
      level,
      body: '[unprintable]',
      severity_number: levelToSeverityNumber(level),
    };
  }
}
