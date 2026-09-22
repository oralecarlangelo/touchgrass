import type { ErrorReport, LogBatchRequest, LogItem } from './types.js';

/**
 * postReport delivers one report, resolving false on any failure.
 * Never throws and never rejects: ingestion trouble must not surface
 * to the host app.
 */
export async function postReport(
  endpoint: string,
  key: string,
  report: ErrorReport,
  timeoutMs: number,
): Promise<boolean> {
  let body: string;

  try {
    body = JSON.stringify(report);
  } catch {
    return false;
  }

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);

  try {
    const response = await fetch(endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Touchgrass-Key': key },
      body,
      signal: controller.signal,
    });

    return response.status === 202;
  } catch {
    return false;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * postLogs delivers one log batch, resolving false on any failure.
 * Sends both the X-Touchgrass-Key header (like /api/ingest) and an
 * Authorization bearer header (per the S20 wire contract) so either
 * server check passes. Never throws and never rejects.
 */
export async function postLogs(
  endpoint: string,
  key: string,
  release: string,
  items: LogItem[],
  timeoutMs: number,
): Promise<boolean> {
  let body: string;

  try {
    const batch: LogBatchRequest = release === '' ? { items } : { release, items };
    body = JSON.stringify(batch);
  } catch {
    return false;
  }

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);

  try {
    const response = await fetch(endpoint, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Touchgrass-Key': key,
        Authorization: `Bearer ${key}`,
      },
      body,
      signal: controller.signal,
    });

    return response.status === 202;
  } catch {
    return false;
  } finally {
    clearTimeout(timer);
  }
}
