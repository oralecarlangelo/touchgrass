import type { ErrorReport } from './types.js';

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
