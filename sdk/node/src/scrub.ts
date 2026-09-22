import type { Breadcrumb, LogAttributes, LogItem, ScrubPattern, StackFrame } from './types.js';

const redacted = '[redacted]';

function toGlobal(pattern: ScrubPattern): RegExp | null {
  try {
    if (typeof pattern === 'string') {
      if (pattern === '') {
        return null;
      }

      return new RegExp(pattern.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'g');
    }

    const flags = pattern.flags.includes('g') ? pattern.flags : `${pattern.flags}g`;

    return new RegExp(pattern.source, flags);
  } catch {
    return null;
  }
}

/**
 * compileScrub pre-compiles scrub patterns, dropping invalid ones.
 * Never throws.
 */
export function compileScrub(patterns: ScrubPattern[] | undefined): RegExp[] {
  try {
    if (!Array.isArray(patterns)) {
      return [];
    }

    const compiled: RegExp[] = [];

    for (const pattern of patterns) {
      const regex = toGlobal(pattern);

      if (regex !== null) {
        compiled.push(regex);
      }
    }

    return compiled;
  } catch {
    return [];
  }
}

function scrubText(value: string, patterns: RegExp[]): string {
  let result = value;

  for (const pattern of patterns) {
    pattern.lastIndex = 0;
    result = result.replace(pattern, redacted);
  }

  return result;
}

/**
 * scrubReport redacts PII patterns from message, crumbs, and stack paths.
 * Never throws: scrub failure returns the report unredacted.
 */
export function scrubReport<T extends { message: string }>(
  report: T,
  crumbs: Breadcrumb[],
  frames: StackFrame[],
  patterns: RegExp[],
): { message: string; breadcrumbs: Breadcrumb[]; stack: StackFrame[] } {
  try {
    if (patterns.length === 0) {
      return { message: report.message, breadcrumbs: crumbs, stack: frames };
    }

    return {
      message: scrubText(report.message, patterns),
      breadcrumbs: crumbs.map((crumb) => ({
        ...crumb,
        category: scrubText(crumb.category, patterns),
        message: scrubText(crumb.message, patterns),
      })),
      stack: frames.map((frame) => ({
        ...frame,
        function: scrubText(frame.function, patterns),
        file: scrubText(frame.file, patterns),
      })),
    };
  } catch {
    return { message: report.message, breadcrumbs: crumbs, stack: frames };
  }
}

/**
 * scrubLogItem redacts PII patterns from a log body and its string
 * attribute values. Never throws: scrub failure returns the item as-is.
 */
export function scrubLogItem(item: LogItem, patterns: RegExp[]): LogItem {
  try {
    if (patterns.length === 0) {
      return item;
    }

    const scrubbed: LogItem = { ...item, body: scrubText(item.body, patterns) };

    if (item.attributes !== undefined) {
      const attributes: LogAttributes = {};

      for (const [key, entry] of Object.entries(item.attributes)) {
        if (entry.type === 'string' && typeof entry.value === 'string') {
          attributes[key] = { value: scrubText(entry.value, patterns), type: entry.type };
        } else {
          attributes[key] = entry;
        }
      }

      scrubbed.attributes = attributes;
    }

    return scrubbed;
  } catch {
    return item;
  }
}
